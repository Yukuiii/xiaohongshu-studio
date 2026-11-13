package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go-rod/rod"
	"github.com/sirupsen/logrus"
)

const (
	accountStatusInterval = 10 * time.Minute
	accountStatusTimeout  = 60 * time.Second
)

// startStatusMonitor 只由服务启动，构造管理器不会访问站点。
func (m *AccountManager) startStatusMonitor(interval time.Duration) {
	m.mu.Lock()
	if m.closing.Load() || m.statusDone != nil {
		m.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	m.statusCancel, m.statusDone = cancel, done
	m.mu.Unlock()

	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		m.refreshAccountStatuses(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.refreshAccountStatuses(ctx)
			}
		}
	}()
}

func (m *AccountManager) refreshAccountStatuses(ctx context.Context) {
	for _, a := range m.store.List() {
		if ctx.Err() != nil {
			return
		}
		r, err := m.runtime(a.ID)
		if err != nil {
			continue
		}
		var checkErr error
		panicErr := rod.Try(func() { checkErr = r.refreshLoginStatus(ctx) })
		err = errors.Join(checkErr, panicErr)
		if err == nil || errors.Is(err, context.Canceled) {
			continue
		}
		if failure := accountFailure(err); failure != nil && failure.Code == "ACCOUNT_BUSY" {
			continue
		}
		logrus.Warnf("账号 %s 登录巡检: %v", a.ID, err)
	}
}

func (r *accountRuntime) refreshLoginStatus(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, accountStatusTimeout)
	defer cancel()
	release, err := r.lease(true)
	if err != nil {
		return err
	}
	defer release()
	b, err := r.borrowBrowserFor(ctx, false, true)
	if err != nil {
		return err
	}
	defer b.Close()
	return r.cacheLoginFailure(r.manager.checkIdentity(ctx, r, b.accountBrowser))
}

// cacheLoginFailure 仅持久化明确失效的结果，检查异常保留上次状态。
func (r *accountRuntime) cacheLoginFailure(err error) error {
	failure := accountFailure(err)
	if failure == nil {
		return err
	}
	switch failure.Code {
	case "XHS_LOGIN_REQUIRED", "ACCOUNT_IDENTITY_MISMATCH", "ACCOUNT_ALREADY_EXISTS":
		if saveErr := r.manager.store.SetLoginStatus(r.id, false); saveErr != nil {
			return fmt.Errorf("保存账号登录状态失败: %w", saveErr)
		}
	}
	return err
}
