package accounts

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func openStore(t *testing.T, root, legacyPath string, seed int) *Store {
	t.Helper()
	s, err := Open(root, legacyPath, seed)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, s.Close()) })
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
	s := openStore(t, root, "", 31415)
	require.Equal(t, root, s.Root())
	a := s.List()[0]
	require.Equal(t, s.LegacyAccountID(), a.ID)
	require.Equal(t, 31415, a.Seed)
	b, err := s.Create("账号 B")
	require.NoError(t, err)
	require.NotEqual(t, a.ID, b.ID)
	require.NotEqual(t, a.Seed, b.Seed)
	require.Positive(t, b.Seed)
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
	require.Equal(t, []Account{a, b}, s.List())
	require.Equal(t, s.List(), s.List())
	metadata := readTestFile(t, filepath.Join(root, a.ID, "account.json"))
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(metadata, &fields))
	require.Contains(t, fields, "active_profile")
	require.Contains(t, fields, "user_id")
	require.Contains(t, fields, "legacy_import_pending")
	require.NotContains(t, fields, "Seed")

	if runtime.GOOS != "windows" {
		for _, path := range []string{root, filepath.Join(root, a.ID), filepath.Join(root, a.ID, "profiles"), s.ProfilePath(a), s.DownloadPath(a.ID)} {
			info, err := os.Stat(path)
			require.NoError(t, err)
			require.Equal(t, os.FileMode(0700), info.Mode().Perm(), path)
		}
		for _, path := range []string{filepath.Join(root, "state.json"), filepath.Join(root, a.ID, "account.json"), filepath.Join(root, ".lock")} {
			info, err := os.Stat(path)
			require.NoError(t, err)
			require.Equal(t, os.FileMode(0600), info.Mode().Perm(), path)
		}
	}
	require.NoError(t, s.Close())
	s = openStore(t, root, "", 99999)
	require.Equal(t, []Account{a, b}, s.List())
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

func TestLegacyMigration(t *testing.T) {
	const raw = `[{"value":"secret","name":"web_session","expires":1.75e9}]`
	for _, test := range []struct {
		name string
		file string
		env  int
		want int
	}{
		{"v1", raw, 0, 0},
		{"v1_env", raw, 11111, 11111},
		{"v2", `{"version":2,"seed":22222,"cookies":` + raw + `}`, 0, 22222},
		{"v2_env", `{"version":2,"seed":22222,"cookies":` + raw + `}`, 11111, 11111},
	} {
		t.Run(test.name, func(t *testing.T) {
			base := t.TempDir()
			legacy := filepath.Join(base, "legacy.json")
			writeTestFile(t, legacy, []byte(test.file))
			root := filepath.Join(base, "accounts")
			s := openStore(t, root, legacy, test.env)
			a := s.List()[0]
			require.True(t, a.LegacyImportPending)
			require.Positive(t, a.Seed)
			if test.want != 0 {
				require.Equal(t, test.want, a.Seed)
			}
			pending, err := s.PendingCookies(a.ID)
			require.NoError(t, err)
			require.Equal(t, raw, string(pending))
			require.Equal(t, test.file, string(readTestFile(t, legacy)))
			if runtime.GOOS != "windows" {
				info, err := os.Stat(s.pendingPath(a.ID))
				require.NoError(t, err)
				require.Equal(t, os.FileMode(0600), info.Mode().Perm())
			}
			b, err := s.Create("B")
			require.NoError(t, err)
			require.NotEqual(t, a.Seed, b.Seed)
			require.False(t, b.LegacyImportPending)
			pending, err = s.PendingCookies(b.ID)
			require.NoError(t, err)
			require.Nil(t, pending)
			require.NoError(t, s.Close())

			// 完成导入前重启保持原待导入数据，不使用新传入的 seed。
			s = openStore(t, root, legacy, 99999)
			loaded, err := s.Get(a.ID)
			require.NoError(t, err)
			require.Equal(t, a, loaded)
			require.NoError(t, s.CompleteImport(a.ID))
			require.NoError(t, s.CompleteImport(a.ID))
			require.NoFileExists(t, s.pendingPath(a.ID))
			require.NoError(t, s.Close())
			writeTestFile(t, legacy, []byte(`corrupt source after import`))
			s = openStore(t, root, legacy, 88888)
			loaded, err = s.Get(a.ID)
			require.NoError(t, err)
			require.Equal(t, a.Seed, loaded.Seed)
			require.False(t, loaded.LegacyImportPending)
			pending, err = s.PendingCookies(a.ID)
			require.NoError(t, err)
			require.Nil(t, pending)
		})
	}
}

