package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

const settingsTestToken = "settings-service-token"

func settingsJSONRequest(t *testing.T, router http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var data []byte
	if body != nil {
		var err error
		data, err = json.Marshal(body)
		require.NoError(t, err)
	}
	req := httptest.NewRequest(method, "http://localhost:18060"+path, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+settingsTestToken)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func settingsResponseView(t *testing.T, rec *httptest.ResponseRecorder) settingsView {
	t.Helper()
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	var response struct {
		Success bool         `json:"success"`
		Data    settingsView `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
	require.True(t, response.Success)
	for _, forbidden := range []string{`"api_key"`, "text-secret", "image-secret", settingsTestToken} {
		require.NotContains(t, rec.Body.String(), forbidden)
	}
	return response.Data
}

func TestSettingsAPIAuthenticationOriginAndAccountIndependence(t *testing.T) {
	manager := testAccountManager(t)
	_, err := manager.store.Create("第二个账号")
	require.NoError(t, err)
	before := manager.store.List()
	server := NewAccountAppServer(manager, settingsTestToken)
	server.settings = testSettingsStore(t)
	router := setupRoutes(server)
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		for _, auth := range []string{"", "Bearer wrong-token", "Basic " + settingsTestToken} {
			req := httptest.NewRequest(method, "/api/v1/settings", strings.NewReader(`{}`))
			req.Header.Set("Authorization", auth)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			require.Equal(t, http.StatusUnauthorized, rec.Code)
			require.Contains(t, rec.Body.String(), "UNAUTHORIZED")
		}
	}
	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		req := httptest.NewRequest(method, "http://localhost:18060/api/v1/settings", strings.NewReader(`{}`))
		req.Header.Set("Authorization", "Bearer "+settingsTestToken)
		req.Header.Set("Origin", "https://untrusted.example")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		require.Equal(t, http.StatusForbidden, rec.Code)
		require.Contains(t, rec.Body.String(), "CROSS_ORIGIN_REQUEST")
	}
	rec := settingsJSONRequest(t, router, http.MethodGet, "/api/v1/settings", nil)
	require.Equal(t, settingsView{}, settingsResponseView(t, rec))
	input := settingsUpdate{
		Text:  settingsInput("https://text.example/v1/chat/completions", "text-model", "text-secret"),
		Image: settingsInput("https://image.example/v1", "image-model", "image-secret"),
	}
	rec = settingsJSONRequest(t, router, http.MethodPut, "/api/v1/settings", input)
	view := settingsResponseView(t, rec)
	require.True(t, view.Initialized)
	require.Equal(t, settingsConfigView{BaseURL: "https://text.example/v1", Model: "text-model", HasAPIKey: true}, view.Text)
	require.True(t, view.Image.HasAPIKey)
	rec = settingsJSONRequest(t, router, http.MethodGet, "/api/v1/settings", nil)
	require.Equal(t, view, settingsResponseView(t, rec))
	rec = settingsJSONRequest(t, router, http.MethodPut, "/api/v1/settings", settingsUpdate{
		Text:  settingsConfigInput{BaseURL: "https://text.example/v1/models/", Model: "renamed"},
		Image: settingsConfigInput{BaseURL: "https://image.example/v1", Model: "image-model"},
	})
	require.Equal(t, "renamed", settingsResponseView(t, rec).Text.Model)
	require.Equal(t, "text-secret", server.settings.snapshot().Text.APIKey)
	rec = settingsJSONRequest(t, router, http.MethodDelete, "/api/v1/settings", nil)
	require.Equal(t, settingsView{Initialized: true}, settingsResponseView(t, rec))
	input.OnlyIfUninitialized = true
	rec = settingsJSONRequest(t, router, http.MethodPut, "/api/v1/settings", input)
	require.Equal(t, http.StatusConflict, rec.Code)
	require.Contains(t, rec.Body.String(), "SETTINGS_ALREADY_INITIALIZED")
	require.NotContains(t, rec.Body.String(), "text-secret")
	require.Equal(t, before, manager.store.List())
	require.Empty(t, manager.runtimes)
	reopened, err := openSettingsStore(server.settings.db)
	require.NoError(t, err)
	require.Equal(t, modelSettings{Initialized: true}, reopened.snapshot())
}

func TestSettingsAPIErrorsDoNotExposeKeysOrLogThem(t *testing.T) {
	server := NewAppServer(nil, settingsTestToken)
	server.settings = testSettingsStore(t)
	router := setupRoutes(server)
	var logs bytes.Buffer
	previousOutput := logrus.StandardLogger().Out
	logrus.SetOutput(&logs)
	t.Cleanup(func() { logrus.SetOutput(previousOutput) })
	for _, body := range []string{
		`{"text":{"base_url":"https://host/?key=text-secret","model":"text","api_key":"text-secret"}}`,
		`{"text":{"api_key":"text-secret"},"image":{"api_key":"image-secret"}}`,
		`{"text":{"api_key":["text-secret"]}}`,
		`{"text":{"api_key":"text-secret"}} {}`,
		`{"text":{"api_key":"text-secret"`,
	} {
		req := httptest.NewRequest(http.MethodPut, "/api/v1/settings", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+settingsTestToken)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		require.Equal(t, http.StatusBadRequest, rec.Code)
		require.NotContains(t, rec.Body.String(), "text-secret")
		require.NotContains(t, rec.Body.String(), "image-secret")
	}
	require.NotContains(t, logs.String(), "text-secret")
	require.NotContains(t, logs.String(), "image-secret")
	require.False(t, server.settings.snapshot().Initialized)
	server.settings = nil
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		rec := settingsJSONRequest(t, router, method, "/api/v1/settings", settingsUpdate{})
		require.Equal(t, http.StatusServiceUnavailable, rec.Code)
		require.Contains(t, rec.Body.String(), "SETTINGS_UNAVAILABLE")
	}
}

func TestAIHandlersUsePersistedTextAndImageConfiguration(t *testing.T) {
	manager := testAccountManager(t)
	png := testPNG(t)
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		key := "text-secret"
		if strings.HasPrefix(r.URL.Path, "/image/") {
			key = "image-secret"
		}
		if r.Header.Get("Authorization") != "Bearer "+key {
			t.Error("upstream received incorrect saved key")
		}
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		if bytes.Contains(data, []byte("-secret")) {
			t.Error("key entered model payload")
		}
		var payload struct {
			Model  string `json:"model"`
			Prompt string `json:"prompt"`
		}
		if len(data) > 0 {
			if err := json.Unmarshal(data, &payload); err != nil {
				t.Error(err)
			}
		}
		switch r.URL.Path {
		case "/text/chat/completions":
			if payload.Model != "text-model" {
				t.Error("compose lost saved model")
			}
			json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": `{"title":"草稿","content":"正文","tags":["测试"]}`}}}})
		case "/image/images/generations":
			if payload.Model != "image-model" || payload.Prompt != "flowers" {
				t.Error("image lost saved model or prompt")
			}
			json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]string{"b64_json": base64.StdEncoding.EncodeToString(png)}}})
		case "/text/models":
			io.WriteString(w, `{"data":[{"id":"text-model"}]}`)
		case "/image/models":
			io.WriteString(w, `{"data":[{"id":"image-model"}]}`)
		default:
			t.Errorf("unexpected upstream path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer upstream.Close()
	store := testSettingsStore(t)
	_, err := store.update(settingsUpdate{
		Text:  settingsInput(upstream.URL+"/text/chat/completions", "text-model", "text-secret"),
		Image: settingsInput(upstream.URL+"/image", "image-model", "image-secret"),
	})
	require.NoError(t, err)
	server := NewAccountAppServer(manager, settingsTestToken)
	server.settings, err = openSettingsStore(store.db)
	require.NoError(t, err)
	router := setupRoutes(server)
	for _, config := range []any{nil, map[string]string{}, map[string]string{"base_url": " ", "api_key": " ", "model": " "}} {
		compose := map[string]any{"topic": "选题", "sources": []composeSource{{ID: "one", Content: "原始资料"}}}
		image := map[string]any{"prompt": "flowers"}
		if config != nil {
			compose["config"] = config
			image["config"] = config
		}
		rec := settingsJSONRequest(t, router, http.MethodPost, "/api/v1/ai/compose", compose)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		require.Contains(t, rec.Body.String(), "草稿")
		rec = settingsJSONRequest(t, router, http.MethodPost, "/api/v1/ai/image", image)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var response struct {
			Data struct {
				Image workbenchImage `json:"image"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
		dir, err := imageStorageDirForAccount(manager.store.List()[0].ID)
		require.NoError(t, err)
		require.Equal(t, dir, filepath.Dir(response.Data.Image.Path))
		data, err := os.ReadFile(response.Data.Image.Path)
		require.NoError(t, err)
		require.Equal(t, png, data)
	}
	for _, kind := range []string{"", "text", "image"} {
		rec := settingsJSONRequest(t, router, http.MethodPost, "/api/v1/ai/models", map[string]any{"kind": kind})
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		want := "text-model"
		if kind == "image" {
			want = "image-model"
		}
		require.Contains(t, rec.Body.String(), want)
		require.NotContains(t, rec.Body.String(), "-secret")
	}
	require.Equal(t, int32(9), calls.Load())
}

func TestAISettingsKeyReuseStaysOnConfiguredURL(t *testing.T) {
	isolateImageStorage(t)
	png := testPNG(t)
	var savedCalls, temporaryCalls atomic.Int32
	provider := func(counter *atomic.Int32, key string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			counter.Add(1)
			if r.Header.Get("Authorization") != "Bearer "+key {
				t.Error("upstream received a key belonging to another configuration")
			}
			switch r.URL.Path {
			case "/v1/chat/completions":
				io.WriteString(w, `{"choices":[{"message":{"content":"{\"title\":\"题\",\"content\":\"文\"}"}}]}`)
			case "/v1/images/generations":
				json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]string{"b64_json": base64.StdEncoding.EncodeToString(png)}}})
			case "/v1/models":
				io.WriteString(w, `{"data":[{"id":"test-model"}]}`)
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
	}
	saved := provider(&savedCalls, "stored-secret")
	defer saved.Close()
	temporary := provider(&temporaryCalls, "temporary-secret")
	defer temporary.Close()
	server := NewAppServer(nil, settingsTestToken)
	server.settings = testSettingsStore(t)
	_, err := server.settings.update(settingsUpdate{
		Text:  settingsInput(saved.URL+"/v1", "text", "stored-secret"),
		Image: settingsInput(saved.URL+"/v1", "image", "stored-secret"),
	})
	require.NoError(t, err)
	router := setupRoutes(server)
	for _, endpoint := range []string{"compose", "image", "models"} {
		body := map[string]any{"topic": "选题", "sources": []composeSource{{ID: "one", Content: "资料"}}, "prompt": "flowers"}
		body["config"] = LLMConfig{BaseURL: saved.URL + "/v1/models/", Model: "override"}
		rec := settingsJSONRequest(t, router, http.MethodPost, "/api/v1/ai/"+endpoint, body)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		for _, base := range []string{temporary.URL + "/v1", saved.URL + "/different-path"} {
			body["config"] = LLMConfig{BaseURL: base, Model: "override"}
			rec = settingsJSONRequest(t, router, http.MethodPost, "/api/v1/ai/"+endpoint, body)
			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			require.Contains(t, rec.Body.String(), "INVALID_CONFIG")
			require.NotContains(t, rec.Body.String(), "stored-secret")
		}
		body["config"] = LLMConfig{BaseURL: temporary.URL + "/v1", Model: "temporary", APIKey: "temporary-secret"}
		rec = settingsJSONRequest(t, router, http.MethodPost, "/api/v1/ai/"+endpoint, body)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	}
	require.Equal(t, int32(3), savedCalls.Load())
	require.Equal(t, int32(3), temporaryCalls.Load())
	for _, kind := range []string{"video", "TEXT", "unknown-secret"} {
		rec := settingsJSONRequest(t, router, http.MethodPost, "/api/v1/ai/models", map[string]any{
			"kind": kind, "config": LLMConfig{BaseURL: temporary.URL + "/v1", APIKey: "temporary-secret"},
		})
		require.Equal(t, http.StatusBadRequest, rec.Code)
		require.Contains(t, rec.Body.String(), "INVALID_KIND")
		require.NotContains(t, rec.Body.String(), "unknown-secret")
	}
	require.Equal(t, int32(3), temporaryCalls.Load())
	server.settings = nil
	for _, endpoint := range []string{"compose", "image", "models"} {
		body := map[string]any{
			"config":  LLMConfig{BaseURL: temporary.URL + "/v1", Model: "legacy", APIKey: "temporary-secret"},
			"topic":   "选题",
			"sources": []composeSource{{ID: "one", Content: "资料"}},
			"prompt":  "flowers",
		}
		rec := settingsJSONRequest(t, router, http.MethodPost, "/api/v1/ai/"+endpoint, body)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	}
	require.Equal(t, int32(6), temporaryCalls.Load())
}
