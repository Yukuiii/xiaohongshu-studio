# 红薯创作台

[English](./README_EN.md) · [Windows 安装](./docs/windows_guide.md) · [Docker 部署](./docker/README.md) · [HTTP API](./docs/API.md)

在浏览器中完成小红书选题、搜索取材、写稿、配图和确认发布。启动后打开 **http://localhost:18060/**；如果已有管理员部署，请打开管理员提供的工作台地址。

前端使用 Vite + React，左侧「工作台」负责搜索、写稿、配图和发布，「账号」管理当前登录会话，「设置」配置模型与服务令牌。点击「账号」里的「添加账号」会直接打开真实网页扫码。

登录使用完整的可视化浏览器：工作台内嵌 noVNC 展示小红书原页面，扫码、手机确认和账号安全验证均可直接看到并处理。默认 Docker 应用将桌面组件与服务运行在同一个容器，只开放 `18060`，不需要另起 noVNC 容器。部署步骤见 [Docker 部署](./docker/README.md)。

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
3. **添加账号并扫码。** 进入左侧「账号」，点击「添加账号」直接打开真实小红书登录页面，在网页中扫码并按手机提示确认。出现「请通过验证」等账号安全验证时，按页面要求用已登录该账号的小红书 App 扫码验证；验证期间登录网页保持打开。成功保存登录状态后，浏览器自动关闭，页面收起登录区域并显示账号。
4. **搜索取材并写稿。** 填写选题、搜索关键词和写作要求，开始搜索并生成草稿。工作台会搜索笔记、读取正文并交给文字模型整理。查看素材来源，调整选择后可以重新写稿。
5. **编辑和配图。** 修改标题、正文、话题和画面描述；上传本地图片，或在设置中单独填写支持 OpenAI `images/generations` 的生图服务地址、Key 和模型。未配置生图服务也能上传图片。
6. **预览并确认发布。** 核对事实、表达、图片顺序和可见范围，在发布确认窗口再次确认。生成草稿不会自动发布。

标题最多 20 字（英文和数字按半字计），正文最多 1000 字。图文笔记需要 1–9 张图片，支持 JPEG、PNG、WebP、GIF，每张不超过 10 MiB。超长草稿会保留供编辑，发布前需缩短。

当前页面围绕图文创作。视频发布、评论、点赞、收藏、用户资料等现有能力可通过 [HTTP API](./docs/API.md) 使用。

## 指纹浏览器与下载源

浏览器固定为 [fingerprint-chromium 148.0.7778.215](https://github.com/adryfish/fingerprint-chromium/releases/tag/148.0.7778.215)，保留原有平台伪装、固定指纹 seed 和 Cookie 配置。默认从该项目官方 GitHub Release 下载，不再依赖原项目作者的自建 CDN。已有浏览器缓存可以继续使用。

源码启动时，可用 `XHS_BROWSER_DOWNLOAD_BASE` 指定兼容镜像，目录须保持 `<基址>/<版本>/<官方文件名>`；Docker 构建使用同名含义的 `BROWSER_DOWNLOAD_BASE` 构建参数或 `.env` 配置。所有来源都必须通过仓库固定的上游 SHA256 校验。部署示例见 [Docker 说明](./docker/README.md)。

## 草稿、账号和数据

- 配置、服务令牌、模型密钥和草稿保存在当前浏览器的 localStorage 中；React 页面继续使用原 `hongshu-workbench.settings.v1` 和 `hongshu-workbench.draft.v1` 数据结构。保留原访问地址即可继续读取已有配置和草稿；更换浏览器、地址或清除站点数据后不会自动恢复（开发端口 `5173` 与生产端口 `18060` 的存储相互独立）。在共用电脑上可通过设置清除配置，草稿会保留。
- 服务端不持久化模型配置；写稿时会把选中的素材发送给你配置的模型服务。
- 所有访问者共用服务的小红书登录账号，没有独立用户账户隔离。登录会话保存在服务端 Cookie 文件中，默认使用工作目录的 `cookies.json`，具体路径取决于部署配置。
- 上传和生成的图片保存在服务端系统临时目录的 `xiaohongshu_images/web` 下。草稿中的图片引用不能替代图片文件；系统清理临时目录后需重新上传。默认 Docker 配置将图片持久化到宿主的 `data/uploads/web/`，重建容器时请保留整个数据目录，详见 [Docker 部署说明](./docker/README.md)。

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
