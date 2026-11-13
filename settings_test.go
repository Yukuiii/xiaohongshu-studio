package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/xpzouying/xiaohongshu-mcp/db"
)

func testSettingsDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "studio.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, database.Close()) })
	return database
}

func testSettingsStore(t *testing.T) *settingsStore {
	t.Helper()
	store, err := openSettingsStore(testSettingsDB(t))
	require.NoError(t, err)
	return store
}

func settingsInput(base, model, key string) settingsConfigInput {
	data, _ := json.Marshal(key)
	return settingsConfigInput{BaseURL: base, Model: model, APIKey: data}
}

func TestSettingsPersistAcrossReopen(t *testing.T) {
	store := testSettingsStore(t)
	require.False(t, store.snapshot().Initialized)
	want, err := store.update(settingsUpdate{
		Text:  settingsInput(" https://text.example/v1/chat/completions/ ", " text-model ", " text-secret "),
		Image: settingsInput("https://image.example/v1/images/generations", "image-model", "image-secret"),
	})
	require.NoError(t, err)
	require.Equal(t, "https://text.example/v1", want.Text.BaseURL)
	require.Equal(t, "text-model", want.Text.Model)
	require.Equal(t, "text-secret", want.Text.APIKey)
	reopened, err := openSettingsStore(store.db)
	require.NoError(t, err)
	require.Equal(t, want, reopened.snapshot())
}

func TestSettingsRejectInvalidExistingData(t *testing.T) {
	database := testSettingsDB(t)
	_, err := database.Exec(
		`INSERT INTO settings (id, initialized, text_base_url, text_model, text_api_key, updated_at)
		 VALUES (1, 0, 'https://host', 'text', 'file-secret', 1)`,
	)
	require.NoError(t, err)
	store, err := openSettingsStore(database)
	require.Error(t, err)
	require.Nil(t, store)
	require.NotContains(t, err.Error(), "file-secret")
}

func TestSettingsFailedSaveKeepsMemory(t *testing.T) {
	database := testSettingsDB(t)
	store, err := openSettingsStore(database)
	require.NoError(t, err)
	before, err := store.update(settingsUpdate{Text: settingsInput("https://old.example/v1", "old", "old-secret")})
	require.NoError(t, err)
	require.NoError(t, database.Close())
	_, err = store.update(settingsUpdate{Text: settingsInput("https://new.example/v1", "new", "new-secret")})
	require.ErrorIs(t, err, errSettingsUnavailable)
	require.Equal(t, before, store.snapshot())
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
	reopened, err := openSettingsStore(store.db)
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
	reopened, err := openSettingsStore(store.db)
	require.NoError(t, err)
	require.Equal(t, store.snapshot(), reopened.snapshot())

	_, err = store.clear()
	require.NoError(t, err)
	reopened, err = openSettingsStore(store.db)
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
	reopened, err := openSettingsStore(store.db)
	require.NoError(t, err)
	require.Equal(t, modelSettings{Initialized: true}, reopened.snapshot())
}

func TestSettingsServerConstructionDoesNotTouchDatabase(t *testing.T) {
	server := NewAppServer(nil, "token")
	require.Nil(t, server.settings)
}
