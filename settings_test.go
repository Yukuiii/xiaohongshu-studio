package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func testSettingsStore(t *testing.T) *settingsStore {
	t.Helper()
	store, err := openSettingsStore(filepath.Join(t.TempDir(), "data", "settings.json"))
	require.NoError(t, err)
	return store
}

func settingsInput(base, model, key string) settingsConfigInput {
	data, _ := json.Marshal(key)
	return settingsConfigInput{BaseURL: base, Model: model, APIKey: data}
}

func TestSettingsPersistAcrossReopenWithPrivatePermissions(t *testing.T) {
	store := testSettingsStore(t)
	require.False(t, store.snapshot().Initialized)
	_, err := os.Stat(store.path)
	require.True(t, os.IsNotExist(err))
	want, err := store.update(settingsUpdate{
		Text:  settingsInput(" https://text.example/v1/chat/completions/ ", " text-model ", " text-secret "),
		Image: settingsInput("https://image.example/v1/images/generations", "image-model", "image-secret"),
	})
	require.NoError(t, err)
	require.Equal(t, "https://text.example/v1", want.Text.BaseURL)
	require.Equal(t, "text-model", want.Text.Model)
	require.Equal(t, "text-secret", want.Text.APIKey)
	info, err := os.Stat(store.path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0600), info.Mode().Perm())
	reopened, err := openSettingsStore(store.path)
	require.NoError(t, err)
	require.Equal(t, want, reopened.snapshot())
	files, err := os.ReadDir(filepath.Dir(store.path))
	require.NoError(t, err)
	require.Len(t, files, 1)
}

func TestSettingsRejectInvalidExistingFileWithoutOverwriting(t *testing.T) {
	for _, data := range []string{
		``, `null`, `{}`, `[]`, `{"initialized":true,"text":{},"image":null}`,
		`{"initialized":true,"text":{"base_url":"https://host","model":"text","api_key":"file-secret"},"image":`,
		`{"initialized":true,"text":{"base_url":"https://host/?key=file-secret","model":"text","api_key":"file-secret"},"image":{}}`,
		`{"initialized":true,"text":{"base_url":"https://host","model":"text"},"image":{}}`,
		`{"initialized":false,"text":{"base_url":"https://host","model":"text","api_key":"file-secret"},"image":{}}`,
	} {
		t.Run(data, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "settings.json")
			require.NoError(t, os.WriteFile(path, []byte(data), 0600))
			_, err := openSettingsStore(path)
			require.Error(t, err)
			require.NotContains(t, err.Error(), "file-secret")
			got, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, data, string(got))
		})
	}
}

func TestSettingsFailedSaveKeepsMemoryAndExistingData(t *testing.T) {
	for _, stage := range []string{"create", "rename"} {
		t.Run(stage, func(t *testing.T) {
			store := testSettingsStore(t)
			before, err := store.update(settingsUpdate{Text: settingsInput("https://old.example/v1", "old", "old-secret")})
			require.NoError(t, err)
			path := store.path
			bytesBefore, err := os.ReadFile(path)
			require.NoError(t, err)
			if stage == "create" {
				store.path = filepath.Join(path, "not-a-directory.json")
			} else {
				require.NoError(t, os.Rename(path, path+".backup"))
				require.NoError(t, os.Mkdir(path, 0700))
				require.NoError(t, os.WriteFile(filepath.Join(path, "blocker"), []byte("keep"), 0600))
			}
			_, err = store.update(settingsUpdate{Text: settingsInput("https://new.example/v1", "new", "new-secret")})
			require.ErrorIs(t, err, errSettingsUnavailable)
			require.Equal(t, before, store.snapshot())
			if stage == "rename" {
				blocker, err := os.ReadFile(filepath.Join(path, "blocker"))
				require.NoError(t, err)
				require.Equal(t, "keep", string(blocker))
				require.NoError(t, os.RemoveAll(path))
				require.NoError(t, os.Rename(path+".backup", path))
			}
			after, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, bytesBefore, after)
			files, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".xhs-settings-*.tmp"))
			require.NoError(t, err)
			require.Empty(t, files)
		})
	}
}

