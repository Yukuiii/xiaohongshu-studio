package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/xpzouying/xiaohongshu-mcp/accounts"
	"github.com/xpzouying/xiaohongshu-mcp/db"
)

func TestAccountListReturnsCachedLoginWithoutBrowser(t *testing.T) {
	m := testAccountManager(t)
	a := m.store.List()[0]
	require.NoError(t, m.store.BindIdentity(a.ID, "user-a", "账号 A"))
	b, err := m.store.Create("账号 B")
	require.NoError(t, err)
	m.factory = func(accounts.Account, string, bool, []byte) (accountBrowser, error) {
		t.Error("读取账号列表不应启动浏览器")
		return nil, errors.New("unexpected browser")
	}
	response := httptest.NewRecorder()
	setupRoutes(NewAccountAppServer(m, "")).ServeHTTP(response, httptest.NewRequest("GET", "/api/v1/accounts", nil))
	require.Equal(t, http.StatusOK, response.Code)
	var payload struct {
		Data struct {
			Accounts []accountSummary `json:"accounts"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload))
	require.Len(t, payload.Data.Accounts, 2)
	byID := make(map[string]accountSummary)
	for _, item := range payload.Data.Accounts {
		byID[item.ID] = item
	}
	require.True(t, byID[a.ID].IsLoggedIn)
	require.NotNil(t, byID[a.ID].LoginCheckedAt)
	require.False(t, byID[b.ID].IsLoggedIn)
	require.Nil(t, byID[b.ID].LoginCheckedAt)
	require.Empty(t, m.runtimes)
	require.NotContains(t, response.Body.String(), "active_profile")
	require.NotContains(t, response.Body.String(), `"seed"`)
}

func TestAccountStatusSweepCachesOnlyDefiniteResults(t *testing.T) {
	for _, tc := range []struct {
		name       string
		checkError error
		panics     bool
		loggedIn   bool
		changes    bool
	}{
		{"valid", nil, false, true, true},
		{"expired", &accountError{http.StatusConflict, "XHS_LOGIN_REQUIRED", "未登录"}, false, false, true},
		{"mismatched", accounts.ErrIdentityMismatch, false, false, true},
		{"duplicate", accounts.ErrDuplicateIdentity, false, false, true},
		{"network", errors.New("网络错误"), false, true, false},
		{"timeout", context.DeadlineExceeded, false, true, false},
		{"canceled", context.Canceled, false, true, false},
		{"incomplete", errors.New("无法读取用户身份"), false, true, false},
		{"panic", nil, true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := testAccountManager(t)
			a := m.store.List()[0]
			require.NoError(t, m.store.BindIdentity(a.ID, "user-a", "账号 A"))
			if tc.name == "valid" {
				require.NoError(t, m.store.SetLoginStatus(a.ID, false))
			}
			before, err := m.store.Get(a.ID)
			require.NoError(t, err)
			b := newFakeAccountBrowser()
			m.headless = false
			m.factory = func(_ accounts.Account, _ string, headless bool, _ []byte) (accountBrowser, error) {
				if !headless {
					return nil, errors.New("巡检必须使用无头浏览器")
				}
				return b, nil
			}
			m.checkIdentity = func(ctx context.Context, r *accountRuntime, _ accountBrowser) error {
				if tc.panics {
					panic("模拟页面异常")
				}
				if tc.checkError != nil {
					return tc.checkError
				}
				return m.store.BindIdentity(r.id, "user-a", "账号 A")
			}
			m.refreshAccountStatuses(t.Context())
			current, err := m.store.Get(a.ID)
			require.NoError(t, err)
			require.Equal(t, tc.loggedIn, current.IsLoggedIn)
			if tc.changes {
				require.False(t, current.LoginCheckedAt.Before(*before.LoginCheckedAt))
			} else {
				require.Equal(t, before, current)
			}
			require.False(t, b.Alive(), "巡检结束应关闭浏览器")
			r, err := m.runtime(a.ID)
			require.NoError(t, err)
			require.Nil(t, r.browser)
			require.Nil(t, r.idle, "巡检不应重新设置十分钟空闲计时")
		})
	}
}

func TestAccountStatusSweepSkipsBusyAndLoginAccounts(t *testing.T) {
	m := testAccountManager(t)
	busy, err := m.runtime(m.store.List()[0].ID)
	require.NoError(t, err)
	busy.browserBusy = true
	login, err := m.store.Create("登录中")
	require.NoError(t, err)
	loginRuntime, err := m.runtime(login.ID)
	require.NoError(t, err)
	loginRuntime.loginID = "active-login"
	writing, err := m.store.Create("上传中")
	require.NoError(t, err)
	writer, err := m.runtime(writing.ID)
	require.NoError(t, err)
	release, err := writer.lease(true)
	require.NoError(t, err)
	defer release()
	free, err := m.store.Create("空闲")
	require.NoError(t, err)
	var checked []string
	m.checkIdentity = func(ctx context.Context, r *accountRuntime, _ accountBrowser) error {
		checked = append(checked, r.id)
		return m.store.BindIdentity(r.id, "free-user", "空闲账号")
	}
	m.refreshAccountStatuses(t.Context())
	require.Equal(t, []string{free.ID}, checked)
	for _, id := range []string{busy.id, login.ID, writing.ID} {
		a, err := m.store.Get(id)
		require.NoError(t, err)
		require.Nil(t, a.LoginCheckedAt)
	}
}

func TestAccountStatusSweepClosesReusedBrowser(t *testing.T) {
	m := testAccountManager(t)
	r, err := m.runtime(m.store.List()[0].ID)
	require.NoError(t, err)
	lease, err := r.borrowBrowser(t.Context(), false)
	require.NoError(t, err)
	b := lease.accountBrowser
	lease.Close()
	m.factory = func(accounts.Account, string, bool, []byte) (accountBrowser, error) {
		return nil, errors.New("不应重复启动浏览器")
	}
	m.checkIdentity = func(ctx context.Context, r *accountRuntime, actual accountBrowser) error {
		if actual != b {
			return fmt.Errorf("没有复用原浏览器")
		}
		return m.store.BindIdentity(r.id, "user-a", "账号 A")
	}
	m.refreshAccountStatuses(t.Context())
	require.False(t, b.Alive())
	require.Nil(t, r.browser)
	require.Nil(t, r.idle)
	a, err := m.store.Get(r.id)
	require.NoError(t, err)
	require.True(t, a.IsLoggedIn)
}

func TestAccountStatusSweepKeepsBrowserWhenExitFails(t *testing.T) {
	m := testAccountManager(t)
	b := &unclosedAccountBrowser{newFakeAccountBrowser()}
	t.Cleanup(func() { b.alive.Store(false) })
	starts := 0
	m.factory = func(accounts.Account, string, bool, []byte) (accountBrowser, error) {
		starts++
		return b, nil
	}
	m.checkIdentity = func(ctx context.Context, r *accountRuntime, _ accountBrowser) error {
		return m.store.BindIdentity(r.id, "user-a", "账号 A")
	}
	m.refreshAccountStatuses(t.Context())
	m.refreshAccountStatuses(t.Context())
	r, err := m.runtime(m.store.List()[0].ID)
	require.NoError(t, err)
	require.Same(t, b, r.browser)
	require.Equal(t, 1, starts, "旧进程存活时不能再打开相同 profile")
	require.Error(t, m.Close())
	database, err := db.Open(filepath.Join(filepath.Dir(m.store.Root()), "studio.db"))
	require.NoError(t, err)
	other, err := accounts.Open(m.store.Root(), database)
	if other != nil {
		_ = other.Close()
	}
	require.ErrorIs(t, err, accounts.ErrLocked)
	require.NoError(t, database.Close())
	b.alive.Store(false)
	require.NoError(t, m.Close())
}

func TestAccountStatusMonitorStartsImmediatelyAndDoesNotOverlap(t *testing.T) {
	m := testAccountManager(t)
	var calls atomic.Int32
	first, next, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	m.checkIdentity = func(ctx context.Context, r *accountRuntime, _ accountBrowser) error {
		switch calls.Add(1) {
		case 1:
			close(first)
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
		case 2:
			close(next)
		}
		return m.store.BindIdentity(r.id, "user-a", "账号 A")
	}
	m.startStatusMonitor(10 * time.Millisecond)
	m.startStatusMonitor(time.Millisecond)
	select {
	case <-first:
	case <-time.After(2 * time.Second):
		t.Fatal("启动后没有立即巡检")
	}
	select {
	case <-next:
		t.Fatal("上一轮未结束时启动了另一轮巡检")
	case <-time.After(40 * time.Millisecond):
	}
	close(release)
	select {
	case <-next:
	case <-time.After(2 * time.Second):
		t.Fatal("没有执行下一轮巡检")
	}
	require.NoError(t, m.Close())
}

type statusShutdownBrowser struct {
	*fakeAccountBrowser
	started chan struct{}
	allow   chan struct{}
	once    sync.Once
}

func (b *statusShutdownBrowser) Shutdown(ctx context.Context) error {
	b.once.Do(func() { close(b.started) })
	select {
	case <-b.allow:
		return b.fakeAccountBrowser.Shutdown(ctx)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestAccountStatusMonitorCancelsBeforeReleasingStore(t *testing.T) {
	m := testAccountManager(t)
	b := &statusShutdownBrowser{fakeAccountBrowser: newFakeAccountBrowser(), started: make(chan struct{}), allow: make(chan struct{})}
	var allowOnce sync.Once
	allowExit := func() { allowOnce.Do(func() { close(b.allow) }) }
	t.Cleanup(allowExit)
	m.factory = func(accounts.Account, string, bool, []byte) (accountBrowser, error) { return b, nil }
	checking := make(chan struct{})
	m.checkIdentity = func(ctx context.Context, _ *accountRuntime, _ accountBrowser) error {
		close(checking)
		<-ctx.Done()
		return ctx.Err()
	}
	m.startStatusMonitor(time.Hour)
	select {
	case <-checking:
	case <-time.After(2 * time.Second):
		t.Fatal("巡检未开始")
	}
	closed := make(chan error, 1)
	go func() { closed <- m.Close() }()
	select {
	case <-b.started:
	case <-time.After(2 * time.Second):
		t.Fatal("关闭服务未取消巡检")
	}
	database, err := db.Open(filepath.Join(filepath.Dir(m.store.Root()), "studio.db"))
	require.NoError(t, err)
	other, err := accounts.Open(m.store.Root(), database)
	if other != nil {
		_ = other.Close()
	}
	require.ErrorIs(t, err, accounts.ErrLocked)
	require.NoError(t, database.Close())
	allowExit()
	select {
	case err := <-closed:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("巡检退出后服务没有完成关闭")
	}
	require.False(t, b.Alive())
	reopenedDB, err := db.Open(filepath.Join(filepath.Dir(m.store.Root()), "studio.db"))
	require.NoError(t, err)
	other, err = accounts.Open(m.store.Root(), reopenedDB)
	require.NoError(t, err)
	require.NoError(t, other.Close())
	require.NoError(t, reopenedDB.Close())
}
