package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func workspaceRequest(t *testing.T, router http.Handler, method, path, accountID, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(accountHeader, accountID)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func TestDraftAndProtectionHandlers(t *testing.T) {
	manager := testAccountManager(t)
	account := manager.store.List()[0]
	server := NewAccountAppServer(manager, "")
	router := setupRoutes(server)

	rec := workspaceRequest(t, router, http.MethodGet, "/api/v1/draft", account.ID, "")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var draft struct {
		Data struct {
			Draft    any   `json:"draft"`
			Revision int64 `json:"revision"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &draft))
	require.Nil(t, draft.Data.Draft)
	require.Zero(t, draft.Data.Revision)

	rec = workspaceRequest(t, router, http.MethodPut, "/api/v1/draft", account.ID,
		`{"draft":{"title":"第一版"},"base_revision":0}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	rec = workspaceRequest(t, router, http.MethodPut, "/api/v1/draft", account.ID,
		`{"draft":{"title":"冲突"},"base_revision":0}`)
	require.Equal(t, http.StatusConflict, rec.Code)
	require.Contains(t, rec.Body.String(), "DRAFT_REVISION_CONFLICT")

	rec = workspaceRequest(t, router, http.MethodGet, "/api/v1/draft", account.ID, "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "第一版")
	require.Contains(t, rec.Body.String(), `"revision":1`)

	rec = workspaceRequest(t, router, http.MethodPut, "/api/v1/publish-protection", account.ID,
		`{"base_revision":0,"uncertain":true,"last_published":""}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	rec = workspaceRequest(t, router, http.MethodPut, "/api/v1/publish-protection", account.ID,
		`{"base_revision":0,"uncertain":false,"last_published":"x"}`)
	require.Equal(t, http.StatusConflict, rec.Code)
	require.Contains(t, rec.Body.String(), "PROTECTION_REVISION_CONFLICT")

	rec = workspaceRequest(t, router, http.MethodGet, "/api/v1/publish-protection", account.ID, "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"uncertain":true`)

	rec = workspaceRequest(t, router, http.MethodDelete, "/api/v1/draft", account.ID, "")
	require.Equal(t, http.StatusOK, rec.Code)
	rec = workspaceRequest(t, router, http.MethodGet, "/api/v1/draft", account.ID, "")
	require.Contains(t, rec.Body.String(), `"draft":null`)
}
