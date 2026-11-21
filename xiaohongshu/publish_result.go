package xiaohongshu

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	xhsErrors "github.com/Yukuiii/xiaohongshu-studio/errors"
	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// PublishedNote 保存本次发布返回的标识；链接缺失不代表发布失败。
type PublishedNote struct {
	NoteID        string `json:"note_id,omitempty"`
	NoteURL       string `json:"note_url,omitempty"`
	NoteLinkError string `json:"note_link_error,omitempty"`
}

// clickAndConfirmPublish 在点击前监听创建响应，不按标题猜测已发布笔记。
func clickAndConfirmPublish(page *rod.Page) (PublishedNote, error) {
	btn, err := waitForPublishButtonClickable(page, 15*time.Second)
	if err != nil {
		return PublishedNote{}, err
	}

	responses, stop := watchPublishResponses(page)
	defer stop()
	ctx := page.GetContext()
	if err := ctx.Err(); err != nil {
		return PublishedNote{}, fmt.Errorf("提交前任务已中止: %w", err)
	}
	// 单调时钟在部分系统休眠时停止，用实际时间检查提交期限。
	if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline.Round(0)) {
		return PublishedNote{}, fmt.Errorf("提交前任务已超时: %w", context.DeadlineExceeded)
	}
	if err := clickPublishButton(page, btn); err != nil {
		return PublishedNote{}, err
	}

	return waitPublishResult(page, responses)
}

func waitPublishResult(page *rod.Page, responses <-chan PublishedNote) (PublishedNote, error) {
	ctx, cancel := context.WithTimeout(page.GetContext(), 15*time.Second)
	defer cancel()
	browser := page.Browser().Context(ctx)
	request := proto.TargetGetTargetInfo{TargetID: page.TargetID}
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	// 等待创作者页完成提交后的处理，超时后仍认可有效创建回执。
	var note PublishedNote
	for {
		select {
		case note = <-responses:
		case <-ticker.C:
			info, err := request.Call(browser)
			if err == nil && isPublishSuccessURL(info.TargetInfo.URL) {
				slog.Info("发布成功，已跳转到发布成功页")
				return waitPublishedNoteLink(page, responses, note), nil
			}
		case <-ctx.Done():
			select {
			case note = <-responses:
			default:
			}
			if note.NoteID != "" {
				slog.Info("发布成功，已收到创建笔记回执", "note_id", note.NoteID)
				return note, nil
			}
			if note.NoteLinkError == "" {
				note.NoteLinkError = "未收到可确认的创建笔记回执"
			}
			return PublishedNote{}, fmt.Errorf("点击发布后未确认成功（%s）: %w", note.NoteLinkError, xhsErrors.ErrPublishUncertain)
		}
	}
}

func isPublishSuccessURL(raw string) bool {
	if index := strings.IndexAny(raw, "?#"); index >= 0 {
		raw = raw[:index]
	}
	return raw == "https://creator.xiaohongshu.com/publish/success"
}

// 已确认发布后仅等待可选的笔记标识。
func waitPublishedNoteLink(page *rod.Page, responses <-chan PublishedNote, fallback PublishedNote) PublishedNote {
	select {
	case note := <-responses:
		return note
	default:
	}
	if fallback.NoteID != "" || fallback.NoteLinkError != "" {
		return fallback
	}
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	select {
	case note := <-responses:
		return note
	case <-page.GetContext().Done():
		return PublishedNote{NoteLinkError: "发布已完成，读取笔记链接时操作被中断"}
	case <-timer.C:
		return PublishedNote{NoteLinkError: "发布已完成，但未收到笔记链接，请到小红书账号查看"}
	}
}

func watchPublishResponses(page *rod.Page) (<-chan PublishedNote, func()) {
	p, cancel := page.WithCancel()
	responses := make(chan PublishedNote, 1)
	// 成功页可能切换渲染进程，响应正文需由浏览器进程短暂保留。
	_, err := p.Call(p.GetContext(), string(p.SessionID), "Network.enable", struct {
		MaxTotalBufferSize    int  `json:"maxTotalBufferSize"`
		MaxResourceBufferSize int  `json:"maxResourceBufferSize"`
		EnableDurableMessages bool `json:"enableDurableMessages"`
	}{1 << 20, 128 << 10, true})
	if err != nil {
		responses <- PublishedNote{NoteLinkError: "无法观察发布响应，暂未取得笔记链接"}
		return responses, cancel
	}
	var requestID proto.NetworkRequestID
	var status int
	update := func(note PublishedNote) {
		select {
		case <-responses:
		default:
		}
		responses <- note
	}
	wait := p.EachEvent(func(event *proto.NetworkRequestWillBeSent) {
		if event.Request.Method != "POST" || !isCreateNoteURL(event.Request.URL) {
			return
		}
		requestID, status = event.RequestID, 0
		select {
		case <-responses:
		default:
		}
	}, func(event *proto.NetworkResponseReceived) bool {
		if requestID == "" || event.RequestID != requestID {
			return false
		}
		if !isCreateNoteURL(event.Response.URL) {
			requestID = ""
			update(PublishedNote{NoteLinkError: "创建笔记请求跳转，未收到回执"})
			return false
		}
		status = event.Response.Status
		// 小响应可能已经读完，先取结果，避免跳转清理旧资源。
		if status >= 200 && status < 300 {
			if note, err := readPublishedNoteResponse(p, requestID); err == nil {
				update(note)
				return true
			}
		}
		return false
	}, func(event *proto.NetworkLoadingFinished) bool {
		if requestID == "" || event.RequestID != requestID {
			return false
		}
		requestID = ""
		if status < 200 || status >= 300 {
			update(PublishedNote{NoteLinkError: fmt.Sprintf("创建笔记请求返回 HTTP %d", status)})
			return false
		}
		note, err := readPublishedNoteResponse(p, event.RequestID)
		if err != nil {
			slog.Warn("读取发布响应失败", "error", err)
			note.NoteLinkError = "无法读取创建笔记回执: " + err.Error()
		}
		update(note)
		return note.NoteID != ""
	}, func(event *proto.NetworkLoadingFailed) {
		if requestID != "" && event.RequestID == requestID {
			requestID = ""
			update(PublishedNote{NoteLinkError: "发布响应读取中断，未取得笔记链接"})
		}
	})
	done := make(chan struct{})
	go func() {
		defer close(done)
		wait()
	}()
	return responses, func() { cancel(); <-done }
}

