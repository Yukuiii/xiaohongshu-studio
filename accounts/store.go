package accounts

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gofrs/flock"
)

var (
	ErrNotFound          = errors.New("account not found")
	ErrInvalid           = errors.New("invalid account data")
	ErrIdentityMismatch  = errors.New("account identity mismatch")
	ErrDuplicateIdentity = errors.New("account identity already bound")
	ErrLocked            = errors.New("accounts root locked")
)

// Account 是账号元数据；profiles/downloads 仍保存在数据目录。
type Account struct {
	ID                  string     `json:"id"`
	Name                string     `json:"name"`
	UserID              string     `json:"user_id"`
	Nickname            string     `json:"nickname"`
	Avatar              string     `json:"avatar,omitempty"`
	IsLoggedIn          bool       `json:"is_logged_in"`
	LoginCheckedAt      *time.Time `json:"login_checked_at"`
	Seed                int        `json:"seed"`
	ActiveProfile       string     `json:"active_profile"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
	Deleted             bool       `json:"deleted"`
	LegacyImportPending bool       `json:"-"`
}

// Store 负责账号元数据和 profile 目录，元数据保存在 SQLite。
type Store struct {
	mu       sync.RWMutex
	root     string
	lock     *flock.Flock
	db       *sql.DB
	accounts map[string]Account
	closed   bool
}

const accountColumns = `id, name, user_id, nickname, avatar, is_logged_in, login_checked_at, seed, active_profile, created_at, updated_at, deleted`

// Open 持有数据根进程锁并加载账号缓存；数据库由调用方打开和关闭。
func Open(root string, database *sql.DB) (*Store, error) {
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("%w: empty root", ErrInvalid)
	}
	if database == nil {
		return nil, fmt.Errorf("%w: nil database", ErrInvalid)
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := makeDir(root); err != nil {
		return nil, err
	}
	lockPath := filepath.Join(root, ".lock")
	if _, err := os.Lstat(lockPath); err == nil {
		if err := regularFile(lockPath); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	lock := flock.New(lockPath, flock.SetPermissions(0600))
	locked, err := lock.TryLock()
	if err != nil {
		_ = lock.Close()
		return nil, err
	}
	if !locked {
		_ = lock.Close()
		return nil, ErrLocked
	}
	s := &Store{root: root, lock: lock, db: database, accounts: make(map[string]Account)}
	if err := s.load(); err != nil {
		_ = lock.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	if err := s.lock.Close(); err != nil {
		return err
	}
	s.closed = true
	return nil
}

func (s *Store) Root() string { return s.root }

// LegacyAccountID 返回最早的存活账号，用于旧图片路径的兼容迁移；没有账号时为空。
func (s *Store) LegacyAccountID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var first Account
	for _, a := range s.accounts {
		if a.Deleted {
			continue
		}
		if first.ID == "" || a.CreatedAt.Before(first.CreatedAt) ||
			(a.CreatedAt.Equal(first.CreatedAt) && a.ID < first.ID) {
			first = a
		}
	}
	return first.ID
}

func (s *Store) List() []Account {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]Account, 0, len(s.accounts))
	for _, a := range s.accounts {
		if !a.Deleted {
			result = append(result, a)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].CreatedAt.Equal(result[j].CreatedAt) {
			return result[i].ID < result[j].ID
		}
		return result[i].CreatedAt.Before(result[j].CreatedAt)
	})
	return result
}

func (s *Store) Get(id string) (Account, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.get(id)
}

func (s *Store) get(id string) (Account, error) {
	if s.closed {
		return Account{}, os.ErrClosed
	}
	if !validID(id) {
		return Account{}, fmt.Errorf("%w: account id", ErrInvalid)
	}
	a, ok := s.accounts[id]
	if !ok || a.Deleted {
		return Account{}, ErrNotFound
	}
	return a, nil
}

func (s *Store) Create(name string) (Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Account{}, os.ErrClosed
	}
	name = strings.TrimSpace(name)
	if !validName(name) {
		return Account{}, fmt.Errorf("%w: name", ErrInvalid)
	}
	a, err := s.newAccount(name, 0)
	if err != nil {
		return Account{}, err
	}
	if err := s.createAccount(a); err != nil {
		return Account{}, err
	}
	s.accounts[a.ID] = a
	return a, nil
}

func (s *Store) Rename(id, name string) (Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, err := s.get(id)
	if err != nil {
		return Account{}, err
	}
	name = strings.TrimSpace(name)
	if !validName(name) {
		return Account{}, fmt.Errorf("%w: name", ErrInvalid)
	}
	a.Name = name
	if err := s.save(a); err != nil {
		return Account{}, err
	}
	return s.accounts[id], nil
}

// Delete 保留不含身份的墓碑，并清理 profile、草稿和发布保护。
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return os.ErrClosed
	}
	if !validID(id) {
		return fmt.Errorf("%w: account id", ErrInvalid)
	}
	a, ok := s.accounts[id]
	if !ok {
		return ErrNotFound
	}
	if !a.Deleted {
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		now := time.Now().UTC().Truncate(time.Millisecond)
		_, err = tx.Exec(
			`UPDATE accounts SET name = '', user_id = '', nickname = '', avatar = '',
			 is_logged_in = 0, login_checked_at = NULL, seed = 0, active_profile = '',
			 updated_at = ?, deleted = 1 WHERE id = ?`,
			now.UnixMilli(), id,
		)
		if err == nil {
			_, err = tx.Exec(`DELETE FROM drafts WHERE account_id = ?`, id)
		}
		if err == nil {
			_, err = tx.Exec(`DELETE FROM publish_protection WHERE account_id = ?`, id)
		}
		if err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		s.accounts[id] = Account{ID: a.ID, CreatedAt: a.CreatedAt, UpdatedAt: now, Deleted: true}
	}
	return s.cleanDeleted(id)
}

// ProfilePath 也接受使用候选 ActiveProfile 的账号快照；非法 ID 返回空路径。
func (s *Store) ProfilePath(a Account) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, err := s.get(a.ID); err != nil || !validID(a.ActiveProfile) {
		return ""
	}
	return s.profilePath(a.ID, a.ActiveProfile)
}

func (s *Store) DownloadPath(id string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, err := s.get(id); err != nil {
		return ""
	}
	return filepath.Join(s.root, id, "downloads")
}

func (s *Store) PendingCookies(id string) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a, err := s.get(id)
	if err != nil {
		return nil, err
	}
	if !a.LegacyImportPending {
		return nil, nil
	}
	if err := directory(filepath.Join(s.root, id)); err != nil {
		return nil, err
	}
	data, err := readFile(s.pendingPath(id))
	if err != nil {
		return nil, err
	}
	return data, validateCookies(data)
}

// CompleteImport 只在浏览器确认 profile 已落盘后调用。
func (s *Store) CompleteImport(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, err := s.get(id)
	if err != nil {
		return err
	}
	if a.LegacyImportPending {
		a.LegacyImportPending = false
		if err := s.save(a); err != nil {
			return err
		}
	}
	return removeFile(s.pendingPath(id))
}

func (s *Store) BindIdentity(id, userID, nickname string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, err := s.get(id)
	if err != nil {
		return err
	}
	if err := s.checkIdentity(a, userID); err != nil {
		return err
	}
	a.UserID, a.Nickname = userID, nickname
	a.setLoginStatus(true)
	return s.save(a)
}

// SetAvatar 保存账号头像地址；空值、非 HTTPS 或非小红书图床地址不覆盖已有头像。
func (s *Store) SetAvatar(id, avatar string) error {
	if !validAvatarURL(avatar) {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	a, err := s.get(id)
	if err != nil {
		return err
	}
	if a.Avatar == avatar {
		return nil
	}
	a.Avatar = avatar
	return s.save(a)
}

func validAvatarURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	for _, suffix := range []string{"xhscdn.com", "xiaohongshu.com"} {
		if host == suffix || strings.HasSuffix(host, "."+suffix) {
			return true
		}
	}
	return false
}

// SetLoginStatus 保存最近一次明确的登录检查结果。
func (s *Store) SetLoginStatus(id string, loggedIn bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, err := s.get(id)
	if err != nil {
		return err
	}
	a.setLoginStatus(loggedIn)
	return s.save(a)
}

func (a *Account) setLoginStatus(loggedIn bool) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	a.IsLoggedIn = loggedIn
	a.LoginCheckedAt = &now
}

func (s *Store) checkIdentity(a Account, userID string) error {
	if strings.TrimSpace(userID) != userID || userID == "" || !utf8.ValidString(userID) {
		return fmt.Errorf("%w: empty or invalid user id", ErrInvalid)
	}
	if a.UserID != "" && a.UserID != userID {
		return ErrIdentityMismatch
	}
	for _, other := range s.accounts {
		if !other.Deleted && other.ID != a.ID && other.UserID == userID {
			return ErrDuplicateIdentity
		}
	}
	return nil
}

func (s *Store) save(a Account) error {
	if err := directory(filepath.Join(s.root, a.ID)); err != nil {
		return err
	}
	a.UpdatedAt = time.Now().UTC().Truncate(time.Millisecond)
	result, err := s.db.Exec(
		`UPDATE accounts SET name = ?, user_id = ?, nickname = ?, avatar = ?,
		 is_logged_in = ?, login_checked_at = ?, seed = ?, active_profile = ?,
		 created_at = ?, updated_at = ?, deleted = ? WHERE id = ?`,
		a.Name, a.UserID, a.Nickname, a.Avatar, boolInt(a.IsLoggedIn), nullableTime(a.LoginCheckedAt),
		a.Seed, a.ActiveProfile, a.CreatedAt.UnixMilli(), a.UpdatedAt.UnixMilli(), boolInt(a.Deleted), a.ID,
	)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNotFound
	}
	// 数据库提交成功后才更新内存。
	s.accounts[a.ID] = a
	return nil
}

func (s *Store) load() error {
	rows, err := s.db.Query(`SELECT ` + accountColumns + ` FROM accounts ORDER BY created_at, id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	identities := make(map[string]string)
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return err
		}
		if err := validateAccount(a, a.ID); err != nil {
			return err
		}
		if a.UserID != "" {
			if _, exists := identities[a.UserID]; exists {
				return ErrDuplicateIdentity
			}
			identities[a.UserID] = a.ID
		}
		s.accounts[a.ID] = a
		if a.Deleted {
			if err := s.cleanDeleted(a.ID); err != nil {
				return err
			}
			continue
		}
		if err := s.profileDirectory(a.ID, a.ActiveProfile); err != nil {
			return err
		}
		if err := directory(filepath.Join(s.root, a.ID, "downloads")); err != nil {
			return err
		}
		if a.LegacyImportPending {
			data, err := readFile(s.pendingPath(a.ID))
			if err != nil {
				return err
			}
			if err := validateCookies(data); err != nil {
				return err
			}
		}
	}
	return rows.Err()
}

