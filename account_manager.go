package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
	"github.com/sirupsen/logrus"
	"github.com/xpzouying/xiaohongshu-mcp/accounts"
	"github.com/xpzouying/xiaohongshu-mcp/browser"
	"github.com/xpzouying/xiaohongshu-mcp/configs"
	"github.com/xpzouying/xiaohongshu-mcp/cookies"
	"github.com/xpzouying/xiaohongshu-mcp/xiaohongshu"
)

const accountHeader = "X-XHS-Account-ID"

// accountError 保留可供前端识别的账号错误。
type accountError struct {
	Status  int
	Code    string
	Message string
}

func (e *accountError) Error() string { return e.Message }

func accountBusy() error {
	return &accountError{http.StatusConflict, "ACCOUNT_BUSY", "该账号正在执行任务，请稍后重试"}
}

func accountFailure(err error) *accountError {
	var failure *accountError
	if errors.As(err, &failure) {
		return failure
	}
	switch {
	case errors.Is(err, accounts.ErrNotFound):
		return &accountError{http.StatusNotFound, "ACCOUNT_NOT_FOUND", "账号不存在或已移除，请重新选择"}
	case errors.Is(err, accounts.ErrInvalid):
		return &accountError{http.StatusBadRequest, "INVALID_ACCOUNT", "账号标识或名称无效"}
	case errors.Is(err, accounts.ErrIdentityMismatch):
		return &accountError{http.StatusConflict, "ACCOUNT_IDENTITY_MISMATCH", "扫码账号与原账号不一致，原浏览器数据已保留"}
	case errors.Is(err, accounts.ErrDuplicateIdentity):
		return &accountError{http.StatusConflict, "ACCOUNT_ALREADY_EXISTS", "该小红书账号已存在，请切换到已有账号"}
	}
	return nil
}

type accountBrowser interface {
	NewPage() *rod.Page
	Shutdown(context.Context) error
	Alive() bool
}

type browserLease struct {
	accountBrowser
	once    sync.Once
	release func()
}

func (b *browserLease) Close() { b.once.Do(b.release) }

// AccountManager 持有每个账号的浏览器；页面选择不会改变服务端全局状态。
type AccountManager struct {
	drainTimeout  time.Duration
	closeMu       sync.Mutex
	checkIdentity func(context.Context, *accountRuntime, accountBrowser) error
	active        sync.WaitGroup
	store         *accounts.Store
	mu            sync.Mutex
	runtimes      map[string]*accountRuntime
	closing       atomic.Bool
	idleTimeout   time.Duration
	headless      bool
	factory       func(accounts.Account, string, bool, []byte) (accountBrowser, error)
	loginMu       sync.Mutex
	visualAccount string
	visualSession string
	onLoginEnd    func(string)
}

type accountRuntime struct {
	orphan      accountBrowser
	manager     *AccountManager
	id          string
	service     *XiaohongshuService
	mu          sync.Mutex
	browser     accountBrowser
	browserBusy bool
	idle        *time.Timer
	readers     int
	writers     int
	removing    bool
	deleted     bool
	loginID     string
	loginDone   chan struct{}
	loginError  string
	loginCode   string
}

func NewAccountManager(store *accounts.Store) *AccountManager {
	m := &AccountManager{store: store, runtimes: make(map[string]*accountRuntime), idleTimeout: 10 * time.Minute, drainTimeout: 30 * time.Second, headless: configs.IsHeadless() || embeddedDesktop()}
	m.checkIdentity = func(ctx context.Context, r *accountRuntime, b accountBrowser) error {
		_, err := r.inspectIdentity(ctx, b)
		return err
	}
	m.factory = func(a accounts.Account, profile string, headless bool, initial []byte) (accountBrowser, error) {
		opts := []browser.Option{browser.WithUserDataDir(profile), browser.WithDownloadDir(store.DownloadPath(a.ID)), browser.WithFingerprintSeed(a.Seed), browser.WithProxy(configs.Proxy())}
		if initial != nil {
			opts = append(opts, browser.WithCookies(initial))
		}
		return browser.Open(headless, opts...)
	}
	return m
}

