package main

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAccountShutdownWaitsForTaskAndRejectsLateLogin(t *testing.T) {
	manager := testAccountManager(t)
	r, err := manager.runtime(manager.store.List()[0].ID)
	require.NoError(t, err)
	release, err := r.lease(true)
	require.NoError(t, err)
	closed := make(chan error, 1)
	go func() { closed <- manager.Close() }()
	require.Eventually(t, manager.closing.Load, time.Second, time.Millisecond)
	_, err = r.lease(true)
	require.Error(t, err)
	b := newFakeAccountBrowser()
	require.Error(t, r.monitorLogin(nil, b, "candidate", "session", "previous"), "关闭后不能再登记后台登录")
	require.NoError(t, b.Shutdown(context.Background()))
	select {
	case <-closed:
		t.Fatal("尚有任务时提前释放了账号数据根")
	case <-time.After(20 * time.Millisecond):
	}
	release()
	select {
	case err := <-closed:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("任务释放后服务未结束")
	}
}

func TestDesktopLeaseIsHeldUntilOldBrowserActuallyStops(t *testing.T) {
	manager := testAccountManager(t)
	a := manager.store.List()[0]
	r, err := manager.runtime(a.ID)
	require.NoError(t, err)
	first := "11111111111111111111111111111111"
	second := "22222222222222222222222222222222"
	require.NoError(t, manager.reserveVisual(a.ID, first))
	b := newFakeAccountBrowser()
	r.finishVisual(b, first)
	require.True(t, manager.visualActive(a.ID, first))
	require.Equal(t, "LOGIN_BROWSER_BUSY", accountFailure(manager.reserveVisual("other", second)).Code)
	require.NoError(t, r.closeLogin(context.Background(), first))
	require.False(t, b.Alive())
	require.False(t, manager.visualActive(a.ID, first))
	require.NoError(t, manager.reserveVisual("other", second))
	manager.releaseVisual("other", second)
}

func TestAccountCanRestartAfterBrowserProcessCrash(t *testing.T) {
	manager := testAccountManager(t)
	r, err := manager.runtime(manager.store.List()[0].ID)
	require.NoError(t, err)
	first, err := r.borrowBrowser(context.Background(), false)
	require.NoError(t, err)
	before := first.accountBrowser.(*fakeAccountBrowser)
	first.Close()
	before.alive.Store(false)
	second, err := r.borrowBrowser(context.Background(), false)
	require.NoError(t, err)
	require.NotSame(t, before, second.accountBrowser)
	second.Close()
}
