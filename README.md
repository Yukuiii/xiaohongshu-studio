# 红薯创作台

[English](./README_EN.md) · [Windows 安装](./docs/windows_guide.md) · [Docker 部署](./docker/README.md) · [HTTP API](./docs/API.md)

在浏览器中完成小红书选题、搜索取材、写稿、配图和确认发布。启动后打开 **http://localhost:18060/**；如果已有管理员部署，请打开管理员提供的工作台地址。

前端使用 Vite + React，左侧「工作台」负责搜索、写稿、配图和发布，「账号」管理多个独立小红书账号及其浏览器数据，「设置」配置模型与服务令牌。在「账号」中添加、选择或重新登录账号，扫码使用真实网页。

登录使用完整的可视化浏览器：工作台内嵌 noVNC 展示小红书原页面，扫码、手机确认和账号安全验证均可直接看到并处理。默认 Docker 应用将桌面组件与服务运行在同一个容器，只开放 `18060`，不需要另起 noVNC 容器。部署步骤见 [Docker 部署](./docker/README.md)。

![多账号管理界面，本地验收数据](./docs/screenshots/multi-account.png)

## 启动工作台

本创作台需要从**当前仓库源码**运行或构建。上游历史发行包不代表包含本页面。安装 Go（当前 [go.mod](./go.mod) 要求 **1.27.4**）和 **Node.js 24**，在包含 go.mod 的项目目录打开终端，先构建前端：

```bash
npm --prefix web ci
npm --prefix web run build
go run .
```

保持终端运行，在浏览器打开 **http://localhost:18060/**。首次启动会安装内置浏览器，请等待终端提示。源码在 macOS/Windows 上运行时，登录会直接弹出本机 Chromium 窗口；如需在工作台页面里操作浏览器，使用上述单应用 Docker 部署。Go 将 `web/dist/` 的页面和哈希资源嵌入程序，编译后的程序和生产容器运行时无需 Node。

需要构建可执行文件时，先更新前端产物再编译 Go：

```bash
npm --prefix web ci
npm --prefix web run build
go build -o xiaohongshu-studio .
./xiaohongshu-studio
```

Windows 具体步骤见 [Windows 安装指南](./docs/windows_guide.md)。使用容器时按 [Docker 部署说明](./docker/README.md) 从当前源码构建。

## 前端开发

先按上面的命令安装依赖并构建一次 `web/dist/`，再在两个终端分别运行：

```bash
# 终端一：Go API，默认 18060
go run .
```

```bash
# 终端二：Vite 热更新，默认 5173
npm --prefix web run dev
```

开发时访问 **http://localhost:5173/**。Vite 将 `/api`、`/desktop`（含 WebSocket）及 `/health` 代理到 `http://127.0.0.1:18060`，页面、接口与登录桌面通过同一个浏览器来源访问。`5173` 是开发服务；生产使用 Go 的 `18060`。修改前端后重新执行 `npm --prefix web run build`，再重新编译或启动 Go，才能更新内嵌页面。缺少 `web/dist/` 时 Go 编译会失败。


## 从选题到发布

1. **配置文字模型。** 打开「设置」，填写 OpenAI 兼容服务的接口地址、API Key 和模型名称。例如接口地址为 `https://api.openai.com/v1`；版本路径由服务商决定，程序不会自动补 `/v1`。可以读取模型列表，也可以手动填写名称。
2. **填写服务访问令牌。** 仅当部署者设置了 `AUTH_TOKEN` 时需要填写。它用于访问工作台服务，与模型 API Key 是两项独立配置。
3. **添加、选择账号并扫码。** 在左侧「账号」管理备注名、选择账号或添加账号，在对应账号打开真实小红书登录页面。扫码并按手机提示确认；出现安全验证时，按页面要求用已登录该账号的小红书 App 完成验证。确认真实身份与绑定账号一致、浏览器数据落盘后才显示保存成功；重新登录失败会保留原浏览器数据。登录桌面同一时间只显示一个账号。
4. **搜索取材并写稿。** 填写选题、搜索关键词和写作要求，开始搜索并生成草稿。工作台会搜索笔记、读取正文并交给文字模型整理。查看素材来源，调整选择后可以重新写稿。
5. **编辑和配图。** 修改标题、正文、话题和画面描述；上传本地图片，或在设置中单独填写支持 OpenAI `images/generations` 的生图服务地址、Key 和模型。未配置生图服务也能上传图片。
6. **预览并确认发布。** 核对事实、表达、图片顺序和可见范围，在发布确认窗口再次确认。生成草稿不会自动发布。

页面发布使用 Web Locks 协调同账号的多个标签页，需要支持该功能的浏览器，并通过 HTTPS 或 `localhost` / 回环地址访问。远程 HTTP 部署可使用 SSH 转发到本机后打开工作台。

标题最多 20 字（英文和数字按半字计），正文最多 1000 字。图文笔记需要 1–9 张图片，支持 JPEG、PNG、WebP、GIF，每张不超过 10 MiB。超长草稿会保留供编辑，发布前需缩短。

当前页面围绕图文创作。视频发布、评论、点赞、收藏、用户资料等现有能力可通过 [HTTP API](./docs/API.md) 使用。

## 指纹浏览器与下载源

