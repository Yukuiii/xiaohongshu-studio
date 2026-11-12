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
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	desktopCookieName     = "xhs_desktop_session"
	desktopTicketDuration = 30 * time.Minute
	desktopFrameURL       = "/desktop/vnc.html?autoconnect=1&resize=scale&path=desktop%2Fwebsockify"
)

type desktopSessionKey struct{}

type desktopGateway struct {
	target             *url.URL
	key                [32]byte
	proxy              *httputil.ReverseProxy
	configurationError error
	sessionActive      func(string, string) bool
	mu                 sync.Mutex
	revoked            map[string]bool
	connections        map[string]map[*desktopConnection]struct{}
}

type desktopConnection struct {
	io.ReadWriteCloser
	gateway *desktopGateway
	session string
	once    sync.Once
}

func (connection *desktopConnection) Close() error {
	var err error
	connection.once.Do(func() {
		err = connection.ReadWriteCloser.Close()
		connection.gateway.mu.Lock()
		delete(connection.gateway.connections[connection.session], connection)
		connection.gateway.mu.Unlock()
	})
	return err
}

// 桌面只转发到本机进程，授权固定到具体账号的登录会话。
func newDesktopGateway(raw string) *desktopGateway {
	d := &desktopGateway{revoked: make(map[string]bool), connections: make(map[string]map[*desktopConnection]struct{})}
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
			prefix := "/desktop"
			if session, _ := r.In.Context().Value(desktopSessionKey{}).(string); session != "" {
				prefix += "/" + session
			}
			r.Out.URL.Path = strings.TrimPrefix(r.In.URL.Path, prefix)
			r.Out.URL.RawPath = ""
			r.Out.Host = target.Host
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
			if response.StatusCode != http.StatusSwitchingProtocols {
				return nil
			}
			session, _ := response.Request.Context().Value(desktopSessionKey{}).(string)
			if session == "" {
				return nil
			}
			upstream, ok := response.Body.(io.ReadWriteCloser)
			if !ok {
				return errors.New("桌面连接不支持双向传输")
			}
			d.mu.Lock()
			defer d.mu.Unlock()
			// 与 revoke 使用同一把锁，防止撤销后才登记的连接漏网。
			if d.revoked[session] {
				_ = upstream.Close()
				return errors.New("登录桌面会话已结束")
			}
			connection := &desktopConnection{ReadWriteCloser: upstream, gateway: d, session: session}
			if d.connections[session] == nil {
				d.connections[session] = make(map[*desktopConnection]struct{})
			}
			d.connections[session][connection] = struct{}{}
			response.Body = connection
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			http.Error(w, "内嵌桌面连接失败，请重新打开登录浏览器", http.StatusBadGateway)
		},
	}
	return d
}

