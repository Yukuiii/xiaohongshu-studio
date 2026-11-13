package browser

import (
	"context"
	"errors"

	"github.com/go-rod/rod/lib/proto"
)

// Session 返回当前浏览器的 UA 和 Cookie 快照，供同一账号的只读 HTTP 请求复用登录态。
func (b *Browser) Session(ctx context.Context) (string, []*proto.NetworkCookie, error) {
	if b == nil || b.browser == nil || !b.Alive() {
		return "", nil, errors.New("浏览器未运行，无法读取登录会话")
	}
	cookies, err := b.browser.Context(ctx).GetCookies()
	if err != nil {
		return "", nil, err
	}
	userAgent := ""
	if b.uaOverride != nil {
		userAgent = b.uaOverride.UserAgent
	}
	return userAgent, cookies, nil
}
