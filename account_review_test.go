package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/xpzouying/xiaohongshu-mcp/accounts"
)

func TestAccountIdentityIsCheckedForEveryTask(t *testing.T) {
	manager := testAccountManager(t)
	r, err := manager.runtime(manager.store.List()[0].ID)
	require.NoError(t, err)
	changed := false
	checks := 0
	manager.checkIdentity = func(context.Context, *accountRuntime, accountBrowser) error {
		checks++
		if changed {
			return accounts.ErrIdentityMismatch
		}
		return nil
	}
	first, err := r.borrowBrowser(context.Background(), true)
	require.NoError(t, err)
	first.Close()
	changed = true
	_, err = r.borrowBrowser(context.Background(), true)
	require.ErrorIs(t, err, accounts.ErrIdentityMismatch)
	require.Equal(t, 2, checks, "不能缓存上一个任务的账号身份")
	_, err = r.borrowBrowser(context.Background(), true)
	require.ErrorIs(t, err, accounts.ErrIdentityMismatch)
	require.Equal(t, 3, checks)
}

func TestAccountShutdownRetainsLockWhileBrowserStarts(t *testing.T) {
	manager := testAccountManager(t)
	manager.drainTimeout = 10 * time.Millisecond
	r, err := manager.runtime(manager.store.List()[0].ID)
	require.NoError(t, err)
	started, allow := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(allow) }) }
	defer unblock()
	b := newFakeAccountBrowser()
	manager.factory = func(accounts.Account, string, bool, []byte) (accountBrowser, error) {
		close(started)
		<-allow
		return b, nil
	}
	release, err := r.lease(true)
	require.NoError(t, err)
	result := make(chan error, 1)
	go func() {
		defer release()
		lease, err := r.borrowBrowser(context.Background(), false)
		if err == nil {
			lease.Close()
		}
		result <- err
	}()
	<-started
	closed := make(chan error, 1)
	go func() { closed <- manager.Close() }()
	require.Eventually(t, manager.closing.Load, time.Second, time.Millisecond)
	select {
	case <-closed:
		t.Fatal("浏览器还在启动时提前关闭")
	case <-time.After(60 * time.Millisecond):
	}
	other, err := accounts.Open(manager.store.Root(), filepath.Join(t.TempDir(), "missing"), 0)
	if other != nil {
		_ = other.Close()
	}
	require.ErrorIs(t, err, accounts.ErrLocked, "排空超时也不能释放数据根锁")
	unblock()
	require.Error(t, <-result)
	select {
	case err := <-closed:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("启动返回后没有完成清理")
	}
	require.False(t, b.Alive(), "关闭期间启动的浏览器必须先退出")
}

type exitedAccountBrowser struct{ *fakeAccountBrowser }

func (b *exitedAccountBrowser) Shutdown(context.Context) error {
	return errors.New("历史异常退出码")
}

func TestCrashedAccountBrowserDoesNotBlockMaintenance(t *testing.T) {
	for _, remove := range []bool{false, true} {
		t.Run(map[bool]string{false: "reset", true: "delete"}[remove], func(t *testing.T) {
			manager := testAccountManager(t)
			a := manager.store.List()[0]
			r, err := manager.runtime(a.ID)
			require.NoError(t, err)
			b := &exitedAccountBrowser{newFakeAccountBrowser()}
			b.alive.Store(false)
			r.browser = b
			require.NoError(t, r.maintenance(context.Background(), remove))
			current, err := manager.store.Get(a.ID)
			if remove {
				require.ErrorIs(t, err, accounts.ErrNotFound)
			} else {
				require.NoError(t, err)
				require.NotEqual(t, a.ActiveProfile, current.ActiveProfile)
			}
		})
	}
}

func TestCrashedAccountBrowserCanBeginRelogin(t *testing.T) {
	manager := testAccountManager(t)
	r, err := manager.runtime(manager.store.List()[0].ID)
	require.NoError(t, err)
	old := &exitedAccountBrowser{newFakeAccountBrowser()}
	old.alive.Store(false)
	r.browser = old
	started := false
	manager.factory = func(accounts.Account, string, bool, []byte) (accountBrowser, error) {
		started = true
		return newFakeAccountBrowser(), nil
	}
	_, err = r.openLogin(context.Background())
	require.True(t, started, "历史退出错误不能挡住新登录窗口的创建")
	require.ErrorContains(t, err, "测试不应启动页面")
}

type unclosedAccountBrowser struct{ *fakeAccountBrowser }

func (b *unclosedAccountBrowser) Shutdown(context.Context) error {
	return errors.New("模拟进程未能退出")
}

func TestLoginCloseReportsSurvivingOrphan(t *testing.T) {
	manager := testAccountManager(t)
	r, err := manager.runtime(manager.store.List()[0].ID)
	require.NoError(t, err)
	b := &unclosedAccountBrowser{newFakeAccountBrowser()}
	t.Cleanup(func() { b.alive.Store(false) })
	session := "11111111111111111111111111111111"
	require.NoError(t, manager.reserveVisual(r.id, session))
	cancelled, done := make(chan struct{}), make(chan struct{})
	r.service.logins.start(func() { close(cancelled) }, time.Now().Add(time.Minute))
	r.loginID = session
	r.loginDone = done
	go func() { <-cancelled; r.finishVisual(b, session); close(done) }()
	err = r.closeLogin(context.Background(), session)
	require.Equal(t, "BROWSER_CLOSE_FAILED", accountFailure(err).Code)
	require.True(t, manager.visualActive(r.id, session))
	r.mu.Lock()
	current := r.loginID
	r.mu.Unlock()
	require.Equal(t, session, current)
	b.alive.Store(false)
	require.NoError(t, r.closeLogin(context.Background(), session))
	require.False(t, manager.visualActive(r.id, session))
}

func TestLegacyImageMigrationRepairsPartialTarget(t *testing.T) {
	manager := testAccountManager(t)
	data := testPNG(t)
	image, err := saveWorkbenchImage(data, "旧图片")
	require.NoError(t, err)
	dest, err := imageStorageDirForAccount(manager.store.LegacyAccountID())
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(dest, 0700))
	target := filepath.Join(dest, image.ID)
	require.NoError(t, os.WriteFile(target, data[:5], 0600))
	require.NoError(t, manager.migrateLegacyImages())
	result, err := os.ReadFile(target)
	require.NoError(t, err)
	require.True(t, bytes.Equal(data, result))
	leftovers, err := filepath.Glob(filepath.Join(dest, ".image-import-*"))
	require.NoError(t, err)
	require.Empty(t, leftovers)
}

func TestFailedLegacyImageCopyKeepsExistingTarget(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "not-a-file")
	require.NoError(t, os.Mkdir(source, 0700))
	target := filepath.Join(dir, "target.png")
	require.NoError(t, os.WriteFile(target, []byte("原文件"), 0600))
	require.Error(t, copyLegacyImage(source, target))
	data, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Equal(t, "原文件", string(data))
	leftovers, err := filepath.Glob(filepath.Join(dir, ".image-import-*"))
	require.NoError(t, err)
	require.Empty(t, leftovers)
}
