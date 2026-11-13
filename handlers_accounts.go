package main

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/xpzouying/xiaohongshu-mcp/accounts"
	"github.com/xpzouying/xiaohongshu-mcp/pkg/downloader"
)

const accountContextKey = "xhs-account"

func requestAccount(c *gin.Context) *accountRuntime {
	value, _ := c.Get(accountContextKey)
	r, _ := value.(*accountRuntime)
	return r
}

func requestAccountID(c *gin.Context) string {
	if r := requestAccount(c); r != nil {
		return r.id
	}
	return ""
}

func (s *XiaohongshuService) accountID() string {
	if s.account != nil {
		return s.account.id
	}
	return ""
}

func (s *AppServer) service(c *gin.Context) *XiaohongshuService {
	if r := requestAccount(c); r != nil {
		return r.service
	}
	return s.xiaohongshuService
}

func (s *AppServer) accountMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if s.accounts == nil {
			c.Next()
			return
		}
		r, err := s.accounts.resolve(strings.TrimSpace(c.GetHeader(accountHeader)))
		if err != nil {
			respondError(c, http.StatusInternalServerError, "ACCOUNT_REQUEST_FAILED", "无法选择账号", err)
			c.Abort()
			return
		}
		readImage := c.Request.Method == http.MethodGet && strings.HasPrefix(c.Request.URL.Path, "/api/v1/images/")
		release, err := r.lease(!readImage)
		if err != nil {
			respondError(c, http.StatusInternalServerError, "ACCOUNT_REQUEST_FAILED", "账号暂不可用", err)
			c.Abort()
			return
		}
		defer release()
		c.Set(accountContextKey, r)
		c.Next()
	}
}

type accountSummary struct {
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	UserID         string     `json:"user_id,omitempty"`
	Nickname       string     `json:"nickname,omitempty"`
	IsLoggedIn     bool       `json:"is_logged_in"`
	LoginCheckedAt *time.Time `json:"login_checked_at"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	BrowserStatus  string     `json:"browser_status"`
	Busy           bool       `json:"busy"`
}

func (m *AccountManager) summary(a accounts.Account) accountSummary {
	result := accountSummary{ID: a.ID, Name: a.Name, UserID: a.UserID, Nickname: a.Nickname, IsLoggedIn: a.IsLoggedIn, LoginCheckedAt: a.LoginCheckedAt, CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt, BrowserStatus: "stopped"}
	m.mu.Lock()
	r := m.runtimes[a.ID]
	m.mu.Unlock()
	if r != nil {
		r.mu.Lock()
		result.Busy = r.browserBusy || r.writers > 0
		if r.loginID != "" {
			result.BrowserStatus = "login"
		} else if r.browser != nil && r.browser.Alive() {
			result.BrowserStatus = "running"
		}
		r.mu.Unlock()
	}
	return result
}

func (s *AppServer) requireManager(c *gin.Context) bool {
	if s.accounts != nil {
		return true
	}
	respondError(c, http.StatusServiceUnavailable, "ACCOUNTS_UNAVAILABLE", "账号管理未初始化", nil)
	return false
}

func (s *AppServer) listAccountsHandler(c *gin.Context) {
	if !s.requireManager(c) {
		return
	}
	list := s.accounts.store.List()
	result := make([]accountSummary, 0, len(list))
	for _, a := range list {
		result = append(result, s.accounts.summary(a))
	}
	s.accounts.loginMu.Lock()
	var active any
	if s.accounts.visualSession != "" {
		active = gin.H{"account_id": s.accounts.visualAccount, "session_id": s.accounts.visualSession}
	}
	s.accounts.loginMu.Unlock()
	respondSuccess(c, gin.H{"accounts": result, "legacy_account_id": s.accounts.store.LegacyAccountID(), "active_login": active}, "账号列表已读取")
}

func bindAccountName(c *gin.Context) (string, bool) {
	var req struct {
		Name string `json:"name"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "INVALID_REQUEST", "请填写有效的账号备注名", nil)
		return "", false
	}
	return strings.TrimSpace(req.Name), true
}

func (s *AppServer) createAccountHandler(c *gin.Context) {
	if !s.requireManager(c) {
		return
	}
	name, ok := bindAccountName(c)
	if !ok {
		return
	}
	a, err := s.accounts.store.Create(name)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "ACCOUNT_CREATE_FAILED", "创建账号失败", err)
		return
	}
	respondSuccess(c, gin.H{"account": s.accounts.summary(a)}, "账号已创建，请完成登录")
}

func (s *AppServer) renameAccountHandler(c *gin.Context) {
	if !s.requireManager(c) {
		return
	}
	name, ok := bindAccountName(c)
	if !ok {
		return
	}
	a, err := s.accounts.store.Rename(c.Param("id"), name)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "ACCOUNT_UPDATE_FAILED", "修改账号失败", err)
		return
	}
	respondSuccess(c, gin.H{"account": s.accounts.summary(a)}, "备注已更新")
}

