package accounts

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCandidateCopiesCompleteProfileAndSkipsProcessFiles(t *testing.T) {
	s := openStore(t, filepath.Join(t.TempDir(), "accounts"), "", 0)
	a := s.List()[0]
	source := s.ProfilePath(a)
	files := []string{
		"Local State", "Default/Network/Cookies", "Default/Preferences",
		"Default/Local Storage/leveldb/LOG", "Default/IndexedDB/site.leveldb/LOG",
		"Default/Service Worker/Database/LOG", "Default/Cache/Cache_Data/data_0",
	}
	for _, relative := range files {
		writeTestFile(t, filepath.Join(source, relative), []byte(relative+":data"))
	}
	for _, relative := range []string{"SingletonLock", "SingletonCookie", "SingletonSocket", "DevToolsActivePort", ".org.chromium.Chromium.temporary/file"} {
		writeTestFile(t, filepath.Join(source, relative), []byte("temporary"))
	}
	candidateID, err := s.NewCandidate(a.ID)
	require.NoError(t, err)
	candidate := a
	candidate.ActiveProfile = candidateID
	target := s.ProfilePath(candidate)
	require.NotEqual(t, source, target)
	for _, relative := range files {
		require.Equal(t, relative+":data", string(readTestFile(t, filepath.Join(target, relative))))
	}
	for _, relative := range []string{"SingletonLock", "SingletonCookie", "SingletonSocket", "DevToolsActivePort", ".org.chromium.Chromium.temporary"} {
		require.NoFileExists(t, filepath.Join(target, relative))
		require.NoDirExists(t, filepath.Join(target, relative))
	}
	writeTestFile(t, filepath.Join(target, "Local State"), []byte("candidate change"))
	require.Equal(t, "Local State:data", string(readTestFile(t, filepath.Join(source, "Local State"))))
	loaded, err := s.Get(a.ID)
	require.NoError(t, err)
	require.Equal(t, a, loaded)
	require.ErrorIs(t, s.DiscardProfile(a.ID, a.ActiveProfile), ErrInvalid)
	require.NoError(t, s.DiscardProfile(a.ID, candidateID))
	require.NoError(t, s.DiscardProfile(a.ID, candidateID))
	require.NoDirExists(t, target)
	require.DirExists(t, source)
}

func TestIdentityChecksAndProfileCommit(t *testing.T) {
	root := filepath.Join(t.TempDir(), "accounts")
	s := openStore(t, root, "", 0)
	a := s.List()[0]
	b, err := s.Create("B")
	require.NoError(t, err)
	require.NoError(t, s.BindIdentity(a.ID, "user-a", "A"))
	a, err = s.Get(a.ID)
	require.NoError(t, err)
	candidateA, err := s.NewCandidate(a.ID)
	require.NoError(t, err)
	candidateB, err := s.NewCandidate(b.ID)
	require.NoError(t, err)
	beforeA := readTestFile(t, filepath.Join(root, a.ID, "account.json"))
	beforeB := readTestFile(t, filepath.Join(root, b.ID, "account.json"))
	require.ErrorIs(t, s.BindIdentity(a.ID, "wrong", "wrong"), ErrIdentityMismatch)
	require.ErrorIs(t, s.CommitProfile(a.ID, candidateA, "wrong", "wrong"), ErrIdentityMismatch)
	require.ErrorIs(t, s.BindIdentity(b.ID, "user-a", "duplicate"), ErrDuplicateIdentity)
	require.ErrorIs(t, s.CommitProfile(b.ID, candidateB, "user-a", "duplicate"), ErrDuplicateIdentity)
	require.ErrorIs(t, s.CommitProfile(a.ID, candidateA, "", "unknown"), ErrInvalid)
	require.ErrorIs(t, s.BindIdentity(a.ID, " user-a", "invalid"), ErrInvalid)
	require.Error(t, s.CommitProfile(b.ID, a.ActiveProfile, "user-b", "foreign profile"))
	require.Equal(t, beforeA, readTestFile(t, filepath.Join(root, a.ID, "account.json")))
	require.Equal(t, beforeB, readTestFile(t, filepath.Join(root, b.ID, "account.json")))
	loadedA, err := s.Get(a.ID)
	require.NoError(t, err)
	require.Equal(t, a, loadedA)
	loadedB, err := s.Get(b.ID)
	require.NoError(t, err)
	require.Equal(t, b, loadedB)

	writeTestFile(t, filepath.Join(s.profilePath(a.ID, candidateA), "Local State"), []byte("committed session"))
	require.NoError(t, s.CommitProfile(a.ID, candidateA, "user-a", "A updated"))
	require.NoError(t, s.CommitProfile(b.ID, candidateB, "user-b", "B"))
	loadedA, err = s.Get(a.ID)
	require.NoError(t, err)
	require.Equal(t, candidateA, loadedA.ActiveProfile)
	require.Equal(t, a.Seed, loadedA.Seed)
	require.Equal(t, "user-a", loadedA.UserID)
	require.Equal(t, "A updated", loadedA.Nickname)
	require.True(t, loadedA.IsLoggedIn)
	require.NotNil(t, loadedA.LoginCheckedAt)
	require.ErrorIs(t, s.DiscardProfile(a.ID, candidateA), ErrInvalid)
	require.NoError(t, s.Close())
	s = openStore(t, root, "", 0)
	restarted, err := s.Get(a.ID)
	require.NoError(t, err)
	require.Equal(t, loadedA, restarted)
	require.Equal(t, "committed session", string(readTestFile(t, filepath.Join(s.ProfilePath(restarted), "Local State"))))
}

