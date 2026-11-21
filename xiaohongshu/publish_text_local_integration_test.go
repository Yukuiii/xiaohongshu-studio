//go:build integration

package xiaohongshu

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	xhsErrors "github.com/Yukuiii/xiaohongshu-studio/errors"
	"github.com/stretchr/testify/require"
)

// 选择器来自真实长文入口；本地拦截所有请求，发布按钮只写断言数据。
const textPublishFixture = `
<main><div class="upload-content"><div class="creator-tab" onclick="openEntry()">写长文</div></div></main>
<script>
function openEntry() {
 document.querySelector('main').innerHTML = '<button class="new-btn" style="opacity:0.00001" onclick="throw Error(\'诱饵按钮\')">新的创作</button><button class="new-btn" onclick="openEditor()">新的创作</button>';
}
function openEditor() {
 document.querySelector('main').innerHTML = '<textarea placeholder="输入标题" maxlength="64"></textarea><div class="tiptap ProseMirror" contenteditable="true" style="min-height:40px"></div><button class="next-btn" onclick="layout()">一键排版</button>';
}
function layout() {
 sessionStorage.setItem('article', JSON.stringify({title:document.querySelector('textarea').value,body:document.querySelector('[contenteditable]').innerText}));
 document.querySelector('main').innerHTML = '<button class="submit" disabled>下一步</button>';
 setTimeout(() => { const button=document.querySelector('button.submit');button.disabled=false;button.onclick=cards; }, 100);
}
function cards() {
 document.querySelector('main').innerHTML = '<div class="img-preview-area"><div class="img-container" aria-busy="false">封面</div><div class="img-container" aria-busy="true">正文卡片</div></div><div class="d-input"><input value="平台自动标题"></div><div class="tiptap ProseMirror" contenteditable="true" style="min-height:40px"></div><div class="publish-page-publish-btn"><button class="bg-red" onclick="finish()">发布</button></div>';
 setTimeout(() => document.querySelectorAll('.img-container')[1].setAttribute('aria-busy','false'), 150);
}
function finish() {
 sessionStorage.setItem('publication', JSON.stringify({title:document.querySelector('input').value,caption:document.querySelector('[contenteditable]').innerText,ready:[...document.querySelectorAll('.img-container')].every(c=>c.getAttribute('aria-busy')==='false')}));
 if (!sessionStorage.getItem('uncertain')) location.href='/publish/success';
}
</script>`

func TestPublishTextLocalPreservesFullArticleAndOriginalTitle(t *testing.T) {
	b := newSearchFixtureBrowser(t)
	for _, uncertain := range []bool{false, true} {
		name := "confirmed"
		if uncertain {
			name = "uncertain"
		}
		t.Run(name, func(t *testing.T) {
			page := newCreatorFixturePage(t, b, textPublishFixture)
			ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
			defer cancel()
			action, err := NewPublishTextAction(page.Context(ctx))
			require.NoError(t, err)
			if uncertain {
				page.MustEval(`() => sessionStorage.setItem('uncertain','1')`)
			}
			content := PublishImageContent{Title: "保留原始标题", Content: strings.Repeat("长文正文不截断。", 150)}
			_, err = action.Publish(ctx, content)
			if uncertain {
				require.ErrorIs(t, err, xhsErrors.ErrPublishUncertain)
			} else {
				require.NoError(t, err)
			}
			var article struct {
				Title string
				Body  string
			}
			require.NoError(t, json.Unmarshal([]byte(page.MustEval(`() => sessionStorage.getItem('article')`).String()), &article))
			require.Equal(t, content.Title, article.Title)
			require.Equal(t, content.Content, article.Body)
			var publication struct {
				Title   string
				Caption string
				Ready   bool
			}
			require.NoError(t, json.Unmarshal([]byte(page.MustEval(`() => sessionStorage.getItem('publication')`).String()), &publication))
			require.Equal(t, content.Title, publication.Title)
			require.Empty(t, strings.TrimSpace(publication.Caption), "简介不能重复完整长文")
			require.True(t, publication.Ready, "必须等所有文字卡片完成")
		})
	}
}

func TestTextPublicationWaitsForEveryCard(t *testing.T) {
	b := newSearchFixtureBrowser(t)
	page := b.MustPage("about:blank")
	defer page.Close()
	page.MustSetDocumentContent(`<div class="img-preview-area"><div class="img-container" aria-busy="false"></div><div class="img-container" aria-busy="true"></div></div>`)
	err := waitTextCardsReady(page.Timeout(300 * time.Millisecond))
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.NotErrorIs(t, err, xhsErrors.ErrPublishUncertain)
	page.MustEval(`() => document.querySelectorAll('.img-container')[1].setAttribute('aria-busy','false')`)
	require.NoError(t, waitTextCardsReady(page.Timeout(time.Second)))
}
