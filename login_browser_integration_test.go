//go:build integration

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/cdp"
	"github.com/go-rod/rod/lib/defaults"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
	"github.com/stretchr/testify/require"
	"github.com/xpzouying/xiaohongshu-mcp/browser"
	"github.com/xpzouying/xiaohongshu-mcp/configs"
	"github.com/xpzouying/xiaohongshu-mcp/cookies"
	"github.com/xpzouying/xiaohongshu-mcp/xiaohongshu"
)

const sessionFixtureLoggedIn = `<main class="main-container"><div class="user"><div class="link-wrapper"><a class="channel">我</a></div></div></main><script>window.__INITIAL_STATE__ = {user: {userInfo: {value: {nickname: "本地账号", userId: "local-user"}}}};</script>`
const sessionFixtureVerification = sessionFixtureLoggedIn + `<aside role="dialog">请通过验证<br>使用已登录该账号的小红书APP扫码验证身份</aside>`

func newLoginSessionFixture(t *testing.T, wrapClient ...func(rod.CDPClient) rod.CDPClient) (*rod.Browser, *rod.Page) {
	t.Helper()
	bin := os.Getenv("XHS_TEST_BROWSER")
	if bin == "" {
		var ok bool
		bin, ok = launcher.LookPath()
		if !ok {
			t.Skip("未找到本地浏览器，可用 XHS_TEST_BROWSER 指定路径")
		}
	}
	l := launcher.New().Bin(bin).Headless(true)
	controlURL, err := l.Launch()
	require.NoError(t, err)
	b := rod.New().ControlURL(controlURL).NoDefaultDevice()
	if len(wrapClient) > 0 {
		client, err := cdp.StartWithURL(context.Background(), controlURL, nil)
		require.NoError(t, err)
		b = rod.New().Client(wrapClient[0](client)).NoDefaultDevice()
	}
	require.NoError(t, b.Connect())
	t.Cleanup(func() {
		_ = b.Close()
		l.Cleanup()
	})
	return b, b.MustPage()
}

func awaitLoginSession(t *testing.T, service *XiaohongshuService, phase xiaohongshu.LoginPhase, active bool) {
	t.Helper()
	require.Eventually(t, func() bool {
		state, currentActive := service.logins.snapshot()
		return state.phase == phase && currentActive == active
	}, 5*time.Second, 20*time.Millisecond)
}

func setLoginFixtureCookie(t *testing.T, b *rod.Browser, value string) {
	t.Helper()
	require.NoError(t, b.SetCookies([]*proto.NetworkCookieParam{{
		Name: "local_login_fixture", Value: value, Domain: ".xiaohongshu.com", Path: "/",
	}}))
}

func TestLoginBrowserLocalVerificationAndAutoClose(t *testing.T) {
	b, page := newLoginSessionFixture(t)
	cookiePath := filepath.Join(t.TempDir(), "cookies.json")
	t.Setenv("COOKIES_PATH", cookiePath)
	require.NoError(t, page.SetDocumentContent(sessionFixtureVerification))
	setLoginFixtureCookie(t, b, "first")
	service := NewXiaohongshuService()
	var closed atomic.Int32
	service.startLoginSession(xiaohongshu.NewLogin(page), page, func() {
		closed.Add(1)
		_ = b.Close()
	}, loginBrowserTimeout)
	defer service.CloseLoginBrowser()
	awaitLoginSession(t, service, xiaohongshu.LoginVerificationRequired, true)
	_, err := os.Stat(cookiePath)
	require.ErrorIs(t, err, os.ErrNotExist, "验证未完成不能写入登录信息")
	state, _ := service.logins.snapshot()
	response, err := service.OpenLoginBrowser(t.Context())
	require.NoError(t, err)
	require.Equal(t, xiaohongshu.LoginVerificationRequired, response.Status)
	require.False(t, response.IsLoggedIn)
	reused, active := service.logins.snapshot()
	require.True(t, active)
	require.Equal(t, state.seq, reused.seq)
	require.Equal(t, state.expiresAt, reused.expiresAt)
	phase, err := xiaohongshu.NewLogin(page).CurrentPhase(t.Context())
	require.NoError(t, err)
	require.Equal(t, xiaohongshu.LoginVerificationRequired, phase, "复用不能刷新掉当前验证页面")

	require.NoError(t, page.SetDocumentContent(sessionFixtureLoggedIn))
	awaitLoginSession(t, service, xiaohongshu.LoginLoggedIn, false)
	require.EqualValues(t, 1, closed.Load(), "保存成功后应自动关闭浏览器")
	completed, active := service.logins.snapshot()
	require.False(t, active)
	require.Equal(t, "local-user", completed.user.UserID)
	data, err := cookies.NewLoadCookie(cookiePath).LoadCookies()
	require.NoError(t, err)
	var saved []*proto.NetworkCookie
	require.NoError(t, json.Unmarshal(data, &saved))
	found := false
	for _, cookie := range saved {
		if cookie.Name == "local_login_fixture" {
			found = true
			require.Equal(t, "first", cookie.Value)
		}
	}
	require.True(t, found)
	beforeClose, err := os.ReadFile(cookiePath)
	require.NoError(t, err)
	service.CloseLoginBrowser()
	require.EqualValues(t, 1, closed.Load())
	_, active = service.logins.snapshot()
	require.False(t, active)
	afterClose, err := os.ReadFile(cookiePath)
	require.NoError(t, err)
	require.Equal(t, beforeClose, afterClose)
}

