package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/cdp"
	"github.com/go-rod/rod/lib/defaults"
	"github.com/go-rod/rod/lib/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	// 在测试参数解析前退出，允许子进程接收 Chromium 的启动参数。
	switch os.Getenv("XHS_BROWSER_STARTUP_EXIT_HELPER") {
	case "21":
		_, _ = fmt.Fprintln(os.Stderr, "fixture Chromium startup failed: profile is already in use")
		os.Exit(21)
	case "0":
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// TestMaskProxyCredentials 校验代理日志脱敏：绝不能把用户名/密码打进日志。
func TestMaskProxyCredentials(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "空字符串", input: "", want: ""},
		{name: "无认证信息原样返回", input: "http://127.0.0.1:8080", want: "http://127.0.0.1:8080"},
		{name: "用户名+密码都脱敏", input: "http://user:pass@host:8080", want: "http://***:***@host:8080"},
		{name: "仅用户名脱敏", input: "http://user@host:8080", want: "http://***@host:8080"},
		{name: "socks5带认证", input: "socks5://alice:secret@127.0.0.1:1080", want: "socks5://***:***@127.0.0.1:1080"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, maskProxyCredentials(tt.input))
		})
	}
}

// TestOptions 校验 Option 正确写入 browserConfig（New+Option 的接线）。
func TestOptions(t *testing.T) {
	cfg := &browserConfig{}
	WithFingerprintSeed(98759)(cfg)
	WithProxy("http://127.0.0.1:8080")(cfg)
	WithUserDataDir("/profiles/a")(cfg)
	WithDownloadDir("/downloads/a")(cfg)
	data := []byte("[]")
	option := WithCookies(data)
	data[0] = 'x'
	option(cfg)

	assert.Equal(t, 98759, cfg.fingerprintSeed)
	assert.Equal(t, "http://127.0.0.1:8080", cfg.proxy)
	assert.Equal(t, "/profiles/a", cfg.userDataDir)
	assert.True(t, cfg.userDataDirSet)
	assert.Equal(t, "/downloads/a", cfg.downloadDir)
	assert.Equal(t, []byte("[]"), cfg.cookies)
	assert.True(t, cfg.cookiesSet)
}

// TestOptions_Defaults 未传 Option 时各字段为零值（回退随机 seed / 不设代理）。
func TestOptions_Defaults(t *testing.T) {
	cfg := &browserConfig{}
	assert.Equal(t, 0, cfg.fingerprintSeed)
	assert.Equal(t, "", cfg.proxy)
	assert.False(t, cfg.userDataDirSet)
	assert.False(t, cfg.cookiesSet)
}

func TestCookieImportDoesNotFallbackAcrossProfiles(t *testing.T) {
	cookieFile := filepath.Join(t.TempDir(), "cookies.json")
	require.NoError(t, os.WriteFile(cookieFile, []byte(`[{"name":"legacy","value":"global","domain":"127.0.0.1","path":"/"}]`), 0o600))
	t.Setenv("COOKIES_PATH", cookieFile)

	legacy, err := (&browserConfig{}).loadCookies()
	require.NoError(t, err)
	require.Len(t, legacy, 1)
	require.Equal(t, "global", legacy[0].Value)

	for _, data := range [][]byte{nil, {}, []byte("[]"), []byte("null")} {
		cfg := &browserConfig{}
		WithCookies(data)(cfg)
		imported, err := cfg.loadCookies()
		require.NoError(t, err)
		require.Empty(t, imported, "显式空 Cookie 不得回退全局文件")
	}
	cfg := &browserConfig{}
	WithUserDataDir(filepath.Join(t.TempDir(), "profile"))(cfg)
	imported, err := cfg.loadCookies()
	require.NoError(t, err)
	require.Empty(t, imported, "持久 profile 不得自动重新导入全局 Cookie")
	WithCookies([]byte(`[{"name":"imported","value":"account-a","domain":"127.0.0.1","path":"/"}]`))(cfg)
	imported, err = cfg.loadCookies()
	require.NoError(t, err)
	require.Len(t, imported, 1)
	require.Equal(t, "account-a", imported[0].Value)

	for _, invalid := range []string{`broken`, `[null]`, `{}`} {
		WithCookies([]byte(invalid))(cfg)
		_, err := cfg.loadCookies()
		require.Error(t, err)
	}
}

