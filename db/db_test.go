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

	for _, table := range []string{"accounts", "settings", "drafts", "publish_protection", "publish_tasks", "scheduled_jobs", "schedule_runs"} {
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

func TestUpgradeVersionOnePreservesWorkspace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "studio.db")
	legacy, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	legacy.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = legacy.Close() })
	_, err = legacy.Exec(`
		CREATE TABLE accounts (
		 id TEXT PRIMARY KEY, name TEXT NOT NULL, user_id TEXT NOT NULL DEFAULT '',
		 nickname TEXT NOT NULL DEFAULT '', avatar TEXT NOT NULL DEFAULT '',
		 is_logged_in INTEGER NOT NULL DEFAULT 0 CHECK (is_logged_in IN (0, 1)),
		 login_checked_at INTEGER, seed INTEGER NOT NULL, active_profile TEXT NOT NULL DEFAULT '',
		 created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL,
		 deleted INTEGER NOT NULL DEFAULT 0 CHECK (deleted IN (0, 1))
		);
		CREATE TABLE settings (
		 id INTEGER PRIMARY KEY CHECK (id = 1), initialized INTEGER NOT NULL DEFAULT 0,
		 text_base_url TEXT NOT NULL DEFAULT '', text_model TEXT NOT NULL DEFAULT '',
		 text_api_key TEXT NOT NULL DEFAULT '', image_base_url TEXT NOT NULL DEFAULT '',
		 image_model TEXT NOT NULL DEFAULT '', image_api_key TEXT NOT NULL DEFAULT '', updated_at INTEGER NOT NULL
		);
		CREATE TABLE drafts (
		 account_id TEXT PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
		 payload TEXT NOT NULL, revision INTEGER NOT NULL DEFAULT 0 CHECK (revision >= 0), updated_at INTEGER NOT NULL
		);
		CREATE TABLE publish_protection (
		 account_id TEXT PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
		 revision INTEGER NOT NULL DEFAULT 0 CHECK (revision >= 0),
		 uncertain INTEGER NOT NULL DEFAULT 0 CHECK (uncertain IN (0, 1)),
		 last_published TEXT NOT NULL DEFAULT '', reviewed_revision INTEGER, updated_at INTEGER NOT NULL
		);
		INSERT INTO accounts (id, name, user_id, is_logged_in, seed, active_profile, created_at, updated_at)
		 VALUES ('legacy', '迁移账号', 'legacy-user', 1, 123, 'profile', 1000, 2000);
		INSERT INTO settings (id, initialized, text_model, image_model, updated_at) VALUES (1, 1, 'text-v1', 'image-v1', 2000);
		INSERT INTO drafts (account_id, payload, revision, updated_at) VALUES ('legacy', '{"title":"旧草稿"}', 7, 2000);
		INSERT INTO publish_protection (account_id, revision, uncertain, last_published, reviewed_revision, updated_at)
		 VALUES ('legacy', 9, 1, '{"title":"已发布"}', 8, 2000);
		PRAGMA user_version = 1;
	`)
	require.NoError(t, err)
	require.NoError(t, legacy.Close())

	database, err := Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, database.Close()) })
	var version, loggedIn, seed int
	var name, userID, profile, payload, textModel, imageModel, lastPublished string
	var draftRevision, protectionRevision, reviewed, uncertain, updated int64
	require.NoError(t, database.QueryRow(`PRAGMA user_version`).Scan(&version))
	require.Equal(t, schemaVersion, version)
	require.NoError(t, database.QueryRow(`SELECT name, user_id, is_logged_in, seed, active_profile FROM accounts WHERE id = 'legacy'`).Scan(&name, &userID, &loggedIn, &seed, &profile))
	require.Equal(t, "迁移账号", name)
	require.Equal(t, "legacy-user", userID)
	require.Equal(t, 1, loggedIn)
	require.Equal(t, 123, seed)
	require.Equal(t, "profile", profile)
	require.NoError(t, database.QueryRow(`SELECT text_model, image_model FROM settings WHERE id = 1`).Scan(&textModel, &imageModel))
	require.Equal(t, "text-v1", textModel)
	require.Equal(t, "image-v1", imageModel)
	require.NoError(t, database.QueryRow(`SELECT payload, revision, updated_at FROM drafts WHERE account_id = 'legacy'`).Scan(&payload, &draftRevision, &updated))
	require.JSONEq(t, `{"title":"旧草稿"}`, payload)
	require.Equal(t, int64(7), draftRevision)
	require.Equal(t, int64(2000), updated)
	require.NoError(t, database.QueryRow(`SELECT revision, uncertain, last_published, reviewed_revision, updated_at FROM publish_protection WHERE account_id = 'legacy'`).Scan(&protectionRevision, &uncertain, &lastPublished, &reviewed, &updated))
	require.Equal(t, int64(9), protectionRevision)
	require.Equal(t, int64(1), uncertain)
	require.Equal(t, int64(8), reviewed)
	require.Equal(t, int64(2000), updated)
	require.JSONEq(t, `{"title":"已发布"}`, lastPublished)
	_, err = database.Exec(`INSERT INTO publish_tasks
	 (id, account_id, account_name, user_id, kind, title, payload, fingerprint, request_id, protection_revision, status, created_at, updated_at)
	 VALUES ('task', 'legacy', '迁移账号', 'legacy-user', 'publish', '新任务', '{}', '{}', 'request', 10, 'queued', 3000, 3000)`)
	require.NoError(t, err, "旧库升级后可实际写入发布任务")
}

