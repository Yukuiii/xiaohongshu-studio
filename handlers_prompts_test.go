package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const promptTestToken = "prompt-service-token"

func promptTestRouter(t *testing.T) (http.Handler, *promptStore) {
	t.Helper()
	server := NewAppServer(nil, promptTestToken)
	server.prompts = testPromptStore(t)
	return setupRoutes(server), server.prompts
}

func promptRequest(t *testing.T, router http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var data []byte
	if body != nil {
		var err error
		data, err = json.Marshal(body)
		require.NoError(t, err)
	}
	req := httptest.NewRequest(method, "http://localhost:18060"+path, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+promptTestToken)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestPromptsAPIListSaveResetWithoutAccountHeader(t *testing.T) {
	router, _ := promptTestRouter(t)
	rec := promptRequest(t, router, http.MethodGet, "/api/v1/prompts", nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	var listed struct {
		Success bool `json:"success"`
		Data    struct {
			Prompts []promptTemplateView `json:"prompts"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &listed))
	require.True(t, listed.Success)
	require.Len(t, listed.Data.Prompts, 4)
	assert.False(t, promptByKey(t, listed.Data.Prompts, "compose_system").Customized)

	custom := "自定义 {{publication_type}} {{title_limit}} {{content_limit}}"
	rec = promptRequest(t, router, http.MethodPut, "/api/v1/prompts/compose_system", map[string]string{"content": custom})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var saved struct {
		Data struct {
			Prompt promptTemplateView `json:"prompt"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &saved))
	assert.True(t, saved.Data.Prompt.Customized)
	assert.Equal(t, custom, saved.Data.Prompt.Content)

	rec = promptRequest(t, router, http.MethodGet, "/api/v1/prompts", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &listed))
	assert.True(t, promptByKey(t, listed.Data.Prompts, "compose_system").Customized)

	rec = promptRequest(t, router, http.MethodDelete, "/api/v1/prompts/compose_system", nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &saved))
	assert.False(t, saved.Data.Prompt.Customized)
	assert.Equal(t, composeSystemTemplate, saved.Data.Prompt.Content)
}

func TestPromptsAPIValidationAndUnknownKey(t *testing.T) {
	router, _ := promptTestRouter(t)
	for _, tc := range []struct {
		name string
		key  string
		body map[string]string
		code string
	}{
		{name: "missing variables", key: "compose_system", body: map[string]string{"content": "没有变量"}, code: "INVALID_PROMPT"},
		{name: "empty", key: "compose_user", body: map[string]string{"content": ""}, code: "INVALID_PROMPT"},
		{name: "unknown key", key: "image", body: map[string]string{"content": "x"}, code: "PROMPT_NOT_FOUND"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := promptRequest(t, router, http.MethodPut, "/api/v1/prompts/"+tc.key, tc.body)
			require.Equal(t, map[string]int{"INVALID_PROMPT": http.StatusBadRequest, "PROMPT_NOT_FOUND": http.StatusNotFound}[tc.code], rec.Code, rec.Body.String())
			assert.Contains(t, rec.Body.String(), tc.code)
		})
	}
}

func TestPromptsAPIReportsUnavailableStore(t *testing.T) {
	server := NewAppServer(nil, promptTestToken)
	router := setupRoutes(server)
	rec := promptRequest(t, router, http.MethodGet, "/api/v1/prompts", nil)
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Contains(t, rec.Body.String(), "PROMPTS_UNAVAILABLE")
}
