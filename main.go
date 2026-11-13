package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"os"

	"github.com/sirupsen/logrus"
	"github.com/xpzouying/xiaohongshu-mcp/accounts"
	"github.com/xpzouying/xiaohongshu-mcp/browser"
	"github.com/xpzouying/xiaohongshu-mcp/configs"
	"github.com/xpzouying/xiaohongshu-mcp/datadir"
	"github.com/xpzouying/xiaohongshu-mcp/db"
)

// version 构建版本号，发布时通过 -ldflags "-X main.version=vX.Y.Z" 注入。
var version = "dev"

func defaultDBPath() string {
	return datadir.Path("studio.db")
}

// watchReaderEOF 在输入流结束或出错时触发取消，用于父进程退出后自动收尾。
func watchReaderEOF(r io.Reader, cancel context.CancelFunc) {
	_, _ = io.Copy(io.Discard, r)
	cancel()
}

func main() {
	var (
		headless   bool
		port       string
		token      string
		watchStdin bool
		printAddr  bool
	)
	flag.BoolVar(&headless, "headless", true, "是否无头模式")
	flag.StringVar(&port, "port", ":18060", "端口")
	flag.StringVar(&token, "token", "", "鉴权 Token，留空则读取 AUTH_TOKEN")
	flag.BoolVar(&watchStdin, "watch-stdin", false, "标准输入关闭时自动优雅退出（供桌面端托管）")
	flag.BoolVar(&printAddr, "print-addr", false, "监听就绪后向标准输出打印监听地址")
	flag.Parse()
	if token == "" {
		token = os.Getenv("AUTH_TOKEN")
	}

	logrus.Infof("xiaohongshu-studio version: %s", version)
	database, err := db.Open(defaultDBPath())
	if err != nil {
		logrus.Fatalf("无法初始化数据库: %v", err)
	}
	defer database.Close()
	settings, err := openSettingsStore(database)
	if err != nil {
		logrus.Fatalf("无法初始化模型配置存储: %v", err)
	}

	// 只用内置浏览器。启动时就备好，缺它直接退出，不拖到第一个请求才失败。
	binPath, err := browser.EnsureBrowser()
	if err != nil {
		logrus.Fatalf("%v", err)
	}
	logrus.Infof("using browser binary: %s", binPath)

	configs.InitHeadless(headless)
	configs.SetProxy(configs.ProxyFromEnv())
	store, err := accounts.Open(defaultAccountsRoot(), database)
	if err != nil {
		logrus.Fatalf("无法初始化账号存储: %v", err)
	}
	manager := NewAccountManager(store)
	manager.PruneProfileCaches()
	if err := manager.migrateLegacyImages(); err != nil {
		_ = manager.Close()
		logrus.Fatalf("无法迁移旧图片: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if watchStdin {
		go watchReaderEOF(os.Stdin, cancel)
	}

	var onListen func(net.Addr)
	if printAddr {
		onListen = func(addr net.Addr) {
			fmt.Fprintf(os.Stdout, "XHS_STUDIO_ADDR=%s\n", addr.String())
		}
	}

	// 账号选择按请求绑定，浏览器和 profile 由管理器持有。
	appServer := NewAccountAppServer(manager, token)
	appServer.settings = settings
	if err := appServer.StartContext(ctx, port, onListen); err != nil {
		logrus.Fatalf("failed to run server: %v", err)
	}
}