func TestSettingsKeepReplaceAndClearKey(t *testing.T) {
	store := testSettingsStore(t)
	_, err := store.update(settingsUpdate{Text: settingsInput("https://text.example/v1", "first", "old-secret")})
	require.NoError(t, err)
	kept, err := store.update(settingsUpdate{Text: settingsConfigInput{BaseURL: " https://text.example/v1/models/ ", Model: "second"}})
	require.NoError(t, err)
	require.Equal(t, "old-secret", kept.Text.APIKey)
	require.Equal(t, "second", kept.Text.Model)
	for _, input := range []settingsConfigInput{
		{BaseURL: "https://other.example/v1", Model: "new"},
		settingsInput("https://text.example/v1", "new", ""),
		{BaseURL: "https://text.example/v1", Model: "new", APIKey: json.RawMessage(`null`)},
		{BaseURL: "https://text.example/v1", Model: "new", APIKey: json.RawMessage(`123`)},
	} {
		_, err := store.update(settingsUpdate{Text: input})
		require.ErrorIs(t, err, errInvalidSettings)
		require.NotContains(t, err.Error(), "old-secret")
		require.Equal(t, kept, store.snapshot())
	}
	replaced, err := store.update(settingsUpdate{Text: settingsInput("https://new.example/v1", "new", "new-secret")})
	require.NoError(t, err)
	require.Equal(t, "new-secret", replaced.Text.APIKey)
	cleared, err := store.update(settingsUpdate{Text: settingsInput("", "", "")})
	require.NoError(t, err)
	require.True(t, cleared.Initialized)
	require.True(t, cleared.Text.empty())
	reopened, err := openSettingsStore(store.path)
	require.NoError(t, err)
	require.Equal(t, cleared, reopened.snapshot())
}

func TestSettingsConditionalMigrationIsAtomicAndClearCannotRevive(t *testing.T) {
	store := testSettingsStore(t)
	const count = 12
	start := make(chan struct{})
	results := make(chan error, count)
	var wg sync.WaitGroup
	for i := range count {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, err := store.update(settingsUpdate{
				OnlyIfUninitialized: true,
				Text:                settingsInput("https://text.example", fmt.Sprintf("model-%d", i), fmt.Sprintf("key-%d", i)),
			})
			results <- err
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		} else {
			require.ErrorIs(t, err, errSettingsInitialized)
		}
	}
	require.Equal(t, 1, winners)
	reopened, err := openSettingsStore(store.path)
	require.NoError(t, err)
	require.Equal(t, store.snapshot(), reopened.snapshot())

	_, err = store.clear()
	require.NoError(t, err)
	reopened, err = openSettingsStore(store.path)
	require.NoError(t, err)
	_, err = reopened.update(settingsUpdate{OnlyIfUninitialized: true, Text: settingsInput("https://old-browser.example", "old", "old-key")})
	require.ErrorIs(t, err, errSettingsInitialized)
	require.Equal(t, modelSettings{Initialized: true}, reopened.snapshot())
}

func TestSettingsConcurrentClearAndMigrationEndsCleared(t *testing.T) {
	store := testSettingsStore(t)
	start := make(chan struct{})
	results := make(chan error, 9)
	var wg sync.WaitGroup
	for i := range 9 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			var err error
			if i == 0 {
				_, err = store.clear()
			} else {
				_, err = store.update(settingsUpdate{OnlyIfUninitialized: true, Text: settingsInput("https://text.example", "migrated", "old-key")})
			}
			results <- err
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)
	for err := range results {
		require.True(t, err == nil || errors.Is(err, errSettingsInitialized))
	}
	reopened, err := openSettingsStore(store.path)
	require.NoError(t, err)
	require.Equal(t, modelSettings{Initialized: true}, reopened.snapshot())
}

func TestSettingsDefaultPathAndServerConstructionAreIsolated(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("COOKIES_PATH", filepath.Join(dir, "legacy", "cookies.json"))
	t.Setenv("XHS_SETTINGS_PATH", "")
	require.Equal(t, filepath.Join(dir, "legacy", ".xhs-settings.json"), defaultSettingsPath())
	path := filepath.Join(dir, "override.json")
	t.Setenv("XHS_SETTINGS_PATH", path)
	require.Equal(t, path, defaultSettingsPath())
	require.NoError(t, os.WriteFile(path, []byte("invalid existing file"), 0600))
	server := NewAppServer(nil, "token")
	require.Nil(t, server.settings)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "invalid existing file", string(data))
}
