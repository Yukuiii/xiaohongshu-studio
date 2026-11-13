//go:build integration

package xiaohongshu

import (
	"context"
	"testing"
	"time"

	"github.com/go-rod/rod"
	"github.com/stretchr/testify/require"
)

const loginFixtureWaiting = `<main><div class="login-container">请使用小红书扫码登录</div></main>`
const loginFixtureConfirmed = `<main class="main-container"><div class="user"><div class="link-wrapper"><a class="channel">我</a></div></div></main>`
const loginFixtureVerification = loginFixtureConfirmed + `<aside role="dialog">请通过验证<br>使用已登录该账号的小红书APP扫码验证身份</aside>`

func TestWaitForLoginLocalProgress(t *testing.T) {
	b := newSearchFixtureBrowser(t)
	page := b.MustPage()
	defer page.Close()
	require.NoError(t, page.SetDocumentContent(loginFixtureWaiting))
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	progress := make(chan LoginPhase, 8)
	done := make(chan error, 1)
	go func() {
		done <- NewLogin(page).WaitForLogin(ctx, func(phase LoginPhase) {
			progress <- phase
		})
	}()

	awaitPhase := func(want LoginPhase) {
		t.Helper()
		select {
		case phase := <-progress:
			require.Equal(t, want, phase)
		case err := <-done:
			t.Fatalf("尚未手机确认，登录等待提前结束: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	awaitPhase(LoginWaitingScan)
	require.NoError(t, page.SetDocumentContent(`<div class="login-container">扫码成功，请在手机上确认</div>`))
	awaitPhase(LoginWaitingConfirmation)

	// 相同页面至少跨过一轮轮询：确认文案不能算登录成功，也不能重复通知。
	hold := time.NewTimer(650 * time.Millisecond)
	defer hold.Stop()
	select {
	case err := <-done:
		t.Fatalf("仅扫码不能登录成功: %v", err)
	case phase := <-progress:
		t.Fatalf("阶段未变化却再次通知: %s", phase)
	case <-hold.C:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}

	require.NoError(t, page.SetDocumentContent(loginFixtureConfirmed))
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case phase := <-progress:
		t.Fatalf("登录完成不应回退扫码阶段: %s", phase)
	default:
	}
}

func TestWaitForLoginLocalConfirmationDoesNotLogin(t *testing.T) {
	b := newSearchFixtureBrowser(t)
	for _, text := range []string{"扫码成功", "请在手机上确认"} {
		t.Run(text, func(t *testing.T) {
			page := b.MustPage()
			defer page.Close()
			require.NoError(t, page.SetDocumentContent(`<div class="login-container">`+text+`</div>`))
			ctx, cancel := context.WithTimeout(t.Context(), 650*time.Millisecond)
			defer cancel()
			var progress []LoginPhase
			err := NewLogin(page).WaitForLogin(ctx, func(phase LoginPhase) {
				progress = append(progress, phase)
			})
			require.ErrorIs(t, err, context.DeadlineExceeded)
			require.Equal(t, []LoginPhase{LoginWaitingConfirmation}, progress)
		})
	}
}

func TestWaitForLoginLocalCancellation(t *testing.T) {
	b := newSearchFixtureBrowser(t)
	page := b.MustPage()
	defer page.Close()
	require.NoError(t, page.SetDocumentContent(`<div class="login-container">请在手机上确认</div>`))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var progress []LoginPhase
	err := NewLogin(page).WaitForLogin(ctx, func(phase LoginPhase) {
		progress = append(progress, phase)
		cancel()
	})
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, []LoginPhase{LoginWaitingConfirmation}, progress)
}

func TestWaitForLoginLocalPageClosed(t *testing.T) {
	b := newSearchFixtureBrowser(t)
	page := b.MustPage()
	require.NoError(t, page.Close())
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	err := NewLogin(page).WaitForLogin(ctx, nil)
	require.Error(t, err)
	require.NotErrorIs(t, err, context.DeadlineExceeded)
}

func TestCheckLoginStatusLocalUninitializedPage(t *testing.T) {
	b := newSearchFixtureBrowser(t)
	page := b.MustPage()
	defer page.Close()
	router := page.HijackRequests()
	router.MustAdd("*", func(h *rod.Hijack) {
		h.OnError = func(error) {}
		h.Response.SetHeader("Content-Type", "text/html; charset=utf-8")
		h.Response.SetBody(`<main>加载中</main>`)
	})
	go router.Run()
	defer router.Stop()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	loggedIn, err := NewLogin(page).CheckLoginStatus(ctx)
	require.False(t, loggedIn)
	require.ErrorIs(t, err, context.DeadlineExceeded, "缺少登录标记的未初始化页面不能判为未登录")
}

func TestCheckLoginStatusLocal(t *testing.T) {
	b := newSearchFixtureBrowser(t)
	for _, tc := range []struct {
		name     string
		html     string
		loggedIn bool
	}{
		{"waiting_scan", loginFixtureWaiting, false},
		{"guest", `<script>window.__INITIAL_STATE__ = {user: {userInfo: {value: {guest: true}}}};</script>`, false},
		{"delayed_login", `<main id="loading"></main><script>setTimeout(() => { document.body.innerHTML = '` + loginFixtureConfirmed + `'; }, 300);</script>`, true},
		{"waiting_confirmation", `<div class="login-container">扫码成功，请在手机上确认</div>`, false},
		{"logged_in", loginFixtureConfirmed, true},
		{"verification_required", loginFixtureVerification, false},
		{"verification_title", loginFixtureConfirmed + `<aside>请通过验证</aside>`, false},
		{"verification_identity", loginFixtureConfirmed + `<aside>使用已登录该账号的小红书 APP 扫码验证身份</aside>`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page := b.MustPage()
			defer page.Close()
			// 拦截全部请求，生产登录检查的导航只会得到本地 fixture。
			router := page.HijackRequests()
			router.MustAdd("*", func(h *rod.Hijack) {
				h.OnError = func(error) {}
				h.Response.SetHeader("Content-Type", "text/html; charset=utf-8")
				h.Response.SetBody(tc.html)
			})
			go router.Run()
			defer router.Stop()
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			loggedIn, err := NewLogin(page).CheckLoginStatus(ctx)
			require.NoError(t, err)
			require.Equal(t, tc.loggedIn, loggedIn)
		})
	}
}