func TestPublishTaskUniqueConstraints(t *testing.T) {
	database := openTestDB(t)
	for _, id := range []string{"a", "b"} {
		_, err := database.Exec(`INSERT INTO accounts (id, name, seed, created_at, updated_at) VALUES (?, ?, 1, 1, 1)`, id, id)
		require.NoError(t, err)
	}
	insert := func(id, accountID, requestID, status string) error {
		_, err := database.Exec(`INSERT INTO publish_tasks
		 (id, account_id, account_name, user_id, kind, title, payload, fingerprint, request_id, protection_revision, status, created_at, updated_at)
		 VALUES (?, ?, '账号', 'user', 'publish', '标题', '{}', '{}', ?, 1, ?, 1, 1)`, id, accountID, requestID, status)
		return err
	}
	require.NoError(t, insert("first", "a", "request", "queued"))
	require.Error(t, insert("active-conflict", "a", "other", "running"))
	require.NoError(t, insert("other-account", "b", "request", "queued"))
	_, err := database.Exec(`UPDATE publish_tasks SET status = 'succeeded' WHERE id = 'first'`)
	require.NoError(t, err)
	require.Error(t, insert("request-conflict", "a", "request", "failed"))
	require.NoError(t, insert("next", "a", "next-request", "queued"))
	_, err = database.Exec(`DELETE FROM accounts WHERE id = 'a'`)
	require.Error(t, err, "有历史任务的账号只能保留墓碑")
}

func versionTwoDatabase(t *testing.T, path string) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	database.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = database.Close() })
	legacySchema, _, found := strings.Cut(schemaSQL, "-- schema version 3")
	require.True(t, found)
	_, err = database.Exec(legacySchema)
	require.NoError(t, err)
	_, err = database.Exec(`PRAGMA user_version = 2`)
	require.NoError(t, err)
	return database
}

