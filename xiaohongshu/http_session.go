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
	urlOfExplore = "https://www.xiaohongshu.com/explore"

	httpPageTimeout  = 20 * time.Second
	httpPageMaxBytes = 5 << 20
)

// jsUndefinedPattern 匹配 JSON 字符串或裸 undefined，只替换字符串外的 JS 空值。
var jsUndefinedPattern = regexp.MustCompile(`"(?:\\.|[^"\\])*"|\bundefined\b`)

// HTTPSession 复用一次账号浏览器会话读取小红书页面。
type HTTPSession struct {
	client    *http.Client
	userAgent string
}

// NewHTTPSession 使用账号会话的 UA 和 Cookie 创建只读客户端。
func NewHTTPSession(client *http.Client, userAgent string, cookies []*proto.NetworkCookie) (*HTTPSession, error) {
	if client == nil {
		return nil, errors.New("HTTP 客户端不能为空")
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	base, _ := url.Parse(urlOfExplore)
	jar.SetCookies(base, sessionCookies(cookies))

	scoped := *client
	scoped.Jar = jar
	scoped.CheckRedirect = sameHostRedirect
	return &HTTPSession{client: &scoped, userAgent: userAgent}, nil
}

// Get 读取页面 HTML；只跟随同站重定向，并识别登录页和安全验证页。
func (s *HTTPSession) Get(ctx context.Context, target string) ([]byte, *url.URL, error) {
	requestCtx, cancel := context.WithTimeout(ctx, httpPageTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, target, nil)
	if err != nil {
		return nil, nil, errors.New("构造页面请求失败")
	}
	if s.userAgent != "" {
		request.Header.Set("User-Agent", s.userAgent)
	}
	request.Header.Set("Accept", "text/html")
	request.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")

	response, err := s.client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		return nil, nil, fmt.Errorf("读取页面失败（%v）", err)
	}
	defer response.Body.Close()

	final := response.Request.URL
	if err := blockedPagePath(final.EscapedPath()); err != nil {
		return nil, nil, err
	}
	if response.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("页面返回 HTTP %d", response.StatusCode)
	}
	page, err := io.ReadAll(io.LimitReader(response.Body, httpPageMaxBytes+1))
	if err != nil {
		return nil, nil, errors.New("读取页面失败")
	}
	if len(page) > httpPageMaxBytes {
		return nil, nil, errors.New("页面过大，未读取内容")
	}
	return page, final, nil
}

// blockedPagePath 识别常见拦截地址；www 站登录页是 /login，普通站是 /website-login。
func blockedPagePath(path string) error {
	if path == "/login" || strings.HasPrefix(path, "/login/") {
		return xhserrors.ErrLoginRequired
	}
	return classifyBlockedPath(path)
}

// sameHostRedirect 只跟随目标站点内的重定向，避免携带会话跳往其它站点。
func sameHostRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 5 {
		return errors.New("页面重定向次数过多")
	}
	if len(via) > 0 && req.URL.Host != via[0].URL.Host {
		return fmt.Errorf("页面跳转到其他站点（%s），已停止", req.URL.Host)
	}
	return nil
}

func sessionCookies(cookies []*proto.NetworkCookie) []*http.Cookie {
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

// decodeInitialState 从 HTML 的 window.__INITIAL_STATE__ 解码一个 JSON 对象。
func decodeInitialState(page []byte, dst any) error {
	start := bytes.Index(page, []byte("window.__INITIAL_STATE__"))
	if start < 0 {
		return errors.New("页面缺少初始化数据")
	}
	rest := page[start:]
	equals := bytes.IndexByte(rest, '=')
	end := bytes.Index(rest, []byte("</script>"))
	if equals < 0 || end <= equals {
		return errors.New("页面初始化数据不完整")
	}
	decoder := json.NewDecoder(bytes.NewReader(normalizeJSUndefined(rest[equals+1 : end])))
	if err := decoder.Decode(dst); err != nil {
		return errors.New("解析页面数据失败")
	}
	return nil
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