func TestBrowserFlagsIgnoreGlobalRodProfile(t *testing.T) {
	oldDir, oldPort, oldProxy := defaults.Dir, defaults.Port, defaults.Proxy
	defaults.Dir, defaults.Port, defaults.Proxy = "/unrelated/profile", "9222", "http://unrelated:8080"
	t.Cleanup(func() { defaults.Dir, defaults.Port, defaults.Proxy = oldDir, oldPort, oldProxy })
	cfg := &browserConfig{fingerprintSeed: 23088}
	l := browserLauncher(true, cfg, "/accounts/a/profile")
	require.Equal(t, "/accounts/a/profile", l.Get("user-data-dir"))
	require.Equal(t, "0", l.Get("remote-debugging-port"))
	require.False(t, l.Has("proxy-server"))
	require.Equal(t, "23088", l.Get("fingerprint"))
	require.Equal(t, "Chrome", l.Get("fingerprint-brand"))
	require.Equal(t, "zh-CN", l.Get("lang"))
	platform := "windows"
	if runtime.GOOS == "darwin" {
		platform = "macos"
	}
	require.Equal(t, platform, l.Get("fingerprint-platform"))
	require.True(t, l.Has("headless"))
	require.False(t, l.Has("rod-preferences"))
	require.True(t, l.Has("no-startup-window"))
	require.False(t, l.Has("restore-last-session"))
	cfg.userDataDirSet = true
	cfg.proxy = "http://127.0.0.1:8080"
	l = browserLauncher(false, cfg, "/accounts/b/profile")
	require.False(t, l.Has("headless"))
	require.True(t, l.Has("no-startup-window"))
	require.False(t, l.Has("restore-last-session"))
	require.Equal(t, cfg.proxy, l.Get("proxy-server"))
}

func TestProfileRejectsConcurrentAndSymlinkReuse(t *testing.T) {
	cfg := &browserConfig{}
	WithUserDataDir(filepath.Join(t.TempDir(), "profile"))(cfg)
	var wg sync.WaitGroup
	var acquired atomic.Int32
	var profile string
	for range 12 {
		wg.Go(func() {
			path, temporary, err := prepareProfile(cfg)
			if err == nil {
				assert.False(t, temporary)
				acquired.Add(1)
				profile = path
			} else {
				assert.ErrorIs(t, err, ErrProfileInUse)
			}
		})
	}
	wg.Wait()
	require.EqualValues(t, 1, acquired.Load())
	owner := &Browser{userDataDir: profile}
	t.Cleanup(func() { require.NoError(t, owner.cleanup()) })

	link := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(profile, link); err != nil {
		t.Skipf("当前环境不允许创建符号链接: %v", err)
	}
	alias := &browserConfig{}
	WithUserDataDir(link)(alias)
	_, _, err := prepareProfile(alias)
	require.ErrorIs(t, err, ErrProfileInUse)
}

func TestFailedStartPreservesOnlyPersistentProfile(t *testing.T) {
	root := t.TempDir()
	// 限定临时 profile 的创建目录，断言失败启动没有遗留文件。
	t.Setenv("TMPDIR", root)
	t.Setenv("TMP", root)
	t.Setenv("TEMP", root)
	cfg := &browserConfig{}
	WithCookies(nil)(cfg)
	_, err := openBrowser(filepath.Join(root, "missing-browser"), true, cfg)
	require.Error(t, err)
	entries, err := os.ReadDir(root)
	require.NoError(t, err)
	require.Empty(t, entries)

	WithUserDataDir(filepath.Join(root, "persistent"))(cfg)
	require.NoError(t, os.MkdirAll(cfg.userDataDir, 0o700))
	marker := filepath.Join(cfg.userDataDir, "Local State")
	require.NoError(t, os.WriteFile(marker, []byte("keep"), 0o600))
	for range 2 {
		_, err := openBrowser(filepath.Join(root, "missing-browser"), true, cfg)
		require.Error(t, err)
		require.NotErrorIs(t, err, ErrProfileInUse, "失败启动必须释放使用权")
		data, err := os.ReadFile(marker)
		require.NoError(t, err)
		require.Equal(t, "keep", string(data))
	}
	WithUserDataDir("")(cfg)
	_, _, err = prepareProfile(cfg)
	require.Error(t, err)
}