func TestUpgradeVersionTwoPreservesPublishHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "studio.db")
	legacy := versionTwoDatabase(t, path)
	_, err := legacy.Exec(`
	 INSERT INTO accounts (id, name, user_id, seed, created_at, updated_at)
	 VALUES ('existing', '已有账号', 'existing-user', 9, 1000, 2000);
	 INSERT INTO settings (id, initialized, text_model, text_api_key, updated_at)
	 VALUES (1, 1, 'saved-model', 'saved-key', 2000);
	 INSERT INTO drafts (account_id, payload, revision, updated_at)
	 VALUES ('existing', '{"title":"已有草稿"}', 4, 2000);
	 INSERT INTO publish_protection (account_id, revision, uncertain, updated_at)
	 VALUES ('existing', 7, 1, 2000);
	 INSERT INTO publish_tasks
	 (id, account_id, account_name, user_id, kind, title, payload, fingerprint, request_id, protection_revision, status, created_at, updated_at)
	 VALUES ('existing-task', 'existing', '已有账号', 'existing-user', 'publish', '已有任务', '{"content":"正文"}', '{}', 'request', 7, 'running', 1000, 2000);
	`)
	require.NoError(t, err)
	require.NoError(t, legacy.Close())
	database, err := Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, database.Close()) })
	var version, revision, uncertain int
	var name, userID, model, key, payload, status string
	require.NoError(t, database.QueryRow(`PRAGMA user_version`).Scan(&version))
	require.Equal(t, 3, version)
	require.NoError(t, database.QueryRow(`SELECT name, user_id FROM accounts WHERE id = 'existing'`).Scan(&name, &userID))
	require.Equal(t, "已有账号", name)
	require.Equal(t, "existing-user", userID)
	require.NoError(t, database.QueryRow(`SELECT text_model, text_api_key FROM settings WHERE id = 1`).Scan(&model, &key))
	require.Equal(t, "saved-model", model)
	require.Equal(t, "saved-key", key)
	require.NoError(t, database.QueryRow(`SELECT payload, revision FROM drafts WHERE account_id = 'existing'`).Scan(&payload, &revision))
	require.JSONEq(t, `{"title":"已有草稿"}`, payload)
	require.Equal(t, 4, revision)
	require.NoError(t, database.QueryRow(`SELECT revision, uncertain FROM publish_protection WHERE account_id = 'existing'`).Scan(&revision, &uncertain))
	require.Equal(t, 7, revision)
	require.Equal(t, 1, uncertain)
	require.NoError(t, database.QueryRow(`SELECT payload, status FROM publish_tasks WHERE id = 'existing-task'`).Scan(&payload, &status))
	require.JSONEq(t, `{"content":"正文"}`, payload)
	require.Equal(t, "running", status)
	_, err = database.Exec(`INSERT INTO scheduled_jobs
	 (id, name, account_id, account_name, user_id, keyword, topic, cron, timezone, enabled, next_run_at, created_at, updated_at)
	 VALUES ('new-job', '新任务', 'existing', '已有账号', 'existing-user', '关键词', '选题', '0 9 * * *', 'Asia/Shanghai', 1, 3000, 2000, 2000)`)
	require.NoError(t, err)
}

