package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Yukuiii/xiaohongshu-studio/accounts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testPromptStore(t *testing.T) *promptStore {
	t.Helper()
	store, err := openPromptStore(testSettingsDB(t))
	require.NoError(t, err)
	return store
}

func promptByKey(t *testing.T, views []promptTemplateView, key string) promptTemplateView {
	t.Helper()
	for _, view := range views {
		if view.Key == key {
			return view
		}
	}
	t.Fatalf("missing prompt %s", key)
	return promptTemplateView{}
}

func TestPromptStoreDefaultsAndOrder(t *testing.T) {
	store := testPromptStore(t)
	views := store.view()
	require.Len(t, views, 4)
	assert.Equal(t, "compose_system", views[0].Key)
	assert.Equal(t, "compose_user", views[1].Key)
	assert.Equal(t, "schedule_system", views[2].Key)
	assert.Equal(t, "schedule_user", views[3].Key)
	for _, view := range views {
		assert.False(t, view.Customized)
		assert.Equal(t, view.DefaultContent, view.Content)
		assert.NotEmpty(t, view.Variables)
		assert.Nil(t, view.UpdatedAt)
	}
	set := store.snapshot()
	assert.Equal(t, composeSystemTemplate, set.ComposeSystem)
	assert.Equal(t, composeUserTemplate, set.ComposeUser)
	assert.Equal(t, scheduleSystemTemplate, set.ScheduleSystem)
	assert.Equal(t, scheduleUserTemplate, set.ScheduleUser)
}

func TestPromptStoreSaveReopenAndReset(t *testing.T) {
	db := testSettingsDB(t)
	store, err := openPromptStore(db)
	require.NoError(t, err)
	custom := "自定义系统提示词 {{publication_type}} / {{title_limit}} / {{content_limit}}"
	view, err := store.save("compose_system", custom)
	require.NoError(t, err)
	assert.True(t, view.Customized)
	assert.Equal(t, custom, view.Content)
	assert.NotNil(t, view.UpdatedAt)

	reopened, err := openPromptStore(db)
	require.NoError(t, err)
	assert.Equal(t, custom, reopened.snapshot().ComposeSystem)
	assert.True(t, promptByKey(t, reopened.view(), "compose_system").Customized)

	view, err = reopened.reset("compose_system")
	require.NoError(t, err)
	assert.False(t, view.Customized)
	assert.Equal(t, composeSystemTemplate, view.Content)

	third, err := openPromptStore(db)
	require.NoError(t, err)
	assert.Equal(t, composeSystemTemplate, third.snapshot().ComposeSystem)
}

func TestPromptStoreValidation(t *testing.T) {
	store := testPromptStore(t)
	for _, tc := range []struct {
		name    string
		key     string
		content string
		want    string
	}{
		{name: "empty", key: "compose_system", content: " \n\t ", want: "不能为空"},
		{name: "missing variable", key: "compose_system", content: "只有 {{publication_type}}", want: "缺少必需变量"},
		{name: "unknown variable", key: "compose_user", content: "{{topic}} {{instructions}} {{sources}} {{extra}}", want: "不可用变量"},
		{name: "invalid utf8", key: "compose_user", content: "{{topic}} {{instructions}} {{sources}}" + string([]byte{0xff}), want: "UTF-8"},
		{name: "oversize", key: "compose_user", content: "{{topic}} {{instructions}} {{sources}}" + strings.Repeat("a", maxPromptBytes), want: "64 KiB"},
		{name: "unknown key", key: "image", content: "x", want: "不存在"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := store.save(tc.key, tc.content)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestPromptStoreRejectsInvalidStoredTemplate(t *testing.T) {
	db := testSettingsDB(t)
	_, err := db.Exec(
		`INSERT INTO prompt_templates (name, content, updated_at) VALUES ('compose_system', '没有变量', 1)`,
	)
	require.NoError(t, err)
	_, err = openPromptStore(db)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "缺少必需变量")
}

func TestNilPromptStoreUsesDefaults(t *testing.T) {
	var store *promptStore
	assert.Equal(t, defaultPromptSet(), store.snapshot())
	assert.Len(t, store.view(), 4)
	_, err := store.save("compose_system", "{{publication_type}} {{title_limit}} {{content_limit}}")
	require.ErrorIs(t, err, errPromptUnavailable)
}

func promptTestUpstream(t *testing.T) (*httptest.Server, *string, *string) {
	t.Helper()
	var system, user string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
			return
		}
		if len(payload.Messages) != 2 {
			t.Error("unexpected messages")
			return
		}
		system, user = payload.Messages[0].Content, payload.Messages[1].Content
		draft, _ := json.Marshal(composedDraft{Title: "标题", Content: "正文"})
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]string{"content": string(draft)}}},
		})
	}))
	t.Cleanup(server.Close)
	return server, &system, &user
}

func TestComposeUsesCustomPromptTemplates(t *testing.T) {
	store := testPromptStore(t)
	_, err := store.save("compose_system", "CUSTOM SYSTEM {{publication_type}} {{title_limit}} {{content_limit}}")
	require.NoError(t, err)
	_, err = store.save("compose_user", "CUSTOM USER {{topic}} {{instructions}} {{sources}}")
	require.NoError(t, err)
	upstream, system, user := promptTestUpstream(t)
	cfg := LLMConfig{BaseURL: upstream.URL, APIKey: "secret", Model: "test"}
	draft, err := composeDraft(context.Background(), cfg, "选题", "要求", []composeSource{{ID: "one", Content: "正文"}}, store.snapshot())
	require.NoError(t, err)
	assert.Equal(t, "标题", draft.Title)
	assert.Contains(t, *system, "CUSTOM SYSTEM")
	assert.Contains(t, *user, "CUSTOM USER")
	assert.NotContains(t, *system, "CUSTOM USER")
	assert.NotContains(t, *user, "CUSTOM SYSTEM")
}

func TestComposeKeywordUsesCustomPromptTemplates(t *testing.T) {
	store := testPromptStore(t)
	_, err := store.save("schedule_system", "SCHEDULE SYSTEM {{publication_type}} {{title_limit}} {{content_limit}}")
	require.NoError(t, err)
	_, err = store.save("schedule_user", "SCHEDULE USER {{keyword}} {{instructions}} {{recent_notes}}")
	require.NoError(t, err)
	upstream, system, user := promptTestUpstream(t)
	cfg := LLMConfig{BaseURL: upstream.URL, APIKey: "secret", Model: "test"}
	_, err = composeKeywordDraft(context.Background(), cfg, "关键词", "", "image", []accounts.PublishedNote{{Title: "旧标题", Content: "旧正文"}}, store.snapshot())
	require.NoError(t, err)
	assert.Contains(t, *system, "SCHEDULE SYSTEM")
	assert.Contains(t, *user, "SCHEDULE USER")
	assert.Contains(t, *user, "旧标题")
}