func TestLoginBrowserLocalExpiry(t *testing.T) {
	b, page := newLoginSessionFixture(t)
	cookiePath := filepath.Join(t.TempDir(), "cookies.json")
	t.Setenv("COOKIES_PATH", cookiePath)
	require.NoError(t, page.SetDocumentContent(`<main>请使用小红书扫码登录</main>`))
	service := NewXiaohongshuService()
	var closed atomic.Int32
	service.startLoginSession(xiaohongshu.NewLogin(page), page, func() {
		closed.Add(1)
		_ = b.Close()
	}, 2*time.Second)
	defer service.CloseLoginBrowser()
	awaitLoginSession(t, service, xiaohongshu.LoginExpired, false)
	require.EqualValues(t, 1, closed.Load())
	_, err := os.Stat(cookiePath)
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestLoginBrowserLocalSaveFailure(t *testing.T) {
	b, page := newLoginSessionFixture(t)
	parent := filepath.Join(t.TempDir(), "file-not-directory")
	require.NoError(t, os.WriteFile(parent, []byte("fixture"), 0600))
	t.Setenv("COOKIES_PATH", filepath.Join(parent, "cookies.json"))
	require.NoError(t, page.SetDocumentContent(sessionFixtureLoggedIn))
	service := NewXiaohongshuService()
	var closed atomic.Int32
	service.startLoginSession(xiaohongshu.NewLogin(page), page, func() {
		closed.Add(1)
		_ = b.Close()
	}, loginBrowserTimeout)
	defer service.CloseLoginBrowser()
	awaitLoginSession(t, service, xiaohongshu.LoginFailed, false)
	require.EqualValues(t, 1, closed.Load())
}

func TestLoginBrowserClosesBrowserOnNavigationFailure(t *testing.T) {
	if runtime.GOOS == "linux" && os.Getenv("DISPLAY") == "" {
		t.Skip("可视化浏览器回归需要 DISPLAY")
	}
	_, err := browser.EnsureBrowser()
	require.NoError(t, err)
	var sawNavigation atomic.Bool
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodConnect && r.Host == "www.xiaohongshu.com:443" {
			sawNavigation.Store(true)
		}
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer proxy.Close()
	oldProxy, oldDir := configs.Proxy(), defaults.Dir
	defer func() {
		configs.SetProxy(oldProxy)
		defaults.Dir = oldDir
	}()
	configs.SetProxy(proxy.URL)
	profileDir := t.TempDir()
	defaults.Dir = profileDir
	t.Setenv("COOKIES_PATH", filepath.Join(t.TempDir(), "cookies.json"))
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	service := NewXiaohongshuService()
	defer service.CloseLoginBrowser()
	_, err = service.OpenLoginBrowser(ctx)
	require.Error(t, err)
	require.True(t, sawNavigation.Load())
	_, active := service.logins.snapshot()
	require.False(t, active)
	// rod 在浏览器退出后才删除 profile。
	_, err = os.Lstat(profileDir)
	require.ErrorIs(t, err, os.ErrNotExist)
}

type loginCookieContextClient struct {
	rod.CDPClient
	interrupted atomic.Bool
}

func (c *loginCookieContextClient) Call(ctx context.Context, sessionID, method string, params interface{}) ([]byte, error) {
	if method == "Storage.getCookies" && c.interrupted.CompareAndSwap(false, true) {
		return nil, cdp.ErrCtxDestroyed
	}
	return c.CDPClient.Call(ctx, sessionID, method, params)
}

func TestLoginBrowserLocalRetriesTransientCookieContext(t *testing.T) {
	client := &loginCookieContextClient{}
	b, page := newLoginSessionFixture(t, func(inner rod.CDPClient) rod.CDPClient {
		client.CDPClient = inner
		return client
	})
	cookiePath := filepath.Join(t.TempDir(), "cookies.json")
	t.Setenv("COOKIES_PATH", cookiePath)
	require.NoError(t, page.SetDocumentContent(sessionFixtureLoggedIn))
	service := NewXiaohongshuService()
	service.startLoginSession(xiaohongshu.NewLogin(page), page, func() { _ = b.Close() }, loginBrowserTimeout)
	defer service.CloseLoginBrowser()
	awaitLoginSession(t, service, xiaohongshu.LoginLoggedIn, false)
	require.True(t, client.interrupted.Load())
	_, err := os.Stat(cookiePath)
	require.NoError(t, err, "短暂上下文失效后应重试保存，不能卡在 saving")
}
