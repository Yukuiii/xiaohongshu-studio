package main

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	desktopCookieName     = "xhs_desktop_session"
	desktopTicketDuration = 30 * time.Minute
	desktopFrameURL       = "/desktop/vnc.html?autoconnect=1&resize=scale&path=desktop%2Fwebsockify"
)

type desktopGateway struct {
	target             *url.URL
	key                [32]byte
	proxy              *httputil.ReverseProxy
	configurationError error
}

// 桌面只转发到本机内部进程，不接受请求参数指定代理目标。
func newDesktopGateway(raw string) *desktopGateway {
	d := &desktopGateway{}
	_, _ = rand.Read(d.key[:])
	if raw == "" {
		return d
	}
	target, err := url.Parse(raw)
	if err != nil || target.Scheme != "http" || target.User != nil || target.RawQuery != "" || target.ForceQuery || target.Fragment != "" || strings.Contains(raw, "#") || target.Port() == "" || (target.Path != "" && target.Path != "/") {
		d.configurationError = errors.New("内嵌桌面地址配置无效")
		return d
	}
	ip := net.ParseIP(target.Hostname())
	if target.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()) {
		d.configurationError = errors.New("内嵌桌面只允许连接本机回环地址")
		return d
	}
	target.Path = ""
	d.target = target
	d.proxy = &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(target)
			r.Out.URL.Path = strings.TrimPrefix(r.In.URL.Path, "/desktop")
			r.Out.URL.RawPath = ""
			r.Out.Host = target.Host
			// 浏览器来源已在入口校验，内部 websockify 仅信任此回环来源。
			r.Out.Header.Set("Origin", target.Scheme+"://"+target.Host)
			r.Out.Header.Del("Authorization")
			r.Out.Header.Del("Cookie")
		},
		ModifyResponse: func(response *http.Response) error {
			response.Header.Set("Cache-Control", "private, no-store")
			response.Header.Set("X-Content-Type-Options", "nosniff")
			response.Header.Set("Referrer-Policy", "no-referrer")
			response.Header.Set("X-Frame-Options", "SAMEORIGIN")
			response.Header.Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; connect-src 'self'; worker-src 'self' blob:; object-src 'none'; base-uri 'self'; frame-ancestors 'self'")
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			http.Error(w, "内嵌桌面连接失败，请重新打开登录浏览器", http.StatusBadGateway)
		},
	}
	return d
}

func (d *desktopGateway) ready(ctx context.Context) error {
	if d.configurationError != nil {
		return d.configurationError
	}
	if d.target == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.target.String()+"/vnc.html", nil)
	if err != nil {
		return errors.New("无法检查内嵌桌面")
	}
	client := &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		return errors.New("内嵌桌面尚未就绪，请稍后重新打开登录浏览器")
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode != http.StatusOK {
		return errors.New("内嵌桌面暂不可用，请检查应用启动日志")
	}
	return nil
}

func (d *desktopGateway) signature(value string) string {
	hash := hmac.New(sha256.New, d.key[:])
	_, _ = hash.Write([]byte(value))
	return base64.RawURLEncoding.EncodeToString(hash.Sum(nil))
}

func (d *desktopGateway) grant(w http.ResponseWriter, r *http.Request) {
	expires := time.Now().Add(desktopTicketDuration)
	value := strconv.FormatInt(expires.Unix(), 10)
	http.SetCookie(w, &http.Cookie{
		Name: desktopCookieName, Value: value + "." + d.signature(value),
		Path: "/desktop", HttpOnly: true, SameSite: http.SameSiteStrictMode,
		Secure: r.TLS != nil, Expires: expires, MaxAge: int(desktopTicketDuration.Seconds()),
	})
}

func (d *desktopGateway) authorized(r *http.Request) bool {
	cookie, err := r.Cookie(desktopCookieName)
	if err != nil {
		return false
	}
	value, signature, ok := strings.Cut(cookie.Value, ".")
	if !ok || !hmac.Equal([]byte(signature), []byte(d.signature(value))) {
		return false
	}
	expires, err := strconv.ParseInt(value, 10, 64)
	return err == nil && expires > time.Now().Unix()
}

func sameDesktopOrigin(r *http.Request) bool {
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return !strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
	}
	u, err := url.Parse(origin)
	return err == nil && u.User == nil && (u.Scheme == "http" || u.Scheme == "https") && strings.EqualFold(u.Host, r.Host) && u.Path == "" && u.RawQuery == "" && u.Fragment == ""
}

func (d *desktopGateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// noVNC 页面与 WebSocket 均只能由同源工作台打开。
	w.Header().Del("Access-Control-Allow-Origin")
	w.Header().Del("Access-Control-Allow-Methods")
	w.Header().Del("Access-Control-Allow-Headers")
	w.Header().Set("Cache-Control", "private, no-store")
	if !sameDesktopOrigin(r) {
		http.Error(w, "拒绝跨站桌面连接", http.StatusForbidden)
		return
	}
	if !d.authorized(r) {
		http.Error(w, "请从工作台打开登录浏览器", http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "桌面不支持该请求方法", http.StatusMethodNotAllowed)
		return
	}
	if d.proxy == nil {
		http.Error(w, "当前运行方式未启用内嵌桌面", http.StatusServiceUnavailable)
		return
	}
	d.proxy.ServeHTTP(w, r)
}

func (s *AppServer) openLoginBrowserHandler(c *gin.Context) {
	if err := s.desktop.ready(c.Request.Context()); err != nil {
		respondError(c, http.StatusServiceUnavailable, "DESKTOP_UNAVAILABLE", err.Error(), nil)
		return
	}
	if s.desktop.target == nil && runtime.GOOS == "linux" && os.Getenv("DISPLAY") == "" {
		respondError(c, http.StatusServiceUnavailable, "DESKTOP_UNAVAILABLE", "当前环境没有可视化桌面，请使用内置桌面的应用镜像启动", nil)
		return
	}
	result, err := s.xiaohongshuService.OpenLoginBrowser(c.Request.Context())
	if err != nil {
		respondError(c, http.StatusInternalServerError, "LOGIN_BROWSER_FAILED", "打开登录浏览器失败", err.Error())
		return
	}
	mode, frameURL := "native", ""
	if s.desktop.target != nil {
		mode, frameURL = "desktop", desktopFrameURL
		s.desktop.grant(c.Writer, c.Request)
	}
	respondSuccess(c, struct {
		*LoginBrowserResponse
		Mode       string `json:"mode"`
		DesktopURL string `json:"desktop_url"`
	}{result, mode, frameURL}, "登录浏览器已打开，请在窗口内完成登录和账号验证")
}

func (s *AppServer) closeLoginBrowserHandler(c *gin.Context) {
	s.xiaohongshuService.CloseLoginBrowser()
	http.SetCookie(c.Writer, &http.Cookie{Name: desktopCookieName, Value: "", Path: "/desktop", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: c.Request.TLS != nil, MaxAge: -1})
	respondSuccess(c, gin.H{}, "登录窗口已结束，已保存的登录信息保留")
}