func TestBrowserStartupReportsExitStatusAndOutput(t *testing.T) {
	t.Setenv("XHS_BROWSER_STARTUP_EXIT_HELPER", "21")
	for _, persistent := range []bool{true, false} {
		t.Run(map[bool]string{true: "persistent", false: "temporary"}[persistent], func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("TMPDIR", root)
			t.Setenv("TMP", root)
			t.Setenv("TEMP", root)
			cfg := &browserConfig{}
			WithCookies(nil)(cfg)
			marker := filepath.Join(root, "profile", "Local State")
			if persistent {
				WithUserDataDir(filepath.Dir(marker))(cfg)
				require.NoError(t, os.MkdirAll(cfg.userDataDir, 0o700))
				require.NoError(t, os.WriteFile(marker, []byte("keep"), 0o600))
			}
			for range 2 {
				b, err := openBrowser(os.Args[0], true, cfg)
				require.Nil(t, b)
				require.ErrorContains(t, err, "浏览器启动后退出")
				require.ErrorContains(t, err, "fixture Chromium startup failed: profile is already in use")
				var exitErr *exec.ExitError
				require.ErrorAs(t, err, &exitErr)
				require.Equal(t, 21, exitErr.ExitCode())
				require.NotErrorIs(t, err, context.Canceled)
				require.NotErrorIs(t, err, context.DeadlineExceeded)
			}
			if persistent {
				data, err := os.ReadFile(marker)
				require.NoError(t, err)
				require.Equal(t, "keep", string(data))
				path, _, err := prepareProfile(cfg)
				require.NoError(t, err, "退出后必须释放 profile 使用权")
				require.NoError(t, (&Browser{userDataDir: path}).cleanup())
			} else {
				entries, err := os.ReadDir(root)
				require.NoError(t, err)
				require.Empty(t, entries, "启动退出后必须清理临时 profile")
			}
		})
	}
}

func TestBrowserStartupReportsSuccessfulProcessExit(t *testing.T) {
	t.Setenv("XHS_BROWSER_STARTUP_EXIT_HELPER", "0")
	cfg := &browserConfig{}
	WithCookies(nil)(cfg)
	b, err := openBrowser(os.Args[0], true, cfg)
	require.Nil(t, b)
	require.ErrorContains(t, err, "浏览器启动后退出")
	require.ErrorContains(t, err, "exit status 0")
	require.NotErrorIs(t, err, context.Canceled)
}

