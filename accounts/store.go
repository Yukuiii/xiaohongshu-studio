package accounts

import (
	"crypto/rand"
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
	"github.com/xpzouying/xiaohongshu-mcp/cookies"
)

var (
	ErrNotFound          = errors.New("account not found")
	ErrInvalid           = errors.New("invalid account data")
	ErrIdentityMismatch  = errors.New("account identity mismatch")
	ErrDuplicateIdentity = errors.New("account identity already bound")
	ErrLocked            = errors.New("accounts root locked")
)

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
	LegacyImportPending bool       `json:"legacy_import_pending"`
}

type diskState struct {
	Version          int      `json:"version"`
	LegacyAccountID  string   `json:"legacy_account_id"`
	Initialized      bool     `json:"initialized"`
	InitialAccount   *Account `json:"initial_account,omitempty"`
	LegacyCookiePath string   `json:"legacy_cookie_path,omitempty"`
}

type Store struct {
	mu       sync.RWMutex
	root     string
	lock     *flock.Flock
	state    diskState
	accounts map[string]Account
	closed   bool
}

// Open 持有数据根的进程锁，直至 Close。
func Open(root, legacyCookiePath string, legacySeed int) (*Store, error) {
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("%w: empty root", ErrInvalid)
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
	s := &Store{root: root, lock: lock, accounts: make(map[string]Account)}
	if err := s.initialize(legacyCookiePath, legacySeed); err != nil {
		_ = lock.Close()
		return nil, err
	}
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

func (s *Store) LegacyAccountID() string { return s.state.LegacyAccountID }

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
	if err := s.createAccount(a, nil); err != nil {
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

// Delete 先持久化不含身份和会话信息的墓碑，再清理数据。
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
		tombstone := Account{ID: a.ID, CreatedAt: a.CreatedAt, Deleted: true}
		if err := s.save(tombstone); err != nil {
			return err
		}
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
	now := time.Now().UTC()
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
	a.UpdatedAt = time.Now().UTC()
	if err := writeJSON(filepath.Join(s.root, a.ID, "account.json"), a); err != nil {
		return err
	}
	// 元数据提交成功后才更新内存。
	s.accounts[a.ID] = a
	return nil
}

func (s *Store) initialize(legacyPath string, legacySeed int) error {
	statePath := filepath.Join(s.root, "state.json")
	data, err := readFile(statePath)
	if errors.Is(err, os.ErrNotExist) {
		entries, err := os.ReadDir(s.root)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if !strings.HasPrefix(entry.Name(), ".") {
				return fmt.Errorf("%w: state.json missing in nonempty root", ErrInvalid)
			}
		}
		var pending []byte
		if legacyPath != "" {
			legacyPath, err = filepath.Abs(legacyPath)
			if err != nil {
				return err
			}
			legacy := cookies.NewLoadCookie(legacyPath)
			pending, err = legacy.LoadCookies()
			if errors.Is(err, os.ErrNotExist) {
				legacyPath = ""
			} else if err != nil {
				return err
			} else if err := validateCookies(pending); err != nil {
				return err
			} else if legacySeed <= 0 {
				legacySeed = legacy.LoadSeed()
			}
		}
		a, err := s.newAccount("默认账号", legacySeed)
		if err != nil {
			return err
		}
		a.LegacyImportPending = legacyPath != ""
		s.state = diskState{Version: 1, LegacyAccountID: a.ID, InitialAccount: &a, LegacyCookiePath: legacyPath}
		if err := writeJSON(statePath, s.state); err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else if err := json.Unmarshal(data, &s.state); err != nil {
		return fmt.Errorf("%w: state.json: %v", ErrInvalid, err)
	}
	if s.state.Version != 1 || !validID(s.state.LegacyAccountID) {
		return fmt.Errorf("%w: state.json", ErrInvalid)
	}
	if s.state.Initialized {
		if s.state.InitialAccount != nil || s.state.LegacyCookiePath != "" {
			return fmt.Errorf("%w: inconsistent state.json", ErrInvalid)
		}
		return nil
	}
	a := s.state.InitialAccount
	if a == nil || a.ID != s.state.LegacyAccountID || a.Deleted || a.LegacyImportPending != (s.state.LegacyCookiePath != "") {
		return fmt.Errorf("%w: incomplete initialization state", ErrInvalid)
	}
	if err := validateAccount(*a, a.ID); err != nil {
		return err
	}
	accountPath := filepath.Join(s.root, a.ID)
	if _, err := os.Lstat(accountPath); errors.Is(err, os.ErrNotExist) {
		var pending []byte
		if a.LegacyImportPending {
			pending, err = cookies.NewLoadCookie(s.state.LegacyCookiePath).LoadCookies()
			if err != nil {
				return err
			}
			if err := validateCookies(pending); err != nil {
				return err
			}
		}
		if err := s.createAccount(*a, pending); err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else {
		existing, err := s.readAccount(a.ID)
		if err != nil {
			return err
		}
		if existing != *a {
			return fmt.Errorf("%w: initialization account changed", ErrInvalid)
		}
	}
	completed := diskState{Version: 1, LegacyAccountID: a.ID, Initialized: true}
	if err := writeJSON(statePath, completed); err != nil {
		return err
	}
	s.state = completed
	return nil
}

func (s *Store) load() error {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return err
	}
	identities := make(map[string]string)
	for _, entry := range entries {
		id := entry.Name()
		if strings.HasPrefix(id, ".") || id == "state.json" {
			continue
		}
		if !validID(id) {
			return fmt.Errorf("%w: unexpected root entry %q", ErrInvalid, id)
		}
		a, err := s.readAccount(id)
		if err != nil {
			return err
		}
		if a.UserID != "" {
			if _, exists := identities[a.UserID]; exists {
				return ErrDuplicateIdentity
			}
			identities[a.UserID] = a.ID
		}
		s.accounts[id] = a
		if a.Deleted {
			if err := s.cleanDeleted(id); err != nil {
				return err
			}
		}
	}
	if _, ok := s.accounts[s.state.LegacyAccountID]; !ok {
		return fmt.Errorf("%w: default account metadata missing", ErrInvalid)
	}
	return nil
}

func (s *Store) readAccount(id string) (Account, error) {
	if err := directory(filepath.Join(s.root, id)); err != nil {
		return Account{}, err
	}
	data, err := readFile(filepath.Join(s.root, id, "account.json"))
	if err != nil {
		return Account{}, fmt.Errorf("account %s metadata: %w", id, err)
	}
	var a Account
	if err := json.Unmarshal(data, &a); err != nil {
		return Account{}, fmt.Errorf("%w: account %s metadata: %v", ErrInvalid, id, err)
	}
	if err := validateAccount(a, id); err != nil {
		return Account{}, err
	}
	if !a.Deleted {
		if err := s.profileDirectory(a.ID, a.ActiveProfile); err != nil {
			return Account{}, err
		}
		if err := directory(filepath.Join(s.root, id, "downloads")); err != nil {
			return Account{}, err
		}
		if a.LegacyImportPending {
			data, err := readFile(s.pendingPath(id))
			if err != nil {
				return Account{}, err
			}
			if err := validateCookies(data); err != nil {
				return Account{}, err
			}
		}
	}
	return a, nil
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
	now := time.Now().UTC()
	return Account{ID: id, Name: name, Seed: seed, ActiveProfile: profileID, CreatedAt: now, UpdatedAt: now}, nil
}

// 临时账号目录整体提交，避免中断后留下缺失元数据的账号。
func (s *Store) createAccount(a Account, pending []byte) error {
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
	if a.LegacyImportPending {
		if err := atomicWrite(filepath.Join(stage, "pending-cookies.json"), pending); err != nil {
			return err
		}
	}
	if err := writeJSON(filepath.Join(stage, "account.json"), a); err != nil {
		return err
	}
	return os.Rename(stage, filepath.Join(s.root, a.ID))
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

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(path, data)
}

// 同目录临时文件同步后原子替换，失败时保留旧文件。
func atomicWrite(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
