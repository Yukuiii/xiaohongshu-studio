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
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func desktopTestCookie(t *testing.T, gateway *desktopGateway) *http.Cookie {
	t.Helper()
	recorder := httptest.NewRecorder()
	gateway.grant(recorder, httptest.NewRequest(http.MethodPost, "http://studio.test/api/v1/login/browser", nil))
	cookies := recorder.Result().Cookies()
	require.Len(t, cookies, 1)
	return cookies[0]
}

func TestDesktopProxyProtectsBrowserControl(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = io.WriteString(w, "desktop")
	}))
	defer upstream.Close()
	gateway := newDesktopGateway(upstream.URL)
	cookie := desktopTestCookie(t, gateway)
	require.True(t, cookie.HttpOnly)
	require.Equal(t, http.SameSiteStrictMode, cookie.SameSite)
	require.Equal(t, "/desktop", cookie.Path)
	for _, tc := range []struct {
		name      string
		cookie    *http.Cookie
		origin    string
		fetchSite string
		upgrade   string
		want      int
	}{
		{name: "anonymous", want: http.StatusUnauthorized},
		{name: "bearer alone cannot authenticate iframe", want: http.StatusUnauthorized},
		{name: "cross origin", cookie: cookie, origin: "https://untrusted.example", want: http.StatusForbidden},
		{name: "opaque origin", cookie: cookie, origin: "null", want: http.StatusForbidden},
		{name: "cross site metadata", cookie: cookie, fetchSite: "cross-site", want: http.StatusForbidden},
		{name: "same site is insufficient", cookie: cookie, fetchSite: "same-site", want: http.StatusForbidden},
		{name: "websocket needs origin", cookie: cookie, upgrade: "websocket", want: http.StatusForbidden},
		{name: "forged ticket", cookie: &http.Cookie{Name: desktopCookieName, Value: cookie.Value + "x"}, want: http.StatusUnauthorized},
		{name: "another server ticket", cookie: desktopTestCookie(t, newDesktopGateway(upstream.URL)), want: http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://studio.test/desktop/vnc.html", nil)
			if tc.cookie != nil {
				req.AddCookie(tc.cookie)
			}
			req.Header.Set("Authorization", "Bearer service-token")
			req.Header.Set("Origin", tc.origin)
			req.Header.Set("Sec-Fetch-Site", tc.fetchSite)
			req.Header.Set("Upgrade", tc.upgrade)
			recorder := httptest.NewRecorder()
			gateway.ServeHTTP(recorder, req)
			require.Equal(t, tc.want, recorder.Code)
		})
	}
	expiry := strconv.FormatInt(time.Now().Add(-time.Minute).Unix(), 10)
	req := httptest.NewRequest(http.MethodGet, "http://studio.test/desktop/vnc.html", nil)
	req.AddCookie(&http.Cookie{Name: desktopCookieName, Value: expiry + "." + gateway.signature(expiry)})
	recorder := httptest.NewRecorder()
	gateway.ServeHTTP(recorder, req)
	require.Equal(t, http.StatusUnauthorized, recorder.Code)
	require.Zero(t, calls.Load(), "未授权请求不得触及桌面")
}

func TestDesktopProxyServesSameOriginAssetsWithoutLeakingCredentials(t *testing.T) {
	observed := make(chan *http.Request, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observed <- r.Clone(r.Context())
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, "<html>visible desktop</html>")
	}))
	defer upstream.Close()
	gateway := newDesktopGateway(upstream.URL)
	req := httptest.NewRequest(http.MethodGet, "http://studio.test/desktop/vnc.html?resize=scale", nil)
	req.AddCookie(desktopTestCookie(t, gateway))
	req.Header.Set("Authorization", "Bearer model-or-service-secret")
	req.Header.Set("Origin", "http://studio.test")
	recorder := httptest.NewRecorder()
	gateway.ServeHTTP(recorder, req)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "<html>visible desktop</html>", recorder.Body.String())
	require.Equal(t, "private, no-store", recorder.Header().Get("Cache-Control"))
	require.Contains(t, recorder.Header().Get("Content-Security-Policy"), "frame-ancestors 'self'")
	request := <-observed
	require.Equal(t, "/vnc.html", request.URL.Path)
	require.Equal(t, "resize=scale", request.URL.RawQuery)
	require.Equal(t, upstream.URL, request.Header.Get("Origin"))
	require.Empty(t, request.Header.Get("Authorization"))
	require.Empty(t, request.Header.Get("Cookie"))
}

func TestDesktopWebSocketUsesSameOriginTicket(t *testing.T) {
	observed := make(chan string, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/websockify" || r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			http.Error(w, "invalid proxy request", http.StatusBadRequest)
			return
		}
		observed <- r.Header.Get("Origin")
		connection, buffer, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer connection.Close()
		digest := sha1.Sum([]byte(r.Header.Get("Sec-WebSocket-Key") + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
		_, _ = fmt.Fprintf(buffer, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Accept: %s\r\n\r\n", base64.StdEncoding.EncodeToString(digest[:]))
		_, _ = buffer.Write([]byte{0x81, 2, 'o', 'k'})
		_ = buffer.Flush()
	}))
	defer upstream.Close()
	gateway := newDesktopGateway(upstream.URL)
	server := httptest.NewServer(gateway)
	defer server.Close()
	u, err := url.Parse(server.URL)
	require.NoError(t, err)
	connection, err := net.DialTimeout("tcp", u.Host, 3*time.Second)
	require.NoError(t, err)
	defer connection.Close()
	require.NoError(t, connection.SetDeadline(time.Now().Add(3*time.Second)))
	cookie := desktopTestCookie(t, gateway)
	_, err = fmt.Fprintf(connection, "GET /desktop/websockify HTTP/1.1\r\nHost: %s\r\nOrigin: %s\r\nCookie: %s=%s\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n\r\n", u.Host, server.URL, cookie.Name, cookie.Value)
	require.NoError(t, err)
	reader := bufio.NewReader(connection)
	response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodGet})
	require.NoError(t, err)
	require.Equal(t, http.StatusSwitchingProtocols, response.StatusCode)
	frame := make([]byte, 4)
	_, err = io.ReadFull(reader, frame)
	require.NoError(t, err)
	require.Equal(t, []byte{0x81, 2, 'o', 'k'}, frame)
	require.Equal(t, upstream.URL, <-observed)
}

func TestDesktopConfigurationRejectsNonLocalTargets(t *testing.T) {
	for _, raw := range []string{"https://127.0.0.1:6080", "http://untrusted.example:6080", "http://secret@127.0.0.1:6080", "http://127.0.0.1:6080/path", "http://127.0.0.1:6080?token=secret", "http://127.0.0.1:6080/#secret"} {
		gateway := newDesktopGateway(raw)
		require.Error(t, gateway.ready(t.Context()))
		require.NotContains(t, gateway.configurationError.Error(), "secret")
	}
	gateway := newDesktopGateway("")
	req := httptest.NewRequest(http.MethodGet, "http://studio.test/desktop/vnc.html", nil)
	req.AddCookie(desktopTestCookie(t, gateway))
	recorder := httptest.NewRecorder()
	gateway.ServeHTTP(recorder, req)
	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
}

func TestDesktopUnavailableDoesNotStartLoginBrowser(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
	defer upstream.Close()
	server := NewAppServer(nil, "service-token")
	server.desktop = newDesktopGateway(upstream.URL)
	router := setupRoutes(server)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/login/browser", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer service-token")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	require.Contains(t, recorder.Body.String(), "DESKTOP_UNAVAILABLE")
}
