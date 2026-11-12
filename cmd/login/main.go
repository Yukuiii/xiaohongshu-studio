package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/go-rod/rod"
	"github.com/sirupsen/logrus"
	"github.com/xpzouying/xiaohongshu-mcp/accounts"
	"github.com/xpzouying/xiaohongshu-mcp/browser"
	"github.com/xpzouying/xiaohongshu-mcp/configs"
	"github.com/xpzouying/xiaohongshu-mcp/cookies"
	"github.com/xpzouying/xiaohongshu-mcp/xiaohongshu"
)

func main() {
	accountID := flag.String("account", "", "待登录的账号 ID；仅有一个账号时可省略")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()
	if err := run(ctx, *accountID); err != nil {
		logrus.Errorf("登录未保存: %v", err)
		os.Exit(1)
	}
}

func accountsRoot(legacyPath string) string {
	if root := strings.TrimSpace(os.Getenv("XHS_ACCOUNTS_DIR")); root != "" {
		return root
	}
	return filepath.Join(filepath.Dir(legacyPath), ".xhs-accounts")
}

func selectAccount(store *accounts.Store, id string) (accounts.Account, error) {
	if id != "" {
		return store.Get(id)
	}
	list := store.List()
	if len(list) == 1 {
		return list[0], nil
	}
	var choices []string
	for _, a := range list {
		choices = append(choices, fmt.Sprintf("%s (%s)", a.ID, a.Name))
	}
	return accounts.Account{}, fmt.Errorf("需要用 -account 指定一个有效账号，当前可选账号: %s", strings.Join(choices, ", "))
}

func run(ctx context.Context, accountID string) (err error) {
	legacyPath := cookies.GetCookiesFilePath()
	store, err := accounts.Open(accountsRoot(legacyPath), legacyPath, configs.FingerprintSeedFromEnv())
	if err != nil {
		return fmt.Errorf("打开账号存储失败（维护前请停止使用同一数据目录的服务）: %w", err)
	}
	defer func() { err = errors.Join(err, store.Close()) }()
	a, err := selectAccount(store, accountID)
	if err != nil {
		return err
	}
	logrus.Infof("为账号 %s (%s) 打开登录窗口", a.ID, a.Name)
	panicErr := rod.Try(func() { err = loginAccount(ctx, store, a) })
	return errors.Join(err, panicErr)
}

func openAccountBrowser(store *accounts.Store, a accounts.Account, headless bool, initial []byte) (*browser.Browser, error) {
	opts := []browser.Option{
		browser.WithUserDataDir(store.ProfilePath(a)),
		browser.WithDownloadDir(store.DownloadPath(a.ID)),
		browser.WithFingerprintSeed(a.Seed),
		browser.WithProxy(configs.ProxyFromEnv()),
	}
	if initial != nil {
		opts = append(opts, browser.WithCookies(initial))
	}
	return browser.Open(headless, opts...)
}

func shutdownBrowser(b *browser.Browser) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return b.Shutdown(ctx)
}

func importLegacy(store *accounts.Store, a accounts.Account) error {
	pending, err := store.PendingCookies(a.ID)
	if err != nil || pending == nil {
		return err
	}
	b, err := openAccountBrowser(store, a, true, pending)
	if err != nil {
		return err
	}
	defer b.Close()
	if err := shutdownBrowser(b); err != nil {
		return err
	}
	return store.CompleteImport(a.ID)
}

func loginAccount(ctx context.Context, store *accounts.Store, a accounts.Account) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	// 旧 Cookie 只导入 active profile，正常关闭后才复制候选目录。
	if err := importLegacy(store, a); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	candidateID, err := store.NewCandidate(a.ID)
	if err != nil {
		return err
	}
	candidate := a
	candidate.ActiveProfile = candidateID
	var b *browser.Browser
	committed := false
	defer func() {
		if b != nil {
			err = errors.Join(err, shutdownBrowser(b))
		}
		if !committed && (b == nil || !b.Alive()) {
			err = errors.Join(err, store.DiscardProfile(a.ID, candidateID))
		}
	}()
	b, err = openAccountBrowser(store, candidate, false, nil)
	if err != nil {
		return err
	}
	page := b.NewPage()
	action := xiaohongshu.NewLogin(page)
	if err := action.Login(ctx); err != nil {
		return err
	}
	user, err := action.CurrentUser(ctx)
	if err != nil {
		return err
	}
	if user == nil || strings.TrimSpace(user.UserID) == "" {
		return fmt.Errorf("无法确认真实账号，原浏览器数据已保留")
	}
	if a.UserID != "" && a.UserID != user.UserID {
		return accounts.ErrIdentityMismatch
	}
	for _, other := range store.List() {
		if other.ID != a.ID && other.UserID == user.UserID {
			return accounts.ErrDuplicateIdentity
		}
	}
	phase, err := action.CurrentPhase(ctx)
	if err != nil {
		return err
	}
	if phase != xiaohongshu.LoginLoggedIn {
		return fmt.Errorf("登录状态已变化，请重试；原浏览器数据已保留")
	}
	if err := shutdownBrowser(b); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := store.CommitProfile(a.ID, candidateID, user.UserID, user.Nickname); err != nil {
		return err
	}
	committed = true
	logrus.Infof("账号 %s 登录已保存", a.ID)
	return nil
}
