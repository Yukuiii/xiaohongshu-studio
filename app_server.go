package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

// AppServer 应用服务器结构体，封装所有服务和处理器
type AppServer struct {
	accounts           *AccountManager
	xiaohongshuService *XiaohongshuService
	router             *gin.Engine
	httpServer         *http.Server
	authToken          string
	settings           *settingsStore
	desktop            *desktopGateway
}

// NewAppServer 创建新的应用服务器实例
func NewAppServer(xiaohongshuService *XiaohongshuService, authToken string) *AppServer {
	return &AppServer{
		xiaohongshuService: xiaohongshuService,
		authToken:          authToken,
		desktop:            newDesktopGateway(os.Getenv("XHS_DESKTOP_URL")),
	}
}

// NewAccountAppServer 为生产服务装配多账号管理和桌面撤销。
func NewAccountAppServer(manager *AccountManager, token string) *AppServer {
	s := NewAppServer(nil, token)
	s.accounts = manager
	s.desktop.sessionActive = manager.visualActive
	manager.onLoginEnd = s.desktop.revoke
	return s
}

// Start 启动服务器
func (s *AppServer) Start(port string) error {
	defer func() {
		if s.accounts != nil {
			if err := s.accounts.Close(); err != nil {
				logrus.Errorf("账号浏览器退出未完成: %v", err)
			}
		} else if s.xiaohongshuService != nil {
			s.xiaohongshuService.CloseLoginBrowser()
		}
	}()
	s.router = setupRoutes(s)

	s.httpServer = &http.Server{
		Addr:    port,
		Handler: s.router,
	}

	listener, err := net.Listen("tcp", port)
	if err != nil {
		return fmt.Errorf("启动 HTTP 服务失败: %w", err)
	}
	defer listener.Close()
	if s.accounts != nil {
		s.accounts.startStatusMonitor(accountStatusInterval)
	}
	serveError := make(chan error, 1)
	go func() {
		logrus.Infof("启动红薯创作台 HTTP 服务器: %s", listener.Addr())
		serveError <- s.httpServer.Serve(listener)
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(quit)
	select {
	case <-quit:
	case err := <-serveError:
		if err != nil && err != http.ErrServerClosed {
			return fmt.Errorf("HTTP 服务异常退出: %w", err)
		}
		return nil
	}

	logrus.Infof("正在关闭服务器...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := s.httpServer.Shutdown(ctx); err != nil {
		logrus.Warnf("等待连接关闭超时，强制退出: %v", err)
		_ = s.httpServer.Close()
	} else {
		logrus.Infof("服务器已优雅关闭")
	}

	return nil
}
