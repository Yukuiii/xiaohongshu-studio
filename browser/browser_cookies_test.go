package browser

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSessionCookieSnapshotConsumesOnceWithAllRestorableAttributes(t *testing.T) {
	profile := t.TempDir()
	cookie := &proto.NetworkCookie{
		Name: "session", Value: "current", Domain: "fixture.test", Path: "/account",
		Session: true, Expires: -1, HTTPOnly: true, Secure: true, SameSite: proto.NetworkCookieSameSiteNone,
		Priority: proto.NetworkCookiePriorityHigh, SourceScheme: proto.NetworkCookieSourceSchemeSecure, SourcePort: 443,
		PartitionKey: &proto.NetworkCookiePartitionKey{TopLevelSite: "https://top.test", HasCrossSiteAncestor: true},
	}
	data, err := json.Marshal([]*proto.NetworkCookie{cookie})
	require.NoError(t, err)
	require.NoError(t, writeSessionCookies(profile, data))
	info, err := os.Stat(filepath.Join(profile, sessionCookiesFile))
	require.NoError(t, err)
	if runtime.GOOS != "windows" {
		require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	}
	stored, err := consumeSessionCookies(profile)
	require.NoError(t, err)
	require.Equal(t, []*proto.NetworkCookie{cookie}, stored)
	require.NoFileExists(t, filepath.Join(profile, sessionCookiesFile))
	again, err := consumeSessionCookies(profile)
	require.NoError(t, err)
	require.Empty(t, again)
	param, err := sessionCookieParam(stored[0])
	require.NoError(t, err)
	require.Equal(t, cookie.PartitionKey, param.PartitionKey)
	require.Equal(t, cookie.SourcePort, *param.SourcePort)
	require.Equal(t, cookie.SourceScheme, param.SourceScheme)
	require.Equal(t, cookie.SameSite, param.SameSite)
	require.Equal(t, cookie.Priority, param.Priority)
	require.True(t, param.Secure)
	require.True(t, param.HTTPOnly)
	require.Zero(t, param.Expires, "恢复时不能把 session Cookie 转成永久 Cookie")
	entries, err := os.ReadDir(profile)
	require.NoError(t, err)
	require.Empty(t, entries, "原子写不应遗留临时文件")
}

func TestInvalidSessionCookieSnapshotsCannotBeReplayed(t *testing.T) {
	for _, data := range []string{`invalid`, `[null]`, `[{"session":false}]`, `[{"session":true,"partitionKeyOpaque":true}]`} {
		profile := t.TempDir()
		require.NoError(t, writeSessionCookies(profile, []byte(data)))
		_, err := consumeSessionCookies(profile)
		require.Error(t, err)
		require.NoFileExists(t, filepath.Join(profile, sessionCookiesFile))
	}
}

func TestSessionCookieRestorePreservesPersistentCookieAndPartitionKeys(t *testing.T) {
	persistent := &proto.NetworkCookie{Name: "auth", Value: "new-persistent", Domain: "fixture.test", Path: "/", Expires: 12345678900}
	staleNative := &proto.NetworkCookie{Name: "auth", Value: "unconfirmed", Domain: "fixture.test", Path: "/", Session: true, Expires: -1,
		PartitionKey: &proto.NetworkCookiePartitionKey{TopLevelSite: "https://other.test"}}
	snapshot := []*proto.NetworkCookie{
		{Name: "auth", Value: "old-session", Domain: "fixture.test", Path: "/", Session: true, Expires: -1},
		{Name: "auth", Value: "partition-session", Domain: "fixture.test", Path: "/", Session: true, Expires: -1, Secure: true,
			PartitionKey: &proto.NetworkCookiePartitionKey{TopLevelSite: "https://top.test", HasCrossSiteAncestor: true}},
	}
	var sets [][]*proto.NetworkCookieParam
	b := &Browser{browser: rod.New().ControlURL("").Client(testCDP{callWithParams: func(_ context.Context, method string, params interface{}) ([]byte, error) {
		switch method {
		case "Storage.getCookies":
			return json.Marshal(proto.StorageGetCookiesResult{Cookies: []*proto.NetworkCookie{persistent, staleNative}})
		case "Storage.setCookies":
			sets = append(sets, params.(proto.StorageSetCookies).Cookies)
			return []byte(`{}`), nil
		default:
			t.Fatalf("意外的 CDP 调用: %s", method)
			return nil, nil
		}
	}})}
	require.NoError(t, b.restoreSessionCookies(context.Background(), snapshot))
	require.Len(t, sets, 2)
	require.Len(t, sets[0], 1)
	require.Equal(t, proto.TimeSinceEpoch(1), sets[0][0].Expires, "先按原分区精确删除未确认退出的 session")
	require.Equal(t, staleNative.PartitionKey, sets[0][0].PartitionKey)
	require.Len(t, sets[1], 1, "不能覆盖同键持久 Cookie")
	require.Equal(t, "partition-session", sets[1][0].Value)
	require.Equal(t, snapshot[1].PartitionKey, sets[1][0].PartitionKey)
	require.Zero(t, sets[1][0].Expires)
}

func TestProfileLeaseIsReleasedAfterSessionSnapshotCommit(t *testing.T) {
	b, stdin := lifecycleBrowser(t, true)
	b.browser = rod.New().ControlURL("").Client(testCDP{call: func(_ context.Context, method string) ([]byte, error) {
		if method == "Storage.getCookies" {
			return []byte(`{"cookies":[{"name":"session","value":"latest","session":true,"expires":-1,"domain":"fixture.test","path":"/"}]}`), nil
		}
		return []byte(`{}`), stdin.Close()
	}})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	shutdown := make(chan error, 1)
	go func() { shutdown <- b.Shutdown(ctx) }()
	cfg := &browserConfig{}
	WithUserDataDir(b.userDataDir)(cfg)
	var path string
	var acquireErr error
	require.Eventually(t, func() bool {
		path, _, acquireErr = prepareProfile(cfg)
		return !errors.Is(acquireErr, ErrProfileInUse)
	}, 10*time.Second, time.Millisecond)
	require.NoError(t, acquireErr)
	t.Cleanup(func() { require.NoError(t, (&Browser{userDataDir: path}).cleanup()) })
	// 不等 Shutdown 返回：下一实例能取得使用权时，快照就必须已经提交。
	data, err := os.ReadFile(filepath.Join(path, sessionCookiesFile))
	require.NoError(t, err)
	var stored []*proto.NetworkCookie
	require.NoError(t, json.Unmarshal(data, &stored))
	require.Len(t, stored, 1)
	require.Equal(t, "latest", stored[0].Value)
	require.NoError(t, <-shutdown)
}

func TestFailedCookieCollectionCannotCommitPreviousSnapshot(t *testing.T) {
	b, stdin := lifecycleBrowser(t, true)
	require.NoError(t, writeSessionCookies(b.userDataDir, []byte(`[{"name":"stale","value":"old","session":true}]`)))
	readErr := errors.New("fixture Cookie 读取失败")
	b.browser = rod.New().ControlURL("").Client(testCDP{call: func(_ context.Context, method string) ([]byte, error) {
		if method == "Storage.getCookies" {
			return nil, readErr
		}
		assert.Equal(t, "Browser.close", method)
		return []byte(`{}`), stdin.Close()
	}})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.ErrorIs(t, b.Shutdown(ctx), readErr)
	require.False(t, b.Alive())
	require.NoFileExists(t, filepath.Join(b.userDataDir, sessionCookiesFile))
}
