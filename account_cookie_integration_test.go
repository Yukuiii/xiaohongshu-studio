//go:build integration

package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-rod/rod/lib/proto"
	"github.com/stretchr/testify/require"
)

func TestAccountClearCookiesLocal(t *testing.T) {
	requireCachedAccountBrowser(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, "<!doctype html><title>cookie fixture</title>")
	}))
	defer server.Close()
	store := openTestStore(t, filepath.Join(t.TempDir(), "accounts"))
	_, err := store.Create("默认账号")
	require.NoError(t, err)
	manager := NewAccountManager(store)
	manager.headless = true
	t.Cleanup(func() { require.NoError(t, manager.Close()) })
	r, err := manager.runtime(store.List()[0].ID)
	require.NoError(t, err)
	lease, err := r.borrowBrowser(t.Context(), false)
	require.NoError(t, err)
	page := lease.NewPage().Timeout(10 * time.Second)
	require.NoError(t, page.Navigate(server.URL))
	require.NoError(t, page.WaitLoad())
	require.NoError(t, page.SetCookies([]*proto.NetworkCookieParam{{Name: "fixture_cookie", Value: "saved", URL: server.URL, Expires: proto.TimeSinceEpoch(time.Now().Add(time.Hour).Unix())}}))
	_, err = page.Eval(`() => localStorage.setItem("keep", "保留")`)
	require.NoError(t, err)
	require.NoError(t, page.Close())
	lease.Close()
	require.NoError(t, store.BindIdentity(r.id, "user-a", "账号 A"))
	require.NoError(t, r.service.DeleteCookies(t.Context()))
	cleared, err := store.Get(r.id)
	require.NoError(t, err)
	require.False(t, cleared.IsLoggedIn)
	require.NotNil(t, cleared.LoginCheckedAt)
	lease, err = r.borrowBrowser(t.Context(), false)
	require.NoError(t, err)
	defer lease.Close()
	page = lease.NewPage().Timeout(10 * time.Second)
	defer page.Close()
	require.NoError(t, page.Navigate(server.URL))
	require.NoError(t, page.WaitLoad())
	cookies, err := page.Cookies([]string{server.URL})
	require.NoError(t, err)
	require.Empty(t, cookies)
	value, err := page.Eval(`() => localStorage.getItem("keep")`)
	require.NoError(t, err)
	require.Equal(t, "保留", value.Value.Str())
}