func (s *AppServer) deleteAccountHandler(c *gin.Context) {
	if !s.requireManager(c) {
		return
	}
	r, err := s.accounts.runtime(c.Param("id"))
	if err == nil {
		err = r.maintenance(c.Request.Context(), true)
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "ACCOUNT_DELETE_FAILED", "移除账号失败", err)
		return
	}
	respondSuccess(c, gin.H{"id": r.id}, "账号及浏览器数据已移除")
}

func (s *AppServer) resetAccountHandler(c *gin.Context) {
	if !s.requireManager(c) {
		return
	}
	r, err := s.accounts.runtime(c.Param("id"))
	if err == nil {
		err = r.maintenance(c.Request.Context(), false)
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "ACCOUNT_RESET_FAILED", "重置浏览器数据失败", err)
		return
	}
	a, err := s.accounts.store.Get(r.id)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "ACCOUNT_RESET_FAILED", "读取账号失败", err)
		return
	}
	respondSuccess(c, gin.H{"account": s.accounts.summary(a)}, "浏览器数据已重置，草稿及素材保留")
}

// migrateLegacyImages 只向旧账号导入一次，保留源文件和草稿中的旧路径。
func (m *AccountManager) migrateLegacyImages() error {
	id := m.store.LegacyAccountID()
	if _, err := m.store.Get(id); err != nil {
		return nil
	}
	dest, err := imageStorageDirForAccount(id)
	if err != nil {
		return err
	}
	marker := filepath.Join(m.store.Root(), id, "legacy-images-imported")
	if _, err := os.Stat(marker); err == nil {
		return nil
	}
	root, err := imageStorageDir()
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		entries = nil
		err = nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.Type().IsRegular() || !workbenchImageID.MatchString(entry.Name()) {
			continue
		}
		if err := os.MkdirAll(dest, 0700); err != nil {
			return err
		}
		sourcePath := filepath.Join(root, entry.Name())
		target := filepath.Join(dest, entry.Name())
		sourceInfo, err := entry.Info()
		if err != nil {
			return err
		}
		if targetInfo, err := os.Lstat(target); err == nil && targetInfo.Mode().IsRegular() && targetInfo.Size() == sourceInfo.Size() {
			continue
		}
		if err := copyLegacyImage(sourcePath, target); err != nil {
			return err
		}
	}
	return os.WriteFile(marker, []byte("1\n"), 0600)
}

// copyLegacyImage 只有完整复制落盘后才替换最终文件，中断不会留下可用名的半张图片。
func copyLegacyImage(sourcePath, target string) error {
	source, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer source.Close()
	file, err := os.CreateTemp(filepath.Dir(target), ".image-import-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := io.Copy(file, source); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), target)
}

func pathWithin(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// canonicalPath 同时处理 macOS 临时目录别名及尚未创建的目录。
func canonicalPath(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	parent := filepath.Dir(path)
	if parent == path {
		return path
	}
	return filepath.Join(canonicalPath(parent), filepath.Base(path))
}

func wrongManagedPath(root, own, original, resolved string) bool {
	fromRoot, fromOwn := pathWithin(root, original), pathWithin(own, original)
	toRoot := pathWithin(canonicalPath(root), resolved)
	toOwn := pathWithin(canonicalPath(own), resolved)
	return (fromRoot && (!fromOwn || !toOwn)) || (toRoot && !toOwn)
}

// imagePaths 校验托管图片的归属；外部素材和 URL 保持原接口能力。
func (r *accountRuntime) imagePaths(images []string) ([]string, error) {
	base, err := imageStorageDir()
	if err != nil {
		return nil, err
	}
	own, err := imageStorageDirForAccount(r.id)
	if err != nil {
		return nil, err
	}
	result := make([]string, len(images))
	for i, value := range images {
		if downloader.IsImageURL(value) {
			result[i] = value
			continue
		}
		absolute, err := filepath.Abs(value)
		if err != nil {
			return nil, err
		}
		if canonicalPath(filepath.Dir(absolute)) == canonicalPath(base) && workbenchImageID.MatchString(filepath.Base(absolute)) && r.id == r.manager.store.LegacyAccountID() {
			absolute = filepath.Join(own, filepath.Base(absolute))
		}
		resolved := canonicalPath(absolute)
		managedRoot := r.manager.store.Root()
		ownRoot := filepath.Join(managedRoot, r.id)
		if wrongManagedPath(managedRoot, ownRoot, absolute, resolved) || wrongManagedPath(base, own, absolute, resolved) {
			return nil, &accountError{http.StatusBadRequest, "IMAGE_ACCOUNT_MISMATCH", "图片属于其他账号，请使用当前账号上传的图片"}
		}
		result[i] = absolute
	}
	return result, nil
}