func (d *desktopGateway) revoke(session string) {
	if session == "" {
		return
	}
	d.mu.Lock()
	d.revoked[session] = true
	list := make([]*desktopConnection, 0, len(d.connections[session]))
	for connection := range d.connections[session] {
		list = append(list, connection)
	}
	delete(d.connections, session)
	d.mu.Unlock()
	for _, connection := range list {
		_ = connection.Close()
	}
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

func (d *desktopGateway) grant(w http.ResponseWriter, r *http.Request) { d.grantSession(w, r, "", "") }

func (d *desktopGateway) grantSession(w http.ResponseWriter, r *http.Request, account, session string) {
	expires := time.Now().Add(desktopTicketDuration)
	value := strconv.FormatInt(expires.Unix(), 10)
	path := "/desktop"
	if session != "" {
		value = account + "." + session + "." + value
		path += "/" + session + "/"
	}
	http.SetCookie(w, &http.Cookie{Name: desktopCookieName, Value: value + "." + d.signature(value), Path: path, HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil, Expires: expires, MaxAge: int(desktopTicketDuration.Seconds())})
}

func (d *desktopGateway) authorized(r *http.Request) bool {
	cookie, err := r.Cookie(desktopCookieName)
	if err != nil {
		return false
	}
	value, signature, ok := strings.Cut(cookie.Value, ".")
	if d.sessionActive == nil {
		if !ok || !hmac.Equal([]byte(signature), []byte(d.signature(value))) {
			return false
		}
		expires, err := strconv.ParseInt(value, 10, 64)
		return err == nil && expires > time.Now().Unix()
	}
	parts := strings.Split(cookie.Value, ".")
	if len(parts) != 4 {
		return false
	}
	value = strings.Join(parts[:3], ".")
	if !hmac.Equal([]byte(parts[3]), []byte(d.signature(value))) {
		return false
	}
	expires, err := strconv.ParseInt(parts[2], 10, 64)
	session, _ := r.Context().Value(desktopSessionKey{}).(string)
	if err != nil || expires <= time.Now().Unix() || session != parts[1] || !d.sessionActive(parts[0], session) {
		return false
	}
	d.mu.Lock()
	revoked := d.revoked[session]
	d.mu.Unlock()
	return !revoked
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
	w.Header().Del("Access-Control-Allow-Origin")
	w.Header().Del("Access-Control-Allow-Methods")
	w.Header().Del("Access-Control-Allow-Headers")
	w.Header().Set("Cache-Control", "private, no-store")
	if !sameDesktopOrigin(r) {
		http.Error(w, "拒绝跨站桌面连接", http.StatusForbidden)
		return
	}
	if d.sessionActive != nil {
		parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/desktop/"), "/", 2)
		if len(parts) != 2 || !validSessionID(parts[0]) {
			http.Error(w, "请从账号页打开登录桌面", http.StatusUnauthorized)
			return
		}
		r = r.WithContext(context.WithValue(r.Context(), desktopSessionKey{}, parts[0]))
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

func validSessionID(id string) bool {
	if len(id) != 32 {
		return false
	}
	for _, c := range id {
		if !(c >= '0' && c <= '9') && !(c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func desktopSessionURL(session string) string {
	query := url.Values{"autoconnect": {"1"}, "resize": {"scale"}, "path": {"desktop/" + session + "/websockify"}}
	return "/desktop/" + session + "/vnc.html?" + query.Encode()
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
	result, err := s.service(c).OpenLoginBrowser(c.Request.Context())
	if err != nil {
		respondError(c, http.StatusInternalServerError, "LOGIN_BROWSER_FAILED", "打开登录浏览器失败", err)
		return
	}
	mode, frameURL := "native", ""
	if s.desktop.target != nil {
		mode, frameURL = "desktop", desktopFrameURL
		if result.SessionID != "" {
			frameURL = desktopSessionURL(result.SessionID)
			s.desktop.grantSession(c.Writer, c.Request, result.AccountID, result.SessionID)
		} else {
			s.desktop.grant(c.Writer, c.Request)
		}
	}
	respondSuccess(c, struct {
		*LoginBrowserResponse
		Mode       string `json:"mode"`
		DesktopURL string `json:"desktop_url"`
	}{result, mode, frameURL}, "登录浏览器已打开，请在窗口内完成登录和账号验证")
}

func (s *AppServer) closeLoginBrowserHandler(c *gin.Context) {
	path := "/desktop"
	if r := requestAccount(c); r != nil {
		session := c.Query("session_id")
		r.mu.Lock()
		active := r.loginID
		r.mu.Unlock()
		if active != "" && session == "" {
			respondError(c, http.StatusBadRequest, "LOGIN_SESSION_REQUIRED", "请指定要结束的登录窗口", nil)
			return
		}
		if err := r.closeLogin(c.Request.Context(), session); err != nil {
			respondError(c, http.StatusInternalServerError, "LOGIN_CLOSE_FAILED", "结束登录窗口失败", err)
			return
		}
		if session != "" {
			path += "/" + session + "/"
		}
	} else {
		s.service(c).CloseLoginBrowser()
	}
	http.SetCookie(c.Writer, &http.Cookie{Name: desktopCookieName, Value: "", Path: path, HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: c.Request.TLS != nil, MaxAge: -1})
	respondSuccess(c, gin.H{}, "登录窗口已结束，浏览器数据保留")
}