func (m *AccountManager) runtime(id string) (*accountRuntime, error) {
	if m.closing.Load() {
		return nil, &accountError{http.StatusServiceUnavailable, "SERVICE_CLOSING", "服务正在关闭"}
	}
	if _, err := m.store.Get(id); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing.Load() {
		return nil, &accountError{http.StatusServiceUnavailable, "SERVICE_CLOSING", "服务正在关闭"}
	}
	if r := m.runtimes[id]; r != nil {
		return r, nil
	}
	r := &accountRuntime{manager: m, id: id}
	r.service = &XiaohongshuService{account: r}
	m.runtimes[id] = r
	return r, nil
}

func (m *AccountManager) resolve(id string) (*accountRuntime, error) {
	if id == "" {
		list := m.store.List()
		if len(list) != 1 {
			return nil, &accountError{http.StatusBadRequest, "ACCOUNT_REQUIRED", "请明确选择要操作的小红书账号"}
		}
		id = list[0].ID
	}
	return m.runtime(id)
}

// lease 只保护账号生命周期，图片读取不占用浏览器互斥锁。
func (r *accountRuntime) lease(write bool) (func(), error) {
	r.manager.mu.Lock()
	defer r.manager.mu.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.removing || r.deleted || r.manager.closing.Load() {
		return nil, &accountError{http.StatusConflict, "ACCOUNT_UNAVAILABLE", "账号正在关闭或已移除"}
	}
	if write {
		r.writers++
	} else {
		r.readers++
	}
	r.manager.active.Add(1)
	var once sync.Once
	return func() {
		once.Do(func() {
			defer r.manager.active.Done()
			r.mu.Lock()
			if write {
				r.writers--
			} else {
				r.readers--
			}
			clean := r.deleted && r.readers == 0 && r.writers == 0
			r.mu.Unlock()
			if clean {
				r.cleanupImages()
			}
		})
	}, nil
}

func (r *accountRuntime) stopIdleLocked() {
	if r.idle != nil {
		r.idle.Stop()
		r.idle = nil
	}
}

func (r *accountRuntime) scheduleIdleLocked(b accountBrowser) {
	r.stopIdleLocked()
	if b == nil || r.manager.closing.Load() || r.deleted {
		return
	}
	r.idle = time.AfterFunc(r.manager.idleTimeout, func() {
		r.mu.Lock()
		if r.browser != b || r.browserBusy || r.loginID != "" || r.removing {
			r.mu.Unlock()
			return
		}
		r.browserBusy = true
		r.mu.Unlock()
		err := stopAccountBrowser(b)
		r.mu.Lock()
		if err == nil || !b.Alive() {
			r.browser = nil
		}
		r.browserBusy = false
		r.mu.Unlock()
		if err != nil {
			logrus.Warnf("账号 %s 浏览器回收失败: %v", r.id, err)
		}
	})
}

