package browser

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPruneProfileCachesKeepsSessionData(t *testing.T) {
	profile := filepath.Join(t.TempDir(), "profile")
	cacheDirs := []string{
		"Default/Cache/Cache_Data/data_0",
		"Default/Code Cache/js/index",
		"Default/GPUCache/data",
		"Default/DawnGraphiteCache/data",
		"Default/ShaderCache/data",
	}
	keepDirs := []string{
		"Default/Cookies",
		"Default/Local Storage/leveldb/LOG",
		"Default/IndexedDB/site.leveldb/LOG",
		"Default/Service Worker/Database/LOG",
		"Default/Session Storage/LOG",
		"Default/Preferences",
		"Local State",
	}
	for _, relative := range append(cacheDirs, keepDirs...) {
		path := filepath.Join(profile, relative)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
		require.NoError(t, os.WriteFile(path, []byte("data"), 0600))
	}

	require.NoError(t, PruneProfileCaches(profile))
	for _, relative := range cacheDirs {
		require.NoFileExists(t, filepath.Join(profile, relative), relative)
	}
	for _, relative := range keepDirs {
		require.FileExists(t, filepath.Join(profile, relative), relative)
	}
}

func TestPruneProfileCachesDoesNotFollowSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("创建符号链接需要额外系统权限")
	}
	profile := filepath.Join(t.TempDir(), "profile")
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret")
	require.NoError(t, os.WriteFile(secret, []byte("keep"), 0600))
	require.NoError(t, os.MkdirAll(filepath.Join(profile, "Default"), 0700))
	require.NoError(t, os.Symlink(outside, filepath.Join(profile, "Default", "Cache")))

	require.NoError(t, PruneProfileCaches(profile))
	require.NoFileExists(t, filepath.Join(profile, "Default", "Cache"))
	require.FileExists(t, secret)
}

func TestPruneProfileCachesMissingProfileIsNoop(t *testing.T) {
	require.NoError(t, PruneProfileCaches(filepath.Join(t.TempDir(), "missing")))
	require.NoError(t, PruneProfileCaches(""))
}
