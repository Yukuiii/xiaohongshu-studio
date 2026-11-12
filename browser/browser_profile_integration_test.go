//go:build integration

package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
	"github.com/stretchr/testify/require"
)

func cachedFixtureBrowser(t *testing.T) string {
	t.Helper()
	_, binName, supported := platformAsset()
	if !supported {
		t.Skipf("本机 %s/%s 没有内置浏览器", runtime.GOOS, runtime.GOARCH)
	}
	cacheDir, err := browserCacheDir()
	require.NoError(t, err)
	bin := findBinary(cacheDir, binName)
	if bin == "" {
		t.Skip("本机缺少缓存的内置指纹浏览器；fixture 测试不自动下载")
	}
	return bin
}

func profileFixtureServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Accept-CH", "Sec-CH-UA-Full-Version, Sec-CH-UA-Full-Version-List")
		switch r.URL.Path {
		case "/download":
			w.Header().Set("Content-Disposition", `attachment; filename="fixture.txt"`)
			w.Header().Set("Content-Type", "text/plain")
			_, _ = fmt.Fprint(w, r.URL.Query().Get("account"))
		case "/headers":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{
				"ua": r.UserAgent(), "language": r.Header.Get("Accept-Language"), "brands": r.Header.Get("Sec-CH-UA"),
				"fullVersion": r.Header.Get("Sec-CH-UA-Full-Version"), "fullVersionList": r.Header.Get("Sec-CH-UA-Full-Version-List"),
			})
		default:
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = fmt.Fprintf(w, `<!doctype html><title>local profile fixture</title><a href="/download?account=%s">download</a>`, r.URL.Query().Get("account"))
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func openFixtureProfile(t *testing.T, profile, download string, options ...Option) *Browser {
	t.Helper()
	opts := []Option{WithUserDataDir(profile), WithDownloadDir(download), WithFingerprintSeed(23088)}
	opts = append(opts, options...)
	b, err := Open(true, opts...)
	require.NoError(t, err)
	require.NoFileExists(t, filepath.Join(profile, sessionCookiesFile), "启动后必须消费一次性快照")
	t.Cleanup(b.Close)
	return b
}

func fixturePage(t *testing.T, b *Browser, url string) *rod.Page {
	t.Helper()
	page := b.NewPage().Timeout(15 * time.Second)
	require.NoError(t, page.Navigate(url))
	require.NoError(t, page.WaitLoad())
	return page
}

func writeFixtureStorage(t *testing.T, page *rod.Page, origin, value string) {
	t.Helper()
	require.NoError(t, page.SetCookies([]*proto.NetworkCookieParam{{
		Name: "account", Value: value, URL: origin, Path: "/",
		Expires: proto.TimeSinceEpoch(time.Now().Add(24 * time.Hour).Unix()),
	}}))
	// localStorage 和 IndexedDB 没有对应的 Rod 写入接口，仅操作本地 fixture。
	_, err := page.Eval(`async value => {
		localStorage.setItem("account", value);
		await new Promise((resolve, reject) => {
			const open = indexedDB.open("profile-fixture", 1);
			open.onupgradeneeded = () => open.result.createObjectStore("values");
			open.onerror = () => reject(open.error);
			open.onsuccess = () => {
				const db = open.result;
				const tx = db.transaction("values", "readwrite");
				tx.objectStore("values").put(value, "account");
				tx.oncomplete = () => { db.close(); resolve(); };
				tx.onerror = () => { db.close(); reject(tx.error); };
			};
		});
	}`, value)
	require.NoError(t, err)
}

func assertFixtureStorage(t *testing.T, page *rod.Page, origin, want string) {
	t.Helper()
	stored, err := page.Cookies([]string{origin})
	require.NoError(t, err)
	account := ""
	for _, cookie := range stored {
		require.NotEqual(t, "global", cookie.Name, "不得导入其他账号的全局 Cookie")
		if cookie.Name == "account" {
			account = cookie.Value
		}
	}
	require.Equal(t, want, account)
	result, err := page.Eval(`async () => {
		const value = await new Promise((resolve, reject) => {
			const open = indexedDB.open("profile-fixture", 1);
			open.onupgradeneeded = () => open.result.createObjectStore("values");
			open.onerror = () => reject(open.error);
			open.onsuccess = () => {
				const db = open.result;
				const tx = db.transaction("values", "readonly");
				const get = tx.objectStore("values").get("account");
				tx.oncomplete = () => { db.close(); resolve(get.result || ""); };
				tx.onerror = () => { db.close(); reject(tx.error); };
			};
		});
		return {local: localStorage.getItem("account") || "", indexed: value};
	}`)
	require.NoError(t, err)
	require.Equal(t, want, result.Value.Get("local").Str())
	require.Equal(t, want, result.Value.Get("indexed").Str())
}