func TestWaitForLoginLocalVerificationBeforeBackgroundLogin(t *testing.T) {
	b := newSearchFixtureBrowser(t)
	page := b.MustPage()
	defer page.Close()
	require.NoError(t, page.SetDocumentContent(loginFixtureVerification))
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	progress := make(chan LoginPhase, 4)
	done := make(chan error, 1)
	go func() {
		done <- NewLogin(page).WaitForLogin(ctx, func(phase LoginPhase) { progress <- phase })
	}()
	select {
	case phase := <-progress:
		require.Equal(t, LoginVerificationRequired, phase)
	case err := <-done:
		t.Fatalf("验证浮层未消失却已经完成登录: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	// 背景侧栏已经存在，浮层仍应阻止成功。
	hold := time.NewTimer(650 * time.Millisecond)
	defer hold.Stop()
	select {
	case err := <-done:
		t.Fatalf("验证未完成却提前结束: %v", err)
	case <-hold.C:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	require.NoError(t, page.SetDocumentContent(loginFixtureConfirmed))
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func TestWaitForLoginLocalRequiresStableLogin(t *testing.T) {
	b := newSearchFixtureBrowser(t)
	page := b.MustPage()
	defer page.Close()
	require.NoError(t, page.SetDocumentContent(loginFixtureConfirmed))
	ctx, cancel := context.WithTimeout(t.Context(), 250*time.Millisecond)
	defer cancel()
	err := NewLogin(page).WaitForLogin(ctx, nil)
	require.ErrorIs(t, err, context.DeadlineExceeded, "一次观察到背景登录标记不能算稳定登录")
}

func TestOpenLoginLocalWithoutQrcode(t *testing.T) {
	b := newSearchFixtureBrowser(t)
	for _, html := range []string{
		`<main>真实页面尚未显示二维码</main>`, loginFixtureVerification, loginFixtureConfirmed,
	} {
		page := b.MustPage()
		func() {
			defer page.Close()
			router := page.HijackRequests()
			router.MustAdd("*", func(h *rod.Hijack) {
				h.OnError = func(error) {}
				h.Response.SetHeader("Content-Type", "text/html; charset=utf-8")
				h.Response.SetBody(html)
			})
			go router.Run()
			defer router.Stop()
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			require.NoError(t, NewLogin(page).Open(ctx))
			require.NoError(t, page.Context(ctx).WaitLoad())
		}()
	}
}
