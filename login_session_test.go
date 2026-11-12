package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xpzouying/xiaohongshu-mcp/xiaohongshu"
)

func TestLoginSessionsLifecycle(t *testing.T) {
	var sessions loginSessions
	state, active := sessions.snapshot()
	require.Empty(t, state.phase)
	require.False(t, active)
	closed := 0
	expiresAt := time.Now().Add(loginBrowserTimeout)
	seq := sessions.start(func() { closed++ }, expiresAt)
	state, active = sessions.snapshot()
	require.True(t, active)
	require.Equal(t, expiresAt, state.expiresAt)
	require.Equal(t, xiaohongshu.LoginWaitingScan, state.phase)
	for _, phase := range []xiaohongshu.LoginPhase{
		xiaohongshu.LoginWaitingConfirmation,
		xiaohongshu.LoginVerificationRequired,
		xiaohongshu.LoginSaving,
	} {
		require.True(t, sessions.setPhase(seq, phase))
		require.False(t, sessions.setPhase(seq, phase))
	}
	sessions.close()
	sessions.close()
	require.Equal(t, 1, closed)
	_, active = sessions.snapshot()
	require.False(t, active)
	require.False(t, sessions.setPhase(seq, xiaohongshu.LoginLoggedIn))
}

func TestLoginSessionsRejectStaleUpdatesAndSaves(t *testing.T) {
	var sessions loginSessions
	oldClosed, newClosed := 0, 0
	oldSeq := sessions.start(func() { oldClosed++ }, time.Now().Add(time.Minute))
	newSeq := sessions.start(func() { newClosed++ }, time.Now().Add(time.Minute))
	require.Equal(t, 1, oldClosed)
	sessions.finish(oldSeq)
	require.False(t, sessions.setPhase(oldSeq, xiaohongshu.LoginLoggedIn))
	persisted := false
	saved, err := sessions.save(oldSeq, t.Context(), xiaohongshu.CurrentUser{}, func() error {
		persisted = true
		return nil
	})
	require.NoError(t, err)
	require.False(t, saved)
	require.False(t, persisted, "旧会话不能覆盖新会话的 cookie")
	state, active := sessions.snapshot()
	require.True(t, active)
	require.Equal(t, newSeq, state.seq)
	require.Equal(t, xiaohongshu.LoginWaitingScan, state.phase)
	sessions.close()
	require.Equal(t, 1, newClosed)
}

func TestLoginSessionsSaveAndExpiry(t *testing.T) {
	t.Run("未保存的会话到期", func(t *testing.T) {
		var sessions loginSessions
		seq := sessions.start(func() {}, time.Now().Add(time.Minute))
		sessions.setPhase(seq, xiaohongshu.LoginVerificationRequired)
		sessions.expire(seq)
		sessions.finish(seq)
		state, active := sessions.snapshot()
		require.False(t, active)
		require.Equal(t, xiaohongshu.LoginExpired, state.phase)
	})
	t.Run("保存后结束会话保留成功状态", func(t *testing.T) {
		var sessions loginSessions
		seq := sessions.start(func() {}, time.Now().Add(time.Minute))
		user := xiaohongshu.CurrentUser{Nickname: "本地账号", UserID: "local-user"}
		saved, err := sessions.save(seq, t.Context(), user, func() error { return nil })
		require.NoError(t, err)
		require.True(t, saved)
		sessions.finish(seq)
		sessions.expire(seq)
		state, active := sessions.snapshot()
		require.False(t, active)
		require.Equal(t, xiaohongshu.LoginLoggedIn, state.phase)
		require.Equal(t, user, state.user)
	})
	t.Run("保存失败不能伪成功", func(t *testing.T) {
		var sessions loginSessions
		seq := sessions.start(func() {}, time.Now().Add(time.Minute))
		sessions.setPhase(seq, xiaohongshu.LoginSaving)
		wantErr := errors.New("local save failed")
		saved, err := sessions.save(seq, t.Context(), xiaohongshu.CurrentUser{}, func() error { return wantErr })
		require.ErrorIs(t, err, wantErr)
		require.False(t, saved)
		state, _ := sessions.snapshot()
		require.Equal(t, xiaohongshu.LoginSaving, state.phase)
		sessions.expire(seq)
		state, _ = sessions.snapshot()
		require.Equal(t, xiaohongshu.LoginExpired, state.phase)
	})
	for _, reason := range []string{"closed", "canceled", "expired"} {
		t.Run(reason+"不能保存", func(t *testing.T) {
			var sessions loginSessions
			expiresAt := time.Now().Add(time.Minute)
			if reason == "expired" {
				expiresAt = time.Now().Add(-time.Minute)
			}
			seq := sessions.start(func() {}, expiresAt)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if reason == "closed" {
				sessions.close()
			} else if reason == "canceled" {
				cancel()
			}
			saved, err := sessions.save(seq, ctx, xiaohongshu.CurrentUser{}, func() error {
				t.Fatal("失效会话不能写 cookie")
				return nil
			})
			require.NoError(t, err)
			require.False(t, saved)
		})
	}
}

func TestLoginSessionsSaveSerializesClose(t *testing.T) {
	var sessions loginSessions
	seq := sessions.start(func() {}, time.Now().Add(time.Minute))
	writing, release, savedDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(savedDone)
		saved, err := sessions.save(seq, t.Context(), xiaohongshu.CurrentUser{}, func() error {
			close(writing)
			<-release
			return nil
		})
		assert.NoError(t, err)
		assert.True(t, saved)
	}()
	<-writing
	closing, closed := make(chan struct{}), make(chan struct{})
	go func() {
		close(closing)
		sessions.close()
		close(closed)
	}()
	<-closing
	select {
	case <-closed:
		t.Error("关闭必须等当前写入结束，防止之后才覆盖新会话")
	default:
	}
	close(release)
	<-savedDone
	<-closed
	_, active := sessions.snapshot()
	require.False(t, active)
}