func scanAccount(scanner interface{ Scan(...any) error }) (Account, error) {
	var (
		a            Account
		loggedIn     int
		deleted      int
		loginChecked sql.NullInt64
		created      int64
		updated      int64
	)
	if err := scanner.Scan(
		&a.ID, &a.Name, &a.UserID, &a.Nickname, &a.Avatar, &loggedIn, &loginChecked,
		&a.Seed, &a.ActiveProfile, &created, &updated, &deleted,
	); err != nil {
		return Account{}, err
	}
	a.IsLoggedIn = loggedIn != 0
	a.Deleted = deleted != 0
	a.CreatedAt = time.UnixMilli(created).UTC()
	a.UpdatedAt = time.UnixMilli(updated).UTC()
	if loginChecked.Valid {
		checked := time.UnixMilli(loginChecked.Int64).UTC()
		a.LoginCheckedAt = &checked
	}
	return a, nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UnixMilli()
}

func validateAccount(a Account, id string) error {
	if a.ID != id || !validID(a.ID) || a.CreatedAt.IsZero() || a.UpdatedAt.Before(a.CreatedAt) {
		return fmt.Errorf("%w: account %s metadata", ErrInvalid, id)
	}
	if a.Deleted {
		if a.Seed != 0 || a.UserID != "" || a.Nickname != "" || a.ActiveProfile != "" || a.LegacyImportPending {
			return fmt.Errorf("%w: tombstone contains account data", ErrInvalid)
		}
	} else if !validName(a.Name) || a.Seed <= 0 || !validID(a.ActiveProfile) {
		return fmt.Errorf("%w: account %s metadata", ErrInvalid, id)
	}
	return nil
}

