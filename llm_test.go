package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNormalizeLLMURL(t *testing.T) {
	for input, want := range map[string]string{
		" https://example.com/v1/chat/completions/ ":    "https://example.com/v1",
		"http://localhost:1234/proxy/models":            "http://localhost:1234/proxy",
		"https://example.com":                           "https://example.com",
		"https://example.com/custom/images/generations": "https://example.com/custom",
	} {
		got, err := normalizeBaseURL(input)
		if err != nil || got != want {
			t.Fatalf("%q: got %q, %v", input, got, err)
		}
	}
	for _, input := range []string{"https://host/?key=secret", "https://host/?", "https://host/#secret", "https://host/#", "https://secret@host", "ftp://host", "https://"} {
		if _, err := normalizeBaseURL(input); err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatalf("unsafe URL error: %q %v", input, err)
		}
	}
}

func TestParseComposedDraft(t *testing.T) {
	long := strings.Repeat("文", 1200)
	payload, _ := json.Marshal(composedDraft{Title: strings.Repeat("题", 30), Content: long, Tags: []string{"生活"}})
	draft, err := parseComposedDraft("```json\n" + string(payload) + "\n```")
	if err != nil || draft.Content != long || len([]rune(draft.Title)) != 30 {
		t.Fatalf("draft was rejected or truncated: %v", err)
	}
	for _, input := range []string{`{}`, `{"title":"题"}`, `{"title":1,"content":"正文"}`, `{"title":"题","content":"正文","tags":[1]}`, `{"title":"题","content":"正文","image_prompt":{}}`, "```json\n{}", `{"title":"题","content":"正文"} trailing`} {
		if _, err := parseComposedDraft(input); err == nil {
			t.Fatalf("accepted malformed draft %q", input)
		}
	}
}

func TestLLMUpstreamBehavior(t *testing.T) {
	key := "test-secret-key"
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/proxy/chat/completions" || r.Header.Get("Authorization") != "Bearer "+key {
			t.Errorf("unexpected request %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"error":{"message":"`+key+`"}}`)
	}))
	defer server.Close()
	_, err := newLLMClient().chat(context.Background(), LLMConfig{BaseURL: server.URL + "/proxy", APIKey: key, Model: "test"}, "system", "user")
	if err == nil || strings.Contains(err.Error(), key) || !strings.Contains(err.Error(), "401") || calls != 1 {
		t.Fatalf("unexpected upstream handling: %v calls=%d", err, calls)
	}
}

func TestLLMCancellation(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := newLLMClient().chat(ctx, LLMConfig{BaseURL: server.URL, APIKey: "secret", Model: "test"}, "s", "u")
		done <- err
	}()
	<-started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("missing cancellation: %v", err)
	}
}

func TestLLMRedirectDoesNotForwardKey(t *testing.T) {
	forwarded := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forwarded = true }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	_, err := newLLMClient().listModels(context.Background(), LLMConfig{BaseURL: server.URL, APIKey: "secret"})
	if err == nil || forwarded {
		t.Fatalf("redirect followed: %v", err)
	}
}

func TestLLMResponseLimitsAndMalformedJSON(t *testing.T) {
	for _, body := range []string{"not json", strings.Repeat(" ", maxLLMResponseBytes+1)} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, body) }))
		_, err := newLLMClient().listModels(context.Background(), LLMConfig{BaseURL: server.URL, APIKey: "secret"})
		server.Close()
		if err == nil {
			t.Fatal("accepted invalid response")
		}
		if len(body) > maxLLMResponseBytes && !strings.Contains(err.Error(), "32MiB") {
			t.Fatalf("missing size error: %v", err)
		}
	}
}

func TestLLMImageFormatsAndModels(t *testing.T) {
	image := []byte("download bytes are validated when saved")
	for _, mode := range []string{"b64", "url"} {
		var server *httptest.Server
		server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/models":
				io.WriteString(w, `{"data":[{"id":"z"},{"id":"a"}]}`)
			case "/images/generations":
				var req map[string]any
				json.NewDecoder(r.Body).Decode(&req)
				if req["prompt"] != "flowers" || req["model"] != "image-test" {
					t.Error("image request lost prompt/model")
				}
				item := map[string]string{"b64_json": base64.StdEncoding.EncodeToString(image)}
				if mode == "url" {
					item = map[string]string{"url": server.URL + "/picture?signature=ok"}
				}
				json.NewEncoder(w).Encode(map[string]any{"data": []any{item}})
			case "/picture":
				if r.Header.Get("Authorization") != "" {
					t.Error("image download leaked model key")
				}
				w.Write(image)
			}
		}))
		cfg := LLMConfig{BaseURL: server.URL, APIKey: "secret", Model: "image-test"}
		got, err := newLLMClient().generateImage(context.Background(), cfg, "flowers")
		if err != nil || string(got) != string(image) {
			t.Fatalf("%s image: %v", mode, err)
		}
		models, err := newLLMClient().listModels(context.Background(), cfg)
		if err != nil || strings.Join(models, ",") != "a,z" {
			t.Fatalf("models: %v %v", models, err)
		}
		server.Close()
	}
}
