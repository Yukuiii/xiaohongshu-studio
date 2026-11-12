package main

import (
	"flag"
	"os"

	"github.com/sirupsen/logrus"
	"github.com/xpzouying/xiaohongshu-mcp/accounts"
	"github.com/xpzouying/xiaohongshu-mcp/browser"
	"github.com/xpzouying/xiaohongshu-mcp/configs"
	"github.com/xpzouying/xiaohongshu-mcp/cookies"
)

// version 构建版本号，发布时通过 -ldflags "-X main.version=vX.Y.Z" 注入。
var version = "dev"

func main() {
	var (
		headless bool
		port     string
		token    string
	)
	flag.BoolVar(&headless, "headless", true, "是否无头模式")
	flag.StringVar(&port, "port", ":18060", "端口")
	flag.StringVar(&token, "token", "", "鉴权 Token，留空则读取 AUTH_TOKEN")
	flag.Parse()
	if token == "" {
		token = os.Getenv("AUTH_TOKEN")
	}

	logrus.Infof("xiaohongshu-studio version: %s", version)

	// 只用内置浏览器。启动时就备好，缺它直接退出，不拖到第一个请求才失败。
	binPath, err := browser.EnsureBrowser()
	if err != nil {
		logrus.Fatalf("%v", err)
	}
	logrus.Infof("using browser binary: %s", binPath)

	configs.InitHeadless(headless)
	configs.SetProxy(configs.ProxyFromEnv())
	store, err := accounts.Open(defaultAccountsRoot(), cookies.GetCookiesFilePath(), configs.FingerprintSeedFromEnv())
	if err != nil {
		logrus.Fatalf("无法初始化账号存储: %v", err)
	}
	manager := NewAccountManager(store)
	if err := manager.migrateLegacyImages(); err != nil {
		_ = manager.Close()
		logrus.Fatalf("无法迁移旧图片: %v", err)
	}

	// 账号选择按请求绑定，浏览器和 profile 由管理器持有。
	appServer := NewAccountAppServer(manager, token)
	if err := appServer.Start(port); err != nil {
		logrus.Fatalf("failed to run server: %v", err)
	}
}
