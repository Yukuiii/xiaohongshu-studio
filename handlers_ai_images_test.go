package main

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func workbenchTestRouter() *gin.Engine {
	r := gin.New()
	s := &AppServer{}
	r.POST("/ai/compose", s.aiComposeHandler)
	r.POST("/images/upload", s.uploadImagesHandler)
	r.GET("/api/v1/images/:id", s.getImageHandler)
	return r
}

func testPNG(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func isolateImageStorage(t *testing.T) {
	t.Helper()
	isolateDataRoot(t)
}

func uploadImageRequest(t *testing.T, data ...[]byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for _, item := range data {
		part, err := writer.CreateFormFile("files", "pretend.png")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(item); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/images/upload", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req
}

func TestComposeRejectsIncompleteSourcesWithoutCallingModel(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	defer upstream.Close()
	for _, sources := range [][]composeSource{nil, {{ID: "one", Title: "仅标题"}}, {{Content: "没有id"}}, make([]composeSource, 6)} {
		body, _ := json.Marshal(composeRequest{Config: LLMConfig{BaseURL: upstream.URL, APIKey: "secret", Model: "test"}, Topic: "选题", Sources: sources})
		rec := httptest.NewRecorder()
		workbenchTestRouter().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/ai/compose", bytes.NewReader(body)))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("incomplete source accepted: %d %s", rec.Code, rec.Body.String())
		}
	}
	if calls != 0 {
		t.Fatalf("called model %d times", calls)
	}
}

func TestComposeKeepsSourceBoundaryAndLongDraft(t *testing.T) {
	long := strings.Repeat("文", 1200)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if len(payload.Messages) != 2 {
			t.Error("unexpected messages")
			return
		}
		if payload.Messages[0].Role != "system" || strings.Contains(payload.Messages[0].Content, "SOURCE_ATTACK") {
			t.Error("source entered system prompt")
		}
		var input composeRequest
		if err := json.Unmarshal([]byte(payload.Messages[1].Content), &input); err != nil || len(input.Sources) != 1 || input.Sources[0].Content != "SOURCE_ATTACK" {
			t.Error("lost structured source boundary")
		}
		if strings.Contains(payload.Messages[1].Content, "secret") {
			t.Error("configuration entered prompt")
		}
		draft, _ := json.Marshal(composedDraft{Title: "草稿", Content: long})
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": "```json\n" + string(draft) + "\n```"}}}})
	}))
	defer upstream.Close()
	body, _ := json.Marshal(composeRequest{Config: LLMConfig{BaseURL: upstream.URL, APIKey: "secret", Model: "test"}, Topic: "选题", Sources: []composeSource{{ID: "one", Content: "SOURCE_ATTACK"}}})
	rec := httptest.NewRecorder()
	workbenchTestRouter().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/ai/compose", bytes.NewReader(body)))
	var response struct {
		Data composedDraft `json:"data"`
	}
	json.Unmarshal(rec.Body.Bytes(), &response)
	if rec.Code != http.StatusOK || response.Data.Content != long {
		t.Fatalf("draft failed or truncated: %d %s", rec.Code, rec.Body.String())
	}
}

