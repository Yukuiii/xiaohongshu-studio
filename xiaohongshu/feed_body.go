package xiaohongshu

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/go-rod/rod/lib/proto"
)

const urlOfFeedExplore = urlOfExplore + "/"

var feedIDPattern = regexp.MustCompile(`^[0-9a-zA-Z]{8,32}$`)

// FeedBody 是写稿所需的笔记基础信息。
type FeedBody struct {
	FeedID  string
	Title   string
	Content string
	Author  string
	URL     string
}

// ValidateFeedRef 校验笔记标识和访问凭证。
func ValidateFeedRef(feedID, xsecToken string) error {
	if !feedIDPattern.MatchString(feedID) {
		return errors.New("笔记标识格式无效")
	}
	if len(xsecToken) == 0 || len(xsecToken) > 512 {
		return errors.New("笔记访问凭证无效")
	}
	if strings.ContainsAny(xsecToken, " \t\r\n") {
		return errors.New("笔记访问凭证无效")
	}
	return nil
}

// FeedBodyURL 由笔记标识构造固定的小红书笔记地址，不接受任意 URL。
func FeedBodyURL(feedID, xsecToken string) (string, error) {
	if err := ValidateFeedRef(feedID, xsecToken); err != nil {
		return "", err
	}
	values := url.Values{"xsec_token": {xsecToken}, "xsec_source": {"pc_search"}}
	return urlOfFeedExplore + feedID + "?" + values.Encode(), nil
}

// FeedBodyFetcher 复用一次账号会话读取多篇笔记正文。
type FeedBodyFetcher struct {
	session *HTTPSession
}

// NewFeedBodyFetcher 使用账号浏览器的 UA 和 Cookie 创建只读客户端。
func NewFeedBodyFetcher(client *http.Client, userAgent string, cookies []*proto.NetworkCookie) (*FeedBodyFetcher, error) {
	session, err := NewHTTPSession(client, userAgent, cookies)
	if err != nil {
		return nil, err
	}
	return &FeedBodyFetcher{session: session}, nil
}

// Fetch 读取单篇笔记的 HTML 初始化数据，不渲染页面、不执行脚本。
func (f *FeedBodyFetcher) Fetch(ctx context.Context, feedID, xsecToken string) (FeedBody, error) {
	target, err := FeedBodyURL(feedID, xsecToken)
	if err != nil {
		return FeedBody{}, err
	}
	return f.fetchURL(ctx, target, feedID)
}

func (f *FeedBodyFetcher) fetchURL(ctx context.Context, target, feedID string) (FeedBody, error) {
	page, final, err := f.session.Get(ctx, target)
	if err != nil {
		return FeedBody{}, err
	}
	if !strings.HasPrefix(final.EscapedPath(), "/explore/") {
		return FeedBody{}, errors.New("笔记页面已跳转，未取得正文")
	}
	return parseFeedBody(page, feedID, target)
}

func parseFeedBody(page []byte, feedID, target string) (FeedBody, error) {
	var state struct {
		Note struct {
			NoteDetailMap map[string]struct {
				Note struct {
					Title string `json:"title"`
					Desc  string `json:"desc"`
					User  User   `json:"user"`
				} `json:"note"`
			} `json:"noteDetailMap"`
		} `json:"note"`
	}
	if err := decodeInitialState(page, &state); err != nil {
		return FeedBody{}, err
	}

	entry, ok := state.Note.NoteDetailMap[feedID]
	if !ok {
		return FeedBody{}, errors.New("笔记不存在或当前账号无权访问")
	}
	content := strings.TrimSpace(entry.Note.Desc)
	if content == "" {
		return FeedBody{}, errors.New("该笔记没有可用正文")
	}
	author := entry.Note.User.Nickname
	if author == "" {
		author = entry.Note.User.NickName
	}
	return FeedBody{
		FeedID:  feedID,
		Title:   strings.TrimSpace(entry.Note.Title),
		Content: content,
		Author:  author,
		URL:     target,
	}, nil
}
