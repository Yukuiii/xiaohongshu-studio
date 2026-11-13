package xiaohongshu

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-rod/rod/lib/proto"
	"github.com/stretchr/testify/require"
	xhserrors "github.com/xpzouying/xiaohongshu-mcp/errors"
)

const bodyFixtureID = "6ae42c16000000002702ce2f"

func bodyHTML(state string) string {
	return `<!doctype html><html><body><script>window.__INITIAL_STATE__=` + state + `</script></body></html>`
}

func TestFeedBodyURL(t *testing.T) {
	target, err := FeedBodyURL(bodyFixtureID, "token+/=")
	require.NoError(t, err)
	parsed, err := url.Parse(target)
	require.NoError(t, err)
	require.Equal(t, "www.xiaohongshu.com", parsed.Host)
	require.Equal(t, "/explore/"+bodyFixtureID, parsed.Path)
	require.Equal(t, "token+/=", parsed.Query().Get("xsec_token"))
	require.Equal(t, "pc_search", parsed.Query().Get("xsec_source"))

	for _, item := range []struct{ id, token string }{
		{"", "token"},
		{"short", "token"},
		{"bad/id/0000000000", "token"},
		{bodyFixtureID, ""},
		{bodyFixtureID, "has space"},
		{bodyFixtureID, strings.Repeat("t", 513)},
	} {
		_, err := FeedBodyURL(item.id, item.token)
		require.Error(t, err, "%+v", item)
	}
}

func TestParseFeedBody(t *testing.T) {
	success := `{"note":{"noteDetailMap":{"` + bodyFixtureID + `":{"note":{"title":"笔记标题","desc":"完整的正文内容","user":{"nickname":"作者A"}}}}}}`
	cases := []struct {
		name    string
		page    string
		want    *FeedBody
		wantErr string
	}{
		{name: "正常正文", page: bodyHTML(success), want: &FeedBody{Title: "笔记标题", Content: "完整的正文内容", Author: "作者A"}},
		{
			name: "字符串外的undefined可以解析",
			page: bodyHTML(`{"extra":undefined,"note":{"noteDetailMap":{"` + bodyFixtureID + `":{"note":{"title":"标题","desc":"字符串里的 undefined 不能替换","user":{"nickname":"作者B"}}}}}}`),
			want: &FeedBody{Title: "标题", Content: "字符串里的 undefined 不能替换", Author: "作者B"},
		},
		{
			name: "忽略初始化数据后的脚本",
			page: bodyHTML(`{"note":{"noteDetailMap":{"` + bodyFixtureID + `":{"note":{"desc":"正文","user":{"nickName":"作者C"}}}}}};window.__OTHER__=1`),
			want: &FeedBody{Content: "正文", Author: "作者C"},
		},
		{name: "缺少初始化数据", page: "<html><body>加载中</body></html>", wantErr: "缺少初始化数据"},
		{name: "没有该笔记", page: bodyHTML(`{"note":{"noteDetailMap":{}}}`), wantErr: "无权访问"},
		{name: "正文为空", page: bodyHTML(`{"note":{"noteDetailMap":{"` + bodyFixtureID + `":{"note":{"desc":"   "}}}}}`), wantErr: "没有可用正文"},
		{name: "数据格式错误", page: bodyHTML(`{"note":`), wantErr: "解析笔记页面数据失败"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseFeedBody([]byte(tc.page), bodyFixtureID, "https://example.invalid")
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want.Title, got.Title)
			require.Equal(t, tc.want.Content, got.Content)
			require.Equal(t, tc.want.Author, got.Author)
			require.Equal(t, bodyFixtureID, got.FeedID)
		})
	}
}

func TestFeedBodyFetcherKeepsOnlyXiaohongshuCookies(t *testing.T) {
	fetcher, err := NewFeedBodyFetcher(&http.Client{}, "test-agent", []*proto.NetworkCookie{
		{Name: "web_session", Value: "session", Domain: ".xiaohongshu.com", Path: "/", HTTPOnly: true, Secure: true, Session: true},
		{Name: "creator", Value: "creator", Domain: "creator.xiaohongshu.com", Path: "/", Session: true},
		{Name: "foreign", Value: "value", Domain: ".example.com", Path: "/"},
		nil,
	})
	require.NoError(t, err)
	target, err := url.Parse("https://www.xiaohongshu.com/explore/" + bodyFixtureID)
	require.NoError(t, err)
	cookies := fetcher.client.Jar.Cookies(target)
	got := make(map[string]string, len(cookies))
	for _, cookie := range cookies {
		got[cookie.Name] = cookie.Value
	}
	require.Equal(t, map[string]string{"web_session": "session"}, got)
}

func newBodyServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server
}

func newTestFetcher(t *testing.T) *FeedBodyFetcher {
	t.Helper()
	fetcher, err := NewFeedBodyFetcher(&http.Client{}, "test-agent", nil)
	require.NoError(t, err)
	return fetcher
}

func TestFeedBodyFetcherLocal(t *testing.T) {
	success := bodyHTML(`{"note":{"noteDetailMap":{"` + bodyFixtureID + `":{"note":{"title":"标题","desc":"正文","user":{"nickname":"作者"}}}}}}`)
	server := newBodyServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != "test-agent" {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		if r.URL.Path == "/explore/"+bodyFixtureID {
			if r.URL.Query().Get("xsec_source") != "pc_search" {
				http.Error(w, "missing source", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(success))
			return
		}
		if r.URL.Path == "/other" {
			_, _ = w.Write([]byte(success))
			return
		}
		http.NotFound(w, r)
	})

	fetcher := newTestFetcher(t)
	body, err := fetcher.fetchURL(t.Context(), server.URL+"/explore/"+bodyFixtureID+"?xsec_source=pc_search", bodyFixtureID)
	require.NoError(t, err)
	require.Equal(t, FeedBody{FeedID: bodyFixtureID, Title: "标题", Content: "正文", Author: "作者", URL: server.URL + "/explore/" + bodyFixtureID + "?xsec_source=pc_search"}, body)

	_, err = fetcher.fetchURL(t.Context(), server.URL+"/other", bodyFixtureID)
	require.ErrorContains(t, err, "已跳转")

	_, err = fetcher.fetchURL(t.Context(), server.URL+"/explore/missing000000000000000000", bodyFixtureID)
	require.ErrorContains(t, err, "HTTP 404")
}

func TestFeedBodyFetcherLocalRedirects(t *testing.T) {
	redirect := newBodyServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/explore/"+bodyFixtureID {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		_, _ = w.Write([]byte("<html></html>"))
	})
	fetcher := newTestFetcher(t)
	_, err := fetcher.fetchURL(t.Context(), redirect.URL+"/explore/"+bodyFixtureID, bodyFixtureID)
	require.ErrorIs(t, err, xhserrors.ErrLoginRequired)

	captcha := newBodyServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/explore/"+bodyFixtureID {
			http.Redirect(w, r, "/website-login/captcha", http.StatusFound)
			return
		}
		_, _ = w.Write([]byte("<html></html>"))
	})
	_, err = fetcher.fetchURL(t.Context(), captcha.URL+"/explore/"+bodyFixtureID, bodyFixtureID)
	require.ErrorIs(t, err, xhserrors.ErrVerificationRequired)
}

func TestFeedBodyFetcherRejectsCrossSiteRedirect(t *testing.T) {
	other := newBodyServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html></html>"))
	})
	origin := newBodyServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/explore/"+bodyFixtureID, http.StatusFound)
	})
	fetcher := newTestFetcher(t)
	_, err := fetcher.fetchURL(t.Context(), origin.URL+"/explore/"+bodyFixtureID, bodyFixtureID)
	require.ErrorContains(t, err, "其他站点")
}

func TestFeedBodyFetcherRejectsOversizedPage(t *testing.T) {
	server := newBodyServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte("a"), feedBodyMaxBytes+1))
	})
	fetcher := newTestFetcher(t)
	_, err := fetcher.fetchURL(t.Context(), server.URL+"/explore/"+bodyFixtureID, bodyFixtureID)
	require.ErrorContains(t, err, "页面过大")
}

func TestFeedBodyFetcherCancellationAndTimeout(t *testing.T) {
	release := make(chan struct{})
	server := newBodyServer(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	t.Cleanup(func() { close(release) })
	fetcher := newTestFetcher(t)

	ctx, cancel := context.WithTimeout(t.Context(), 80*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := fetcher.fetchURL(ctx, server.URL+"/explore/"+bodyFixtureID, bodyFixtureID)
	require.Error(t, err)
	require.Less(t, time.Since(started), 2*time.Second)
}