func TestUploadAndAuthenticatedFetchCompatibleImage(t *testing.T) {
	isolateImageStorage(t)
	r := setupRoutes(NewAppServer(NewXiaohongshuService(), "service-token"))
	data := testPNG(t)
	rec := httptest.NewRecorder()
	upload := uploadImageRequest(t, data)
	upload.URL.Path = "/api/v1/images/upload"
	upload.Header.Set("Authorization", "Bearer service-token")
	r.ServeHTTP(rec, upload)
	var response struct {
		Data struct {
			Images []workbenchImage `json:"images"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusOK || len(response.Data.Images) != 1 {
		t.Fatalf("upload failed: %d %s", rec.Code, rec.Body.String())
	}
	img := response.Data.Images[0]
	if !filepath.IsAbs(img.Path) || !workbenchImageID.MatchString(img.ID) {
		t.Fatalf("invalid publish path: %+v", img)
	}
	stat, err := os.Stat(img.Path)
	if err != nil || stat.Mode().Perm() != 0600 {
		t.Fatalf("invalid file permissions: %v", err)
	}
	stored, err := os.ReadFile(img.Path)
	if err != nil || !bytes.Equal(stored, data) {
		t.Fatal("publish input bytes changed")
	}
	unauthenticated := httptest.NewRecorder()
	r.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodGet, img.URL, nil))
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatal("image accessible without service token")
	}
	get := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, img.URL, nil)
	req.Header.Set("Authorization", "Bearer service-token")
	r.ServeHTTP(get, req)
	if get.Code != http.StatusOK || !bytes.Equal(get.Body.Bytes(), data) || get.Header().Get("Content-Type") != "image/png" || get.Header().Get("X-Content-Type-Options") != "nosniff" || get.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("invalid image response: %d %v", get.Code, get.Header())
	}
}

func TestUploadRejectsForgedOversizedAndInvalidCountAtomically(t *testing.T) {
	for _, scenario := range []string{"forged", "oversized", "empty", "too-many", "broken-header"} {
		t.Run(scenario, func(t *testing.T) {
			isolateImageStorage(t)
			good := testPNG(t)
			var data [][]byte
			switch scenario {
			case "forged":
				data = [][]byte{good, []byte("<html>not an image</html>")}
			case "oversized":
				data = [][]byte{good, make([]byte, maxImageBytes+1)}
			case "too-many":
				for range 10 {
					data = append(data, good)
				}
			case "broken-header":
				data = [][]byte{good, []byte("\x89PNG\r\n\x1a\n")}
			}
			rec := httptest.NewRecorder()
			workbenchTestRouter().ServeHTTP(rec, uploadImageRequest(t, data...))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("invalid upload accepted: %d", rec.Code)
			}
			dir, _ := imageStorageDir()
			entries, err := os.ReadDir(dir)
			if err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Fatalf("failed batch left %d files", len(entries))
			}
		})
	}
}

func TestImageFetchRejectsTraversalAndEscapingSymlink(t *testing.T) {
	isolateImageStorage(t)
	s := &AppServer{}
	for _, id := range []string{"../secret", "%2e%2e%2fsecret", "file.png", strings.Repeat("a", 32) + ".svg", ""} {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
		c.Params = gin.Params{{Key: "id", Value: id}}
		s.getImageHandler(c)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("accepted id %q", id)
		}
	}
	dir, _ := imageStorageDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "private")
	if err := os.WriteFile(outside, []byte("private bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	id := strings.Repeat("b", 32) + ".png"
	if err := os.Symlink(outside, filepath.Join(dir, id)); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	workbenchTestRouter().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/images/"+id, nil))
	if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "private bytes") {
		t.Fatal("symlink escaped image storage")
	}
}

func TestAIRequestRejectsOversizeAndTrailingJSON(t *testing.T) {
	for _, body := range []string{`{} {}`, `{"topic":"` + strings.Repeat("x", maxAIRequestBytes) + `"}`} {
		rec := httptest.NewRecorder()
		workbenchTestRouter().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/ai/compose", strings.NewReader(body)))
		if rec.Code != http.StatusBadRequest {
			t.Fatal("accepted invalid request")
		}
		if bytes.Contains(rec.Body.Bytes(), []byte(strings.Repeat("x", 100))) {
			t.Fatal("reflected request body")
		}
	}
}

func TestGeneratedImageDownloadLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.CopyN(w, strings.NewReader(strings.Repeat("x", maxImageBytes+1)), maxImageBytes+1)
	}))
	defer server.Close()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if _, err := newLLMClient().downloadImage(req.Context(), server.URL); err == nil || !strings.Contains(err.Error(), "10MiB") {
		t.Fatalf("download limit missing: %v", err)
	}
}