func TestInitializationRetryUsesFixedTarget(t *testing.T) {
	for _, stage := range []string{"planned", "staging", "account_committed"} {
		t.Run(stage, func(t *testing.T) {
			base := t.TempDir()
			legacy := filepath.Join(base, "legacy.json")
			data := []byte(`[{"name":"session","value":"original"}]`)
			writeTestFile(t, legacy, data)
			root := filepath.Join(base, "accounts")
			require.NoError(t, os.Mkdir(root, 0700))
			fixture := &Store{root: root}
			a, err := fixture.newAccount("默认账号", 12345)
			require.NoError(t, err)
			a.LegacyImportPending = true
			state := diskState{Version: 1, LegacyAccountID: a.ID, InitialAccount: &a, LegacyCookiePath: legacy}
			require.NoError(t, writeJSON(filepath.Join(root, "state.json"), state))
			if stage == "staging" {
				writeTestFile(t, filepath.Join(root, ".creating-interrupted", "account.json"), []byte(`partial`))
			}
			if stage == "account_committed" {
				require.NoError(t, fixture.createAccount(a, data))
				require.NoError(t, os.Remove(legacy))
			}
			otherSource := filepath.Join(base, "other.json")
			writeTestFile(t, otherSource, []byte(`[{"name":"session","value":"other"}]`))
			s := openStore(t, root, otherSource, 99999)
			require.Equal(t, a.ID, s.LegacyAccountID())
			require.Equal(t, []Account{a}, s.List())
			pending, err := s.PendingCookies(a.ID)
			require.NoError(t, err)
			require.Equal(t, data, pending)
			require.NoError(t, s.Close())
			s = openStore(t, root, otherSource, 99999)
			require.Equal(t, []Account{a}, s.List())
		})
	}
}

func TestStoreProcessLock(t *testing.T) {
	if root := os.Getenv("XHS_ACCOUNTS_LOCK_TEST_ROOT"); root != "" {
		s, err := Open(root, "", 0)
		if s != nil {
			_ = s.Close()
		}
		require.ErrorIs(t, err, ErrLocked)
		return
	}
	root := filepath.Join(t.TempDir(), "accounts")
	s := openStore(t, root, "", 0)
	other, err := Open(root, "", 0)
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
	openStore(t, root, "", 0)
}

func TestTombstoneNeverRevives(t *testing.T) {
	base := t.TempDir()
	legacy := filepath.Join(base, "legacy.json")
	writeTestFile(t, legacy, []byte(`[{"name":"session","value":"secret"}]`))
	root := filepath.Join(base, "accounts")
	s := openStore(t, root, legacy, 12345)
	a := s.List()[0]
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
	require.NoFileExists(t, s.pendingPath(a.ID))
	metadata := readTestFile(t, filepath.Join(root, a.ID, "account.json"))
	var tombstone Account
	require.NoError(t, json.Unmarshal(metadata, &tombstone))
	require.True(t, tombstone.Deleted)
	require.Empty(t, tombstone.UserID)
	require.Empty(t, tombstone.Nickname)
	require.Empty(t, tombstone.ActiveProfile)
	require.Zero(t, tombstone.Seed)
	require.NotContains(t, string(metadata), "secret")
	require.NotContains(t, string(metadata), "user-a")
	_, err := s.Rename(a.ID, "late callback")
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

	// 模拟墓碑落盘后、清理完成前退出；重启只补清理，不重建默认账号。
	writeTestFile(t, filepath.Join(profile, "leftover"), []byte("private"))
	writeTestFile(t, filepath.Join(downloads, "leftover"), []byte("private"))
	s = openStore(t, root, legacy, 99999)
	require.Equal(t, a.ID, s.LegacyAccountID())
	require.Empty(t, s.List())
	require.NoDirExists(t, filepath.Dir(profile))
	require.NoDirExists(t, downloads)
	_, err = s.Get(a.ID)
	require.ErrorIs(t, err, ErrNotFound)
	b, err := s.Create("B")
	require.NoError(t, err)
	require.NotEqual(t, a.ID, b.ID)
	require.False(t, b.LegacyImportPending)
	require.NoError(t, s.BindIdentity(b.ID, "user-a", "可重新绑定"))
}

