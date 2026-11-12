package browser

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
	"github.com/sirupsen/logrus"
	"github.com/xpzouying/xiaohongshu-mcp/cookies"
)

type browserConfig struct {
	fingerprintSeed int
	proxy           string
	userDataDir     string
	userDataDirSet  bool
	downloadDir     string
	cookies         []byte
	cookiesSet      bool
}

type Option func(*browserConfig)

// WithProxy 设置代理（http/https/socks5）。空字符串视为不启用。
func WithProxy(proxy string) Option {
	return func(c *browserConfig) { c.proxy = proxy }
}

// WithFingerprintSeed 固定指纹 seed，seed<=0 时每次随机。
func WithFingerprintSeed(seed int) Option {
	return func(c *browserConfig) { c.fingerprintSeed = seed }
}

// WithUserDataDir 指定完整的持久 profile，关闭或启动失败均不删除目录。
func WithUserDataDir(path string) Option {
	return func(c *browserConfig) {
		c.userDataDir = path
		c.userDataDirSet = true
	}
}

// WithDownloadDir 设置本实例的下载目录，不改写 profile Preferences。
func WithDownloadDir(path string) Option {
	return func(c *browserConfig) { c.downloadDir = path }
}

// WithCookies 仅用于旧 Cookie 首次导入；显式空值不读取全局 Cookie，也不清空 profile。
func WithCookies(data []byte) Option {
	data = bytes.Clone(data)
	return func(c *browserConfig) {
		c.cookies = bytes.Clone(data)
		c.cookiesSet = true
	}
}

// maskProxyCredentials 隐去代理地址中的用户名和密码。
func maskProxyCredentials(proxyURL string) string {
	u, err := url.Parse(proxyURL)
	if err != nil || u.User == nil {
		return proxyURL
	}
	cred := "***"
	if _, hasPassword := u.User.Password(); hasPassword {
		cred = "***:***"
	}
	return strings.Replace(proxyURL, u.User.String()+"@", cred+"@", 1)
}

// NewBrowser 保留旧调用的 panic 语义；需要处理启动错误时使用 Open。
func NewBrowser(headless bool, options ...Option) *Browser {
	b, err := Open(headless, options...)
	if err != nil {
		panic(err)
	}
	return b
}

// Open 使用固定的内置指纹引擎启动独立进程，不绑定请求 context。
func Open(headless bool, options ...Option) (*Browser, error) {
	cfg := &browserConfig{}
	for _, opt := range options {
		opt(cfg)
	}
	// 不允许 Rod 自动查找或下载普通 Chromium，避免指纹参数静默失效。
	binPath, err := EnsureBrowser()
	if err != nil {
		return nil, fmt.Errorf("内置浏览器不可用，拒绝启动: %w", err)
	}
	return openBrowser(binPath, headless, cfg)
}

