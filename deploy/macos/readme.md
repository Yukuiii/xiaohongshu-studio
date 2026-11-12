# macOS 后台运行红薯创作台

通过 launchd 管理工作台进程，日常使用浏览器打开 [http://127.0.0.1:18060/](http://127.0.0.1:18060/)。

## 构建与安装

先安装 Go（版本以 [go.mod](../../go.mod) 为准）和 Node.js 24。在当前项目源码根目录依次构建前端与 Go 程序，并准备用户级 LaunchAgents 目录：

```bash
npm --prefix web ci
npm --prefix web run build
go build -o xiaohongshu-studio .
mkdir -p "$HOME/Library/LaunchAgents"
cp deploy/macos/xhs-studio.plist "$HOME/Library/LaunchAgents/xhs-studio.plist"
```

编辑刚复制的 `~/Library/LaunchAgents/xhs-studio.plist`：

1. 将 `{二进制路径}` 替换为刚构建的 `xiaohongshu-studio` 的绝对路径，例如 `/Users/yourname/projects/studio/xiaohongshu-studio`。
2. 将 `{工作路径}` 替换为运行目录的绝对路径。迁移已有安装时使用原目录，保留 `cookies.json`、`.xhs-accounts/`（或自定义账号数据根）和运行数据。旧 Cookie 仅在数据根首次初始化时导入固定默认账号；首次安装可直接在账号页登录。
3. 可按需修改 `StandardOutPath`、`StandardErrorPath`。默认分别为 `/tmp/xhs-studio.log` 和 `/tmp/xhs-studio.err`。
4. `RunAtLoad` 默认关闭；改为 `true` 可在用户登录并加载任务时启动。`KeepAlive` 默认关闭；改为 `true` 可让进程退出后自动重启，此时停止服务应卸载任务。

launchd 不会读取终端的 shell 配置。需要令牌、代理或指定登录数据时，在 plist 的顶层 `<dict>` 内添加 `EnvironmentVariables`，例如：

```xml
<key>EnvironmentVariables</key>
<dict>
    <key>AUTH_TOKEN</key>
    <string>replace-with-your-own-secret</string>
    <key>COOKIES_PATH</key>
    <string>/Users/yourname/projects/studio/cookies.json</string>
    <key>XHS_ACCOUNTS_DIR</key>
    <string>/Users/yourname/projects/studio/.xhs-accounts</string>
</dict>
```

令牌在工作台页面填写，授权整个工作台及所有账号；账号槽位不构成访问者权限隔离。`COOKIES_PATH` 是旧版本首次导入源，`XHS_ACCOUNTS_DIR` 保存账号元数据、固定 seed、完整 Chromium profile 及下载；未设时使用旧 Cookie 路径同级的隐藏目录 `.xhs-accounts/`。模型配置共用，草稿及托管图片按账号隔离；旧草稿和图片只迁入固定默认账号一次并保留源数据、草稿结构和发布结果未知时的防重状态。备份时先停服务并保存整个账号数据根、素材目录和原访问者浏览器数据。

如果曾安装旧后台任务，先按原任务配置卸载它，避免两个进程占用同一端口或使用同一账号。然后加载并启动新任务：

```bash
launchctl bootstrap "gui/$(id -u)" "$HOME/Library/LaunchAgents/xhs-studio.plist"
launchctl start xhs-studio
```

打开工作台，在「账号」选择或添加账号，切换至本机 Chromium 完成扫码和安全验证；重新登录使用候选 profile，身份验证与正常关闭落盘后才启用。每个账号的完整浏览器数据独立保留，headless 实例空闲 10 分钟后退出；选择按本页和每个请求固定，不影响其他标签页。源码后台服务使用本机窗口；需要内嵌 noVNC 时使用 [Docker 单应用部署](../../docker/README.md)。首次启动若无浏览器缓存会先下载。

离线维护先停止或卸载 launchd 任务，避免 `KeepAlive` 自动重启；在使用与 plist 相同的 `XHS_ACCOUNTS_DIR`、`COOKIES_PATH` 的终端运行：

```bash
go run ./cmd/login -account ACCOUNT_ID
```

将 `ACCOUNT_ID` 替换为账号页或 `GET /api/v1/accounts` 返回的 ID；仅有一个有效账号时可省略 `-account`。CLI 与服务持有同一数据根进程锁，不能同时运行；错误账号或登录失败不会替换原 profile。「重置浏览器数据」保留账号身份、seed、草稿及素材；「移除账号」清理该账号数据。profile 跨机器或操作系统迁移不保证登录有效。

## 管理服务

```bash
# 启动
launchctl start xhs-studio

# 停止（默认 KeepAlive=false）
launchctl stop xhs-studio

# 查看任务状态，PID 表示正在运行
launchctl list xhs-studio

# 检查 HTTP 服务
curl http://127.0.0.1:18060/health

# 卸载任务，保留配置和数据
launchctl bootout "gui/$(id -u)" "$HOME/Library/LaunchAgents/xhs-studio.plist"
```

修改 plist 后先卸载，再重新执行 `bootstrap` 和 `start`。更新源码后先停止服务，在项目根目录依次重新构建前端与 Go 程序，再启动；保持工作目录和数据文件不变。编译后的程序运行时无需 Node.js。

```bash
npm --prefix web ci
npm --prefix web run build
go build -o xiaohongshu-studio .
```

## fish 快捷命令

从项目根目录将助手脚本安装到 fish 的配置片段目录：

```fish
mkdir -p "$HOME/.config/fish/conf.d"
cp deploy/macos/xhs-studio.fish "$HOME/.config/fish/conf.d/xhs-studio.fish"
source "$HOME/.config/fish/conf.d/xhs-studio.fish"
```

随后可使用 `xhs_studio_start`、`xhs_studio_stop` 和 `xhs_studio_status`，它们直接调用 launchctl，不依赖个人 shell 函数。任务仍需先按上述步骤安装并加载。