func (s *Store) newAccount(name string, seed int) (Account, error) {
	id, err := newID()
	if err != nil {
		return Account{}, err
	}
	profileID, err := newID()
	if err != nil {
		return Account{}, err
	}
	if seed <= 0 {
		for {
			n, err := rand.Int(rand.Reader, big.NewInt((1<<31)-1))
			if err != nil {
				return Account{}, err
			}
			seed = int(n.Int64()) + 1
			unique := true
			for _, a := range s.accounts {
				if a.Seed == seed {
					unique = false
					break
				}
			}
			if unique {
				break
			}
		}
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	return Account{ID: id, Name: name, Seed: seed, ActiveProfile: profileID, CreatedAt: now, UpdatedAt: now}, nil
}

// createAccount 先落盘目录，再写数据库；数据库失败时删除新目录。
func (s *Store) createAccount(a Account) error {
	stage, err := os.MkdirTemp(s.root, ".creating-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if err := makeDir(filepath.Join(stage, "profiles", a.ActiveProfile)); err != nil {
		return err
	}
	if err := makeDir(filepath.Join(stage, "downloads")); err != nil {
		return err
	}
	target := filepath.Join(s.root, a.ID)
	if err := os.Rename(stage, target); err != nil {
		return err
	}
	_, err = s.db.Exec(
		`INSERT INTO accounts (`+accountColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.Name, a.UserID, a.Nickname, a.Avatar, boolInt(a.IsLoggedIn), nullableTime(a.LoginCheckedAt),
		a.Seed, a.ActiveProfile, a.CreatedAt.UnixMilli(), a.UpdatedAt.UnixMilli(), boolInt(a.Deleted),
	)
	if err != nil {
		_ = os.RemoveAll(target)
		return err
	}
	return nil
}

func (s *Store) cleanDeleted(id string) error {
	if err := directory(filepath.Join(s.root, id)); err != nil {
		return err
	}
	return errors.Join(
		os.RemoveAll(filepath.Join(s.root, id, "profiles")),
		os.RemoveAll(filepath.Join(s.root, id, "downloads")),
		removeFile(s.pendingPath(id)),
	)
}

func (s *Store) profilePath(id, profileID string) string {
	return filepath.Join(s.root, id, "profiles", profileID)
}

func (s *Store) pendingPath(id string) string {
	return filepath.Join(s.root, id, "pending-cookies.json")
}

func validID(id string) bool {
	if len(id) != 32 {
		return false
	}
	for _, c := range id {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func validName(name string) bool {
	return strings.TrimSpace(name) != "" && utf8.ValidString(name) && utf8.RuneCountInString(name) <= 200
}

func newID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func validateCookies(data []byte) error {
	var entries []json.RawMessage
	if len(strings.TrimSpace(string(data))) == 0 || strings.TrimSpace(string(data))[0] != '[' {
		return fmt.Errorf("%w: cookies must be an array", ErrInvalid)
	}
	if err := json.Unmarshal(data, &entries); err != nil {
		return fmt.Errorf("%w: cookies: %v", ErrInvalid, err)
	}
	for _, entry := range entries {
		var object map[string]json.RawMessage
		if err := json.Unmarshal(entry, &object); err != nil || object == nil {
			return fmt.Errorf("%w: invalid cookie entry", ErrInvalid)
		}
	}
	return nil
}

func makeDir(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	if err := directory(path); err != nil {
		return err
	}
	return os.Chmod(path, 0700)
}

func directory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%w: not a real directory: %s", ErrInvalid, path)
	}
	return nil
}

func regularFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%w: not a regular file: %s", ErrInvalid, path)
	}
	return nil
}

func readFile(path string) ([]byte, error) {
	if err := regularFile(path); err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

func removeFile(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
