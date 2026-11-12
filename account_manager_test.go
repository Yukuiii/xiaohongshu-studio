package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
	"github.com/stretchr/testify/require"
	"github.com/xpzouying/xiaohongshu-mcp/accounts"
)

type fakeAccountBrowser struct {
	alive  atomic.Bool
	closed chan struct{}
	once   sync.Once
}

func newFakeAccountBrowser() *fakeAccountBrowser {
	b := &fakeAccountBrowser{closed: make(chan struct{})}
	b.alive.Store(true)
	return b
}
func (b *fakeAccountBrowser) NewPage() *rod.Page { panic("测试不应启动页面") }
func (b *fakeAccountBrowser) Alive() bool        { return b.alive.Load() }
func (b *fakeAccountBrowser) Shutdown(context.Context) error {
	b.alive.Store(false)
	b.once.Do(func() { close(b.closed) })
	return nil
}

func testAccountManager(t *testing.T) *AccountManager {
	t.Helper()
	isolateImageStorage(t)
	store, err := accounts.Open(filepath.Join(t.TempDir(), "accounts"), filepath.Join(t.TempDir(), "missing.json"), 0)
	require.NoError(t, err)
	manager := NewAccountManager(store)
	manager.factory = func(accounts.Account, string, bool, []byte) (accountBrowser, error) {
		return newFakeAccountBrowser(), nil
	}
	t.Cleanup(func() { require.NoError(t, manager.Close()) })
	return manager
}

func TestAccountBrowserReuseAndIsolation(t *testing.T) {
	manager := testAccountManager(t)
	a := manager.store.List()[0]
	b, err := manager.store.Create("账号 B")
	require.NoError(t, err)
	runtimeA, err := manager.runtime(a.ID)
	require.NoError(t, err)
	runtimeB, err := manager.runtime(b.ID)
	require.NoError(t, err)
	var starts int
	manager.factory = func(record accounts.Account, profile string, _ bool, _ []byte) (accountBrowser, error) {
		starts++
		require.Equal(t, manager.store.ProfilePath(record), profile)
		return newFakeAccountBrowser(), nil
	}
	first, err := runtimeA.borrowBrowser(context.Background(), false)
	require.NoError(t, err)
	_, err = runtimeA.borrowBrowser(context.Background(), false)
	require.Equal(t, "ACCOUNT_BUSY", accountFailure(err).Code)
	first.Close()
	second, err := runtimeA.borrowBrowser(context.Background(), false)
	require.NoError(t, err)
	require.Same(t, first.accountBrowser, second.accountBrowser)
	other, err := runtimeB.borrowBrowser(context.Background(), false)
	require.NoError(t, err)
	require.NotSame(t, second.accountBrowser, other.accountBrowser)
	require.Equal(t, 2, starts)
	second.Close()
	other.Close()
}

func TestAccountIdleCloseKeepsProfileAndDoesNotCloseBusyBrowser(t *testing.T) {
	manager := testAccountManager(t)
	manager.idleTimeout = 20 * time.Millisecond
	a := manager.store.List()[0]
	profile := manager.store.ProfilePath(a)
	require.NoError(t, os.MkdirAll(profile, 0700))
	marker := filepath.Join(profile, "saved-state")
	require.NoError(t, os.WriteFile(marker, []byte("保留"), 0600))
	r, err := manager.runtime(a.ID)
	require.NoError(t, err)
	lease, err := r.borrowBrowser(context.Background(), false)
	require.NoError(t, err)
	fake := lease.accountBrowser.(*fakeAccountBrowser)
	lease.Close()
	held, err := r.borrowBrowser(context.Background(), false)
	require.NoError(t, err)
	select {
	case <-fake.closed:
		t.Fatal("运行任务被空闲定时器关闭")
	case <-time.After(60 * time.Millisecond):
	}
	held.Close()
	select {
	case <-fake.closed:
	case <-time.After(2 * time.Second):
		t.Fatal("空闲浏览器未关闭")
	}
	data, err := os.ReadFile(marker)
	require.NoError(t, err)
	require.Equal(t, "保留", string(data))
	restarted, err := r.borrowBrowser(context.Background(), false)
	require.NoError(t, err)
	require.NotSame(t, fake, restarted.accountBrowser)
	restarted.Close()
}

func TestAccountRemovalRejectsWritersAndOldReferences(t *testing.T) {
	manager := testAccountManager(t)
	a := manager.store.List()[0]
	r, err := manager.runtime(a.ID)
	require.NoError(t, err)
	for _, operation := range []string{"上传", "生图", "登录保存"} {
		release, err := r.lease(true)
		require.NoError(t, err)
		err = r.maintenance(context.Background(), true)
		require.Equal(t, "ACCOUNT_BUSY", accountFailure(err).Code, operation)
		release()
	}
	require.NoError(t, r.maintenance(context.Background(), true))
	_, err = manager.store.Get(a.ID)
	require.ErrorIs(t, err, accounts.ErrNotFound)
	_, err = r.lease(true)
	require.Error(t, err)
	_, err = r.borrowBrowser(context.Background(), false)
	require.Error(t, err)
}