func stopAccountBrowser(b accountBrowser) error {
	if b == nil || !b.Alive() {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return b.Shutdown(ctx)
}

func (r *accountRuntime) launchProfile(a accounts.Account, path string, headless bool, initial []byte) (accountBrowser, error) {
	closingError := &accountError{http.StatusServiceUnavailable, "SERVICE_CLOSING", "服务正在关闭"}
	if r.manager.closing.Load() {
		return nil, closingError
	}
	b, err := r.manager.factory(a, path, headless, initial)
	if err != nil {
		return b, err
	}
	if r.manager.closing.Load() {
		stopErr := stopAccountBrowser(b)
		if b.Alive() {
			r.mu.Lock()
			r.orphan = b
			r.mu.Unlock()
		}
		return b, errors.Join(closingError, stopErr)
	}
	return b, nil
}

func (r *accountRuntime) openProfile() (accountBrowser, error) {
	a, err := r.manager.store.Get(r.id)
	if err != nil {
		return nil, err
	}
	pending, err := r.manager.store.PendingCookies(r.id)
	if err != nil {
		return nil, err
	}
	if pending != nil {
		b, err := r.launchProfile(a, r.manager.store.ProfilePath(a), true, pending)
		if err != nil {
			return nil, err
		}
		if err := stopAccountBrowser(b); err != nil {
			return nil, err
		}
		if err := r.manager.store.CompleteImport(r.id); err != nil {
			return nil, err
		}
	}
	return r.launchProfile(a, r.manager.store.ProfilePath(a), r.manager.headless, nil)
}

func (r *accountRuntime) borrowBrowser(ctx context.Context, verify bool) (*browserLease, error) {
	r.mu.Lock()
	if r.browserBusy || r.loginID != "" || r.removing || r.deleted {
		r.mu.Unlock()
		return nil, accountBusy()
	}
	if r.manager.closing.Load() {
		r.mu.Unlock()
		return nil, accountBusy()
	}
	r.browserBusy = true
	r.stopIdleLocked()
	b := r.browser
	r.mu.Unlock()
	release := func() {
		r.mu.Lock()
		r.browserBusy = false
		r.scheduleIdleLocked(r.browser)
		r.mu.Unlock()
	}
	ok := false
	defer func() {
		if !ok {
			release()
		}
	}()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if b == nil || !b.Alive() {
		var err error
		b, err = r.openProfile()
		if err != nil {
			return nil, err
		}
		r.mu.Lock()
		r.browser = b
		r.mu.Unlock()
	}
	// 每次任务都核对真实身份，不能沿用浏览器运行期间的旧验证结果。
	if verify {
		if err := r.manager.checkIdentity(ctx, r, b); err != nil {
			return nil, err
		}
	}
	ok = true
	return &browserLease{accountBrowser: b, release: release}, nil
}

func (r *accountRuntime) inspectIdentity(ctx context.Context, b accountBrowser) (*xiaohongshu.CurrentUser, error) {
	p := b.NewPage()
	defer p.Close()
	login := xiaohongshu.NewLogin(p)
	logged, err := login.CheckLoginStatus(ctx)
	if err != nil {
		return nil, err
	}
	if !logged {
		return nil, &accountError{http.StatusConflict, "XHS_LOGIN_REQUIRED", "该账号尚未登录或登录已失效，请重新登录"}
	}
	user, err := login.CurrentUser(ctx)
	if err != nil {
		return nil, err
	}
	if user == nil || user.UserID == "" {
		return nil, fmt.Errorf("无法确认小红书账号身份，请重试")
	}
	if err := r.manager.store.BindIdentity(r.id, user.UserID, user.Nickname); err != nil {
		return nil, err
	}
	return user, nil
}

func (r *accountRuntime) loginStatus(ctx context.Context) (*LoginStatusResponse, error) {
	if state, active := r.service.logins.snapshot(); active {
		response := loginSessionResponse(state)
		r.mu.Lock()
		response.AccountID = r.id
		response.SessionID = r.loginID
		response.ErrorCode = r.loginCode
		r.mu.Unlock()
		return response, nil
	}
	b, err := r.borrowBrowser(ctx, false)
	if err != nil {
		return nil, err
	}
	defer b.Close()
	user, err := r.inspectIdentity(ctx, b.accountBrowser)
	if err != nil {
		var failure *accountError
		if errors.As(err, &failure) && failure.Code == "XHS_LOGIN_REQUIRED" {
			state, _ := r.service.logins.snapshot()
			phase := state.phase
			if phase != xiaohongshu.LoginFailed && phase != xiaohongshu.LoginExpired {
				phase = xiaohongshu.LoginNotLoggedIn
			}
			response := loginProgressResponse(phase)
			r.mu.Lock()
			response.AccountID = r.id
			response.ErrorCode = r.loginCode
			if r.loginError != "" {
				response.Message = r.loginError
			}
			r.mu.Unlock()
			return response, nil
		}
		return nil, err
	}
	return &LoginStatusResponse{AccountID: r.id, IsLoggedIn: true, Username: user.Nickname, UserID: user.UserID, Status: xiaohongshu.LoginLoggedIn, Message: "登录成功"}, nil
}

func randomSessionID() (string, error) {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(data[:]), nil
}

func (m *AccountManager) reserveVisual(id, session string) error {
	m.loginMu.Lock()
	defer m.loginMu.Unlock()
	if m.visualSession != "" {
		return &accountError{http.StatusConflict, "LOGIN_BROWSER_BUSY", "另一个账号的登录窗口正在使用，请先结束该窗口"}
	}
	m.visualAccount, m.visualSession = id, session
	return nil
}

func (m *AccountManager) visualActive(id, session string) bool {
	m.loginMu.Lock()
	defer m.loginMu.Unlock()
	return session != "" && m.visualAccount == id && m.visualSession == session
}

func (m *AccountManager) revokeVisual(id, session string) {
	if m.onLoginEnd != nil {
		m.onLoginEnd(session)
	}
}

func (m *AccountManager) releaseVisual(id, session string) {
	m.loginMu.Lock()
	if m.visualAccount == id && m.visualSession == session {
		m.visualAccount, m.visualSession = "", ""
	}
	m.loginMu.Unlock()
}

func (r *accountRuntime) openLogin(ctx context.Context) (response *LoginBrowserResponse, err error) {
	r.service.loginMu.Lock()
	defer r.service.loginMu.Unlock()
	if state, active := r.service.logins.snapshot(); active {
		response = loginBrowserResponse(state)
		r.mu.Lock()
		response.AccountID = r.id
		response.SessionID = r.loginID
		r.mu.Unlock()
		return response, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	session, err := randomSessionID()
	if err != nil {
		return nil, err
	}
	if err := r.manager.reserveVisual(r.id, session); err != nil {
		return nil, err
	}
	r.mu.Lock()
	if r.browserBusy || r.removing || r.deleted || r.manager.closing.Load() {
		r.mu.Unlock()
		r.manager.releaseVisual(r.id, session)
		return nil, accountBusy()
	}
	r.browserBusy = true
	r.stopIdleLocked()
	old := r.browser
	r.mu.Unlock()
	var candidate string
	var b accountBrowser
	handedOff := false
	defer func() {
		if recovered := recover(); recovered != nil {
			response = nil
			err = fmt.Errorf("打开账号登录窗口失败: %v", recovered)
		}
		if !handedOff {
			if b != nil {
				_ = stopAccountBrowser(b)
			}
			if candidate != "" && (b == nil || !b.Alive()) {
				_ = r.manager.store.DiscardProfile(r.id, candidate)
			}
			r.manager.revokeVisual(r.id, session)
			r.finishVisual(b, session)
		}
	}()
	if old != nil {
		if err := stopAccountBrowser(old); err != nil {
			return nil, err
		}
		r.mu.Lock()
		r.browser = nil
		r.mu.Unlock()
	}
	// 首次 Cookie 导入必须在候选 profile 复制前完成。
	pending, err := r.manager.store.PendingCookies(r.id)
	if err != nil {
		return nil, err
	}
	if pending != nil {
		initial, err := r.openProfile()
		if err != nil {
			return nil, err
		}
		if err := stopAccountBrowser(initial); err != nil {
			return nil, err
		}
	}
	candidate, err = r.manager.store.NewCandidate(r.id)
	if err != nil {
		return nil, err
	}
	a, err := r.manager.store.Get(r.id)
	if err != nil {
		return nil, err
	}
	candidateAccount := a
	candidateAccount.ActiveProfile = candidate
	b, err = r.launchProfile(a, r.manager.store.ProfilePath(candidateAccount), false, nil)
	if err != nil {
		return nil, err
	}
	page := b.NewPage()
	if err := page.Context(ctx).Timeout(5 * time.Second).SetWindow(&proto.BrowserBounds{WindowState: loginWindowState()}); err != nil {
		logrus.Debugf("设置登录窗口状态失败: %v", err)
	}
	action := xiaohongshu.NewLogin(page)
	if err := action.Open(ctx); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := r.monitorLogin(action, b, candidate, session, a.ActiveProfile); err != nil {
		return nil, err
	}
	handedOff = true
	state, _ := r.service.logins.snapshot()
	response = loginBrowserResponse(state)
	response.AccountID, response.SessionID = r.id, session
	return response, nil
}

func (r *accountRuntime) monitorLogin(action *xiaohongshu.LoginAction, b accountBrowser, candidate, session, previous string) error {
	ctx, cancel := context.WithTimeout(context.Background(), loginBrowserTimeout)
	deadline, _ := ctx.Deadline()
	release, err := r.lease(true)
	if err != nil {
		cancel()
		return err
	}
	done := make(chan struct{})
	r.mu.Lock()
	r.loginID = session
	r.loginDone = done
	r.loginError = ""
	r.loginCode = ""
	r.mu.Unlock()
	seq := r.service.logins.start(cancel, deadline)
	go func() {
		committed, stopped := false, false
		defer func() {
			if recovered := recover(); recovered != nil {
				r.recordLoginFailure(seq, fmt.Errorf("登录浏览器异常: %v", recovered))
			}
			cancel()
			r.manager.revokeVisual(r.id, session)
			if !stopped {
				if err := stopAccountBrowser(b); err != nil {
					logrus.Errorf("登录浏览器关闭失败: %v", err)
				}
			}
			if !committed {
				if !b.Alive() {
					_ = r.manager.store.DiscardProfile(r.id, candidate)
				}
			}
			r.service.logins.finish(seq)
			r.finishVisual(b, session)
			if release != nil {
				release()
			}
			close(done)
		}()
		err := action.MonitorLogin(ctx, func(phase xiaohongshu.LoginPhase) error {
			if phase != xiaohongshu.LoginLoggedIn {
				r.service.logins.setPhase(seq, phase)
				return nil
			}
			r.service.logins.setPhase(seq, xiaohongshu.LoginSaving)
			user, err := action.CurrentUser(ctx)
			if err != nil {
				return err
			}
			if user == nil || user.UserID == "" {
				return fmt.Errorf("无法确认真实账号，原浏览器数据已保留")
			}
			a, err := r.manager.store.Get(r.id)
			if err != nil {
				return err
			}
			if a.UserID != "" && a.UserID != user.UserID {
				return accounts.ErrIdentityMismatch
			}
			for _, other := range r.manager.store.List() {
				if other.ID != r.id && other.UserID == user.UserID {
					return accounts.ErrDuplicateIdentity
				}
			}
			current, err := action.CurrentPhase(ctx)
			if err != nil {
				return err
			}
			if current != xiaohongshu.LoginLoggedIn {
				r.service.logins.setPhase(seq, current)
				return nil
			}
			// 先让 Chromium 完整落盘，再切换唯一的 profile 指针。
			r.manager.revokeVisual(r.id, session)
			if err := stopAccountBrowser(b); err != nil {
				return err
			}
			stopped = true
			saved, err := r.service.logins.save(seq, ctx, *user, func() error { return r.manager.store.CommitProfile(r.id, candidate, user.UserID, user.Nickname) })
			if saved {
				committed = true
				cancel()
				if previous != "" {
					_ = r.manager.store.DiscardProfile(r.id, previous)
				}
			}
			return err
		})
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			r.service.logins.expire(seq)
		} else if err != nil && !errors.Is(err, context.Canceled) {
			r.recordLoginFailure(seq, err)
		}
	}()
	return nil
}

func (r *accountRuntime) recordLoginFailure(seq uint64, err error) {
	r.service.logins.setPhase(seq, xiaohongshu.LoginFailed)
	failure := accountFailure(err)
	message, code := "登录失败，原浏览器数据已保留，请重试", "LOGIN_FAILED"
	if failure != nil {
		message, code = failure.Message, failure.Code
	}
	r.mu.Lock()
	r.loginError = message
	r.loginCode = code
	r.mu.Unlock()
	logrus.Warnf("账号 %s 登录未提交: %v", r.id, err)
}

// finishVisual 只有旧进程真正退出后才释放共享桌面。
func (r *accountRuntime) finishVisual(b accountBrowser, session string) {
	alive := b != nil && b.Alive()
	r.mu.Lock()
	r.loginDone = nil
	if alive {
		r.orphan = b
		r.loginID = session
		r.browserBusy = true
		r.loginError = "登录浏览器仍在关闭，请重试结束窗口"
		r.loginCode = "BROWSER_CLOSE_FAILED"
	} else {
		r.orphan = nil
		r.loginID = ""
		r.browserBusy = false
	}
	r.mu.Unlock()
	if !alive {
		r.manager.releaseVisual(r.id, session)
	}
}

func (r *accountRuntime) closeLogin(ctx context.Context, session string) error {
	r.service.loginMu.Lock()
	defer r.service.loginMu.Unlock()
	r.mu.Lock()
	current, done, orphan := r.loginID, r.loginDone, r.orphan
	r.mu.Unlock()
	if session != "" && session != current {
		return &accountError{http.StatusConflict, "LOGIN_SESSION_CHANGED", "登录窗口已变化，请刷新状态"}
	}
	if done == nil {
		if orphan == nil {
			return nil
		}
		err := stopAccountBrowser(orphan)
		r.finishVisual(orphan, current)
		if orphan.Alive() {
			logrus.Warnf("登录浏览器仍未退出: %v", err)
			return &accountError{http.StatusServiceUnavailable, "BROWSER_CLOSE_FAILED", "登录浏览器仍在关闭，请重试结束窗口"}
		}
		return nil
	}
	r.manager.revokeVisual(r.id, current)
	r.service.logins.close()
	select {
	case <-done:
		r.mu.Lock()
		orphan := r.orphan
		r.mu.Unlock()
		if orphan != nil && orphan.Alive() {
			return &accountError{http.StatusServiceUnavailable, "BROWSER_CLOSE_FAILED", "登录浏览器仍在关闭，请重试结束窗口"}
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *accountRuntime) maintenance(ctx context.Context, remove bool) error {
	r.mu.Lock()
	if r.removing || r.deleted || r.writers != 0 || r.browserBusy || r.loginID != "" {
		r.mu.Unlock()
		return accountBusy()
	}
	r.removing = true
	r.stopIdleLocked()
	b := r.browser
	r.mu.Unlock()
	finish := func() { r.mu.Lock(); r.removing = false; r.mu.Unlock() }
	if err := stopAccountBrowser(b); err != nil {
		finish()
		return err
	}
	r.mu.Lock()
	r.browser = nil
	r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		finish()
		return err
	}
	var err error
	if remove {
		err = r.manager.store.Delete(r.id)
	} else {
		err = r.manager.store.ResetProfile(r.id)
	}
	if err != nil {
		finish()
		return err
	}
	r.mu.Lock()
	r.deleted = remove
	r.removing = remove
	clean := remove && r.readers == 0
	r.loginError = ""
	r.loginCode = ""
	r.mu.Unlock()
	if clean {
		r.cleanupImages()
	}
	return nil
}

func (r *accountRuntime) cleanupImages() {
	dir, err := imageStorageDirForAccount(r.id)
	if err == nil {
		if err := os.RemoveAll(dir); err != nil {
			logrus.Warnf("账号图片清理失败: %v", err)
		}
	}
}

func (m *AccountManager) shutdownBrowsers(list []*accountRuntime) error {
	var wg sync.WaitGroup
	var errorMu sync.Mutex
	var failures []error
	for _, r := range list {
		wg.Add(1)
		go func(r *accountRuntime) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			if err := r.closeLogin(ctx, ""); err != nil {
				logrus.Warnf("结束登录浏览器: %v", err)
			}
			r.mu.Lock()
			r.stopIdleLocked()
			b, orphan := r.browser, r.orphan
			r.mu.Unlock()
			for _, instance := range []accountBrowser{b, orphan} {
				if err := stopAccountBrowser(instance); err != nil {
					logrus.Warnf("账号浏览器退出: %v", err)
				}
				if instance != nil && instance.Alive() {
					errorMu.Lock()
					failures = append(failures, fmt.Errorf("账号 %s 的浏览器仍在运行，保留数据根锁", r.id))
					errorMu.Unlock()
				}
			}
		}(r)
	}
	wg.Wait()
	return errors.Join(failures...)
}

func (m *AccountManager) Close() error {
	m.closeMu.Lock()
	defer m.closeMu.Unlock()
	m.closing.Store(true)
	m.mu.Lock()
	list := make([]*accountRuntime, 0, len(m.runtimes))
	for _, r := range m.runtimes {
		list = append(list, r)
	}
	m.mu.Unlock()
	for _, r := range list {
		r.service.logins.close()
	}
	drained := make(chan struct{})
	go func() { m.active.Wait(); close(drained) }()
	select {
	case <-drained:
	case <-time.After(m.drainTimeout):
		logrus.Warn("等待账号任务结束超时，正在关闭浏览器")
		_ = m.shutdownBrowsers(list)
		// 启动中的进程尚未登记到 runtime；必须等待其返回并自行关闭，不能提前释放根锁。
		<-drained
	}
	if err := m.shutdownBrowsers(list); err != nil {
		return err
	}
	return m.store.Close()
}

func embeddedDesktop() bool { return os.Getenv("XHS_DESKTOP_URL") != "" }

// loginWindowState 内嵌桌面直接全屏，避免用户误触窗口关闭按钮；原生窗口保留最大化。
func loginWindowState() proto.BrowserWindowState {
	if embeddedDesktop() {
		return proto.BrowserWindowStateFullscreen
	}
	return proto.BrowserWindowStateMaximized
}

func defaultAccountsRoot() string {
	if root := os.Getenv("XHS_ACCOUNTS_DIR"); root != "" {
		return root
	}
	return filepath.Join(filepath.Dir(cookies.GetCookiesFilePath()), ".xhs-accounts")
}
