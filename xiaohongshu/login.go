package xiaohongshu

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/pkg/errors"
)

type LoginAction struct {
	page *rod.Page
}

// LoginPhase 区分扫码、手机确认、账号验证和登录信息保存的进度。
type LoginPhase string

const (
	LoginWaitingScan          LoginPhase = "waiting_scan"
	LoginWaitingConfirmation  LoginPhase = "waiting_confirmation"
	LoginVerificationRequired LoginPhase = "verification_required"
	LoginSaving               LoginPhase = "saving"
	LoginLoggedIn             LoginPhase = "logged_in"
	LoginExpired              LoginPhase = "expired"
	LoginFailed               LoginPhase = "failed"
	LoginNotLoggedIn          LoginPhase = "not_logged_in"
)

func NewLogin(page *rod.Page) *LoginAction {
	return &LoginAction{page: page}
}

// Open 只打开真实登录页面，不依赖二维码或安全验证浮层的样式。
func (a *LoginAction) Open(ctx context.Context) error {
	navigationCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return errors.Wrap(a.page.Context(navigationCtx).Navigate("https://www.xiaohongshu.com/explore"), "open login browser failed")
}

func (a *LoginAction) CheckLoginStatus(ctx context.Context) (bool, error) {
	checkCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := a.Open(checkCtx); err != nil {
		return false, err
	}
	if err := a.page.Context(checkCtx).WaitLoad(); err != nil {
		return false, errors.Wrap(err, "load login page failed")
	}
	page := a.page.Context(checkCtx)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := checkCtx.Err(); err != nil {
			return false, err
		}
		if CheckPageBlocked(page) != nil {
			return false, nil
		}
		phase, err := a.CurrentPhase(checkCtx)
		if err != nil {
			return false, err
		}
		switch phase {
		case LoginLoggedIn:
			return true, nil
		case LoginVerificationRequired, LoginWaitingConfirmation:
			return false, nil
		}
		prompt, _, err := page.Has(".login-container")
		if err != nil {
			return false, err
		}
		if prompt {
			return false, nil
		}
		guest, err := page.Eval(`() => {
			const u = window.__INITIAL_STATE__ && window.__INITIAL_STATE__.user;
			const info = u && u.userInfo && u.userInfo.value !== undefined ? u.userInfo.value : (u && u.userInfo);
			return Boolean(info && info.guest);
		}`)
		if err != nil {
			return false, errors.Wrap(err, "read guest state failed")
		}
		if guest.Value.Bool() {
			return false, nil
		}
		// 尚未初始化的页面不能当作登录已失效。
		select {
		case <-checkCtx.Done():
			return false, checkCtx.Err()
		case <-ticker.C:
		}
	}
}

// CurrentUser 当前登录用户的基础信息。
type CurrentUser struct {
	Nickname string `json:"nickname"`
	UserID   string `json:"userId"`
	Avatar   string `json:"avatar,omitempty"`
}

// CurrentUser 从当前页面的 __INITIAL_STATE__ 读取登录用户信息。
// 需在 CheckLoginStatus 之后调用：复用已加载的 explore 页，不做额外导航。
func (a *LoginAction) CurrentUser(ctx context.Context) (*CurrentUser, error) {
	pp := a.page.Context(ctx).Timeout(10 * time.Second)

	res, err := pp.Eval(`() => {
		const u = window.__INITIAL_STATE__ && window.__INITIAL_STATE__.user;
		const info = u && u.userInfo && u.userInfo.value !== undefined ? u.userInfo.value : (u && u.userInfo);
		if (!info || info.guest) return "";
		return JSON.stringify({nickname: info.nickname, userId: info.userId || info.user_id, avatar: info.images || info.imageb || ""});
	}`)
	if err != nil {
		return nil, errors.Wrap(err, "read current user state failed")
	}

	raw := res.Value.String()
	if raw == "" {
		return nil, errors.New("current user not found in page state")
	}

	var user CurrentUser
	if err := json.Unmarshal([]byte(raw), &user); err != nil {
		return nil, errors.Wrap(err, "unmarshal current user failed")
	}

	return &user, nil
}

func (a *LoginAction) Login(ctx context.Context) error {
	if err := a.Open(ctx); err != nil {
		return err
	}
	return a.WaitForLogin(ctx, nil)
}

// WaitForLogin 连续两次观察到无验证浮层的登录状态后才返回。
func (a *LoginAction) WaitForLogin(ctx context.Context, onProgress func(LoginPhase)) error {
	return a.monitorLogin(ctx, func(phase LoginPhase) error {
		if onProgress != nil {
			onProgress(phase)
		}
		return nil
	}, true)
}

// MonitorLogin 持续观察真实页面，调用方保存登录信息后取消监控。
// 稳定登录信号会重复通知，允许短暂上下文失效后重新尝试保存。
func (a *LoginAction) MonitorLogin(ctx context.Context, onProgress func(LoginPhase) error) error {
	return a.monitorLogin(ctx, onProgress, false)
}

func (a *LoginAction) monitorLogin(ctx context.Context, onProgress func(LoginPhase) error, stopOnLogin bool) error {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	var phase LoginPhase
	stableLogin := 0

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		next, err := a.CurrentPhase(ctx)
		if err != nil {
			stableLogin = 0
			// 手机确认会触发页面跳转，短暂丢失上下文时继续等待。
			if !isTransientContextError(err) {
				return err
			}
		} else {
			if next == LoginLoggedIn {
				if stableLogin < 2 {
					stableLogin++
				}
			} else {
				stableLogin = 0
			}
			if next != LoginLoggedIn || stableLogin >= 2 {
				if next == LoginLoggedIn && stopOnLogin {
					return ctx.Err()
				}
				if next != phase || next == LoginLoggedIn {
					phase = next
					if onProgress != nil {
						if err := onProgress(phase); err != nil {
							if !isTransientContextError(err) {
								return err
							}
							stableLogin = 0
						}
					}
					if next == LoginLoggedIn {
						stableLogin = 0
					}
				}
			}
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// CurrentPhase 先读取正文里的实际验证文案，再判断背景页面的登录标记。
func (a *LoginAction) CurrentPhase(ctx context.Context) (LoginPhase, error) {
	return readLoginPhase(a.page.Context(ctx))
}

func readLoginPhase(page *rod.Page) (LoginPhase, error) {
	exists, body, err := page.Has("body")
	if err != nil {
		return "", errors.Wrap(err, "check login page body failed")
	}
	var text string
	if exists {
		text, err = body.Text()
		if err != nil {
			return "", errors.Wrap(err, "read login page body failed")
		}
		text = strings.Join(strings.Fields(text), "")
		if strings.Contains(text, "请通过验证") || strings.Contains(text, "扫码验证身份") ||
			strings.Contains(text, "使用已登录该账号的小红书APP") {
			return LoginVerificationRequired, nil
		}
	}
	exists, _, err = page.Has(".main-container .user .link-wrapper .channel")
	if err != nil {
		return "", errors.Wrap(err, "check login completion failed")
	}
	if exists {
		return LoginLoggedIn, nil
	}
	if strings.Contains(text, "请在手机上确认") || strings.Contains(text, "扫码成功") {
		return LoginWaitingConfirmation, nil
	}
	return LoginWaitingScan, nil
}
