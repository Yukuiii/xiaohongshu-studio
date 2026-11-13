package main

import (
	"encoding/json"
	"io/fs"
	"mime"
	"net/http"
	"net/http/httptest"
	"path"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceRemainsPublicWithAuthentication(t *testing.T) {
	router := setupRoutes(NewAppServer(nil, "secret-token"))
	assets := []struct {
		path        string
		contentType string
		data        []byte
	}{{"/", "text/html; charset=utf-8", webIndex}}
	err := fs.WalkDir(webAssets, "web/dist/assets", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := webAssets.ReadFile(name)
		if err != nil {
			return err
		}
		contentType := mime.TypeByExtension(path.Ext(name))
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		assets = append(assets, struct {
			path        string
			contentType string
			data        []byte
		}{"/web/assets/" + strings.TrimPrefix(name, "web/dist/assets/"), contentType, data})
		return nil
	})
	require.NoError(t, err)
	for _, asset := range assets {
		t.Run(asset.path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, asset.path, nil))
			require.Equal(t, http.StatusOK, recorder.Code)
			assert.Equal(t, asset.contentType, recorder.Header().Get("Content-Type"))
			assert.Equal(t, asset.data, recorder.Body.Bytes())
			assert.Equal(t, "no-cache", recorder.Header().Get("Cache-Control"))
			assert.Equal(t, "nosniff", recorder.Header().Get("X-Content-Type-Options"))
			assert.Equal(t, "no-referrer", recorder.Header().Get("Referrer-Policy"))
			assert.Contains(t, recorder.Header().Get("Content-Security-Policy"), "script-src 'self'")
			assert.Contains(t, recorder.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'")
		})
	}
}

func TestWorkspaceDoesNotExposeSourceOrDirectories(t *testing.T) {
	router := setupRoutes(NewAppServer(nil, "secret-token"))
	for _, assetPath := range []string{
		"/web/app.js", "/web/style.css", "/web/index.html", "/web/src/main.jsx",
		"/web/package.json", "/web/dist/index.html", "/web/assets/",
		"/web/assets/missing.js", "/web/assets/../index.html",
	} {
		t.Run(assetPath, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, assetPath, nil))
			assert.Equal(t, http.StatusNotFound, recorder.Code)
		})
	}
}

