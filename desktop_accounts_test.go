package main

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDesktopTicketsCannotFollowAnotherAccountSession(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/vnc.html", r.URL.Path)
		_, _ = io.WriteString(w, "desktop")
	}))
	defer upstream.Close()
	d := newDesktopGateway(upstream.URL)
	accountA, accountB := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	sessionA, sessionB := "11111111111111111111111111111111", "22222222222222222222222222222222"
	activeAccount, activeSession := accountA, sessionA
	d.sessionActive = func(account, session string) bool { return account == activeAccount && session == activeSession }
	grant := func(account, session string) *http.Cookie {
		response := httptest.NewRecorder()
		d.grantSession(response, httptest.NewRequest("POST", "http://studio.test/api/v1/login/browser", nil), account, session)
		return response.Result().Cookies()[0]
	}
	cookieA := grant(accountA, sessionA)
	require.Equal(t, "/desktop/"+sessionA+"/", cookieA.Path)
	get := func(session string, cookie *http.Cookie) int {
		r := httptest.NewRequest("GET", "http://studio.test/desktop/"+session+"/vnc.html", nil)
		r.AddCookie(cookie)
		response := httptest.NewRecorder()
		d.ServeHTTP(response, r)
		return response.Code
	}
	require.Equal(t, http.StatusOK, get(sessionA, cookieA))
	d.revoke(sessionA)
	activeAccount, activeSession = accountB, sessionB
	cookieB := grant(accountB, sessionB)
	require.Equal(t, http.StatusUnauthorized, get(sessionA, cookieA))
	require.Equal(t, http.StatusUnauthorized, get(sessionA, cookieB))
	require.Equal(t, http.StatusUnauthorized, get(sessionB, cookieA))
	require.Equal(t, http.StatusOK, get(sessionB, cookieB))
}

func TestDesktopSessionRevocationClosesEstablishedWebSocket(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/websockify" {
			http.NotFound(w, r)
			return
		}
		connection, buffer, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer connection.Close()
		digest := sha1.Sum([]byte(r.Header.Get("Sec-WebSocket-Key") + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
		_, _ = fmt.Fprintf(buffer, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Accept: %s\r\n\r\n", base64.StdEncoding.EncodeToString(digest[:]))
		_ = buffer.Flush()
		_, _ = io.Copy(io.Discard, connection)
	}))
	defer upstream.Close()
	d := newDesktopGateway(upstream.URL)
	account, session := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "11111111111111111111111111111111"
	var active sync.Map
	active.Store(session, true)
	d.sessionActive = func(id, sid string) bool { _, ok := active.Load(sid); return id == account && ok }
	server := httptest.NewServer(d)
	defer server.Close()
	u, err := url.Parse(server.URL)
	require.NoError(t, err)
	granted := httptest.NewRecorder()
	d.grantSession(granted, httptest.NewRequest("POST", server.URL+"/api/v1/login/browser", nil), account, session)
	cookie := granted.Result().Cookies()[0]
	connection, err := net.DialTimeout("tcp", u.Host, time.Second)
	require.NoError(t, err)
	defer connection.Close()
	require.NoError(t, connection.SetDeadline(time.Now().Add(3*time.Second)))
	_, err = fmt.Fprintf(connection, "GET /desktop/%s/websockify HTTP/1.1\r\nHost: %s\r\nOrigin: %s\r\nCookie: %s=%s\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n\r\n", session, u.Host, server.URL, cookie.Name, cookie.Value)
	require.NoError(t, err)
	reader := bufio.NewReader(connection)
	response, err := http.ReadResponse(reader, &http.Request{Method: "GET"})
	require.NoError(t, err)
	require.Equal(t, http.StatusSwitchingProtocols, response.StatusCode)
	active.Delete(session)
	d.revoke(session)
	_, err = reader.ReadByte()
	require.Error(t, err)
	if networkError, ok := err.(net.Error); ok {
		require.False(t, networkError.Timeout(), "撤销应主动断开连接，而非等待读超时")
	}
}
