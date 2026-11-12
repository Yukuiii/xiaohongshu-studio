package main

import (
	"context"
	"sync"
	"time"

	"github.com/xpzouying/xiaohongshu-mcp/xiaohongshu"
)

// loginSessions 只保留一个可视化登录浏览器，关闭后保留最终状态和已保存的 cookie。
type loginSessions struct {
	mu        sync.Mutex
	seq       uint64
	cancel    func()
	phase     xiaohongshu.LoginPhase
	expiresAt time.Time
	user      xiaohongshu.CurrentUser
	saved     bool
}

type loginSessionSnapshot struct {
	seq       uint64
	phase     xiaohongshu.LoginPhase
	expiresAt time.Time
	user      xiaohongshu.CurrentUser
}

// start 登记新会话；序号隔离被替换会话的进度和 cookie 写入。
func (l *loginSessions) start(cancel func(), expiresAt time.Time) uint64 {
	l.mu.Lock()
	prev := l.cancel
	l.seq++
	seq := l.seq
	l.cancel = cancel
	l.phase = xiaohongshu.LoginWaitingScan
	l.expiresAt = expiresAt
	l.user = xiaohongshu.CurrentUser{}
	l.saved = false
	l.mu.Unlock()

	if prev != nil {
		prev()
	}
	return seq
}

func (l *loginSessions) setPhase(seq uint64, phase xiaohongshu.LoginPhase) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.seq != seq || l.cancel == nil || l.phase == phase {
		return false
	}
	l.phase = phase
	return true
}

func (l *loginSessions) snapshot() (loginSessionSnapshot, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return loginSessionSnapshot{
		seq: l.seq, phase: l.phase, expiresAt: l.expiresAt, user: l.user,
	}, l.cancel != nil
}

// save 将落盘和当前会话检查放在同一把锁内，关闭或替换后旧会话不能再写入。
func (l *loginSessions) save(seq uint64, ctx context.Context, user xiaohongshu.CurrentUser, persist func() error) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.seq != seq || l.cancel == nil || ctx.Err() != nil || !time.Now().Before(l.expiresAt) {
		return false, nil
	}
	if err := persist(); err != nil {
		return false, err
	}
	l.user = user
	l.saved = true
	l.phase = xiaohongshu.LoginLoggedIn
	return true, nil
}

// expire 不把已经保存成功的登录改成过期，也不删除 cookie。
func (l *loginSessions) expire(seq uint64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.seq == seq && l.cancel != nil && !l.saved {
		l.phase = xiaohongshu.LoginExpired
	}
}

func (l *loginSessions) finish(seq uint64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.seq == seq {
		l.cancel = nil
	}
}

// close 先使序号失效，再在锁外取消和关闭浏览器。
func (l *loginSessions) close() {
	l.mu.Lock()
	cancel := l.cancel
	l.cancel = nil
	l.seq++
	l.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}
