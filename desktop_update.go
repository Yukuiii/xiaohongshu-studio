package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Yukuiii/xiaohongshu-studio/accounts"
	"github.com/Yukuiii/xiaohongshu-studio/datadir"
	"github.com/Yukuiii/xiaohongshu-studio/internal/appupdate"
	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

const desktopUpdateExit = "XHS_STUDIO_UPDATE_EXIT"

type desktopUpdateStatus struct {
	CurrentVersion string `json:"current_version"`
	Version        string `json:"version,omitempty"`
	Phase          string `json:"phase"`
	Size           int64  `json:"size,omitempty"`
	Downloaded     int64  `json:"downloaded,omitempty"`
	Message        string `json:"message,omitempty"`
	InstallError   string `json:"install_error,omitempty"`
}

type desktopUpdater struct {
	ctx      context.Context
	cancel   context.CancelFunc
	host     string
	hostPID  int
	dataDir  string
	kind     appupdate.InstallerKind
	work     sync.WaitGroup
	bytes    atomic.Int64
	mu       sync.Mutex
	status   desktopUpdateStatus
	release  appupdate.Release
	prepared string
}

func newDesktopUpdater(parent context.Context, host, installer string) *desktopUpdater {
	if !appupdate.SupportedVersion(version) {
		return nil
	}
	kind, err := appupdate.InstalledKind(host, installer)
	if err != nil {
		logrus.WithError(err).Debug("当前桌面安装不支持自动更新")
		return nil
	}
	dataDir, err := filepath.Abs(datadir.Root)
	if err == nil {
		dataDir, err = filepath.EvalSymlinks(dataDir)
	}
	if err != nil {
		logrus.WithError(err).Warn("无法确定桌面更新数据目录")
		return nil
	}
	ctx, cancel := context.WithCancel(parent)
	u := &desktopUpdater{
		ctx: ctx, cancel: cancel, host: host, hostPID: os.Getppid(), dataDir: dataDir, kind: kind,
		status: desktopUpdateStatus{CurrentVersion: version, Phase: "current", InstallError: appupdate.LastInstallError(dataDir)},
	}
	u.check()
	u.work.Go(func() {
		ticker := time.NewTicker(6 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				u.check()
			case <-ctx.Done():
				return
			}
		}
	})
	return u
}

func (u *desktopUpdater) close() {
	u.mu.Lock()
	u.cancel()
	u.mu.Unlock()
	u.work.Wait()
	if u.prepared != "" && u.status.Phase != "installing" {
		_ = os.RemoveAll(filepath.Dir(u.prepared))
	}
}

func (u *desktopUpdater) snapshot() desktopUpdateStatus {
	u.mu.Lock()
	defer u.mu.Unlock()
	status := u.status
	if status.Phase == "downloading" || status.Phase == "ready" || status.Phase == "installing" {
		status.Downloaded = min(u.bytes.Load(), status.Size)
	}
	return status
}

func (u *desktopUpdater) check() {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.ctx.Err() != nil {
		return
	}
	switch u.status.Phase {
	case "checking", "downloading", "ready", "installing":
		return
	}
	u.status.Phase, u.status.Message = "checking", ""
	current := u.status.CurrentVersion
	u.work.Go(func() {
		ctx, cancel := context.WithTimeout(u.ctx, 15*time.Second)
		defer cancel()
		release, err := appupdate.Check(ctx, current, u.kind)
		u.mu.Lock()
		defer u.mu.Unlock()
		u.release = release
		u.status.Version, u.status.Size = release.Version, release.Size
		switch {
		case err != nil:
			u.status.Phase, u.status.Message = "error", err.Error()
		case release.Version == "":
			u.status.Phase = "current"
		default:
			u.status.Phase = "available"
		}
	})
}

func (u *desktopUpdater) download() error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.status.Phase == "downloading" {
		return nil
	}
	if u.ctx.Err() != nil || u.status.Phase != "available" {
		return &accountError{http.StatusConflict, "APP_UPDATE_NOT_AVAILABLE", "请先检查可用更新"}
	}
	u.status.Phase, u.status.Message = "downloading", ""
	u.bytes.Store(0)
	release := u.release
	u.work.Go(func() {
		ctx, cancel := context.WithTimeout(u.ctx, 30*time.Minute)
		defer cancel()
		path, err := appupdate.Download(ctx, release, filepath.Join(u.dataDir, "updates"), u.bytes.Store)
		u.mu.Lock()
		defer u.mu.Unlock()
		if err != nil {
			u.status.Phase, u.status.Message = "available", err.Error()
			return
		}
		u.prepared = path
		u.status.Phase = "ready"
	})
	return nil
}

func (u *desktopUpdater) install(ctx context.Context, store *accounts.Store) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.ctx.Err() != nil || u.status.Phase != "ready" {
		return &accountError{http.StatusConflict, "APP_UPDATE_NOT_READY", "请先下载并校验更新包"}
	}
	release, err := store.ReserveRestart(ctx)
	if errors.Is(err, accounts.ErrTaskActive) {
		return &accountError{http.StatusConflict, "APP_UPDATE_TASKS_ACTIVE", "仍有发布或定时任务正在处理，请等任务结束后再安装"}
	}
	if err != nil {
		return err
	}
	if err := appupdate.StartInstaller(u.dataDir, u.host, u.prepared, u.kind, u.release, u.hostPID); err != nil {
		release()
		return err
	}
	u.status.Phase = "installing"
	return nil
}

func (s *AppServer) registerDesktopUpdateRoutes(api *gin.RouterGroup) {
	u := s.appUpdate
	if u == nil {
		return
	}
	update := api.Group("/app-update", func(c *gin.Context) {
		// 只接受桌面工作台使用的本机监听地址，拒绝代理头与自定义 Host。
		host, _, hostErr := net.SplitHostPort(c.Request.Host)
		peer, _, peerErr := net.SplitHostPort(c.Request.RemoteAddr)
		if hostErr != nil || host != "127.0.0.1" || peerErr != nil || !net.ParseIP(peer).IsLoopback() {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		c.Header("Cache-Control", "no-store")
		c.Next()
	})
	reply := func(c *gin.Context) {
		c.JSON(http.StatusOK, SuccessResponse{Success: true, Data: u.snapshot()})
	}
	update.GET("", reply)
	update.POST("/check", func(c *gin.Context) {
		u.check()
		reply(c)
	})
	update.POST("/download", func(c *gin.Context) {
		if err := u.download(); err != nil {
			respondError(c, http.StatusInternalServerError, "APP_UPDATE_DOWNLOAD_FAILED", "无法开始下载更新", err)
			return
		}
		reply(c)
	})
	update.POST("/install", func(c *gin.Context) {
		if err := u.install(c.Request.Context(), s.accounts.store); err != nil {
			respondError(c, http.StatusInternalServerError, "APP_UPDATE_INSTALL_FAILED", "无法启动更新安装", err)
			return
		}
		reply(c)
		c.Writer.Flush()
		// 桌面宿主退出时会关闭 stdin，让服务走正常停止流程。
		fmt.Fprintln(os.Stdout, desktopUpdateExit)
	})
}