func TestLoginSessionsRejectStalePhasesConcurrently(t *testing.T) {
	var sessions loginSessions
	const workers = 50
	ready := make(chan struct{})
	var wg sync.WaitGroup
	var persisted atomic.Int32
	for range workers {
		seq := sessions.start(func() {}, time.Now().Add(time.Minute))
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-ready
			assert.False(t, sessions.setPhase(seq, xiaohongshu.LoginLoggedIn))
			saved, err := sessions.save(seq, t.Context(), xiaohongshu.CurrentUser{}, func() error {
				persisted.Add(1)
				return nil
			})
			assert.NoError(t, err)
			assert.False(t, saved)
			sessions.finish(seq)
		}()
	}
	seq := sessions.start(func() {}, time.Now().Add(time.Minute))
	close(ready)
	wg.Wait()
	require.Zero(t, persisted.Load())
	require.True(t, sessions.setPhase(seq, xiaohongshu.LoginVerificationRequired))
	state, active := sessions.snapshot()
	require.True(t, active)
	require.Equal(t, xiaohongshu.LoginVerificationRequired, state.phase)
}

func TestLoginBrowserReusesActiveSession(t *testing.T) {
	service := NewXiaohongshuService()
	closed := 0
	expiresAt := time.Now().Add(loginBrowserTimeout)
	seq := service.logins.start(func() { closed++ }, expiresAt)
	service.logins.setPhase(seq, xiaohongshu.LoginVerificationRequired)
	defer service.CloseLoginBrowser()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for range 3 {
		response, err := service.OpenLoginBrowser(ctx)
		require.NoError(t, err, "复用不会导航或访问请求上下文")
		require.Equal(t, xiaohongshu.LoginVerificationRequired, response.Status)
		require.False(t, response.IsLoggedIn)
		require.Contains(t, response.Message, "已登录该账号")
		require.NotEmpty(t, response.Timeout)
		state, active := service.logins.snapshot()
		require.True(t, active)
		require.Equal(t, seq, state.seq)
		require.Equal(t, expiresAt, state.expiresAt, "复用不延长会话")
	}
	require.Zero(t, closed)
	require.Equal(t, 20*time.Minute, loginBrowserTimeout)
}

func TestCheckLoginStatusUsesActiveSession(t *testing.T) {
	service := NewXiaohongshuService()
	seq := service.logins.start(func() {}, time.Now().Add(time.Minute))
	defer service.CloseLoginBrowser()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, phase := range []xiaohongshu.LoginPhase{
		xiaohongshu.LoginWaitingScan,
		xiaohongshu.LoginWaitingConfirmation,
		xiaohongshu.LoginVerificationRequired,
		xiaohongshu.LoginSaving,
		xiaohongshu.LoginExpired,
		xiaohongshu.LoginFailed,
		xiaohongshu.LoginLoggedIn,
	} {
		t.Run(string(phase), func(t *testing.T) {
			service.logins.setPhase(seq, phase)
			status, err := service.CheckLoginStatus(ctx)
			require.NoError(t, err)
			require.Equal(t, phase, status.Status)
			require.Equal(t, phase == xiaohongshu.LoginLoggedIn, status.IsLoggedIn)
		})
	}
}

func TestCloseLoginBrowserPreservesCookies(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookies.json")
	t.Setenv("COOKIES_PATH", path)
	data := []byte(`{"seed":123,"cookies":[]}`)
	require.NoError(t, os.WriteFile(path, data, 0600))
	service := NewXiaohongshuService()
	closed := 0
	service.logins.start(func() { closed++ }, time.Now().Add(time.Minute))
	service.CloseLoginBrowser()
	service.CloseLoginBrowser()
	require.Equal(t, 1, closed)
	_, active := service.logins.snapshot()
	require.False(t, active)
	stored, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, data, stored)
}

func TestDeleteCookiesClosesLoginBrowser(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookies.json")
	t.Setenv("COOKIES_PATH", path)
	require.NoError(t, os.WriteFile(path, []byte(`{"seed":123,"cookies":[]}`), 0600))
	service := NewXiaohongshuService()
	closed := false
	seq := service.logins.start(func() { closed = true }, time.Now().Add(time.Minute))
	require.NoError(t, service.DeleteCookies(t.Context()))
	require.True(t, closed)
	_, active := service.logins.snapshot()
	require.False(t, active)
	saved, err := service.logins.save(seq, t.Context(), xiaohongshu.CurrentUser{}, func() error {
		t.Fatal("重置后的旧浏览器不能恢复 cookie")
		return nil
	})
	require.NoError(t, err)
	require.False(t, saved)
}

func TestLoginBrowserMonitorPanicRecordsFailure(t *testing.T) {
	service := NewXiaohongshuService()
	var closed atomic.Int32
	// nil action 模拟监控内部 panic，不启动浏览器或访问外部站点。
	service.startLoginSession(nil, nil, func() { closed.Add(1) }, loginBrowserTimeout)
	defer service.CloseLoginBrowser()
	require.Eventually(t, func() bool {
		state, active := service.logins.snapshot()
		return state.phase == xiaohongshu.LoginFailed && !active
	}, time.Second, time.Millisecond)
	require.EqualValues(t, 1, closed.Load())
}