func TestAccountHTTPSelectionAndImageOwnership(t *testing.T) {
	manager := testAccountManager(t)
	a := manager.store.List()[0]
	b, err := manager.store.Create("账号 B")
	require.NoError(t, err)
	router := setupRoutes(NewAccountAppServer(manager, "secret"))
	request := func(method, path, id string, body *http.Request) *httptest.ResponseRecorder {
		if body == nil {
			body = httptest.NewRequest(method, path, nil)
		}
		body.URL.Path = path
		body.Method = method
		body.Header.Set("Authorization", "Bearer secret")
		if id != "" {
			body.Header.Set(accountHeader, id)
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, body)
		return response
	}
	require.Equal(t, http.StatusBadRequest, request("GET", "/api/v1/login/status", "", nil).Code)
	require.Equal(t, http.StatusBadRequest, request("GET", "/api/v1/login/status", "../invalid", nil).Code)
	require.Equal(t, http.StatusNotFound, request("GET", "/api/v1/login/status", "00000000000000000000000000000000", nil).Code)
	upload := request("POST", "/api/v1/images/upload", a.ID, uploadImageRequest(t, testPNG(t)))
	require.Equal(t, http.StatusOK, upload.Code, upload.Body.String())
	var payload struct {
		Data struct {
			Images []workbenchImage `json:"images"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(upload.Body.Bytes(), &payload))
	require.Len(t, payload.Data.Images, 1)
	image := payload.Data.Images[0]
	require.Contains(t, image.Path, a.ID)
	require.Equal(t, http.StatusOK, request("GET", image.URL, a.ID, nil).Code)
	require.Equal(t, http.StatusNotFound, request("GET", image.URL, b.ID, nil).Code)
	runtimeB, err := manager.runtime(b.ID)
	require.NoError(t, err)
	_, err = runtimeB.imagePaths([]string{image.Path})
	require.Equal(t, "IMAGE_ACCOUNT_MISMATCH", accountFailure(err).Code)
	foreign := filepath.Join(manager.store.DownloadPath(a.ID), "material.png")
	_, err = runtimeB.imagePaths([]string{foreign})
	require.Equal(t, "IMAGE_ACCOUNT_MISMATCH", accountFailure(err).Code)
	list := request("GET", "/api/v1/accounts", "", nil)
	require.Equal(t, http.StatusOK, list.Code)
	require.NotContains(t, list.Body.String(), "active_profile")
	require.NotContains(t, list.Body.String(), `"seed"`)
	require.NotContains(t, list.Body.String(), manager.store.Root())
	models := httptest.NewRequest("POST", "/api/v1/ai/models", bytes.NewBufferString(`{}`))
	models.Header.Set("Content-Type", "application/json")
	require.Equal(t, http.StatusBadRequest, request("POST", "/api/v1/ai/models", "", models).Code)
}

func TestLegacyImagesOnlyBelongToImportedAccount(t *testing.T) {
	manager := testAccountManager(t)
	old, err := saveWorkbenchImage(testPNG(t), "旧图片")
	require.NoError(t, err)
	require.NoError(t, manager.migrateLegacyImages())
	id := manager.store.LegacyAccountID()
	r, err := manager.runtime(id)
	require.NoError(t, err)
	paths, err := r.imagePaths([]string{old.Path})
	require.NoError(t, err)
	require.NotEqual(t, old.Path, paths[0])
	require.FileExists(t, paths[0])
	require.FileExists(t, old.Path)
	other, err := manager.store.Create("新账号")
	require.NoError(t, err)
	otherRuntime, err := manager.runtime(other.ID)
	require.NoError(t, err)
	_, err = otherRuntime.imagePaths([]string{old.Path})
	require.Error(t, err)
	require.NoError(t, r.maintenance(context.Background(), true))
	require.NoError(t, manager.migrateLegacyImages())
	_, err = manager.store.Get(id)
	require.True(t, errors.Is(err, accounts.ErrNotFound))
}

func TestLoginWindowHidesCloseButtonInEmbeddedDesktop(t *testing.T) {
	t.Setenv("XHS_DESKTOP_URL", "http://127.0.0.1:6080")
	require.Equal(t, proto.BrowserWindowStateFullscreen, loginWindowState())
	t.Setenv("XHS_DESKTOP_URL", "")
	require.Equal(t, proto.BrowserWindowStateMaximized, loginWindowState(), "原生窗口保留标题栏")
}

func TestImagePreviewLeasesAreConcurrent(t *testing.T) {
	manager := testAccountManager(t)
	r, err := manager.runtime(manager.store.List()[0].ID)
	require.NoError(t, err)
	var releases []func()
	for i := 0; i < 9; i++ {
		release, err := r.lease(false)
		require.NoError(t, err)
		releases = append(releases, release)
	}
	require.NoError(t, r.maintenance(context.Background(), true))
	for _, release := range releases {
		release()
	}
	_, err = r.lease(false)
	require.Error(t, err)
}