浏览器固定为 [fingerprint-chromium 148.0.7778.215](https://github.com/adryfish/fingerprint-chromium/releases/tag/148.0.7778.215)，保留原有平台伪装、固定指纹 seed 和 Cookie 配置。默认从该项目官方 GitHub Release 下载，不再依赖原项目作者的自建 CDN。已有浏览器缓存可以继续使用。

源码启动时，可用 `XHS_BROWSER_DOWNLOAD_BASE` 指定兼容镜像，目录须保持 `<基址>/<版本>/<官方文件名>`；Docker 构建使用同名含义的 `BROWSER_DOWNLOAD_BASE` 构建参数或 `.env` 配置。所有来源都必须通过仓库固定的上游 SHA256 校验。部署示例见 [Docker 说明](./docker/README.md)。

## 草稿、账号和数据

- 配置、服务令牌和模型密钥仍保存在当前浏览器的 localStorage，共用 `hongshu-workbench.settings.v1`；每账号草稿使用 `hongshu-workbench.draft.v1:<account-id>`，保留原草稿结构；发布保护另存于 `hongshu-workbench.publish.v1:<account-id>`，普通草稿保存不会覆盖其他标签页的新发布状态。旧草稿只向固定旧账号迁入一次，不覆盖目标已有草稿，发布结果未知时的防重状态一并保留。保持原访问地址和浏览器；更换地址、浏览器或清除站点数据不会自动恢复。开发端口 `5173` 与生产端口 `18060` 的存储相互独立。
- 服务端不持久化模型配置；写稿时会把选中的素材发送给你配置的模型服务。模型配置与服务令牌由整个工作台共用；令牌授权所有账号，账号槽位不是访问者权限隔离。
- 每个账号保存独立固定 seed、下载目录及完整 Chromium user-data-dir，包括 Cookie、localStorage、IndexedDB、Service Worker、缓存和站点配置。数据根由 `XHS_ACCOUNTS_DIR` 指定，未设时取旧 Cookie 文件目录下的隐藏目录 `.xhs-accounts/`；账号元数据和 profile 位于 `<root>/<account-id>/`。按需启动浏览器、任务间复用，headless 实例空闲 10 分钟后正常退出并保留数据。会话 Cookie 由 profile 内的一次性快照接续，不自动重开旧页面；异常退出后可能需要重新登录。
- 当前选择属于本页，每个请求通过 `X-XHS-Account-ID` 固定账号；切换不影响其他标签页。工作台任务进行中禁止切换，同一账号任务串行，不同账号独立运行。可视化登录共用一个桌面窗口，登录成功只代表最近验证通过，站点会话仍可能过期。
- 首次初始化创建固定默认账号；原 `cookies.json` 或 `COOKIES_PATH` 指定的 v1/v2 文件仅用于一次迁移并保留源文件。默认账号优先保留迁移时有效的 `XHS_FP_SEED`，否则使用文件中的 seed 或独立生成；新增账号独立生成 seed。之后以完整 profile 为会话权威。移除默认账号不会再次导入旧数据，也无法补回旧版本未保存的站点数据。
- 上传和生成的图片按账号保存在服务端系统临时目录 `xiaohongshu_images/web/<account-id>/`；旧图片只迁入固定旧账号并保留源文件。草稿引用不能替代图片文件，系统清理后需重新上传。Docker 将图片持久化到 `data/uploads/web/`，账号数据保存在 `data/accounts/`；备份或迁移前停止服务并保留整个 `data/`。profile 主要适用于同一机器和运行环境，不保证跨系统会话可用。
- 「重置浏览器数据」清空站点数据，保留账号 ID、身份绑定、seed、草稿和素材；「移除账号」会删除该账号的浏览器数据、下载、托管图片和本浏览器中的草稿，不能在任务或登录进行中操作。兼容的 `DELETE /api/v1/login/cookies` 只清除所选浏览器 Cookie。

## 离线维护登录

先停止使用同一数据根的服务，再从源码目录运行以下命令，将 `ACCOUNT_ID` 替换为账号页或 `GET /api/v1/accounts` 返回的 ID：

```bash
go run ./cmd/login -account ACCOUNT_ID
```

CLI 使用与服务相同的 `XHS_ACCOUNTS_DIR`、默认目录规则和进程锁；仅有一个有效账号时可省略 `-account`，零个或多个账号必须明确选择。重新登录在候选 profile 中完成，真实身份匹配且浏览器正常关闭落盘后才启用，失败保留原 profile。

## 常见问题

**页面打不开：** 确认服务终端仍在运行，使用的是启动日志中的端口；默认地址是 http://localhost:18060/。

**提示未授权：** 在设置中填写部署者提供的服务令牌。页面公开可打开，业务接口和图片仍受令牌保护。

**模型请求失败：** 检查地址、Key 和模型名称是否属于同一服务；包含服务商需要的版本路径，生图模型需支持对应的图片接口。

**扫码后仍未登录：** 查看「账号」页中的真实浏览器画面。普通扫码后还需手机确认，部分账号会出现额外安全验证；验证未完成前不会标记为登录成功。二维码过期时在小红书原页面内刷新；未完成的会话最长保留 20 分钟，也可主动结束。登录信息保存成功后会自动关闭浏览器。

部署时让页面和 API 使用同一地址；浏览器跨站写请求会被拒绝。服务鉴权配置见 [HTTP API 文档](./docs/API.md)。

## 来源与许可证

本项目基于 [xpzouying/xiaohongshu-mcp](https://github.com/xpzouying/xiaohongshu-mcp) 的浏览器自动化与 HTTP 功能改造，当前产品名为「红薯创作台」（xiaohongshu-studio）。原作者版权为 **Copyright 2025 xpzouying**，采用 [Apache License 2.0](./LICENSE)。Go module 路径继续使用上游标识，以兼容现有内部引用。

欢迎按[贡献指南](./CONTRIBUTING.md)提交改进。

## 📄 许可证

本项目采用 [Apache License 2.0](LICENSE) 开源。

你可以自由地使用、修改和分发本项目，包括用于商业用途，只需保留原始的版权声明和许可证文件。详细条款以 [LICENSE](LICENSE) 文件为准。

向本项目提交的贡献，默认按同一许可证授权。
