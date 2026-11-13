package accounts

import (
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/xpzouying/xiaohongshu-mcp/db"
)

func storeDBPath(root string) string {
	return filepath.Join(filepath.Dir(root), "studio.db")
}

func openDatabase(t *testing.T, root string) *sql.DB {
	t.Helper()
	database, err := db.Open(storeDBPath(root))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, database.Close()) })
	return database
}

func openEmptyStore(t *testing.T, root string) *Store {
	t.Helper()
	database := openDatabase(t, root)
	s, err := Open(root, database)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Close()) })
	return s
}

// openStore 兼容旧测试调用；空库时创建一个默认账号。
func openStore(t *testing.T, root string, _ ...any) *Store {
	t.Helper()
	s := openEmptyStore(t, root)
	if len(s.List()) == 0 {
		_, err := s.Create("默认账号")
		require.NoError(t, err)
	}
	return s
}

func writeTestFile(t *testing.T, path string, data []byte) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
	require.NoError(t, os.WriteFile(path, data, 0600))
}

func readTestFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return data
}

func TestAccountsIsolationAndPersistence(t *testing.T) {
	root := filepath.Join(t.TempDir(), "accounts")
	s := openStore(t, root)
	a := s.List()[0]
	b, err := s.Create("账号 B")
	require.NoError(t, err)
	require.NotEqual(t, a.ID, b.ID)
	require.NotEqual(t, a.Seed, b.Seed)
	require.NotEqual(t, s.ProfilePath(a), s.ProfilePath(b))
	require.NotEqual(t, s.DownloadPath(a.ID), s.DownloadPath(b.ID))

	files := []string{
		"Local State", "Default/Network/Cookies", "Default/Local Storage/leveldb/000003.log",
		"Default/IndexedDB/site.leveldb/000003.log", "Default/Service Worker/Database/LOG",
		"Default/Preferences", "Default/Cache/Cache_Data/data_0",
	}
	for _, relative := range files {
		writeTestFile(t, filepath.Join(s.ProfilePath(a), relative), []byte("A:"+relative))
		writeTestFile(t, filepath.Join(s.ProfilePath(b), relative), []byte("B:"+relative))
	}
	writeTestFile(t, filepath.Join(s.DownloadPath(a.ID), "file"), []byte("A download"))
	writeTestFile(t, filepath.Join(s.DownloadPath(b.ID), "file"), []byte("B download"))
	a, err = s.Rename(a.ID, "  账号 A  ")
	require.NoError(t, err)
	require.Equal(t, "账号 A", a.Name)
	require.ElementsMatch(t, []Account{a, b}, s.List())

	if runtime.GOOS != "windows" {
		for _, path := range []string{root, filepath.Join(root, a.ID), filepath.Join(root, a.ID, "profiles"), s.ProfilePath(a), s.DownloadPath(a.ID)} {
			info, err := os.Stat(path)
			require.NoError(t, err)
			require.Equal(t, os.FileMode(0700), info.Mode().Perm(), path)
		}
		for _, path := range []string{filepath.Join(root, ".lock"), storeDBPath(root)} {
			info, err := os.Stat(path)
			require.NoError(t, err)
			require.Equal(t, os.FileMode(0600), info.Mode().Perm(), path)
		}
	}
	require.NoError(t, s.Close())
	s = openStore(t, root)
	require.ElementsMatch(t, []Account{a, b}, s.List())
	for _, account := range []Account{a, b} {
		prefix := "A:"
		if account.ID == b.ID {
			prefix = "B:"
		}
		for _, relative := range files {
			require.Equal(t, prefix+relative, string(readTestFile(t, filepath.Join(s.ProfilePath(account), relative))))
		}
	}
	require.Equal(t, "A download", string(readTestFile(t, filepath.Join(s.DownloadPath(a.ID), "file"))))
	require.Equal(t, "B download", string(readTestFile(t, filepath.Join(s.DownloadPath(b.ID), "file"))))
}