func TestUpgradeVersionTwoRollsBackTablesAndVersionOnFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "studio.db")
	legacy := versionTwoDatabase(t, path)
	_, err := legacy.Exec(`CREATE TABLE schedule_runs (id TEXT PRIMARY KEY)`)
	require.NoError(t, err)
	require.NoError(t, legacy.Close())
	database, err := Open(path)
	require.Error(t, err, "不兼容的已有表使后续索引创建失败")
	require.Nil(t, database)
	inspect, err := sql.Open("sqlite", path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, inspect.Close()) })
	var version, jobs int
	require.NoError(t, inspect.QueryRow(`PRAGMA user_version`).Scan(&version))
	require.Equal(t, 2, version)
	require.NoError(t, inspect.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name = 'scheduled_jobs'`).Scan(&jobs))
	require.Zero(t, jobs, "前面已创建的表也必须回滚")
}

func TestScheduleRunDatabaseConstraints(t *testing.T) {
	database := openTestDB(t)
	_, err := database.Exec(`
	 INSERT INTO accounts (id, name, seed, created_at, updated_at) VALUES ('account', '账号', 1, 1, 1);
	 INSERT INTO scheduled_jobs
	 (id, name, account_id, account_name, keyword, topic, cron, timezone, created_at, updated_at)
	 VALUES ('job', '定时任务', 'account', '账号', '关键词', '选题', '* * * * *', 'UTC', 1, 1);
	`)
	require.NoError(t, err)
	insert := func(id string, scheduled int, status, stage string, finished any) error {
		_, err := database.Exec(`INSERT INTO schedule_runs
		 (id, schedule_id, account_id, account_name, status, stage, scheduled_at, started_at, finished_at, snapshot)
		 VALUES (?, 'job', 'account', '账号', ?, ?, ?, 1, ?, '{}')`, id, status, stage, scheduled, finished)
		return err
	}
	require.NoError(t, insert("first", 1, "running", "searching", nil))
	require.Error(t, insert("busy", 2, "running", "searching", nil))
	require.NoError(t, insert("skipped", 2, "skipped", "finished", 2))
	require.Error(t, insert("duplicate", 2, "failed", "finished", 2))
	require.Error(t, insert("invalid-status", 3, "queued", "searching", nil))
	require.Error(t, insert("unfinished-terminal", 3, "failed", "finished", nil))
	require.Error(t, insert("invalid-stage", 3, "failed", "searching", 3))
	_, err = database.Exec(`UPDATE schedule_runs SET status = 'succeeded', stage = 'finished', finished_at = 3 WHERE id = 'first'`)
	require.NoError(t, err)
	require.NoError(t, insert("next", 3, "running", "searching", nil))
	_, err = database.Exec(`UPDATE schedule_runs SET sources = '{}' WHERE id = 'next'`)
	require.Error(t, err)
	_, err = database.Exec(`UPDATE schedule_runs SET status = 'publishing', stage = 'publishing' WHERE id = 'next'`)
	require.Error(t, err)
	_, err = database.Exec(`DELETE FROM scheduled_jobs WHERE id = 'job'`)
	require.Error(t, err, "运行记录要求定时配置保留墓碑")
	_, err = database.Exec(`DELETE FROM accounts WHERE id = 'account'`)
	require.Error(t, err, "运行记录要求账号保留墓碑")
}

func TestScheduleRetryDatabaseConstraints(t *testing.T) {
	database := openTestDB(t)
	_, err := database.Exec(`
	 INSERT INTO accounts (id, name, seed, created_at, updated_at) VALUES ('a', '账号 A', 1, 1, 1), ('b', '账号 B', 2, 1, 1);
	 INSERT INTO scheduled_jobs (id, name, account_id, account_name, keyword, topic, cron, timezone, created_at, updated_at)
	 VALUES ('job', '定时任务', 'a', '账号 A', '关键词', '选题', '* * * * *', 'UTC', 1, 1);
	 INSERT INTO schedule_runs (id, schedule_id, account_id, account_name, status, stage, scheduled_at, started_at, finished_at, snapshot)
	 VALUES ('source', 'job', 'a', '账号 A', 'failed', 'finished', 1, 1, 2, '{}');
	 INSERT INTO schedule_runs (id, schedule_id, account_id, account_name, status, stage, scheduled_at, started_at, finished_at, snapshot, retry_of_id)
	 VALUES ('retry', 'job', 'a', '账号 A', 'failed', 'finished', 1, 1, 2, '{}', 'source');
	`)
	require.NoError(t, err, "重试与普通时点可以使用相同 scheduled_at")
	_, err = database.Exec(`INSERT INTO schedule_runs
	 (id, schedule_id, account_id, account_name, status, stage, scheduled_at, started_at, finished_at, snapshot, retry_of_id)
	 VALUES ('duplicate-retry', 'job', 'a', '账号 A', 'failed', 'finished', 3, 3, 4, '{}', 'source')`)
	require.Error(t, err, "同一来源最多产生一个重试")
	_, err = database.Exec(`INSERT INTO schedule_runs
	 (id, schedule_id, account_id, account_name, status, stage, scheduled_at, started_at, snapshot, retry_of_id)
	 VALUES ('active-retry', 'job', 'a', '账号 A', 'running', 'imaging', 1, 1, '{}', 'retry')`)
	require.NoError(t, err, "重试链按来源分别防重")
	_, err = database.Exec(`INSERT INTO schedule_runs
	 (id, schedule_id, account_id, account_name, status, stage, scheduled_at, started_at, snapshot)
	 VALUES ('changed-account', 'job', 'b', '账号 B', 'running', 'searching', 5, 5, '{}')`)
	require.Error(t, err, "更换账号不能绕过同计划活动租约")
	_, err = database.Exec(`UPDATE schedule_runs SET images = '{}' WHERE id = 'active-retry'`)
	require.Error(t, err)
	_, err = database.Exec(`UPDATE scheduled_jobs SET publication_type = 'video' WHERE id = 'job'`)
	require.Error(t, err)
}
