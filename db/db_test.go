package db

import (
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := Open(filepath.Join(t.TempDir(), "data", "studio.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, database.Close()) })
	return database
}

func TestOpenAppliesSchemaAndPragmas(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data", "studio.db")
	database, err := Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, database.Close()) })

	for _, table := range []string{"accounts", "settings", "drafts", "publish_protection"} {
		var count int
		require.NoError(t, database.QueryRow(
			`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table,
		).Scan(&count))
		require.Equal(t, 1, count, table)
	}
	var mode string
	require.NoError(t, database.QueryRow(`PRAGMA journal_mode`).Scan(&mode))
	require.Equal(t, "wal", strings.ToLower(mode))
	var foreignKeys int
	require.NoError(t, database.QueryRow(`PRAGMA foreign_keys`).Scan(&foreignKeys))
	require.Equal(t, 1, foreignKeys)

	var version int
	require.NoError(t, database.QueryRow(`PRAGMA user_version`).Scan(&version))
	require.Equal(t, schemaVersion, version)

	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	}
}

func TestAccountConstraintsAndCascade(t *testing.T) {
	database := openTestDB(t)
	insertAccount := func(id, userID string, deleted int) error {
		_, err := database.Exec(
			`INSERT INTO accounts (id, name, user_id, seed, active_profile, created_at, updated_at, deleted)
			 VALUES (?, ?, ?, 1, 'profile', 1, 1, ?)`,
			id, "账号"+id, userID, deleted,
		)
		return err
	}
	require.NoError(t, insertAccount("a", "user-1", 0))
	require.Error(t, insertAccount("b", "user-1", 0), "同一真实账号只能绑定一个本地账号")

	_, err := database.Exec(
		`INSERT INTO drafts (account_id, payload, revision, updated_at) VALUES ('a', '{}', 0, 1)`,
	)
	require.NoError(t, err)
	_, err = database.Exec(`DELETE FROM accounts WHERE id = 'a'`)
	require.NoError(t, err)
	var drafts int
	require.NoError(t, database.QueryRow(`SELECT count(*) FROM drafts`).Scan(&drafts))
	require.Zero(t, drafts, "删除账号时草稿应级联删除")

	require.NoError(t, insertAccount("d", "user-2", 1))
	require.NoError(t, insertAccount("e", "user-2", 0), "墓碑不占用唯一索引")
	require.Error(t, insertAccount("f", "user-2", 0))
}

func TestOpenIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data", "studio.db")
	first, err := Open(path)
	require.NoError(t, err)
	_, err = first.Exec(
		`INSERT INTO settings (id, initialized, updated_at) VALUES (1, 0, 1)`,
	)
	require.NoError(t, err)
	require.NoError(t, first.Close())

	second, err := Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, second.Close()) })
	var version, rows int
	require.NoError(t, second.QueryRow(`PRAGMA user_version`).Scan(&version))
	require.Equal(t, schemaVersion, version)
	require.NoError(t, second.QueryRow(`SELECT count(*) FROM settings`).Scan(&rows))
	require.Equal(t, 1, rows)
}