func TestStoreProcessLock(t *testing.T) {
	if root := os.Getenv("XHS_ACCOUNTS_LOCK_TEST_ROOT"); root != "" {
		s, err := Open(root, openDatabase(t, root))
		if s != nil {
			_ = s.Close()
		}
		require.ErrorIs(t, err, ErrLocked)
		return
	}
	root := filepath.Join(t.TempDir(), "accounts")
	s := openStore(t, root)
	database := openDatabase(t, root)
	other, err := Open(root, database)
	require.Nil(t, other)
	require.ErrorIs(t, err, ErrLocked)
	cmd := exec.Command(os.Args[0], "-test.run=^TestStoreProcessLock$")
	cmd.Env = append(os.Environ(), "XHS_ACCOUNTS_LOCK_TEST_ROOT="+root)
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, string(output))
	require.NoError(t, s.Close())
	require.NoError(t, s.Close())
	_, err = s.Create("closed")
	require.ErrorIs(t, err, os.ErrClosed)
	openStore(t, root)
}

func TestTombstoneNeverRevives(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "accounts")
	s := openEmptyStore(t, root)
	a, err := s.Create("A")
	require.NoError(t, err)
	require.NoError(t, s.BindIdentity(a.ID, "user-a", "昵称"))
	profile := s.ProfilePath(a)
	downloads := s.DownloadPath(a.ID)
	writeTestFile(t, filepath.Join(profile, "Local State"), []byte("secret"))
	writeTestFile(t, filepath.Join(downloads, "private"), []byte("secret"))
	require.NoError(t, s.Delete(a.ID))
	require.NoError(t, s.Delete(a.ID))
	require.Empty(t, s.List())
	require.NoDirExists(t, filepath.Dir(profile))
	require.NoDirExists(t, downloads)

	var deleted, seed int
	var storedUser string
	require.NoError(t, s.db.QueryRow(
		`SELECT deleted, user_id, seed FROM accounts WHERE id = ?`, a.ID,
	).Scan(&deleted, &storedUser, &seed))
	require.Equal(t, 1, deleted)
	require.Empty(t, storedUser)
	require.Zero(t, seed)
	_, err = s.Rename(a.ID, "late callback")
	require.ErrorIs(t, err, ErrNotFound)
	_, err = s.NewCandidate(a.ID)
	require.ErrorIs(t, err, ErrNotFound)
	require.ErrorIs(t, s.BindIdentity(a.ID, "user-a", "late"), ErrNotFound)
	require.ErrorIs(t, s.CommitProfile(a.ID, a.ActiveProfile, "user-a", "late"), ErrNotFound)
	require.ErrorIs(t, s.ResetProfile(a.ID), ErrNotFound)
	require.ErrorIs(t, s.CompleteImport(a.ID), ErrNotFound)
	require.Empty(t, s.ProfilePath(a))
	require.Empty(t, s.DownloadPath(a.ID))
	require.NoError(t, s.Close())

	// 重启后只补清理墓碑目录，不复活账号。
	writeTestFile(t, filepath.Join(profile, "leftover"), []byte("private"))
	writeTestFile(t, filepath.Join(downloads, "leftover"), []byte("private"))
	s = openEmptyStore(t, root)
	require.Empty(t, s.List())
	require.NoDirExists(t, filepath.Dir(profile))
	require.NoDirExists(t, downloads)
	_, err = s.Get(a.ID)
	require.ErrorIs(t, err, ErrNotFound)
	b, err := s.Create("B")
	require.NoError(t, err)
	require.NoError(t, s.BindIdentity(b.ID, "user-a", "可重新绑定"))
}

func TestDeleteCascadesDraftAndProtection(t *testing.T) {
	root := filepath.Join(t.TempDir(), "accounts")
	s := openStore(t, root)
	a := s.List()[0]
	_, err := s.db.Exec(`INSERT INTO drafts (account_id, payload, revision, updated_at) VALUES (?, '{}', 0, 1)`, a.ID)
	require.NoError(t, err)
	_, err = s.db.Exec(`INSERT INTO publish_protection (account_id, revision, updated_at) VALUES (?, 0, 1)`, a.ID)
	require.NoError(t, err)
	require.NoError(t, s.Delete(a.ID))
	var drafts, protections int
	require.NoError(t, s.db.QueryRow(`SELECT count(*) FROM drafts`).Scan(&drafts))
	require.NoError(t, s.db.QueryRow(`SELECT count(*) FROM publish_protection`).Scan(&protections))
	require.Zero(t, drafts)
	require.Zero(t, protections)
}

