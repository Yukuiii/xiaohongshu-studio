package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/xpzouying/xiaohongshu-mcp/accounts"
)

func TestLoggedInAccountDoesNotCreateLoginCandidate(t *testing.T) {
	manager := testAccountManager(t)
	a := manager.store.List()[0]
	require.NoError(t, manager.store.BindIdentity(a.ID, "user-a", "账号 A"))
	before, err := manager.store.Get(a.ID)
	require.NoError(t, err)
	r, err := manager.runtime(a.ID)
	require.NoError(t, err)
	browser := newFakeAccountBrowser()
	r.browser = browser
	starts := 0
	manager.factory = func(accounts.Account, string, bool, []byte) (accountBrowser, error) {
		starts++
		return nil, errors.New("不应创建登录浏览器")
	}
	response, err := r.openLogin(context.Background())
	require.Nil(t, response)
	failure := accountFailure(err)
	require.NotNil(t, failure)
	require.Equal(t, http.StatusConflict, failure.Status)
	require.Equal(t, "ACCOUNT_ALREADY_LOGGED_IN", failure.Code)
	require.Zero(t, starts)
	require.True(t, browser.Alive(), "已登录账号的原浏览器应继续保留")
	require.Same(t, browser, r.browser)
	require.False(t, r.browserBusy)
	require.Empty(t, manager.visualSession)
	after, err := manager.store.Get(a.ID)
	require.NoError(t, err)
	require.Equal(t, before, after)
	entries, err := os.ReadDir(filepath.Dir(manager.store.ProfilePath(after)))
	require.NoError(t, err)
	require.Len(t, entries, 1)
}

func TestLoggedInLoginHTTPReturnsConflictWithoutCandidate(t *testing.T) {
	t.Setenv("XHS_DESKTOP_URL", "")
	t.Setenv("DISPLAY", ":policy-test")
	manager := testAccountManager(t)
	a := manager.store.List()[0]
	require.NoError(t, manager.store.BindIdentity(a.ID, "user-a", "账号 A"))
	starts := 0
	manager.factory = func(accounts.Account, string, bool, []byte) (accountBrowser, error) {
		starts++
		return nil, errors.New("不应启动浏览器")
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/login/browser", nil)
	request.Header.Set(accountHeader, a.ID)
	response := httptest.NewRecorder()
	setupRoutes(NewAccountAppServer(manager, "")).ServeHTTP(response, request)
	require.Equal(t, http.StatusConflict, response.Code, response.Body.String())
	var payload ErrorResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload))
	require.Equal(t, "ACCOUNT_ALREADY_LOGGED_IN", payload.Code)
	require.Zero(t, starts)
	entries, err := os.ReadDir(filepath.Dir(manager.store.ProfilePath(a)))
	require.NoError(t, err)
	require.Len(t, entries, 1)
}

func TestActiveLoginSessionIsReusedDespiteLoggedInCache(t *testing.T) {
	manager := testAccountManager(t)
	a := manager.store.List()[0]
	require.NoError(t, manager.store.BindIdentity(a.ID, "user-a", "账号 A"))
	r, err := manager.runtime(a.ID)
	require.NoError(t, err)
	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.service.logins.start(cancel, time.Now().Add(time.Minute))
	r.loginID = "existing-login-session"
	manager.factory = func(accounts.Account, string, bool, []byte) (accountBrowser, error) {
		t.Error("继续登录不能创建新浏览器")
		return nil, errors.New("不应启动")
	}
	response, err := r.openLogin(context.Background())
	require.NoError(t, err)
	require.Equal(t, "existing-login-session", response.SessionID)
	entries, err := os.ReadDir(filepath.Dir(manager.store.ProfilePath(a)))
	require.NoError(t, err)
	require.Len(t, entries, 1)
}

func TestNewAndExpiredAccountsCanStartLoginCandidate(t *testing.T) {
	for _, expired := range []bool{false, true} {
		t.Run(map[bool]string{false: "new", true: "expired"}[expired], func(t *testing.T) {
			manager := testAccountManager(t)
			a := manager.store.List()[0]
			if expired {
				require.NoError(t, manager.store.BindIdentity(a.ID, "user-a", "账号 A"))
				require.NoError(t, manager.store.SetLoginStatus(a.ID, false))
			}
			r, err := manager.runtime(a.ID)
			require.NoError(t, err)
			marker := filepath.Join(manager.store.ProfilePath(a), "saved-state")
			require.NoError(t, os.WriteFile(marker, []byte("keep"), 0600))
			starts := 0
			startupError := errors.New("模拟候选浏览器启动失败")
			manager.factory = func(_ accounts.Account, profile string, _ bool, _ []byte) (accountBrowser, error) {
				starts++
				require.NotEqual(t, manager.store.ProfilePath(a), profile)
				value, err := os.ReadFile(filepath.Join(profile, "saved-state"))
				require.NoError(t, err)
				require.Equal(t, "keep", string(value))
				return nil, startupError
			}
			response, err := r.openLogin(context.Background())
			require.Nil(t, response)
			require.ErrorIs(t, err, startupError)
			require.Equal(t, 1, starts)
			entries, err := os.ReadDir(filepath.Dir(manager.store.ProfilePath(a)))
			require.NoError(t, err)
			require.Len(t, entries, 1, "失败候选应删除，原目录保留")
			value, err := os.ReadFile(marker)
			require.NoError(t, err)
			require.Equal(t, "keep", string(value))
		})
	}
}