func openBrowser(binPath string, headless bool, cfg *browserConfig) (_ *Browser, err error) {
	importCookies, err := cfg.loadCookies()
	if err != nil {
		return nil, err
	}
	profile, temporary, err := prepareProfile(cfg)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	b := &Browser{
		userDataDir: profile,
		temporary:   temporary,
		exit:        make(chan struct{}),
		cancel:      cancel,
	}
	sessionCookies, err := consumeSessionCookies(profile)
	if err != nil {
		cancel()
		return nil, errors.Join(err, b.cleanup())
	}
	startupCtx, stopStartup := context.WithTimeout(ctx, 30*time.Second)
	defer stopStartup()

	parser := launcher.NewURLParser().Context(startupCtx)
	b.cmd = exec.Command(binPath, browserLauncher(headless, cfg, profile).FormatArgs()...)
	b.cmd.Stdout = parser
	b.cmd.Stderr = parser
	if err := b.start(); err != nil {
		return nil, fmt.Errorf("启动浏览器失败: %w", err)
	}
	// Rod 的 WebSocket 握手读操作不响应 context，启动超时必须终止进程以解阻塞。
	stopTimeout := context.AfterFunc(startupCtx, func() {
		cancel()
		if errors.Is(startupCtx.Err(), context.DeadlineExceeded) {
			_ = b.terminate()
		}
	})
	defer stopTimeout()
	startupExitError := func() error {
		return fmt.Errorf("浏览器启动后退出（%s，profile 可能正被占用）: %w", b.cmd.ProcessState, errors.Join(b.exitErr, parser.Err()))
	}
	startupCanceledByExit := false
	defer func() {
		if err != nil {
			terminateErr := b.terminate()
			if startupCanceledByExit && !b.Alive() {
				err = startupExitError()
			}
			err = errors.Join(err, terminateErr)
		}
	}()

	var endpoint string
	select {
	case endpoint = <-parser.URL:
	case <-b.exit:
		return nil, startupExitError()
	case <-startupCtx.Done():
		// 启动等待的 Canceled 来自进程退出，回收完成后再读取退出状态和输出。
		startupCanceledByExit = errors.Is(startupCtx.Err(), context.Canceled)
		return nil, fmt.Errorf("等待浏览器启动: %w", startupCtx.Err())
	}
	controlURL, err := resolveControlURL(startupCtx, endpoint)
	if err != nil {
		return nil, fmt.Errorf("读取浏览器调试地址: %w", err)
	}
	b.browser = rod.New().Context(ctx).ControlURL(controlURL)
	if err := b.browser.Connect(); err != nil {
		return nil, fmt.Errorf("连接浏览器: %w", err)
	}
	b.uaOverride, err = buildUAOverride(b.browser.Context(startupCtx), "zh-CN")
	if err != nil {
		return nil, fmt.Errorf("构建 UA 一致性配置: %w", err)
	}
	if cfg.downloadDir != "" {
		dir, err := filepath.Abs(cfg.downloadDir)
		if err != nil {
			return nil, fmt.Errorf("解析下载目录: %w", err)
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("创建下载目录: %w", err)
		}
		if err := (proto.BrowserSetDownloadBehavior{
			Behavior:     proto.BrowserSetDownloadBehaviorBehaviorAllow,
			DownloadPath: dir,
		}).Call(b.browser.Context(startupCtx)); err != nil {
			return nil, fmt.Errorf("设置下载目录: %w", err)
		}
	}
	if err := b.restoreSessionCookies(startupCtx, sessionCookies); err != nil {
		return nil, err
	}
	if len(importCookies) > 0 {
		if err := b.browser.Context(startupCtx).SetCookies(importCookies); err != nil {
			return nil, fmt.Errorf("导入旧 Cookie: %w", err)
		}
	}
	if err := startupCtx.Err(); err != nil {
		return nil, err
	}
	return b, nil
}

func browserLauncher(headless bool, cfg *browserConfig, profile string) *launcher.Launcher {
	seed := cfg.fingerprintSeed
	if seed <= 0 {
		seed = rand.Intn(89999) + 10000
	}
	platform := "windows"
	if runtime.GOOS == "darwin" {
		platform = "macos"
	}
	// 显式覆盖 Rod 全局 dir/port/proxy，避免串用其他账号或连接已有实例。
	l := launcher.New().Headless(headless).UserDataDir(profile).RemoteDebuggingPort(0).
		Leakless(false).Set("no-sandbox").Set("fingerprint", strconv.Itoa(seed)).
		Set("fingerprint-platform", platform).Set("fingerprint-brand", "Chrome").
		Set("lang", "zh-CN").Set("no-startup-window").Delete("proxy-server")
	if cfg.proxy != "" {
		l.Proxy(cfg.proxy)
		logrus.Infof("Using proxy: %s", maskProxyCredentials(cfg.proxy))
	}
	return l
}

func (cfg *browserConfig) loadCookies() ([]*proto.NetworkCookieParam, error) {
	data := cfg.cookies
	// 仅旧的临时浏览器调用兼容全局文件；持久 profile 本身是数据来源。
	if !cfg.cookiesSet && !cfg.userDataDirSet {
		var err error
		data, err = cookies.NewLoadCookie(cookies.GetCookiesFilePath()).LoadCookies()
		if err != nil {
			logrus.Debugf("未加载旧 Cookie: %v", err)
			return nil, nil
		}
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, nil
	}
	var stored []*proto.NetworkCookie
	if err := json.Unmarshal(data, &stored); err != nil {
		return nil, fmt.Errorf("解析旧 Cookie: %w", err)
	}
	for _, cookie := range stored {
		if cookie == nil {
			return nil, fmt.Errorf("旧 Cookie 数组包含 null")
		}
	}
	return proto.CookiesToParams(stored), nil
}

func resolveControlURL(ctx context.Context, endpoint string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/json/version", nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var version struct {
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&version); err != nil {
		return "", err
	}
	if version.WebSocketDebuggerURL == "" {
		return "", fmt.Errorf("浏览器未返回 WebSocket 调试地址")
	}
	return version.WebSocketDebuggerURL, nil
}

// NewPage 使用源码指纹补丁，不注入 stealth JS；返回前补齐 UA 与 Client Hints。
func (b *Browser) NewPage() *rod.Page {
	page := b.browser.MustPage()
	if err := b.uaOverride.Call(page); err != nil {
		_ = page.Close()
		panic(fmt.Errorf("设置页面 UA 一致性配置: %w", err))
	}
	return page
}