func assertFixtureUA(t *testing.T, b *Browser, page *rod.Page) {
	t.Helper()
	version, err := proto.BrowserGetVersion{}.Call(b.browser)
	require.NoError(t, err)
	fullVersion := strings.TrimPrefix(version.Product, "Chrome/")
	result, err := page.Eval(`async () => ({
		ua: navigator.userAgent,
		languages: navigator.languages,
		metadata: await navigator.userAgentData.getHighEntropyValues(["uaFullVersion", "fullVersionList", "platform"]),
		headers: await (await fetch("/headers")).json()
	})`)
	require.NoError(t, err)
	require.Equal(t, version.UserAgent, result.Value.Get("ua").Str())
	require.Equal(t, version.UserAgent, result.Value.Get("headers.ua").Str())
	// 源码指纹补丁可调整补丁版本，校验网页和请求头自洽，主版本仍须匹配引擎。
	pageVersion := result.Value.Get("metadata.uaFullVersion").Str()
	require.NotEmpty(t, pageVersion)
	major := strings.SplitN(fullVersion, ".", 2)[0]
	require.Equal(t, major, strings.SplitN(pageVersion, ".", 2)[0])
	require.Contains(t, version.UserAgent, "Chrome/"+major+".")
	require.Equal(t, `"`+pageVersion+`"`, result.Value.Get("headers.fullVersion").Str())
	require.Contains(t, result.Value.Get("headers.fullVersionList").Str(), pageVersion)
	require.Equal(t, "zh-CN", result.Value.Get("languages.0").Str())
	require.Equal(t, "zh", result.Value.Get("languages.1").Str())
	require.True(t, strings.HasPrefix(result.Value.Get("headers.language").Str(), "zh-CN"))
	require.Contains(t, result.Value.Get("headers.brands").Str(), "Google Chrome")
	platform := "Windows"
	if runtime.GOOS == "darwin" {
		platform = "macOS"
	}
	require.Equal(t, platform, result.Value.Get("metadata.platform").Str())
	brands := result.Value.Get("metadata.fullVersionList").Arr()
	chromeVersion := ""
	for _, brand := range brands {
		if brand.Get("brand").Str() == "Google Chrome" {
			chromeVersion = brand.Get("version").Str()
		}
	}
	require.Equal(t, pageVersion, chromeVersion)
}

func shutdownFixture(t *testing.T, b *Browser) {
	t.Helper()
	// 此处验证落盘正确性，给并行构建时的磁盘延迟留出余量。
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.NoError(t, b.Shutdown(ctx))
	require.False(t, b.Alive())
	require.True(t, b.cmd.ProcessState.Exited())
	require.FileExists(t, filepath.Join(b.userDataDir, "Local State"))
	require.DirExists(t, filepath.Join(b.userDataDir, "Default"))
}

