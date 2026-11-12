package browser

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
	"github.com/sirupsen/logrus"
)

// ErrProfileInUse 表示本进程已有浏览器使用该 profile；外部占用由 Chromium 的锁校验。
var ErrProfileInUse = errors.New("浏览器 profile 正在使用")

var profilesInUse = struct {
	sync.Mutex
	paths map[string]bool
}{paths: make(map[string]bool)}

// Browser 管理 Rod 连接、独立进程和完整 profile 的生命周期。
type Browser struct {
	browser     *rod.Browser
	uaOverride  *proto.NetworkSetUserAgentOverride
	cmd         *exec.Cmd
	exit        chan struct{}
	cancel      context.CancelFunc
	closeOnce   sync.Once
	userDataDir string
	temporary   bool

	snapshotMu      sync.Mutex
	pendingSnapshot []byte
	snapshotErr     error
	forced          bool
	// 仅等待进程的 goroutine 写入，读取前必须收到 exit 通知。
	exitErr    error
	cleanupErr error
}

func prepareProfile(cfg *browserConfig) (path string, temporary bool, err error) {
	temporary = !cfg.userDataDirSet
	if temporary {
		path, err = os.MkdirTemp("", "xhs-browser-")
	} else {
		if cfg.userDataDir == "" {
			return "", false, errors.New("持久 profile 目录不能为空")
		}
		path, err = filepath.Abs(cfg.userDataDir)
		if err == nil {
			err = os.MkdirAll(path, 0o700)
		}
	}
	if err != nil {
		return "", temporary, fmt.Errorf("创建浏览器 profile: %w", err)
	}
	// 统一符号链接和相对路径，防止同目录通过不同路径启动两次。
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		if temporary {
			_ = os.RemoveAll(path)
		}
		return "", temporary, fmt.Errorf("解析浏览器 profile: %w", err)
	}
	profilesInUse.Lock()
	defer profilesInUse.Unlock()
	if profilesInUse.paths[canonical] {
		return "", temporary, fmt.Errorf("%w: %s", ErrProfileInUse, canonical)
	}
	profilesInUse.paths[canonical] = true
	return canonical, temporary, nil
}

func (b *Browser) cleanup() error {
	var err error
	// 只能删除本封装创建的临时目录，绝不调用 launcher.Cleanup。
	if b.temporary {
		err = os.RemoveAll(b.userDataDir)
	}
	profilesInUse.Lock()
	delete(profilesInUse.paths, b.userDataDir)
	profilesInUse.Unlock()
	return err
}

func (b *Browser) start() error {
	configureProcess(b.cmd)
	if err := b.cmd.Start(); err != nil {
		b.finish(err)
		return b.exitErr
	}
	go func() { b.finish(b.cmd.Wait()) }()
	return nil
}

func (b *Browser) finish(waitErr error) {
	b.cancel()
	b.snapshotMu.Lock()
	defer b.snapshotMu.Unlock()
	// 快照提交必须先于释放使用权，否则下一实例可能先启动再读到迟到的旧快照。
	snapshotErr := b.commitSessionCookies(waitErr)
	b.cleanupErr = errors.Join(snapshotErr, b.cleanup())
	b.exitErr = errors.Join(waitErr, b.cleanupErr)
	close(b.exit)
}

// Alive 表示受管理的进程尚未退出，不发起网络请求。
func (b *Browser) Alive() bool {
	if b == nil || b.exit == nil {
		return false
	}
	select {
	case <-b.exit:
		return false
	default:
		return true
	}
}

// Shutdown 先通过 CDP 正常关闭并等进程落盘退出；context 到期后终止进程树。
// 终止后最多等待 5 秒回收进程，持久 profile 在所有路径都保留。
func (b *Browser) Shutdown(ctx context.Context) error {
	if b == nil || b.exit == nil {
		return nil
	}
	if !b.Alive() {
		return b.exitErr
	}
	b.closeOnce.Do(func() {
		if b.browser != nil {
			go b.closeGracefully(ctx)
		}
	})
	select {
	case <-b.exit:
		return b.exitErr
	case <-ctx.Done():
		return errors.Join(ctx.Err(), b.terminate())
	}
}

func (b *Browser) closeGracefully(ctx context.Context) {
	if !b.temporary {
		data, err := b.collectSessionCookies(ctx)
		b.snapshotMu.Lock()
		if b.Alive() && !b.forced {
			b.pendingSnapshot, b.snapshotErr = data, err
		}
		b.snapshotMu.Unlock()
	}
	// 以进程退出为准：Browser.close 成功时连接也可能先返回 EOF。
	_ = b.browser.Context(ctx).Close()
}

func (b *Browser) terminate() error {
	b.snapshotMu.Lock()
	if !b.Alive() {
		b.snapshotMu.Unlock()
		return b.cleanupErr
	}
	b.forced = true
	b.pendingSnapshot = nil
	b.snapshotMu.Unlock()
	killErr := killProcess(b.cmd)
	if errors.Is(killErr, os.ErrProcessDone) {
		killErr = nil
	}
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case <-b.exit:
		return errors.Join(killErr, b.cleanupErr)
	case <-timer.C:
		return errors.Join(killErr, errors.New("终止浏览器后等待进程退出超时，profile 使用权仍保留"))
	}
}

// Close 保留 defer/func() 调用兼容性，默认给浏览器 10 秒正常落盘。
func (b *Browser) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := b.Shutdown(ctx); err != nil {
		logrus.Warnf("关闭浏览器: %v", err)
	}
}
