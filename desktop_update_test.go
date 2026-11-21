package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDesktopUpdateRoutesRequireLocalDesktopAndOrigin(t *testing.T) {
	const location = "http://127.0.0.1:18060"
	const path = "/api/v1/app-update"
	server := NewAppServer(nil, "secret")
	makeRequest := func(method, endpoint string) *http.Request {
		request := httptest.NewRequest(method, location+endpoint, nil)
		request.RemoteAddr = "127.0.0.1:18061"
		request.Header.Set("Authorization", "Bearer secret")
		return request
	}
	response := httptest.NewRecorder()
	setupRoutes(server).ServeHTTP(response, makeRequest(http.MethodGet, path))
	require.Equal(t, http.StatusNotFound, response.Code)

	server.appUpdate = &desktopUpdater{status: desktopUpdateStatus{Phase: "current", CurrentVersion: "v1.0.1"}}
	router := setupRoutes(server)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, makeRequest(http.MethodGet, path))
	require.Equal(t, http.StatusOK, response.Code)
	require.JSONEq(t, `{"success":true,"data":{"phase":"current","current_version":"v1.0.1"}}`, response.Body.String())
	for _, tc := range []struct {
		name   string
		change func(*http.Request)
		want   int
	}{
		{"wrong origin", func(r *http.Request) { r.Header.Set("Origin", "https://example.com") }, http.StatusForbidden},
		{"wrong host", func(r *http.Request) { r.Host = "example.com:18060" }, http.StatusNotFound},
		{"untrusted peer", func(r *http.Request) { r.RemoteAddr = "192.0.2.1:18061"; r.Header.Set("X-Forwarded-For", "127.0.0.1") }, http.StatusNotFound},
		{"missing token", func(r *http.Request) { r.Header.Del("Authorization") }, http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := makeRequest(http.MethodPost, path+"/install")
			tc.change(request)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			require.Equal(t, tc.want, response.Code)
		})
	}
}
