package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/xpzouying/xiaohongshu-mcp/accounts"
	xhsErrors "github.com/xpzouying/xiaohongshu-mcp/errors"
	"github.com/xpzouying/xiaohongshu-mcp/xiaohongshu"
)

func TestAccountVerificationSurvivesChecksAndClearsOnSuccess(t *testing.T) {
	manager := testAccountManager(t)
	a := manager.store.List()[0]
	require.NoError(t, manager.store.BindIdentity(a.ID, "user-a", "账号 A"))
	other, err := manager.store.Create("账号 B")
	require.NoError(t, err)
	require.NoError(t, manager.store.BindIdentity(other.ID, "user-b", "账号 B"))
	manager.checkIdentity = func(_ context.Context, r *accountRuntime, _ accountBrowser) error {
		if r.id == a.ID {
			return fmt.Errorf("核验页面: %w", xhsErrors.ErrVerificationRequired)
		}
		return manager.store.BindIdentity(r.id, "user-b", "账号 B")
	}
	manager.refreshAccountStatuses(t.Context())
	cached, err := manager.store.Get(a.ID)
	require.NoError(t, err)
	require.True(t, cached.IsLoggedIn, "保留最近明确的登录结果")
	require.True(t, cached.VerificationRequired)
	require.Equal(t, a.ActiveProfile, cached.ActiveProfile)
	other, err = manager.store.Get(other.ID)
	require.NoError(t, err)
	require.False(t, other.VerificationRequired)

	router := setupRoutes(NewAccountAppServer(manager, ""))
	getStatus := func(path string) LoginStatusResponse {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set(accountHeader, a.ID)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		var payload struct {
			Data LoginStatusResponse `json:"data"`
		}
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload))
		return payload.Data
	}
	for _, path := range []string{"/api/v1/login/session", "/api/v1/account/verify"} {
		status := getStatus(path)
		require.Equal(t, a.ID, status.AccountID)
		require.Equal(t, xiaohongshu.LoginVerificationRequired, status.Status)
		require.True(t, status.VerificationRequired)
		require.False(t, status.IsLoggedIn)
		require.Equal(t, "XHS_VERIFICATION_REQUIRED", status.ErrorCode)
		require.NotEmpty(t, status.Message)
		require.Empty(t, status.SessionID, "验证要求本身不代表已打开浏览器")
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/accounts", nil))
	require.Equal(t, http.StatusOK, response.Code)
	var listed struct {
		Data struct {
			Accounts []accountSummary `json:"accounts"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &listed))
	var flagged []string
	for _, item := range listed.Data.Accounts {
		if item.VerificationRequired {
			flagged = append(flagged, item.ID)
		}
	}
	require.Equal(t, []string{a.ID}, flagged)

	manager.checkIdentity = func(context.Context, *accountRuntime, accountBrowser) error {
		return context.DeadlineExceeded
	}
	r, err := manager.runtime(a.ID)
	require.NoError(t, err)
	_, err = r.verifyLoginStatus(t.Context())
	require.ErrorIs(t, err, context.DeadlineExceeded)
	cached, err = manager.store.Get(a.ID)
	require.NoError(t, err)
	require.True(t, cached.VerificationRequired, "网络失败不能清除安全验证要求")

	manager.checkIdentity = func(_ context.Context, r *accountRuntime, _ accountBrowser) error {
		return manager.store.BindIdentity(r.id, "user-a", "账号 A")
	}
	verified := getStatus("/api/v1/account/verify")
	require.True(t, verified.IsLoggedIn)
	require.False(t, verified.VerificationRequired)
	require.Equal(t, xiaohongshu.LoginLoggedIn, verified.Status)
	cachedStatus := getStatus("/api/v1/login/session")
	require.True(t, cachedStatus.IsLoggedIn)
	require.False(t, cachedStatus.VerificationRequired)
	cached, err = manager.store.Get(a.ID)
	require.NoError(t, err)
	require.False(t, manager.summary(cached).VerificationRequired)
}

func TestVerificationRequiredAllowsLoginCandidateWithoutLosingProfile(t *testing.T) {
	manager := testAccountManager(t)
	a := manager.store.List()[0]
	require.NoError(t, manager.store.BindIdentity(a.ID, "user-a", "账号 A"))
	require.NoError(t, manager.store.SetVerificationRequired(a.ID))
	r, err := manager.runtime(a.ID)
	require.NoError(t, err)
	original := manager.store.ProfilePath(a)
	marker := filepath.Join(original, "saved-state")
	require.NoError(t, os.WriteFile(marker, []byte("keep"), 0600))
	startupError := errors.New("模拟候选浏览器启动失败")
	starts := 0
	manager.factory = func(_ accounts.Account, profile string, _ bool, _ []byte) (accountBrowser, error) {
		starts++
		require.NotEqual(t, original, profile)
		value, err := os.ReadFile(filepath.Join(profile, "saved-state"))
		require.NoError(t, err)
		require.Equal(t, "keep", string(value))
		return nil, startupError
	}
	_, err = r.openLogin(t.Context())
	require.ErrorIs(t, err, startupError)
	require.Equal(t, 1, starts, "安全验证不能被已登录门禁阻断")
	value, err := os.ReadFile(marker)
	require.NoError(t, err)
	require.Equal(t, "keep", string(value))
	entries, err := os.ReadDir(filepath.Dir(original))
	require.NoError(t, err)
	require.Len(t, entries, 1, "失败候选应清理，原profile保留")
	cached, err := manager.store.Get(a.ID)
	require.NoError(t, err)
	require.True(t, cached.IsLoggedIn)
	require.True(t, cached.VerificationRequired)
}