func TestResetProfilePreservesAccountAndBinding(t *testing.T) {
	base := t.TempDir()
	legacy := filepath.Join(base, "legacy.json")
	writeTestFile(t, legacy, []byte(`[{"name":"session","value":"old"}]`))
	s := openStore(t, filepath.Join(base, "accounts"), legacy, 12345)
	a := s.List()[0]
	require.NoError(t, s.BindIdentity(a.ID, "user-a", "A"))
	a, err := s.Get(a.ID)
	require.NoError(t, err)
	oldPath := s.ProfilePath(a)
	writeTestFile(t, filepath.Join(oldPath, "Default", "private"), []byte("old session"))
	candidateID, err := s.NewCandidate(a.ID)
	require.NoError(t, err)
	b, err := s.Create("B")
	require.NoError(t, err)
	writeTestFile(t, filepath.Join(s.ProfilePath(b), "Local State"), []byte("B session"))
	require.NoError(t, s.ResetProfile(a.ID))
	reset, err := s.Get(a.ID)
	require.NoError(t, err)
	require.NotEqual(t, a.ActiveProfile, reset.ActiveProfile)
	require.Equal(t, a.ID, reset.ID)
	require.Equal(t, a.Name, reset.Name)
	require.Equal(t, a.Seed, reset.Seed)
	require.Equal(t, a.UserID, reset.UserID)
	require.Equal(t, a.Nickname, reset.Nickname)
	require.Equal(t, a.CreatedAt, reset.CreatedAt)
	require.False(t, reset.LegacyImportPending)
	require.False(t, reset.IsLoggedIn)
	require.NotNil(t, reset.LoginCheckedAt)
	entries, err := os.ReadDir(s.ProfilePath(reset))
	require.NoError(t, err)
	require.Empty(t, entries)
	require.NoDirExists(t, oldPath)
	require.NoDirExists(t, s.profilePath(a.ID, candidateID))
	require.NoFileExists(t, s.pendingPath(a.ID))
	require.ErrorIs(t, s.BindIdentity(a.ID, "other-user", "other"), ErrIdentityMismatch)
	require.Equal(t, "B session", string(readTestFile(t, filepath.Join(s.ProfilePath(b), "Local State"))))
	require.NoError(t, s.Close())
	s = openStore(t, s.Root(), legacy, 99999)
	loaded, err := s.Get(a.ID)
	require.NoError(t, err)
	require.Equal(t, reset, loaded)
}

func TestCandidateRejectsUnknownSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("创建符号链接需要额外系统权限")
	}
	s := openStore(t, filepath.Join(t.TempDir(), "accounts"), "", 0)
	a := s.List()[0]
	source := s.ProfilePath(a)
	outside := t.TempDir()
	writeTestFile(t, filepath.Join(outside, "secret"), []byte("outside"))
	require.NoError(t, os.Symlink(outside, filepath.Join(source, "escape")))
	candidate, err := s.NewCandidate(a.ID)
	require.Empty(t, candidate)
	require.ErrorIs(t, err, ErrInvalid)
	profiles, err := os.ReadDir(filepath.Dir(source))
	require.NoError(t, err)
	require.Len(t, profiles, 1)
	require.NoError(t, os.Remove(filepath.Join(source, "escape")))
	// Chromium 自己的 Singleton 链接无需读取即可跳过。
	require.NoError(t, os.Symlink(outside, filepath.Join(source, "SingletonLock")))
	candidate, err = s.NewCandidate(a.ID)
	require.NoError(t, err)
	require.NoFileExists(t, filepath.Join(s.profilePath(a.ID, candidate), "SingletonLock"))
	require.Equal(t, "outside", string(readTestFile(t, filepath.Join(outside, "secret"))))
}

func TestProfileParentSymlinkCannotEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("创建符号链接需要额外系统权限")
	}
	root := filepath.Join(t.TempDir(), "accounts")
	s := openStore(t, root, "", 0)
	a := s.List()[0]
	profiles := filepath.Join(root, a.ID, "profiles")
	require.NoError(t, os.Rename(profiles, profiles+".saved"))
	outside := t.TempDir()
	writeTestFile(t, filepath.Join(outside, a.ActiveProfile, "secret"), []byte("outside"))
	require.NoError(t, os.Symlink(outside, profiles))
	_, err := s.NewCandidate(a.ID)
	require.ErrorIs(t, err, ErrInvalid)
	require.ErrorIs(t, s.ResetProfile(a.ID), ErrInvalid)
	require.ErrorIs(t, s.DiscardProfile(a.ID, "00000000000000000000000000000000"), ErrInvalid)
	require.Equal(t, "outside", string(readTestFile(t, filepath.Join(outside, a.ActiveProfile, "secret"))))
	require.NoError(t, os.Remove(profiles))
	require.NoError(t, os.Rename(profiles+".saved", profiles))
}
