package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/go-rod/rod/lib/proto"
)

// 快照属于完整 profile；只在正常退出后提交，下次启动消费一次。
const sessionCookiesFile = "xhs-session-cookies.json"

func consumeSessionCookies(profile string) ([]*proto.NetworkCookie, error) {
	path := filepath.Join(profile, sessionCookiesFile)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取会话 Cookie 快照: %w", err)
	}
	// 必须先移除再启动进程，避免启动后崩溃或登出时复活上一次凭证。
	if err := os.Remove(path); err != nil {
		return nil, fmt.Errorf("消费会话 Cookie 快照: %w", err)
	}
	var cookies []*proto.NetworkCookie
	if err := json.Unmarshal(data, &cookies); err != nil {
		return nil, fmt.Errorf("解析会话 Cookie 快照: %w", err)
	}
	for _, cookie := range cookies {
		if _, err := sessionCookieParam(cookie); err != nil {
			return nil, err
		}
	}
	return cookies, nil
}

func sessionCookieParam(cookie *proto.NetworkCookie) (*proto.NetworkCookieParam, error) {
	if cookie == nil || !cookie.Session {
		return nil, errors.New("会话 Cookie 快照包含非 session Cookie")
	}
	if cookie.PartitionKeyOpaque {
		return nil, errors.New("无法恢复 opaque 分区的 session Cookie")
	}
	return &proto.NetworkCookieParam{
		Name: cookie.Name, Value: cookie.Value, Domain: cookie.Domain, Path: cookie.Path,
		Secure: cookie.Secure, HTTPOnly: cookie.HTTPOnly, SameSite: cookie.SameSite,
		Priority: cookie.Priority, SameParty: cookie.SameParty, SourceScheme: cookie.SourceScheme,
		SourcePort: &cookie.SourcePort, PartitionKey: cookie.PartitionKey,
		// 不设置 Expires，保留 session 语义。
	}, nil
}

type cookieIdentity struct {
	name, domain, path string
	partitioned        bool
	partition          proto.NetworkCookiePartitionKey
}

func cookieKey(cookie *proto.NetworkCookie) cookieIdentity {
	key := cookieIdentity{name: cookie.Name, domain: cookie.Domain, path: cookie.Path}
	if cookie.PartitionKey != nil {
		key.partitioned = true
		key.partition = *cookie.PartitionKey
	}
	return key
}

func (b *Browser) restoreSessionCookies(ctx context.Context, snapshot []*proto.NetworkCookie) error {
	if b.temporary {
		return nil
	}
	stored, err := b.browser.Context(ctx).GetCookies()
	if err != nil {
		return fmt.Errorf("读取 profile Cookie: %w", err)
	}
	existing := make(map[cookieIdentity]bool, len(stored))
	var discard []*proto.NetworkCookieParam
	for _, cookie := range stored {
		if cookie.Session {
			param, err := sessionCookieParam(cookie)
			if err != nil {
				return err
			}
			// 崩溃后 Chromium 也会恢复 session Cookie，只允许恢复正常退出的快照。
			// 用同分区的过期 Cookie 精确删除，避免 deleteCookies 的无分区通配误删持久 Cookie。
			param.Expires = 1
			discard = append(discard, param)
		} else {
			existing[cookieKey(cookie)] = true
		}
	}
	if len(discard) > 0 {
		if err := b.browser.Context(ctx).SetCookies(discard); err != nil {
			return fmt.Errorf("清除未确认退出的会话 Cookie: %w", err)
		}
	}
	var restore []*proto.NetworkCookieParam
	for _, cookie := range snapshot {
		// 磁盘 profile 已有的 Cookie 优先，尤其不能覆盖已刷新的同键持久 Cookie。
		if existing[cookieKey(cookie)] {
			continue
		}
		param, err := sessionCookieParam(cookie)
		if err != nil {
			return err
		}
		restore = append(restore, param)
	}
	if len(restore) == 0 {
		return nil
	}
	if err := b.browser.Context(ctx).SetCookies(restore); err != nil {
		return fmt.Errorf("恢复会话 Cookie: %w", err)
	}
	return nil
}

func (b *Browser) collectSessionCookies(ctx context.Context) ([]byte, error) {
	stored, err := b.browser.Context(ctx).GetCookies()
	if err != nil {
		return nil, fmt.Errorf("读取退出前的会话 Cookie: %w", err)
	}
	cookies := make([]*proto.NetworkCookie, 0)
	for _, cookie := range stored {
		if cookie.Session {
			if _, err := sessionCookieParam(cookie); err != nil {
				return nil, err
			}
			cookies = append(cookies, cookie)
		}
	}
	return json.Marshal(cookies)
}

func writeSessionCookies(profile string, data []byte) error {
	file, err := os.CreateTemp(profile, ".xhs-session-cookies-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	// CreateTemp 默认 0600，原子 rename 也不会继承旧文件的宽松权限。
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), filepath.Join(profile, sessionCookiesFile))
}

func removeSessionCookies(profile string) error {
	err := os.Remove(filepath.Join(profile, sessionCookiesFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// 调用方必须持有 snapshotMu，且尚未释放 profile 使用权。
func (b *Browser) commitSessionCookies(waitErr error) error {
	if b.temporary {
		return nil
	}
	if waitErr == nil && !b.forced && b.pendingSnapshot != nil {
		if err := writeSessionCookies(b.userDataDir, b.pendingSnapshot); err == nil {
			return nil
		} else {
			return errors.Join(fmt.Errorf("提交会话 Cookie 快照: %w", err), removeSessionCookies(b.userDataDir))
		}
	}
	return errors.Join(b.snapshotErr, removeSessionCookies(b.userDataDir))
}