func TestInvalidIDsCannotEscape(t *testing.T) {
	s := openStore(t, filepath.Join(t.TempDir(), "accounts"))
	a := s.List()[0]
	for _, id := range []string{"", "..", "../outside", "/tmp/outside", strings.Repeat("a", 31), strings.Repeat("g", 32), strings.Repeat("A", 32)} {
		_, err := s.Get(id)
		require.ErrorIs(t, err, ErrInvalid, id)
		_, err = s.Rename(id, "name")
		require.ErrorIs(t, err, ErrInvalid, id)
		require.ErrorIs(t, s.Delete(id), ErrInvalid, id)
		_, err = s.NewCandidate(id)
		require.ErrorIs(t, err, ErrInvalid, id)
		require.Empty(t, s.DownloadPath(id))
		require.Empty(t, s.ProfilePath(Account{ID: id, ActiveProfile: a.ActiveProfile}))
		require.Empty(t, s.ProfilePath(Account{ID: a.ID, ActiveProfile: id}))
		require.ErrorIs(t, s.CommitProfile(a.ID, id, "user", "name"), ErrInvalid, id)
		require.ErrorIs(t, s.DiscardProfile(a.ID, id), ErrInvalid, id)
	}
	_, err := s.Get(strings.Repeat("0", 32))
	require.ErrorIs(t, err, ErrNotFound)
	for _, name := range []string{"", "   ", strings.Repeat("名", 201)} {
		_, err := s.Create(name)
		require.ErrorIs(t, err, ErrInvalid)
		_, err = s.Rename(a.ID, name)
		require.ErrorIs(t, err, ErrInvalid)
	}
}

func TestMissingProfileFailsOpen(t *testing.T) {
	root := filepath.Join(t.TempDir(), "accounts")
	s := openStore(t, root)
	a := s.List()[0]
	profile := s.ProfilePath(a)
	require.NoError(t, s.Close())
	require.NoError(t, os.RemoveAll(profile))

	database := openDatabase(t, root)
	broken, err := Open(root, database)
	require.Error(t, err)
	require.Nil(t, broken)
	probe, err := Open(root, database)
	require.Error(t, err)
	require.False(t, errors.Is(err, ErrLocked))
	require.Nil(t, probe)
}

func TestConcurrentCreatesAndIdentityBinding(t *testing.T) {
	s := openEmptyStore(t, filepath.Join(t.TempDir(), "accounts"))
	const count = 8
	created := make(chan Account, count)
	failures := make(chan error, count)
	var workers sync.WaitGroup
	for range count {
		workers.Go(func() {
			a, err := s.Create("并发账号")
			if err != nil {
				failures <- err
				return
			}
			created <- a
		})
	}
	workers.Wait()
	close(created)
	require.Empty(t, failures)
	seeds := map[int]bool{}
	for a := range created {
		require.False(t, seeds[a.Seed])
		seeds[a.Seed] = true
		workers.Go(func() { failures <- s.BindIdentity(a.ID, "shared-user", "同一真实账号") })
	}
	workers.Wait()
	close(failures)
	successes := 0
	for err := range failures {
		if err == nil {
			successes++
		} else {
			require.ErrorIs(t, err, ErrDuplicateIdentity)
		}
	}
	require.Equal(t, 1, successes)
	require.Len(t, s.List(), count)
}

func TestSetAvatarPersistsAndValidates(t *testing.T) {
	root := filepath.Join(t.TempDir(), "accounts")
	s := openStore(t, root)
	a := s.List()[0]
	require.NoError(t, s.BindIdentity(a.ID, "user-a", "昵称"))
	avatar := "https://sns-avatar-qc.xhscdn.com/avatar/example.webp?imageView2/2/w/360/format/webp"
	require.NoError(t, s.SetAvatar(a.ID, avatar))
	require.Equal(t, avatar, s.List()[0].Avatar)

	for _, invalid := range []string{"", "http://sns-avatar-qc.xhscdn.com/a.webp", "https://evil.example/a.webp", "javascript:alert(1)"} {
		require.NoError(t, s.SetAvatar(a.ID, invalid))
		require.Equal(t, avatar, s.List()[0].Avatar, invalid)
	}
	require.NoError(t, s.Close())
	s = openStore(t, root)
	require.Equal(t, avatar, s.List()[0].Avatar)
}