// 本地子进程只等待 stdin 关闭，用于验证真实的进程退出和回收顺序。
func TestBrowserLifecycleHelper(t *testing.T) {
	if os.Getenv("XHS_BROWSER_LIFECYCLE_HELPER") != "1" {
		return
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
	os.Exit(0)
}

func lifecycleBrowser(t *testing.T, persistent bool) (*Browser, io.Closer) {
	t.Helper()
	cfg := &browserConfig{}
	if persistent {
		WithUserDataDir(filepath.Join(t.TempDir(), "profile"))(cfg)
	}
	path, temporary, err := prepareProfile(cfg)
	require.NoError(t, err)
	_, cancel := context.WithCancel(context.Background())
	b := &Browser{userDataDir: path, temporary: temporary, cancel: cancel, exit: make(chan struct{})}
	b.cmd = exec.Command(os.Args[0], "-test.run=^TestBrowserLifecycleHelper$")
	b.cmd.Env = append(os.Environ(), "XHS_BROWSER_LIFECYCLE_HELPER=1")
	stdin, err := b.cmd.StdinPipe()
	require.NoError(t, err)
	require.NoError(t, b.start())
	t.Cleanup(func() { _ = stdin.Close(); _ = b.terminate() })
	return b, stdin
}

func TestShutdownWaitsForExitAndCleansOnlyTemporaryProfiles(t *testing.T) {
	for _, persistent := range []bool{true, false} {
		t.Run(map[bool]string{true: "persistent", false: "temporary"}[persistent], func(t *testing.T) {
			b, stdin := lifecycleBrowser(t, persistent)
			marker := filepath.Join(b.userDataDir, "Local State")
			require.NoError(t, os.WriteFile(marker, []byte("keep"), 0o600))
			var calls atomic.Int32
			b.browser = rod.New().ControlURL("").Client(testCDP{call: func(ctx context.Context, method string) ([]byte, error) {
				if method == "Storage.getCookies" {
					return []byte(`{"cookies":[]}`), nil
				}
				assert.Equal(t, "Browser.close", method)
				calls.Add(1)
				return []byte(`{}`), stdin.Close()
			}})
			require.True(t, b.Alive())
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			var wg sync.WaitGroup
			for range 4 {
				wg.Go(func() { assert.NoError(t, b.Shutdown(ctx)) })
			}
			wg.Wait()
			require.False(t, b.Alive())
			require.True(t, b.cmd.ProcessState.Exited())
			require.EqualValues(t, 1, calls.Load(), "重复关闭只能发送一次 CDP close")
			require.NoError(t, b.Shutdown(ctx))
			if persistent {
				require.FileExists(t, marker)
				snapshot, err := os.ReadFile(filepath.Join(b.userDataDir, sessionCookiesFile))
				require.NoError(t, err, "退出通知前必须提交快照，包括空集")
				require.JSONEq(t, `[]`, string(snapshot))
				cfg := &browserConfig{}
				WithUserDataDir(b.userDataDir)(cfg)
				path, _, err := prepareProfile(cfg)
				require.NoError(t, err, "返回前必须释放 profile 使用权")
				require.NoError(t, (&Browser{userDataDir: path}).cleanup())
			} else {
				require.NoDirExists(t, b.userDataDir)
			}
		})
	}
}

func TestShutdownDeadlineKillsProcessAndKeepsProfile(t *testing.T) {
	b, _ := lifecycleBrowser(t, true)
	marker := filepath.Join(b.userDataDir, "Local State")
	require.NoError(t, os.WriteFile(marker, []byte("keep"), 0o600))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, b.Shutdown(ctx), context.Canceled)
	require.False(t, b.Alive(), "超时返回前应回收进程")
	require.FileExists(t, marker)
	cfg := &browserConfig{}
	WithUserDataDir(b.userDataDir)(cfg)
	path, _, err := prepareProfile(cfg)
	require.NoError(t, err)
	require.NoError(t, (&Browser{userDataDir: path}).cleanup())
}

type testCDP struct {
	call           func(context.Context, string) ([]byte, error)
	callWithParams func(context.Context, string, interface{}) ([]byte, error)
}

func (c testCDP) Call(ctx context.Context, _ string, method string, params interface{}) ([]byte, error) {
	if c.callWithParams != nil {
		return c.callWithParams(ctx, method, params)
	}
	return c.call(ctx, method)
}

func (c testCDP) Event() <-chan *cdp.Event { return nil }

func TestUAOverrideUsesBrowserVersionAndLeavesFingerprintPlatform(t *testing.T) {
	version := &proto.BrowserGetVersionResult{Product: "Chrome/148.0.7778.215", UserAgent: "fixture browser UA"}
	b := rod.New().ControlURL("").Client(testCDP{call: func(_ context.Context, method string) ([]byte, error) {
		require.Equal(t, "Browser.getVersion", method)
		return json.Marshal(version)
	}})
	ov, err := buildUAOverride(b, "zh-CN")
	require.NoError(t, err)
	require.Equal(t, version.UserAgent, ov.UserAgent)
	require.Equal(t, "zh-CN,zh", ov.AcceptLanguage)
	metadata := ov.UserAgentMetadata
	require.Equal(t, "148.0.7778.215", metadata.FullVersion)
	require.Equal(t, "148", metadata.Brands[1].Version)
	require.Equal(t, "Google Chrome", metadata.Brands[1].Brand)
	require.Equal(t, "148.0.7778.215", metadata.FullVersionList[1].Version)
	require.Empty(t, metadata.Platform)
	require.Empty(t, metadata.PlatformVersion)
	require.Empty(t, metadata.Architecture)
	version.Product = "Chrome/149.1.2.3"
	ov, err = buildUAOverride(b, "")
	require.NoError(t, err)
	require.Equal(t, "149", ov.UserAgentMetadata.Brands[1].Version)
	require.Empty(t, ov.AcceptLanguage)
}