func TestInvalidIDsCannotEscape(t *testing.T) {
	s := openStore(t, filepath.Join(t.TempDir(), "accounts"), "", 0)
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

func TestCorruptOrMissingMetadataFails(t *testing.T) {
	for _, scenario := range []string{
		"state_missing", "state_json", "state_version", "account_missing", "account_json", "account_fields", "profile_missing", "pending_missing", "pending_json", "extra_account_missing",
	} {
		t.Run(scenario, func(t *testing.T) {
			base := t.TempDir()
			legacy := filepath.Join(base, "legacy.json")
			writeTestFile(t, legacy, []byte(`[]`))
			root := filepath.Join(base, "accounts")
			s := openStore(t, root, legacy, 12345)
			a := s.List()[0]
			require.NoError(t, s.Close())
			switch scenario {
			case "state_missing":
				require.NoError(t, os.Remove(filepath.Join(root, "state.json")))
			case "state_json":
				writeTestFile(t, filepath.Join(root, "state.json"), []byte(`{"broken":`))
			case "state_version":
				require.NoError(t, writeJSON(filepath.Join(root, "state.json"), diskState{Version: 99, LegacyAccountID: a.ID, Initialized: true}))
			case "account_missing":
				require.NoError(t, os.Remove(filepath.Join(root, a.ID, "account.json")))
			case "account_json":
				writeTestFile(t, filepath.Join(root, a.ID, "account.json"), []byte(`{`))
			case "account_fields":
				a.Seed = 0
				require.NoError(t, writeJSON(filepath.Join(root, a.ID, "account.json"), a))
			case "profile_missing":
				require.NoError(t, os.RemoveAll(s.profilePath(a.ID, a.ActiveProfile)))
			case "pending_missing":
				require.NoError(t, os.Remove(s.pendingPath(a.ID)))
			case "pending_json":
				writeTestFile(t, s.pendingPath(a.ID), []byte(`null`))
			case "extra_account_missing":
				require.NoError(t, os.Mkdir(filepath.Join(root, strings.Repeat("0", 32)), 0700))
			}
			broken, err := Open(root, legacy, 99999)
			require.Error(t, err)
			require.Nil(t, broken)
			// 失败的 Open 必须释放进程锁。
			probe, err := Open(root, legacy, 99999)
			require.Error(t, err)
			require.False(t, errors.Is(err, ErrLocked))
			require.Nil(t, probe)
		})
	}
}

func TestInvalidLegacyCookiesFail(t *testing.T) {
	for _, data := range []string{`broken`, `null`, `{}`, `{"version":2,"cookies":{}}`, `{"version":2,"cookies":[}`, `[null]`, `[42]`} {
		t.Run(data, func(t *testing.T) {
			base := t.TempDir()
			legacy := filepath.Join(base, "legacy.json")
			writeTestFile(t, legacy, []byte(data))
			root := filepath.Join(base, "accounts")
			s, err := Open(root, legacy, 0)
			require.Nil(t, s)
			require.ErrorIs(t, err, ErrInvalid)
			require.NoFileExists(t, filepath.Join(root, "state.json"))
			require.Equal(t, data, string(readTestFile(t, legacy)))
		})
	}
}

func TestConcurrentCreatesAndIdentityBinding(t *testing.T) {
	s := openStore(t, filepath.Join(t.TempDir(), "accounts"), "", 0)
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
	seeds := map[int]bool{s.List()[0].Seed: true}
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
	require.Len(t, s.List(), count+1)
}

func TestFailedMetadataWriteKeepsMemoryAndProfiles(t *testing.T) {
	base := t.TempDir()
	legacy := filepath.Join(base, "legacy.json")
	writeTestFile(t, legacy, []byte(`[]`))
	s := openStore(t, filepath.Join(base, "accounts"), legacy, 12345)
	a := s.List()[0]
	profilePath := s.ProfilePath(a)
	writeTestFile(t, filepath.Join(profilePath, "Local State"), []byte("original"))
	candidate, err := s.NewCandidate(a.ID)
	require.NoError(t, err)
	metadataPath := filepath.Join(s.Root(), a.ID, "account.json")
	original := readTestFile(t, metadataPath)
	require.NoError(t, os.Remove(metadataPath))
	require.NoError(t, os.Mkdir(metadataPath, 0700))
	_, err = s.Rename(a.ID, "changed")
	require.Error(t, err)
	require.Error(t, s.BindIdentity(a.ID, "user-a", "A"))
	require.Error(t, s.SetLoginStatus(a.ID, true))
	require.Error(t, s.CommitProfile(a.ID, candidate, "user-a", "A"))
	require.Error(t, s.CompleteImport(a.ID))
	require.Error(t, s.Delete(a.ID))
	require.Error(t, s.ResetProfile(a.ID))
	loaded, err := s.Get(a.ID)
	require.NoError(t, err)
	require.Equal(t, a, loaded)
	require.Equal(t, "original", string(readTestFile(t, filepath.Join(profilePath, "Local State"))))
	require.FileExists(t, s.pendingPath(a.ID))
	profiles, err := os.ReadDir(filepath.Dir(profilePath))
	require.NoError(t, err)
	require.Len(t, profiles, 2)
	entries, err := os.ReadDir(filepath.Dir(metadataPath))
	require.NoError(t, err)
	for _, entry := range entries {
		require.False(t, strings.HasPrefix(entry.Name(), ".account.json-"))
	}
	require.NoError(t, os.Remove(metadataPath))
	writeTestFile(t, metadataPath, original)
	require.NoError(t, s.Close())
	s = openStore(t, s.Root(), legacy, 0)
	loaded, err = s.Get(a.ID)
	require.NoError(t, err)
	require.Equal(t, a, loaded)
}
