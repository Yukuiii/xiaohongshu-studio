# macOS 后台运行红薯创作台

通过 launchd 管理工作台进程，日常使用浏览器打开 [http://127.0.0.1:18060/](http://127.0.0.1:18060/)。

## 构建与安装

在当前项目源码根目录构建，并准备用户级 LaunchAgents 目录：

```bash
go build -o xiaohongshu-studio .
mkdir -p "$HOME/Library/LaunchAgents"
cp deploy/macos/xhs-studio.plist "$HOME/Library/LaunchAgents/xhs-studio.plist"
```

编辑刚复制的 `~/Library/LaunchAgents/xhs-studio.plist`：

1. 将 `{二进制路径}` 替换为刚构建的 `xiaohongshu-studio` 的绝对路径，例如 `/Users/yourname/projects/studio/xiaohongshu-studio`。
2. 将 `{工作路径}` 替换为运行目录的绝对路径。迁移已有安装时使用原目录，保留其中的 `cookies.json` 和运行数据。首次安装允许没有 cookie，启动后在页面扫码。
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
</dict>
```

令牌在工作台页面填写；`COOKIES_PATH` 使用已有登录文件的绝对路径。不需要自定义时可省略这个配置块。

如果曾安装旧后台任务，先按原任务配置卸载它，避免两个进程占用同一端口或使用同一账号。然后加载并启动新任务：

```bash
launchctl bootstrap "gui/$(id -u)" "$HOME/Library/LaunchAgents/xhs-studio.plist"
launchctl start xhs-studio
```

打开工作台，在账号弹窗点击「打开登录浏览器」，切换至本机弹出的 Chromium 完成扫码和安全验证，再回工作台创作。源码后台服务使用本机窗口；需要页面内嵌 noVNC 时改用 [Docker 单应用部署](../../docker/README.md)，不另外启动桌面容器。首次启动若无浏览器缓存会先下载，已有缓存沿用原存储位置。

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

修改 plist 后先卸载，再重新执行 `bootstrap` 和 `start`。更新源码后先停止服务，在项目根目录重新执行 `go build -o xiaohongshu-studio .`，再启动；保持工作目录和数据文件不变。

## fish 快捷命令

从项目根目录将助手脚本安装到 fish 的配置片段目录：

```fish
mkdir -p "$HOME/.config/fish/conf.d"
cp deploy/macos/xhs-studio.fish "$HOME/.config/fish/conf.d/xhs-studio.fish"
source "$HOME/.config/fish/conf.d/xhs-studio.fish"
```

随后可使用 `xhs_studio_start`、`xhs_studio_stop` 和 `xhs_studio_status`，它们直接调用 launchctl，不依赖个人 shell 函数。任务仍需先按上述步骤安装并加载。