func TestProfileStoragePersistsAndIsolatesAccounts(t *testing.T) {
	cachedFixtureBrowser(t)
	server := profileFixtureServer(t)
	root := t.TempDir()
	legacy := filepath.Join(root, "global-cookies.json")
	require.NoError(t, os.WriteFile(legacy, []byte(`[{"name":"global","value":"stale","domain":"127.0.0.1","path":"/"}]`), 0o600))
	t.Setenv("COOKIES_PATH", legacy)

	type account struct {
		name, profile, download string
		browser                 *Browser
		page                    *rod.Page
	}
	accounts := []*account{
		{name: "A", profile: filepath.Join(root, "a", "profile"), download: filepath.Join(root, "a", "downloads")},
		{name: "B", profile: filepath.Join(root, "b", "profile"), download: filepath.Join(root, "b", "downloads")},
	}
	for _, a := range accounts {
		require.NoError(t, os.MkdirAll(filepath.Join(a.profile, "Default"), 0o700))
		preferences := []byte(fmt.Sprintf(`{"homepage":"about:blank#%s","homepage_is_newtabpage":false}`, a.name))
		require.NoError(t, os.WriteFile(filepath.Join(a.profile, "Default", "Preferences"), preferences, 0o600))
		a.browser = openFixtureProfile(t, a.profile, a.download)
		a.page = fixturePage(t, a.browser, server.URL+"/?account="+a.name)
		assertFixtureStorage(t, a.page, server.URL, "")
	}
	writeFixtureStorage(t, accounts[0].page, server.URL, "A")
	assertFixtureStorage(t, accounts[1].page, server.URL, "")
	writeFixtureStorage(t, accounts[1].page, server.URL, "B")
	for _, a := range accounts {
		assertFixtureStorage(t, a.page, server.URL, a.name)
		assertFixtureUA(t, a.browser, a.page)
		anchor, err := a.page.Element("a")
		require.NoError(t, err)
		require.NoError(t, anchor.Click(proto.InputMouseButtonLeft, 1))
		require.Eventually(t, func() bool {
			data, err := os.ReadFile(filepath.Join(a.download, "fixture.txt"))
			return err == nil && string(data) == a.name
		}, 10*time.Second, 20*time.Millisecond, "下载必须进入该账号的目录")
	}
	for _, a := range accounts {
		shutdownFixture(t, a.browser)
		data, err := os.ReadFile(filepath.Join(a.profile, "Default", "Preferences"))
		require.NoError(t, err)
		var preferences map[string]interface{}
		require.NoError(t, json.Unmarshal(data, &preferences))
		require.Equal(t, "about:blank#"+a.name, preferences["homepage"], "下载设置不得整体覆盖 Preferences")
	}
	for _, a := range accounts {
		// 显式空 Cookie 也不能清空已有 profile 或回退全局文件。
		a.browser = openFixtureProfile(t, a.profile, a.download, WithCookies(nil))
		a.page = fixturePage(t, a.browser, server.URL+"/?account="+a.name)
		assertFixtureStorage(t, a.page, server.URL, a.name)
		_, err := Open(true, WithUserDataDir(a.profile))
		require.ErrorIs(t, err, ErrProfileInUse)
		require.True(t, a.browser.Alive())
	}
	for _, a := range accounts {
		shutdownFixture(t, a.browser)
	}
}

