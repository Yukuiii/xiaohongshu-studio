package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/xpzouying/xiaohongshu-mcp/accounts"
)

func TestSelectAccountRequiresExplicitChoice(t *testing.T) {
	store, err := accounts.Open(filepath.Join(t.TempDir(), "accounts"), "", 0)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	a, err := selectAccount(store, "")
	require.NoError(t, err)
	require.Equal(t, store.LegacyAccountID(), a.ID)
	b, err := store.Create("B")
	require.NoError(t, err)
	_, err = selectAccount(store, "")
	require.ErrorContains(t, err, "-account")
	selected, err := selectAccount(store, b.ID)
	require.NoError(t, err)
	require.Equal(t, b, selected)
	_, err = selectAccount(store, "../outside")
	require.ErrorIs(t, err, accounts.ErrInvalid)
	_, err = selectAccount(store, strings.Repeat("0", 32))
	require.ErrorIs(t, err, accounts.ErrNotFound)
	require.NoError(t, store.Delete(a.ID))
	require.NoError(t, store.Delete(b.ID))
	_, err = selectAccount(store, "")
	require.Error(t, err)
	_, err = selectAccount(store, a.ID)
	require.ErrorIs(t, err, accounts.ErrNotFound)
}

func TestMaintenanceUsesSameRootAndLock(t *testing.T) {
	root := filepath.Join(t.TempDir(), "accounts")
	t.Setenv("XHS_ACCOUNTS_DIR", root)
	t.Setenv("COOKIES_PATH", filepath.Join(t.TempDir(), "cookies.json"))
	require.Equal(t, root, accountsRoot("ignored.json"))
	store, err := accounts.Open(root, "", 0)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	require.ErrorIs(t, run(context.Background(), ""), accounts.ErrLocked)
	require.NoError(t, store.Close())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, run(ctx, ""), context.Canceled)
	// 取消操作仍释放根目录锁，且没有改动 profile 指针。
	reopened, err := accounts.Open(root, "", 0)
	require.NoError(t, err)
	require.Equal(t, store.List(), reopened.List())
	require.NoError(t, reopened.Close())
	t.Setenv("XHS_ACCOUNTS_DIR", "")
	legacy := filepath.Join(t.TempDir(), "legacy.json")
	require.Equal(t, filepath.Join(filepath.Dir(legacy), ".xhs-accounts"), accountsRoot(legacy))
}
