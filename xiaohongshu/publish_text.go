package xiaohongshu

import (
	"context"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/pkg/errors"
	"github.com/xpzouying/xiaohongshu-mcp/humanize"
)

// PublishTextAction 使用创作平台原生长文排版，不上传额外配图。
type PublishTextAction struct {
	page *rod.Page
}

// 2026-11 真机核实：写长文 → 新的创作 → 一键排版 → 下一步 → 发布表单。
const (
	longTextTitleSelector = `textarea[placeholder="输入标题"]`
	longTextBodySelector  = `div.tiptap.ProseMirror[contenteditable="true"]`
)

func NewPublishTextAction(page *rod.Page) (*PublishTextAction, error) {
	page = page.Timeout(300 * time.Second)
	if err := openCreatorPublishPage(page); err != nil {
		return nil, err
	}
	if err := mustClickPublishTab(page, "写长文"); err != nil {
		return nil, err
	}
	button, err := waitTextPublishElement(page, "button.new-btn", "新的创作")
	if err != nil {
		return nil, errors.Wrap(err, "查找长文创作入口失败")
	}
	if err := humanize.Click(button); err != nil {
		return nil, errors.Wrap(err, "打开长文编辑器失败")
	}
	return &PublishTextAction{page: page}, nil
}

func (p *PublishTextAction) Publish(ctx context.Context, content PublishImageContent) (PublishedNote, error) {
	if len(content.ImagePaths) != 0 {
		return PublishedNote{}, errors.New("长文不能上传额外图片")
	}
	page := p.page.Context(ctx).Timeout(300 * time.Second)
	if err := prepareTextPublication(page, content.Title, content.Content); err != nil {
		return PublishedNote{}, err
	}
	tags := content.Tags
	if len(tags) > 10 {
		tags = tags[:10]
	}
	atUsers := content.AtUsers
	if len(atUsers) > maxAtUsers {
		atUsers = atUsers[:maxAtUsers]
	}
	// 全文已经在平台排版卡片中，1000字简介只放话题和提及。
	// 复用最终点击及成功确认，点击后的连接中断仍记录为 uncertain。
	return submitPublish(ctx, page, content.Title, "", tags, atUsers, content.ScheduleTime, content.IsOriginal, content.Visibility, content.Products)
}

func prepareTextPublication(page *rod.Page, title, content string) error {
	titleInput, err := waitTextPublishElement(page, longTextTitleSelector, "")
	if err != nil {
		return errors.Wrap(err, "查找长文标题失败")
	}
	if err := titleInput.Input(title); err != nil {
		return errors.Wrap(err, "输入长文标题失败")
	}
	editor, err := waitTextPublishElement(page, longTextBodySelector, "")
	if err != nil {
		return errors.Wrap(err, "查找长文正文失败")
	}
	// 原生文本输入保留完整正文，避免逐字输入万字内容耗尽发布超时。
	if err := editor.Input(content); err != nil {
		return errors.Wrap(err, "输入长文正文失败")
	}
	layout, err := waitTextPublishElement(page, "button.next-btn", "一键排版")
	if err != nil {
		return errors.Wrap(err, "查找长文排版按钮失败")
	}
	if err := humanize.Click(layout); err != nil {
		return errors.Wrap(err, "开始长文排版失败")
	}
	next, err := waitTextPublishElement(page, "button.submit", "下一步")
	if err != nil {
		return errors.Wrap(err, "等待长文排版失败")
	}
	if err := humanize.Click(next); err != nil {
		return errors.Wrap(err, "生成平台文字卡片失败")
	}
	finalTitle, err := waitTextPublishElement(page, "div.d-input input", "")
	if err != nil {
		return errors.Wrap(err, "等待长文发布表单失败")
	}
	if err := waitTextCardsReady(page); err != nil {
		return err
	}
	closeFeatureGuide(page)
	// 平台会自动生成带换行的封面标题；清空后由共用发布流程填写原请求标题。
	if err := finalTitle.SelectAllText(); err != nil {
		return errors.Wrap(err, "选中平台生成标题失败")
	}
	if err := finalTitle.Input(""); err != nil {
		return errors.Wrap(err, "清空平台生成标题失败")
	}
	return nil
}

func waitTextPublishElement(page *rod.Page, selector, text string) (*rod.Element, error) {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := page.GetContext().Err(); err != nil {
			return nil, err
		}
		elements, err := page.Elements(selector)
		if err != nil {
			return nil, err
		}
		for _, element := range elements {
			if !isElementVisible(element) || !humanize.Visible(element) || isButtonDisabled(element) {
				continue
			}
			if text != "" {
				label, err := element.Text()
				if err != nil {
					return nil, err
				}
				if strings.TrimSpace(label) != text {
					continue
				}
			}
			return element, nil
		}
		select {
		case <-page.GetContext().Done():
			return nil, page.GetContext().Err()
		case <-ticker.C:
		}
	}
}

func waitTextCardsReady(page *rod.Page) error {
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := page.GetContext().Err(); err != nil {
			return errors.Wrap(err, "等待平台文字卡片完成失败")
		}
		cards, err := page.Elements(".img-preview-area .img-container")
		if err != nil {
			return err
		}
		ready := len(cards) > 0
		for _, card := range cards {
			busy, err := card.Attribute("aria-busy")
			if err != nil {
				return err
			}
			if busy == nil || *busy != "false" {
				ready = false
			}
		}
		if ready {
			return nil
		}
		select {
		case <-page.GetContext().Done():
			return errors.Wrap(page.GetContext().Err(), "等待平台文字卡片完成失败")
		case <-ticker.C:
		}
	}
}
