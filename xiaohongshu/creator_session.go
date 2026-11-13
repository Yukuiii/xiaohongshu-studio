package xiaohongshu

import (
	"context"
	"time"

	"github.com/go-rod/rod"
	"github.com/pkg/errors"
	xhserrors "github.com/xpzouying/xiaohongshu-mcp/errors"
)

const creatorPageTimeout = 30 * time.Second

// openCreatorPublishPage 以真实发布界面判断就绪，不依赖特定 Cookie。
func openCreatorPublishPage(page *rod.Page) error {
	ctx, cancel := context.WithTimeout(page.GetContext(), creatorPageTimeout)
	defer cancel()
	page = page.Context(ctx)
	if err := page.Navigate(urlOfPublic); err != nil {
		return errors.Wrap(err, "打开创作者中心发布页失败")
	}

	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return errors.Wrap(err, "等待创作者中心发布页就绪失败，请检查网络后重试")
		}
		if err := CheckPageBlocked(page); err != nil {
			return err
		}
		ready, err := creatorPublishPageReady(page)
		if err != nil && !isTransientContextError(err) {
			return errors.Wrap(err, "检查创作者中心发布页失败")
		}
		if ready {
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.Wrap(ctx.Err(), "等待创作者中心发布页就绪失败，请检查网络后重试")
		case <-ticker.C:
		}
	}
}

func creatorPublishPageReady(page *rod.Page) (bool, error) {
	// 登录浮层优先于背景中的上传区域，隐藏的登录模板不影响发布。
	exists, element, err := page.Has(".login-container")
	if err != nil {
		return false, err
	}
	if exists {
		visible, err := element.Visible()
		if err != nil {
			return false, err
		}
		if visible {
			return false, errors.Wrap(xhserrors.ErrLoginRequired, "创作者中心需要登录")
		}
	}
	exists, element, err = page.Has("div.upload-content")
	if err != nil || !exists {
		return false, err
	}
	return element.Visible()
}
