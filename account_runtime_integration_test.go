//go:build integration

package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/go-rod/rod/lib/proto"
	"github.com/stretchr/testify/require"
	"github.com/xpzouying/xiaohongshu-mcp/accounts"
	"github.com/xpzouying/xiaohongshu-mcp/xiaohongshu"
)

func requireCachedAccountBrowser(t *testing.T) {
	t.Helper()
	if !((runtime.GOOS == "darwin" && runtime.GOARCH == "arm64") || (runtime.GOOS == "linux" && runtime.GOARCH == "amd64") || (runtime.GOOS == "windows" && runtime.GOARCH == "amd64")) {
		t.Skip("本平台没有内置浏览器")
	}
	cache, err := os.UserCacheDir()
	require.NoError(t, err)
	version, err := os.ReadFile("browser/browser_version.txt")
	require.NoError(t, err)
	if _, err := os.Stat(filepath.Join(cache, "xiaohongshu-mcp", "browser", strings.TrimSpace(string(version)))); err != nil {
		t.Skip("没有缓存的内置浏览器，本地回归不自动下载")
	}
}

func TestAccountRuntimeProfilePersistenceLocal(t *testing.T) {
	requireCachedAccountBrowser(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprint(w, "<!doctype html><title>account fixture</title>")
	}))
	defer server.Close()
	root := filepath.Join(t.TempDir(), "accounts")
	missing := filepath.Join(t.TempDir(), "no-cookies.json")
	store, err := accounts.Open(root, missing, 0)
	require.NoError(t, err)
	manager := NewAccountManager(store)
	manager.headless = true
	t.Cleanup(func() { _ = manager.Close() })
	a := store.List()[0]
	b, err := store.Create("B")
	require.NoError(t, err)
	for _, item := range []accounts.Account{a, b} {
		r, err := manager.runtime(item.ID)
		require.NoError(t, err)
		lease, err := r.borrowBrowser(t.Context(), false)
		require.NoError(t, err)
		p := lease.NewPage().Timeout(10 * time.Second)
		require.NoError(t, p.Navigate(server.URL))
		require.NoError(t, p.WaitLoad())
		require.NoError(t, p.SetCookies([]*proto.NetworkCookieParam{{Name: "account", Value: item.ID, URL: server.URL, Expires: proto.TimeSinceEpoch(time.Now().Add(time.Hour).Unix())}}))
		_, err = p.Eval(`value => localStorage.setItem("account", value)`, item.ID)
		require.NoError(t, err)
		require.NoError(t, p.Close())
		lease.Close()
		reused, err := r.borrowBrowser(t.Context(), false)
		require.NoError(t, err)
		require.Same(t, lease.accountBrowser, reused.accountBrowser)
		reused.Close()
	}
	require.NoError(t, manager.Close())
	store, err = accounts.Open(root, missing, 0)
	require.NoError(t, err)
	restarted := NewAccountManager(store)
	restarted.headless = true
	t.Cleanup(func() { require.NoError(t, restarted.Close()) })
	for _, item := range []accounts.Account{a, b} {
		r, err := restarted.runtime(item.ID)
		require.NoError(t, err)
		lease, err := r.borrowBrowser(t.Context(), false)
		require.NoError(t, err)
		p := lease.NewPage().Timeout(10 * time.Second)
		require.NoError(t, p.Navigate(server.URL))
		require.NoError(t, p.WaitLoad())
		value, err := p.Eval(`() => ({local: localStorage.getItem("account"), cookie: document.cookie})`)
		require.NoError(t, err)
		require.Equal(t, item.ID, value.Value.Get("local").Str())
		require.Contains(t, value.Value.Get("cookie").Str(), "account="+item.ID)
		require.NoError(t, p.Close())
		lease.Close()
	}
}

func TestAccountCandidateLoginCommitLocal(t *testing.T) {
	requireCachedAccountBrowser(t)
	for _, mismatch := range []bool{false, true} {
		t.Run(fmt.Sprintf("mismatch=%t", mismatch), func(t *testing.T) {
			store, err := accounts.Open(filepath.Join(t.TempDir(), "accounts"), filepath.Join(t.TempDir(), "no-cookies.json"), 0)
			require.NoError(t, err)
			manager := NewAccountManager(store)
			manager.headless = true
			t.Cleanup(func() { require.NoError(t, manager.Close()) })
			a := store.List()[0]
			if mismatch {
				require.NoError(t, store.BindIdentity(a.ID, "original-user", "原账号"))
			}
			original := store.ProfilePath(a)
			require.NoError(t, os.WriteFile(filepath.Join(original, "original-marker"), []byte("保留"), 0600))
			candidate, err := store.NewCandidate(a.ID)
			require.NoError(t, err)
			copy := a
			copy.ActiveProfile = candidate
			b, err := manager.factory(a, store.ProfilePath(copy), true, nil)
			require.NoError(t, err)
			page := b.NewPage()
			require.NoError(t, page.SetDocumentContent(sessionFixtureLoggedIn))
			r, err := manager.runtime(a.ID)
			require.NoError(t, err)
			session, err := randomSessionID()
			require.NoError(t, err)
			require.NoError(t, manager.reserveVisual(a.ID, session))
			r.mu.Lock()
			r.browserBusy = true
			r.mu.Unlock()
			require.NoError(t, r.monitorLogin(xiaohongshu.NewLogin(page), b, candidate, session, a.ActiveProfile))
			r.mu.Lock()
			done := r.loginDone
			r.mu.Unlock()
			select {
			case <-done:
			case <-time.After(15 * time.Second):
				t.Fatal("候选登录未结束")
			}
			current, err := store.Get(a.ID)
			require.NoError(t, err)
			if mismatch {
				require.Equal(t, a.ActiveProfile, current.ActiveProfile)
				require.Equal(t, "original-user", current.UserID)
				require.FileExists(t, filepath.Join(original, "original-marker"))
				require.NoDirExists(t, store.ProfilePath(copy))
				r.mu.Lock()
				code := r.loginCode
				r.mu.Unlock()
				require.Equal(t, "ACCOUNT_IDENTITY_MISMATCH", code)
			} else {
				require.Equal(t, candidate, current.ActiveProfile)
				require.Equal(t, "local-user", current.UserID)
				require.FileExists(t, filepath.Join(store.ProfilePath(current), "original-marker"))
				state, active := r.service.logins.snapshot()
				require.False(t, active)
				require.Equal(t, xiaohongshu.LoginLoggedIn, state.phase)
			}
			require.False(t, b.Alive())
			require.False(t, manager.visualActive(a.ID, session))
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			require.NoError(t, r.closeLogin(ctx, ""))
		})
	}
}
