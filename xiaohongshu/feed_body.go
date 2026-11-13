package xiaohongshu

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/go-rod/rod/lib/proto"
	xhserrors "github.com/xpzouying/xiaohongshu-mcp/errors"
)

const (
	urlOfFeedExplore = "https://www.xiaohongshu.com/explore/"

	feedBodyTimeout  = 20 * time.Second
	feedBodyMaxBytes = 5 << 20
)

var feedIDPattern = regexp.MustCompile(`^[0-9a-zA-Z]{8,32}$`)

// jsUndefinedPattern 匹配 JSON 字符串或裸 undefined，只替换字符串外的 JS 空值。
var jsUndefinedPattern = regexp.MustCompile(`"(?:\\.|[^"\\])*"|\bundefined\b`)

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
	client    *http.Client
	userAgent string
}

// NewFeedBodyFetcher 使用账号浏览器的 UA 和 Cookie 创建只读客户端。
func NewFeedBodyFetcher(client *http.Client, userAgent string, cookies []*proto.NetworkCookie) (*FeedBodyFetcher, error) {
	if client == nil {
		return nil, errors.New("HTTP 客户端不能为空")
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	base, _ := url.Parse(urlOfFeedExplore)
	jar.SetCookies(base, feedCookies(cookies))

	scoped := *client
	scoped.Jar = jar
	scoped.CheckRedirect = sameHostRedirect
	return &FeedBodyFetcher{client: &scoped, userAgent: userAgent}, nil
}

// sameHostRedirect 只跟随目标站点内的重定向，避免携带会话跳往其它站点。
func sameHostRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 5 {
		return errors.New("笔记页面重定向次数过多")
	}
	if len(via) > 0 && req.URL.Host != via[0].URL.Host {
		return fmt.Errorf("笔记页面跳转到其他站点（%s），已停止", req.URL.Host)
	}
	return nil
}

func feedCookies(cookies []*proto.NetworkCookie) []*http.Cookie {
	list := make([]*http.Cookie, 0, len(cookies))
	for _, cookie := range cookies {
		if cookie == nil {
			continue
		}
		domain := strings.TrimPrefix(cookie.Domain, ".")
		if domain != "xiaohongshu.com" && !strings.HasSuffix(domain, ".xiaohongshu.com") {
			continue
		}
		path := cookie.Path
		if path == "" {
			path = "/"
		}
		item := &http.Cookie{
			Name:     cookie.Name,
			Value:    cookie.Value,
			Domain:   cookie.Domain,
			Path:     path,
			Secure:   cookie.Secure,
			HttpOnly: cookie.HTTPOnly,
		}
		if !cookie.Session {
			item.Expires = time.Unix(int64(cookie.Expires), 0)
		}
		list = append(list, item)
	}
	return list
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
	requestCtx, cancel := context.WithTimeout(ctx, feedBodyTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, target, nil)
	if err != nil {
		return FeedBody{}, errors.New("构造笔记请求失败")
	}
	if f.userAgent != "" {
		request.Header.Set("User-Agent", f.userAgent)
	}
	request.Header.Set("Accept", "text/html")
	request.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")

	response, err := f.client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return FeedBody{}, ctx.Err()
		}
		return FeedBody{}, fmt.Errorf("读取笔记页面失败（%v）", err)
	}
	defer response.Body.Close()

	if err := feedBlockedPath(response.Request.URL.EscapedPath()); err != nil {
		return FeedBody{}, err
	}
	if response.StatusCode != http.StatusOK {
		return FeedBody{}, fmt.Errorf("笔记页面返回 HTTP %d", response.StatusCode)
	}
	if !strings.HasPrefix(response.Request.URL.EscapedPath(), "/explore/") {
		return FeedBody{}, errors.New("笔记页面已跳转，未取得正文")
	}

	page, err := io.ReadAll(io.LimitReader(response.Body, feedBodyMaxBytes+1))
	if err != nil {
		return FeedBody{}, errors.New("读取笔记页面失败")
	}
	if len(page) > feedBodyMaxBytes {
		return FeedBody{}, errors.New("笔记页面过大，未读取正文")
	}
	return parseFeedBody(page, feedID, target)
}

// feedBlockedPath 识别笔记页常见的拦截地址；www 站登录页是 /login，普通站是 /website-login。
func feedBlockedPath(path string) error {
	if path == "/login" || strings.HasPrefix(path, "/login/") {
		return xhserrors.ErrLoginRequired
	}
	return classifyBlockedPath(path)
}

func parseFeedBody(page []byte, feedID, target string) (FeedBody, error) {
	start := bytes.Index(page, []byte("window.__INITIAL_STATE__"))
	if start < 0 {
		return FeedBody{}, errors.New("笔记页面缺少初始化数据")
	}
	rest := page[start:]
	equals := bytes.IndexByte(rest, '=')
	end := bytes.Index(rest, []byte("</script>"))
	if equals < 0 || end <= equals {
		return FeedBody{}, errors.New("笔记页面初始化数据不完整")
	}

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
	decoder := json.NewDecoder(bytes.NewReader(normalizeJSUndefined(rest[equals+1 : end])))
	if err := decoder.Decode(&state); err != nil {
		return FeedBody{}, errors.New("解析笔记页面数据失败")
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

// normalizeJSUndefined 把 JS 对象里的裸 undefined 换成 JSON null，字符串内容保持不变。
func normalizeJSUndefined(raw []byte) []byte {
	return jsUndefinedPattern.ReplaceAllFunc(raw, func(token []byte) []byte {
		if bytes.Equal(token, []byte("undefined")) {
			return []byte("null")
		}
		return token
	})
}