func TestProfileSessionCookiesSurviveRestartWithoutRestoringPages(t *testing.T) {
	cachedFixtureBrowser(t)
	for _, headless := range []bool{true, false} {
		name := "headed"
		if headless {
			name = "headless"
		}
		t.Run(name, func(t *testing.T) {
			if !headless && runtime.GOOS == "linux" && os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
				t.Skip("本机没有可用于有头浏览器的桌面")
			}
			var visits, executions atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Cache-Control", "no-store")
				switch r.URL.Path {
				case "/previous-task":
					visits.Add(1)
					http.SetCookie(w, &http.Cookie{Name: "session", Value: "refreshed", Path: "/", HttpOnly: true})
					http.SetCookie(w, &http.Cookie{Name: "native", Value: "site-created", Path: "/", HttpOnly: true})
					http.SetCookie(w, &http.Cookie{Name: "removed", Path: "/", MaxAge: -1})
					w.Header().Set("Content-Type", "text/html; charset=utf-8")
					_, _ = fmt.Fprint(w, `<!doctype html><title>previous local task</title><script>window.taskFinished = fetch("/executed-task", {method: "POST"});</script>`)
				case "/executed-task":
					executions.Add(1)
					w.WriteHeader(http.StatusNoContent)
				default:
					w.Header().Set("Content-Type", "text/html; charset=utf-8")
					_, _ = fmt.Fprint(w, "<!doctype html><title>new controlled page</title>")
				}
			}))
			t.Cleanup(server.Close)
			root := t.TempDir()
			profile := filepath.Join(root, "profile")
			imported, err := json.Marshal([]*proto.NetworkCookie{
				{Name: "session", Value: "legacy", Domain: "127.0.0.1", Path: "/", Session: true, Expires: -1, HTTPOnly: true},
				{Name: "removed", Value: "legacy", Domain: "127.0.0.1", Path: "/", Session: true, Expires: -1},
				{Name: "persistent", Value: "saved", Domain: "127.0.0.1", Path: "/", Expires: proto.TimeSinceEpoch(time.Now().Add(24 * time.Hour).Unix())},
			})
			require.NoError(t, err)
			legacy := filepath.Join(root, "legacy.json")
			require.NoError(t, os.WriteFile(legacy, imported, 0o600))
			t.Setenv("COOKIES_PATH", legacy)
			open := func(options ...Option) *Browser {
				t.Helper()
				opts := []Option{WithUserDataDir(profile), WithFingerprintSeed(23088)}
				opts = append(opts, options...)
				b, err := Open(headless, opts...)
				require.NoError(t, err)
				require.NoFileExists(t, filepath.Join(profile, sessionCookiesFile))
				t.Cleanup(b.Close)
				return b
			}
			assertCookies := func(b *Browser, expected map[string]string) {
				t.Helper()
				stored, err := b.browser.GetCookies()
				require.NoError(t, err)
				got := make(map[string]string)
				for _, cookie := range stored {
					got[cookie.Name] = cookie.Value
					if cookie.Name == "persistent" {
						require.False(t, cookie.Session)
						require.Greater(t, float64(cookie.Expires), float64(time.Now().Unix()))
					} else {
						require.True(t, cookie.Session, "不能把 session Cookie 改成永久 Cookie")
						require.Equal(t, proto.TimeSinceEpoch(-1), cookie.Expires)
						if cookie.Name == "partitioned" {
							require.Equal(t, &proto.NetworkCookiePartitionKey{TopLevelSite: "https://127.0.0.1", HasCrossSiteAncestor: true}, cookie.PartitionKey)
							require.True(t, cookie.Secure)
							require.True(t, cookie.HTTPOnly)
						}
					}
				}
				require.Equal(t, expected, got)
			}

			// 迁移只导入一次，允许未创建任何页面就正常关闭。
			b := open(WithCookies(imported))
			initial := map[string]string{"session": "legacy", "removed": "legacy", "persistent": "saved"}
			assertCookies(b, initial)
			shutdownFixture(t, b)
			b = open()
			assertCookies(b, initial)
			page := fixturePage(t, b, server.URL+"/previous-task")
			_, err = page.Eval(`async () => { await window.taskFinished; }`)
			require.NoError(t, err)
			require.EqualValues(t, 1, visits.Load())
			require.EqualValues(t, 1, executions.Load())
			require.NoError(t, b.browser.SetCookies([]*proto.NetworkCookieParam{{
				Name: "partitioned", Value: "current-partition", URL: "https://127.0.0.1/", Secure: true, HTTPOnly: true,
				SameSite:     proto.NetworkCookieSameSiteNone,
				PartitionKey: &proto.NetworkCookiePartitionKey{TopLevelSite: "https://127.0.0.1", HasCrossSiteAncestor: true},
			}}))
			updated := map[string]string{"session": "refreshed", "native": "site-created", "persistent": "saved", "partitioned": "current-partition"}
			assertCookies(b, updated)
			// 保留旧任务标签到 Browser.close，验证恢复 Cookie 不会恢复该标签。
			shutdownFixture(t, b)

			for restart := range 2 {
				b = open()
				assertCookies(b, updated)
				targets, err := (proto.TargetGetTargets{}).Call(b.browser)
				require.NoError(t, err)
				for _, target := range targets.TargetInfos {
					require.NotEqual(t, "page", target.Type, "Open 不能自动重开上次页面")
				}
				page = fixturePage(t, b, server.URL+"/controlled")
				require.Never(t, func() bool {
					return visits.Load() != 1 || executions.Load() != 1
				}, time.Second, 20*time.Millisecond, "重启 %d 及首次 NewPage 不能恢复旧任务", restart+1)
				targets, err = (proto.TargetGetTargets{}).Call(b.browser)
				require.NoError(t, err)
				var pages []string
				for _, target := range targets.TargetInfos {
					if target.Type == "page" {
						pages = append(pages, target.URL)
					}
				}
				require.Equal(t, []string{server.URL + "/controlled"}, pages, "只能保留调用方明确创建的页面")
				assertCookies(b, updated)
				shutdownFixture(t, b)
			}
		})
	}
}

