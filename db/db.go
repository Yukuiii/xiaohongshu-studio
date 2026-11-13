// Package db 打开元数据 SQLite 并执行建表语句。
package db

import (
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

const schemaVersion = 1

// Open 创建目录、打开 SQLite 并应用 schema；同一数据库只保留一个连接。
func Open(path string) (*sql.DB, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("数据库路径不能为空")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("解析数据库路径: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
		return nil, fmt.Errorf("创建数据库目录: %w", err)
	}

	dsn := url.URL{Scheme: "file", Path: absolute}
	dsn.RawQuery = strings.Join([]string{
		"_pragma=busy_timeout(5000)",
		"_pragma=foreign_keys(1)",
		"_pragma=journal_mode(WAL)",
		"_pragma=synchronous(NORMAL)",
	}, "&")
	database, err := sql.Open("sqlite", dsn.String())
	if err != nil {
		return nil, fmt.Errorf("打开数据库: %w", err)
	}
	database.SetMaxOpenConns(1)
	if err := database.Ping(); err != nil {
		_ = database.Close()
		return nil, fmt.Errorf("连接数据库: %w", err)
	}
	if err := applySchema(database); err != nil {
		_ = database.Close()
		return nil, err
	}
	if err := tightenPermissions(absolute); err != nil {
		_ = database.Close()
		return nil, err
	}
	return database, nil
}

func applySchema(database *sql.DB) error {
	var version int
	if err := database.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return fmt.Errorf("读取数据库版本: %w", err)
	}
	if version > schemaVersion {
		return fmt.Errorf("数据库版本 %d 高于当前程序支持的 %d", version, schemaVersion)
	}
	if version == schemaVersion {
		return nil
	}
	for _, statement := range schemaStatements() {
		if _, err := database.Exec(statement); err != nil {
			return fmt.Errorf("执行建表语句: %w", err)
		}
	}
	if _, err := database.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, schemaVersion)); err != nil {
		return fmt.Errorf("记录数据库版本: %w", err)
	}
	return nil
}

// schemaStatements 去掉注释行后按分号拆分；schema 中没有触发器和存储过程。
func schemaStatements() []string {
	lines := strings.Split(schemaSQL, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}
		kept = append(kept, line)
	}
	var statements []string
	for _, statement := range strings.Split(strings.Join(kept, "\n"), ";") {
		if statement = strings.TrimSpace(statement); statement != "" {
			statements = append(statements, statement)
		}
	}
	return statements
}

func tightenPermissions(path string) error {
	for _, name := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.Chmod(name, 0o600); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("收紧数据库文件权限: %w", err)
		}
	}
	return nil
}
