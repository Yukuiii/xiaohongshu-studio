//go:build integration

package xiaohongshu

import (
	"context"
	"testing"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
	"github.com/stretchr/testify/require"
	xhserrors "github.com/xpzouying/xiaohongshu-mcp/errors"
)

const creatorUploadFixture = `<div class="upload-content">上传图文</div>`
const creatorLoginFixture = `<div class="login-container"><div class="sso-login-wrapper">短信登录</div></div>`

// 所有请求都使用本地页面，不读取真实账号数据，也不上传或发布内容。
func newCreatorFixturePage(t *testing.T, b *rod.Browser, body string) *rod.Page {
	t.Helper()
	page := b.MustPage()
	t.Cleanup(func() { _ = page.Close() })
	router := page.HijackRequests()
	router.MustAdd("*", func(h *rod.Hijack) {
		h.OnError = func(error) {}
		h.Response.SetHeader("Content-Type", "text/html; charset=utf-8")
		h.Response.SetBody(`<!doctype html><meta charset="utf-8">` + body)
	})
	go router.Run()
	t.Cleanup(func() { _ = router.Stop() })
	return page
}

func TestCreatorPublishPageLocal(t *testing.T) {
	b := newSearchFixtureBrowser(t)
	cases := []struct {
		name        string
		body        string
		cookie      bool
		wantErr     error
		wantTimeout bool
	}{
		{name: "无需旧Cookie也可发布", body: creatorUploadFixture},
		{
			name: "等待上传区域渲染",
			body: `<main>加载中</main><script>setTimeout(() => {
				document.querySelector('main').innerHTML = '<div class="upload-content">上传图文</div>';
			}, 300)</script>`,
		},
		{
			name: "隐藏上传区域不能提前就绪",
			body: `<div class="upload-content" style="display:none">上传图文</div>
			<script>setTimeout(() => document.querySelector('.upload-content').style.display = 'block', 300)</script>`,
		},
		{
			name: "隐藏登录模板不影响发布",
			body: `<div style="display:none">` + creatorLoginFixture + `</div>` + creatorUploadFixture,
		},
		{name: "真实登录页", body: creatorLoginFixture, wantErr: xhserrors.ErrLoginRequired},
		{
			name:    "登录浮层优先于背景上传区域",
			body:    creatorUploadFixture + creatorLoginFixture,
			wantErr: xhserrors.ErrLoginRequired,
		},
		{
			name:    "旧Cookie不能证明页面已登录",
			body:    creatorLoginFixture,
			cookie:  true,
			wantErr: xhserrors.ErrLoginRequired,
		},
		{
			name: "延迟跳转到登录页",
			body: `<script>if (location.pathname !== '/login') {
				setTimeout(() => location.href = '/login', 300);
			} else document.write('` + creatorLoginFixture + `');</script>`,
			wantErr: xhserrors.ErrLoginRequired,
		},
		{
			name: "安全验证跳转",
			body: `<script>if (location.pathname !== '/website-login/captcha') {
				setTimeout(() => location.href = '/website-login/captcha', 300);
			}</script>`,
			wantErr: xhserrors.ErrVerificationRequired,
		},
		{name: "加载超时不能报登录失效", body: `<main>加载中</main>`, wantTimeout: true},
		{name: "隐藏上传区域不能算成功", body: `<div class="upload-content" style="display:none">上传图文</div>`, wantTimeout: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.NoError(t, b.SetCookies(nil))
			if tc.cookie {
				require.NoError(t, b.SetCookies([]*proto.NetworkCookieParam{{
					Name: "galaxy_creator_session_id", Value: "expired-fixture", Domain: ".xiaohongshu.com", Path: "/",
				}}))
			}
			page := newCreatorFixturePage(t, b, tc.body)
			timeout := 5 * time.Second
			if tc.wantTimeout {
				timeout = 800 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(t.Context(), timeout)
			defer cancel()
			err := openCreatorPublishPage(page.Context(ctx))
			switch {
			case tc.wantErr != nil:
				require.ErrorIs(t, err, tc.wantErr)
			case tc.wantTimeout:
				require.ErrorIs(t, err, context.DeadlineExceeded)
				require.NotErrorIs(t, err, xhserrors.ErrLoginRequired)
			default:
				require.NoError(t, err)
				visible, err := page.MustElement("div.upload-content").Visible()
				require.NoError(t, err)
				require.True(t, visible)
			}
		})
	}
}

func TestCreatorPublishPageLocalCancellation(t *testing.T) {
	b := newSearchFixtureBrowser(t)
	page := newCreatorFixturePage(t, b, `<main>加载中</main>`)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	timer := time.AfterFunc(300*time.Millisecond, cancel)
	defer timer.Stop()
	err := openCreatorPublishPage(page.Context(ctx))
	require.ErrorIs(t, err, context.Canceled)
	require.NotErrorIs(t, err, xhserrors.ErrLoginRequired)
}

func TestPublishActionsLocalWithoutCreatorCookie(t *testing.T) {
	b := newSearchFixtureBrowser(t)
	for _, tc := range []struct {
		name string
		open func(*rod.Page) (*PublishAction, error)
	}{
		{"上传图文", NewPublishImageAction},
		{"上传视频", NewPublishVideoAction},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page := newCreatorFixturePage(t, b, `<div class="upload-content">
				<div class="creator-tab" style="width:120px;height:40px" onclick="document.body.dataset.tab=this.innerText">上传图文</div>
				<div class="creator-tab" style="width:120px;height:40px" onclick="document.body.dataset.tab=this.innerText">上传视频</div>
			</div>`)
			cookies, err := b.GetCookies()
			require.NoError(t, err)
			require.Empty(t, cookies)
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			action, err := tc.open(page.Context(ctx))
			require.NoError(t, err)
			require.NotNil(t, action)
			selected, err := page.MustElement("body").Attribute("data-tab")
			require.NoError(t, err)
			require.NotNil(t, selected)
			require.Equal(t, tc.name, *selected)
		})
	}
}
