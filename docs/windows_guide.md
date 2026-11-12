# 红薯创作台 Windows 安装指南

本指南从当前仓库源码启动 Vite + React Web 创作台。先用 Node.js 24 构建页面，再嵌入 Go 程序；编译后的程序运行时无需 Node。历史上游发行包不保证包含当前工作台。

## 1. 准备 Go、Node.js 和源码

在 Windows 终端打开 PowerShell，安装 Go 与 Node.js 24（若已有其他 Node 版本，请切换至 24）：

```powershell
winget install GoLang.Go
```

Go 安装后重新打开终端，执行 `go version`。当前 [go.mod](../go.mod) 要求 Go **1.27.4**；如果版本不足，请先更新 Go。

从 [Node.js 官方下载页](https://nodejs.org/en/download) 选择 **24.x** 的 Windows 安装包。安装后重新打开 PowerShell，执行 `node --version` 和 `npm --version`，确认 Node 为 **24.x**。

从当前仓库页面下载源码 ZIP 并解压，或使用已有的当前源码副本。在包含 `go.mod` 的文件夹右键打开终端。不要使用上游历史二进制替代本次源码构建。

## 2. 启动并打开页面

```powershell
npm --prefix web ci
npm --prefix web run build
go run .
```

保持终端运行，在浏览器打开 **http://localhost:18060/**。首次启动会先下载并安装内置浏览器，完成后才开始提供页面；请等待终端提示。

也可以先构建再启动（前端变更后需重新生成 `web/dist/`）：

```powershell
npm --prefix web ci
npm --prefix web run build
go build -o xiaohongshu-studio.exe .
.\xiaohongshu-studio.exe
```

Go 编译必须能找到 `web/dist/index.html` 与 `web/dist/assets/`，缺少产物时会失败；不要删除嵌入声明或跳过缺失资源。Go 程序启动后只需分发可执行文件，无需同时分发源码、`node_modules` 或 Node。

前端开发时，先按上面命令构建一次，再在一个 PowerShell 终端运行 `go run .`，另一个运行 `npm --prefix web run dev`，打开 **http://localhost:5173/**。Vite 将 `/api`、`/desktop`（含 WebSocket）和 `/health` 代理到 Go 的 `http://127.0.0.1:18060`；生产页面仍使用 `18060`。

如需设置服务访问令牌，在启动程序前执行：

```powershell
$env:AUTH_TOKEN = "替换为自己的服务访问令牌"
.\xiaohongshu-studio.exe
```

之后在页面设置里填写同一令牌。它与模型服务的 API Key 不同。仅在当前 PowerShell 窗口设置的环境变量不会自动应用到其他终端。

## 3. 配置、扫码和创作

1. 打开「设置」，填写文字模型的 OpenAI 兼容接口地址、API Key 和模型名称。地址应包含服务商要求的版本路径，例如 `https://api.openai.com/v1`。
2. 如服务启用了访问令牌，填写对应令牌并保存设置。
3. 进入左侧「账号」，点击「添加账号」直接打开真实小红书扫码页面。源码运行时切换至本机弹出的 Chromium，扫码并完成手机确认、安全验证。需要在工作台内嵌浏览器时，使用默认 [Docker 单应用部署](../docker/README.md)，无需额外桌面容器。
4. 返回左侧「工作台」，填写选题、搜索关键词和写作要求，搜索素材并生成草稿。
5. 编辑标题、正文与话题，上传图片；需要 AI 配图时单独配置生图服务。
6. 检查预览、图片顺序和可见范围，在发布窗口再次确认。

完整使用说明、图片限制和数据保存位置见[首页](../README.md)。左侧「工作台」「账号」「设置」分别用于创作、扫码登录与模型配置。升级保留原 localStorage 配置与草稿格式；使用原访问地址和浏览器即可继续读取，`5173` 与 `18060` 的存储相互独立。登录账号仍由同一服务的使用者共享，保留原 `cookies.json` 或 `COOKIES_PATH` 指定的文件。

## 常见问题

- **找不到 go 命令：** 安装完成后关闭并重新打开终端，再检查 `go version`。
- **页面无法打开：** 确认服务没有退出，检查终端报错及端口；默认端口为 18060。
- **浏览器下载失败：** 查看终端中的下载错误，确认网络可用后再启动。
- **程序被 Windows 安全中心拦截：** 查看保护历史中的具体文件和原因，确认使用当前源码构建的程序；不要关闭整机防护或排除整个临时目录。
- **扫码未成功：** 查看真实登录浏览器中的错误或验证弹窗，按提示用已登录该账号的手机小红书完成验证。二维码过期时在小红书页面内刷新；登录浏览器会话可保留 20 分钟。

开发和维护接口见 [HTTP API 文档](./API.md)。
