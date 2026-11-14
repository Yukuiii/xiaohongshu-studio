package xiaohongshu

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"time"

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
	responses, stop := watchPublishResponses(page)
	defer stop()
	if err := clickPublishButton(page); err != nil {
		return PublishedNote{}, err
	}
	if err := waitPublishSuccess(page, 15*time.Second); err != nil {
		return PublishedNote{}, err
	}

	// 发布已确认，后续取消或读取失败只影响链接，不能触发重发。
	select {
	case note := <-responses:
		return note, nil
	default:
	}
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	select {
	case note := <-responses:
		return note, nil
	case <-page.GetContext().Done():
		return PublishedNote{NoteLinkError: "发布已完成，读取笔记链接时操作被中断"}, nil
	case <-timer.C:
		return PublishedNote{NoteLinkError: "发布已完成，但未收到笔记链接，请到小红书账号查看"}, nil
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
	}, func(event *proto.NetworkResponseReceived) {
		if event.RequestID == requestID {
			status = event.Response.Status
			// 小响应可能已经读完，先取结果，避免跳转清理旧资源。
			if status >= 200 && status < 300 {
				if note, err := readPublishedNoteResponse(p, requestID); err == nil {
					requestID = ""
					update(note)
				}
			}
		}
	}, func(event *proto.NetworkLoadingFinished) {
		if requestID == "" || event.RequestID != requestID {
			return
		}
		requestID = ""
		if status < 200 || status >= 300 {
			update(PublishedNote{NoteLinkError: fmt.Sprintf("无法读取发布响应中的笔记链接（HTTP %d）", status)})
			return
		}
		note, err := readPublishedNoteResponse(p, event.RequestID)
		if err != nil {
			slog.Warn("读取发布响应失败", "error", err)
			note.NoteLinkError = "发布已完成，但无法读取笔记链接"
		}
		update(note)
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
		return PublishedNote{}, fmt.Errorf("发布已完成，但笔记链接响应格式无效")
	}
	if (response.Code == nil && response.Success == nil) || (response.Code != nil && *response.Code != 0) || (response.Success != nil && !*response.Success) {
		return PublishedNote{}, fmt.Errorf("发布响应未提供可确认的笔记链接")
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
		return PublishedNote{}, fmt.Errorf("发布已完成，但平台未返回笔记标识")
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