func TestHealthReportsStudioVersionWithoutAuthentication(t *testing.T) {
	router := setupRoutes(NewAppServer(nil, "secret-token"))
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/health", nil))
	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Success bool              `json:"success"`
		Data    map[string]string `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	assert.True(t, response.Success)
	assert.Equal(t, map[string]string{
		"status":    "healthy",
		"service":   "xiaohongshu-studio",
		"version":   version,
		"timestamp": "now",
	}, response.Data)
}

func TestProtectedRoutesRequireBearerToken(t *testing.T) {
	router := setupRoutes(NewAppServer(nil, "secret-token"))
	for _, endpoint := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/login/session"},
		{http.MethodGet, "/api/v1/account/verify"},
		{http.MethodPost, "/api/v1/login/browser"},
		{http.MethodDelete, "/api/v1/login/browser"},
		{http.MethodDelete, "/api/v1/login/cookies"},
		{http.MethodPost, "/api/v1/publish"},
		{http.MethodPost, "/api/v1/publish_video"},
		{http.MethodGet, "/api/v1/feeds/list"},
		{http.MethodGet, "/api/v1/feeds/search"},
		{http.MethodPost, "/api/v1/feeds/search"},
		{http.MethodPost, "/api/v1/feeds/detail"},
		{http.MethodPost, "/api/v1/feeds/bodies"},
		{http.MethodPost, "/api/v1/user/profile"},
		{http.MethodPost, "/api/v1/feeds/comment"},
		{http.MethodPost, "/api/v1/feeds/comment/reply"},
		{http.MethodPost, "/api/v1/feeds/like"},
		{http.MethodPost, "/api/v1/feeds/favorite"},
		{http.MethodGet, "/api/v1/user/me"},
		{http.MethodGet, "/api/v1/notifications/unread"},
		{http.MethodGet, "/api/v1/notifications/list"},
		{http.MethodPost, "/api/v1/notifications/list"},
		{http.MethodPost, "/api/v1/notifications/reply"},
		{http.MethodPost, "/api/v1/notifications/like"},
		{http.MethodPost, "/api/v1/ai/models"},
		{http.MethodPost, "/api/v1/ai/compose"},
		{http.MethodPost, "/api/v1/ai/image"},
		{http.MethodPost, "/api/v1/images/upload"},
		{http.MethodGet, "/api/v1/images/example.png"},
	} {
		t.Run(endpoint.method+" "+endpoint.path, func(t *testing.T) {
			for _, authorization := range []string{"", "Bearer wrong-token", "Basic secret-token"} {
				request := httptest.NewRequest(endpoint.method, endpoint.path, strings.NewReader(`{}`))
				request.Header.Set("Content-Type", "application/json")
				request.Header.Set("Authorization", authorization)
				recorder := httptest.NewRecorder()
				router.ServeHTTP(recorder, request)
				require.Equal(t, http.StatusUnauthorized, recorder.Code, "authorization: %q", authorization)
				assert.Equal(t, "Bearer", recorder.Header().Get("WWW-Authenticate"))
				var response ErrorResponse
				require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
				assert.Equal(t, "UNAUTHORIZED", response.Code)
			}
		})
	}
}

func TestAPIPreflightRemainsPublic(t *testing.T) {
	router := setupRoutes(NewAppServer(nil, "secret-token"))
	request := httptest.NewRequest(http.MethodOptions, "/api/v1/ai/compose", nil)
	request.Header.Set("Origin", "http://localhost:18060")
	request.Header.Set("Access-Control-Request-Method", http.MethodPost)
	request.Header.Set("Access-Control-Request-Headers", "Authorization, Content-Type")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	assert.Equal(t, http.StatusNoContent, recorder.Code)
	assert.Contains(t, recorder.Header().Get("Access-Control-Allow-Headers"), "Authorization")
}

func TestRemovedProtocolEndpointsReturnNotFound(t *testing.T) {
	for _, token := range []string{"", "secret-token"} {
		router := setupRoutes(NewAppServer(nil, token))
		for _, path := range []string{"/mcp", "/mcp/", "/mcp/child", "/mcp/child/nested"} {
			for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodDelete, http.MethodOptions} {
				for _, authorization := range []string{"", "Bearer secret-token"} {
					t.Run(token+" "+method+" "+path+" "+authorization, func(t *testing.T) {
						request := httptest.NewRequest(method, path, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
						request.Header.Set("Content-Type", "application/json")
						request.Header.Set("Authorization", authorization)
						recorder := httptest.NewRecorder()
						router.ServeHTTP(recorder, request)
						assert.Equal(t, http.StatusNotFound, recorder.Code)
						assert.Empty(t, recorder.Header().Get("WWW-Authenticate"))
					})
				}
			}
		}
	}
}

func TestLoginStatusEndpointRemoved(t *testing.T) {
	router := setupRoutes(NewAppServer(nil, ""))
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		request := httptest.NewRequest(method, "/api/v1/login/status", nil)
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		assert.Equal(t, http.StatusNotFound, recorder.Code)
	}
}

func TestRESTRejectsCrossSiteWrites(t *testing.T) {
	for _, token := range []string{"", "workspace-token"} {
		router := setupRoutes(NewAppServer(nil, token))
		for _, endpoint := range []struct {
			method string
			path   string
		}{
			{http.MethodPost, "/api/v1/publish"},
			{http.MethodDelete, "/api/v1/login/cookies"},
			{http.MethodPost, "/api/v1/login/browser"},
			{http.MethodDelete, "/api/v1/login/browser"},
			{http.MethodPost, "/api/v1/ai/models"},
			{http.MethodPost, "/api/v1/ai/compose"},
			{http.MethodPost, "/api/v1/ai/image"},
			{http.MethodPost, "/api/v1/images/upload"},
		} {
			for _, source := range []struct {
				name        string
				origin      string
				fetchSite   string
				contentType string
			}{
				{"cross-site JSON", "https://untrusted.example", "", "application/json"},
				{"cross-site simple request", "https://untrusted.example", "cross-site", "text/plain"},
				{"opaque origin", "null", "", "application/json"},
				{"fetch metadata", "", "cross-site", "application/json"},
			} {
				t.Run(token+endpoint.path+"/"+source.name, func(t *testing.T) {
					request := httptest.NewRequest(endpoint.method, "http://localhost:18060"+endpoint.path, strings.NewReader(`{}`))
					request.Header.Set("Origin", source.origin)
					request.Header.Set("Sec-Fetch-Site", source.fetchSite)
					request.Header.Set("Content-Type", source.contentType)
					if token != "" {
						request.Header.Set("Authorization", "Bearer "+token)
					}
					recorder := httptest.NewRecorder()
					router.ServeHTTP(recorder, request)
					assert.Equal(t, http.StatusForbidden, recorder.Code)
					assert.Contains(t, recorder.Body.String(), "CROSS_ORIGIN_REQUEST")
				})
			}
		}
	}
}

func TestRESTAcceptsSameOriginAndNativeRequests(t *testing.T) {
	for _, token := range []string{"", "workspace-token"} {
		router := setupRoutes(NewAppServer(nil, token))
		for _, path := range []string{"/api/v1/publish", "/api/v1/ai/models"} {
			for _, origin := range []string{"", "http://localhost:18060"} {
				t.Run(token+path+"/"+origin, func(t *testing.T) {
					request := httptest.NewRequest(http.MethodPost, "http://localhost:18060"+path, strings.NewReader(`{`))
					request.Header.Set("Content-Type", "application/json")
					request.Header.Set("Origin", origin)
					if token != "" {
						request.Header.Set("Authorization", "Bearer "+token)
					}
					recorder := httptest.NewRecorder()
					router.ServeHTTP(recorder, request)
					assert.Equal(t, http.StatusBadRequest, recorder.Code)
					assert.Contains(t, recorder.Body.String(), "INVALID_REQUEST")
				})
			}
		}
	}
}

func TestExtractedQREndpointIsRemoved(t *testing.T) {
	router := setupRoutes(NewAppServer(nil, ""))
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/login/qrcode", nil))
	assert.Equal(t, http.StatusNotFound, recorder.Code)
}