func readPublishedNoteResponse(page *rod.Page, id proto.NetworkRequestID) (PublishedNote, error) {
	response, err := (proto.NetworkGetResponseBody{RequestID: id}).Call(page)
	if err != nil {
		return PublishedNote{}, err
	}
	body := []byte(response.Body)
	if response.Base64Encoded {
		body, err = base64.StdEncoding.DecodeString(response.Body)
		if err != nil {
			return PublishedNote{}, err
		}
	}
	return parsePublishedNoteResponse(body)
}

func isCreateNoteURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.User == nil && u.Host == "edith.xiaohongshu.com" && u.Path == "/web_api/sns/v2/note"
}

func parsePublishedNoteResponse(body []byte) (PublishedNote, error) {
	var response struct {
		Code    *int  `json:"code"`
		Success *bool `json:"success"`
		Data    struct {
			ID          string `json:"id"`
			NoteID      string `json:"note_id"`
			CamelNoteID string `json:"noteId"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return PublishedNote{}, fmt.Errorf("创建笔记回执格式无效")
	}
	if response.Code != nil && *response.Code != 0 {
		return PublishedNote{}, fmt.Errorf("小红书创建回执 code=%d", *response.Code)
	}
	if response.Success != nil && !*response.Success {
		return PublishedNote{}, fmt.Errorf("小红书创建回执 success=false")
	}
	if response.Code == nil && response.Success == nil {
		return PublishedNote{}, fmt.Errorf("创建笔记回执缺少状态")
	}
	// 创建页面自身兼容这三个字段；存在冲突时不选择其中任意一个。
	var id string
	for _, value := range [...]string{response.Data.ID, response.Data.NoteID, response.Data.CamelNoteID} {
		if value == "" {
			continue
		}
		if !feedIDPattern.MatchString(value) || (id != "" && id != value) {
			return PublishedNote{}, fmt.Errorf("发布响应中的笔记标识无效或不一致")
		}
		id = value
	}
	if id == "" {
		return PublishedNote{}, fmt.Errorf("创建笔记回执缺少笔记标识")
	}
	return PublishedNote{NoteID: id}, nil
}

// ResolvePublishedNoteLink 只按创建响应的 ID 取访问参数，失败不改变发布结果。
func ResolvePublishedNoteLink(ctx context.Context, page *rod.Page, note PublishedNote) (result PublishedNote) {
	result = note
	if note.NoteID == "" {
		return result
	}
	result.NoteURL = ""
	defer func() {
		if recover() != nil {
			result.NoteLinkError = "发布已完成，但读取笔记访问参数失败，请到小红书账号查看"
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	profile, err := NewUserProfileAction(page).GetMyProfileViaSidebar(ctx, TabNotes)
	if err != nil {
		result.NoteLinkError = "发布已完成，但读取笔记访问参数失败，请到小红书账号查看"
		return result
	}
	result.NoteURL, err = publishedNoteURLFromFeeds(note.NoteID, profile.Feeds)
	if err != nil {
		result.NoteLinkError = err.Error()
	} else {
		result.NoteLinkError = ""
	}
	return result
}

func publishedNoteURLFromFeeds(noteID string, feeds []Feed) (string, error) {
	for _, feed := range feeds {
		if feed.ID != noteID {
			continue
		}
		if err := ValidateFeedRef(noteID, feed.XsecToken); err != nil {
			return "", fmt.Errorf("发布已完成，但笔记访问参数暂不可用")
		}
		query := url.Values{"xsec_token": {feed.XsecToken}, "xsec_source": {"pc_user"}}
		return urlOfFeedExplore + noteID + "?" + query.Encode(), nil
	}
	return "", fmt.Errorf("发布已完成，但笔记尚未出现在账号主页，暂未取得链接")
}
