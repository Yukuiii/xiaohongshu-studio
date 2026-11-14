//go:build integration

package xiaohongshu

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/stretchr/testify/require"
	xhsErrors "github.com/xpzouying/xiaohongshu-mcp/errors"
)

// 所有域名只解析到本地 HTTPS 夹具，保留真实 Network 事件，不连接实际账号。
func TestPublishedNoteCaptureLocal(t *testing.T) {
	const id = "6af8bd4c000000002a01272b"
	for _, test := range []struct {
		name, response string
		redirect       bool
		wantLink       bool
	}{
		{"捕获本次创建结果", `{"code":0,"success":true,"data":{"id":"` + id + `"}}`, true, true},
		{"缺失链接仍是发布成功", `{"code":0,"success":true,"data":{}}`, true, false},
		{"未确认跳转仍是结果未知", `{"code":0,"success":true,"data":{"id":"` + id + `"}}`, false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var creations atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Access-Control-Allow-Origin", "*")
				w.Header().Set("Cache-Control", "no-store")
				if r.Host == "www.xiaohongshu.com" {
					w.Header().Set("Content-Type", "text/html; charset=utf-8")
					if r.URL.Path == "/explore" {
						fmt.Fprint(w, `<div id="app"><div class="main-container"><li class="user side-bar-component"><a class="link-wrapper" href="/user/profile/fixture"><span class="channel">我</span></a></li></div></div>`)
					} else {
						fmt.Fprint(w, `<div id="app">我的笔记</div><script>window.__INITIAL_STATE__={user:{userPageData:{value:{basicInfo:{nickname:"创作者"},interactions:[]}},notes:{value:[[{id:"`+id+`",xsecToken:"profile-token"}]]},activeTab:{value:{query:"note",index:0}}}};</script>`)
					}
					return
				}
				if r.Host == "edith.xiaohongshu.com" {
					w.Header().Set("Content-Type", "application/json")
					if r.Method == http.MethodPost && r.URL.Path == "/web_api/sns/v2/note" {
						creations.Add(1)
						fmt.Fprint(w, test.response)
					} else {
						fmt.Fprint(w, `{"code":0,"success":true,"data":{"id":"6af8bd4c000000002a01272c"}}`)
					}
					return
				}
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				if r.URL.Path == "/published" {
					fmt.Fprint(w, "发布完成")
					return
				}
				fmt.Fprintf(w, `<!doctype html><meta charset="utf-8">
<div class="publish-page-publish-btn"><button class="bg-red" onclick="publish()">发布</button></div>
<script>
async function publish() {
 await fetch('https://edith.xiaohongshu.com/web_api/sns/v2/note');
 await fetch('https://edith.xiaohongshu.com/web_api/sns/capa/postgw/note/update', {method:'POST'});
 const response = await fetch('https://edith.xiaohongshu.com/web_api/sns/v2/note', {method:'POST', body:'{}'});
 await response.json();
 if (%t) location.href='/published';
}
</script>`, test.redirect)
			}))
			defer server.Close()
			bin := os.Getenv("XHS_TEST_BROWSER")
			if bin == "" {
				var found bool
				bin, found = launcher.LookPath()
				if !found {
					t.Skip("未找到本地测试浏览器")
				}
			}
			launch := launcher.New().Bin(bin).Headless(true).
				Set("host-resolver-rules", "MAP * "+strings.TrimPrefix(server.URL, "https://")).
				Set("no-proxy-server")
			controlURL, err := launch.Launch()
			require.NoError(t, err)
			b := rod.New().ControlURL(controlURL).NoDefaultDevice()
			require.NoError(t, b.Connect())
			defer func() { _ = b.Close(); launch.Cleanup() }()
			require.NoError(t, b.IgnoreCertErrors(true))
			page := b.MustPage(urlOfPublic).MustWaitLoad()
			defer page.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			note, err := clickAndConfirmPublish(page.Context(ctx))
			require.Equal(t, int32(1), creations.Load())
			if !test.redirect {
				require.ErrorIs(t, err, xhsErrors.ErrPublishUncertain)
				require.Empty(t, note.NoteURL)
				return
			}
			require.NoError(t, err)
			if test.wantLink {
				require.Equal(t, id, note.NoteID, note.NoteLinkError)
				note = ResolvePublishedNoteLink(t.Context(), page, note)
				require.Equal(t, "https://www.xiaohongshu.com/explore/"+id+"?xsec_source=pc_user&xsec_token=profile-token", note.NoteURL)
				require.Empty(t, note.NoteLinkError)
			} else {
				require.Empty(t, note.NoteURL)
				require.NotEmpty(t, note.NoteLinkError)
			}
		})
	}
}

func TestPublishedNoteLinkFailureKeepsKnownID(t *testing.T) {
	b := newSearchFixtureBrowser(t)
	page := b.MustPage()
	defer page.Close()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	const id = "6af8bd4c000000002a01272b"
	note := ResolvePublishedNoteLink(ctx, page, PublishedNote{NoteID: id})
	require.Equal(t, id, note.NoteID)
	require.Empty(t, note.NoteURL)
	require.NotEmpty(t, note.NoteLinkError)
}
