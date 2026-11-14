package xiaohongshu

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPublishedNoteResponse(t *testing.T) {
	const id = "6af8bd4c000000002a01272b"
	for _, field := range []string{"id", "note_id", "noteId"} {
		t.Run(field, func(t *testing.T) {
			note, err := parsePublishedNoteResponse([]byte(`{"code":0,"success":true,"data":{"` + field + `":"` + id + `"}}`))
			require.NoError(t, err)
			require.Equal(t, id, note.NoteID)
			require.Empty(t, note.NoteURL, "裸 ID 不能作为可用网页链接")
			require.Empty(t, note.NoteLinkError)
		})
	}
	for _, body := range []string{
		`{"code":0,"success":true,"data":{}}`,
		`{"code":0,"success":false,"data":{"id":"6af8bd4c000000002a01272b"}}`,
		`{"code":-1,"success":true,"data":{"id":"6af8bd4c000000002a01272b"}}`,
		`{"data":{"id":"6af8bd4c000000002a01272b"}}`,
		`{"code":0,"data":{"id":"6af8bd4c000000002a01272b","note_id":"6af8bd4c000000002a01272c"}}`,
		`{"code":0,"data":{"id":"../../other"}}`,
		`{"code":0,"data":{"id":"note?redirect=bad"}}`,
		`{"code":0,"data":{"id":123}}`,
		`<html>错误页</html>`,
	} {
		note, err := parsePublishedNoteResponse([]byte(body))
		require.Error(t, err, body)
		require.Empty(t, note.NoteID, body)
		require.Empty(t, note.NoteURL, body)
	}
}

func TestPublishedNoteURLUsesExactIDAndAccessToken(t *testing.T) {
	const id = "6af8bd4c000000002a01272b"
	feeds := []Feed{{ID: "6af8bd4c000000002a01272c", XsecToken: "wrong-token"}, {ID: id, XsecToken: "token+a/="}}
	raw, err := publishedNoteURLFromFeeds(id, feeds)
	require.NoError(t, err)
	link, err := url.Parse(raw)
	require.NoError(t, err)
	require.Equal(t, "https", link.Scheme)
	require.Equal(t, "www.xiaohongshu.com", link.Host)
	require.Equal(t, "/explore/"+id, link.Path)
	require.Equal(t, "token+a/=", link.Query().Get("xsec_token"))
	require.Equal(t, "pc_user", link.Query().Get("xsec_source"))
	for _, items := range [][]Feed{feeds[:1], {{ID: id}}, {{ID: id, XsecToken: "bad token"}}} {
		link, err := publishedNoteURLFromFeeds(id, items)
		require.Error(t, err)
		require.Empty(t, link)
	}
}

func TestCreateNoteURLBoundary(t *testing.T) {
	require.True(t, isCreateNoteURL("https://edith.xiaohongshu.com/web_api/sns/v2/note"))
	require.True(t, isCreateNoteURL("https://edith.xiaohongshu.com/web_api/sns/v2/note?source=web"))
	for _, raw := range []string{
		"https://edith.xiaohongshu.com/web_api/sns/capa/postgw/note/update",
		"https://edith.xiaohongshu.com/web_api/sns/v2/note/detail",
		"https://edith.xiaohongshu.com.example.com/web_api/sns/v2/note",
		"https://example.com/web_api/sns/v2/note",
		"http://edith.xiaohongshu.com/web_api/sns/v2/note",
		"https://other@edith.xiaohongshu.com/web_api/sns/v2/note",
		"://invalid",
	} {
		require.False(t, isCreateNoteURL(raw), raw)
	}
}