func TestProfileLogoutAndCrashCannotReviveSessionCookies(t *testing.T) {
	cachedFixtureBrowser(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/logout" {
			http.SetCookie(w, &http.Cookie{Name: "session", Path: "/", MaxAge: -1, HttpOnly: true})
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprint(w, "<!doctype html><title>local logout fixture</title>")
	}))
	t.Cleanup(server.Close)
	for _, mode := range []string{"normal", "crash", "deadline"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			profile := filepath.Join(root, "profile")
			imported, err := json.Marshal([]*proto.NetworkCookie{
				{Name: "session", Value: "previous-login", Domain: "127.0.0.1", Path: "/", Session: true, Expires: -1, HTTPOnly: true},
				{Name: "persistent", Value: "keep", Domain: "127.0.0.1", Path: "/", Expires: proto.TimeSinceEpoch(time.Now().Add(24 * time.Hour).Unix())},
			})
			require.NoError(t, err)
			legacy := filepath.Join(root, "legacy.json")
			require.NoError(t, os.WriteFile(legacy, imported, 0o600))
			t.Setenv("COOKIES_PATH", legacy)
			b := openFixtureProfile(t, profile, "", WithCookies(imported))
			shutdownFixture(t, b)
			b = openFixtureProfile(t, profile, "")
			stored, err := b.browser.GetCookies()
			require.NoError(t, err)
			require.Len(t, stored, 2)
			fixturePage(t, b, server.URL+"/logout")
			stored, err = b.browser.GetCookies()
			require.NoError(t, err)
			require.Len(t, stored, 1)
			require.Equal(t, "persistent", stored[0].Name)
			switch mode {
			case "normal":
				shutdownFixture(t, b)
				data, err := os.ReadFile(filepath.Join(profile, sessionCookiesFile))
				require.NoError(t, err)
				require.JSONEq(t, `[]`, string(data), "登出必须提交空 session 集合")
			case "crash":
				require.NoError(t, killProcess(b.cmd))
				select {
				case <-b.exit:
				case <-time.After(5 * time.Second):
					t.Fatal("崩溃 fixture 浏览器未退出")
				}
				require.NoFileExists(t, filepath.Join(profile, sessionCookiesFile))
			case "deadline":
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				require.ErrorIs(t, b.Shutdown(ctx), context.Canceled)
				require.NoFileExists(t, filepath.Join(profile, sessionCookiesFile))
			}
			b = openFixtureProfile(t, profile, "")
			stored, err = b.browser.GetCookies()
			require.NoError(t, err)
			require.Len(t, stored, 1, "不得从旧快照、legacy 或 Chromium 崩溃恢复中复活已登出的 session")
			require.Equal(t, "persistent", stored[0].Name)
			require.Equal(t, "keep", stored[0].Value)
			shutdownFixture(t, b)
		})
	}
}

func TestProfileRejectsExternalBrowserWithoutRemovingLocks(t *testing.T) {
	bin := cachedFixtureBrowser(t)
	profile := filepath.Join(t.TempDir(), "profile")
	require.NoError(t, os.MkdirAll(profile, 0o700))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	parser := launcher.NewURLParser().Context(ctx)
	// 直接启动未登记在封装内的进程，模拟其他程序占用同一 profile。
	cmd := exec.Command(bin, browserLauncher(true, &browserConfig{fingerprintSeed: 23088}, profile).FormatArgs()...)
	configureProcess(cmd)
	cmd.Stdout, cmd.Stderr = parser, parser
	require.NoError(t, cmd.Start())
	exit := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exit) }()
	t.Cleanup(func() {
		_ = killProcess(cmd)
		select {
		case <-exit:
		case <-time.After(5 * time.Second):
			t.Error("外部 fixture 浏览器未退出")
		}
	})
	var endpoint string
	select {
	case endpoint = <-parser.URL:
	case <-exit:
		t.Fatalf("外部 fixture 启动失败: %v", parser.Err())
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	lockPath := filepath.Join(profile, "SingletonLock")
	lockBefore, lockErr := os.Readlink(lockPath)
	_, err := Open(true, WithUserDataDir(profile), WithCookies(nil))
	require.Error(t, err, "已被外部进程占用的 profile 不可强行打开")
	if lockErr == nil {
		lockAfter, err := os.Readlink(lockPath)
		require.NoError(t, err)
		require.Equal(t, lockBefore, lockAfter, "不能删除或替换原浏览器锁")
	}
	_, err = resolveControlURL(ctx, endpoint)
	require.NoError(t, err, "失败启动不能关闭原先占用 profile 的浏览器")
}
