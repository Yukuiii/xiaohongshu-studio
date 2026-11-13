# Docker 部署红薯创作台

默认镜像同时包含 Vite + React 工作台、Chromium 和 noVNC 桌面。左侧提供「工作台」「账号」「设置」导航；在「账号」点击「添加账号」，会通过弹窗展示真实小红书扫码页面，可完成手机确认与安全验证。只运行一个应用容器，不需要单独的桌面镜像或端口。

## 启动

安装并启动 Docker。使用已发布镜像时不需要本机安装 Go/Node，也不要求完整源码；从源码构建时需要完整的当前项目源码。

### 直接运行镜像（推荐，不需要源码）

```bash
docker run -d \
  --tty \
  --name xiaohongshu-studio \
  --hostname xiaohongshu-studio \
  --platform linux/amd64 \
  --restart unless-stopped \
  --stop-timeout 60 \
  --shm-size=1g \
  -p 127.0.0.1:18060:18060 \
  -v "$PWD/xiaohongshu-studio-data:/app/data" \
  --cap-drop ALL \
  --cap-add DAC_OVERRIDE \
  --security-opt no-new-privileges:true \
  ghcr.io/yukuiii/xiaohongshu-studio:latest
```

默认使用 `ghcr.io/yukuiii/xiaohongshu-studio:latest`。固定发布版本时，把最后的 `:latest` 换成 `:v0.1.0`。固定数据布局尚待发版；当前 `latest`（`v0.1.0`）还需在镜像名前追加 `-v "$PWD/xiaohongshu-studio-data/uploads:/tmp/xiaohongshu_images"` 以持久化图片。

新布局将账号、数据库和上传图片统一保存在容器内 `/app/data`，对应宿主机的 `xiaohongshu-studio-data/`，只需一个数据挂载。后续请在同一目录运行，或把挂载源改成固定的绝对路径。已有图片素材可追加 `-v /绝对路径/images:/app/images:ro`，需要 API 令牌时追加 `-e AUTH_TOKEN=...`，需要代理时追加 `-e XHS_PROXY=...`。更新镜像时执行 `docker pull ghcr.io/yukuiii/xiaohongshu-studio:latest`，再 `docker rm -f xiaohongshu-studio` 并重新执行上面的 `docker run`；数据目录会保留。

打开 [红薯创作台](http://127.0.0.1:18060/)，在左侧「账号」点击「添加账号」直接扫码并完成账号安全验证，在「设置」配置模型，再回「工作台」编辑草稿和确认发布。

### 已有源码时使用 Compose

当前 Compose 配置采用新的单数据挂载布局，配套镜像尚待发版；发版前请使用下方 `./up.sh` 从源码构建。支持新布局的发布镜像可在 `docker/` 目录执行：

```bash
docker compose up -d --pull always --no-build
```

默认拉取 `ghcr.io/yukuiii/xiaohongshu-studio:latest`，可用 `XHS_IMAGE_TAG` 固定兼容的发布版本。

### 从源码构建

```bash
./up.sh
```

`up.sh` 用当前提交的 12 位短 SHA 作为镜像标签；工作区有未提交的已跟踪改动时追加 `-dirty`。例如 `ghcr.io/yukuiii/xiaohongshu-studio:f7a6aac291e1`。设置 `XHS_IMAGE_TAG` 可覆盖镜像标签；本地 Compose 构建的程序版本固定为 `dev`，正式发布由 CI 注入版本 tag。

首次构建需要下载 npm/Go 依赖、系统软件包和内置浏览器；系统依赖与浏览器安装在应用复制之前，后续 Go 更改可复用这些缓存层。

镜像使用完整仓库名 `ghcr.io/yukuiii/xiaohongshu-studio`，发布标签为 `vX.Y.Z` 与 `latest`，本地构建标签为当前提交的短 SHA；服务名和容器名为 `xiaohongshu-studio`。当前内置浏览器镜像支持 `linux/amd64`；Apple Silicon 需要 Docker 的 amd64 模拟支持。

Dockerfile 先用 **Node.js 24** 执行 `npm --prefix web ci` 与 `npm --prefix web run build`，再将 `web/dist/` 复制到 Go 构建阶段并嵌入二进制。最终 Ubuntu 运行镜像只复制 Go 程序和桌面组件，不包含 Node 或 npm；宿主机无需安装 Node。构建上下文排除宿主的 `node_modules/` 与 `dist/`，每次使用锁文件生成容器内产物。修改前端后执行 `./up.sh` 即可更新。

## 数据与素材

以下为当前源码的数据布局，尚待发版。

- `docker/data/` 保存账号、完整浏览器数据、上传图片及运行配置，Compose 固定挂载到 `/app/data`；直接运行镜像时对应宿主机的 `xiaohongshu-studio-data/`。更新时保留整个数据目录。
- 每个账号在 `accounts/<account-id>/` 下保存独立 `profiles/<profile-id>/` 和 `downloads/`；整个 Chromium user-data-dir（包括 `Local State`、`Default/`、Cookie、localStorage、IndexedDB、Service Worker 和站点配置）都是持久数据。账号元数据、模型配置、草稿和发布防重记录保存在 `studio.db` 的 `accounts`、`settings`、`drafts`、`publish_protection` 四张表中。浏览器正常退出和服务启动时会删除 Cache、Code Cache、GPU/Shader Cache 等可重建缓存，保留登录态和站点存储。浏览器空闲 10 分钟后关闭，正常关闭、切换账号和重建容器均保留 profile；备份时先停止服务，再保留整个数据目录。
- Compose 固定容器主机名为 `xiaohongshu-studio`，并提供 60 秒的优雅停机时间。退出时先等待应用关闭浏览器、保存 profile，再关闭 Xvfb 和 DBus，避免重建后被旧容器的 Chromium 锁阻挡。
- 全新数据库不创建默认账号，也不自动导入旧 JSON、Cookie 文件或浏览器 localStorage 草稿；请在账号页添加账号并扫码登录。数据目录下的 `cookies.json` 仅供底层 Cookie 文件 API 兼容使用，不会据此创建或恢复账号。
- 工作台上传与生成图片保存在数据目录的 `uploads/web/<account-id>/`；草稿引用不能替代图片备份，旧临时图片不会自动搬入。
- 模型地址、名称与 API Key 保存在 `studio.db` 的 `settings` 表，数据库权限为 `0600`，所有账号共用；工作台读取地址和名称，密钥只返回是否已保存。首次打开旧浏览器时，仅在服务端尚未初始化的情况下自动导入旧模型配置，成功后移除浏览器旧缓存。清空服务端配置后不会再次被旧浏览器导入。
- 草稿和发布防重记录按账号保存在 `studio.db`，使用 revision 协调并发更新；工作台访问令牌仍保存在浏览器 localStorage。换浏览器后填写访问令牌即可读取服务端草稿和发布防重记录。
- 账号页可添加、选择、重命名、重新登录、重置浏览器数据和移除账号。重置保留账号 ID、绑定身份、seed、草稿和素材；移除会清理该账号的浏览器数据、下载、托管图片、草稿和发布记录。服务令牌允许访问整个工作台，多个账号槽位不构成访问者权限隔离。
- 页面可上传图片；已有图片、视频可放入 `docker/images/`，对应容器内只读素材目录 `/app/images/`。桌面文件窗口也有 images 书签。数据与上传目录保持可写。
- 切换已有部署前，先在旧配置所在目录执行 `docker compose down`，再使用新配置启动；Docker 数据根仍为 `/app/data`，无需迁移。保留原数据目录，不要让两个实例同时操作同一账号。
- 镜像内浏览器缓存仍使用 `/app/cache/xiaohongshu-mcp/browser` 这一历史存储身份，与应用探测路径一致，无需因改名重新下载。不要将数据卷覆盖到 `/app/cache`。

## 日常管理

以下命令在 `docker/` 目录执行：

| 操作 | 命令 |
| --- | --- |
| 查看日志 | `docker compose logs -f` |
| 停止服务并保留数据 | `docker compose down` |
| 更新到最新发布镜像 | `docker compose up -d --pull always --no-build` |
| 更新当前源码后重新构建 | `./up.sh` |

直接运行镜像时，容器名为 `xiaohongshu-studio`，用 `docker logs -f`、`docker stop`、`docker rm` 管理；更新镜像后需要重建容器。

## 访问令牌、端口与代理

服务默认仅映射到本机回环地址。可在 `docker/.env` 中配置：

```dotenv
AUTH_TOKEN=replace-with-your-own-secret
XHS_WEB_PORT=18060
# 可选：固定支持当前数据布局的发布版本。
# XHS_IMAGE_TAG=vX.Y.Z
```

保存后执行 `docker compose up -d`，在工作台填写相同令牌。根页面和健康检查公开，HTTP API 及图片资源需要 `Authorization: Bearer <token>`。未设置 `AUTH_TOKEN` 时不启用 API 令牌鉴权；令牌授权整个工作台，与模型 API Key 是独立配置。账号业务请求通过 `X-XHS-Account-ID` 明确归属，仅有一个有效账号时兼容省略。登录浏览器由 Go 签发绑定账号与会话的短时 HttpOnly cookie，桌面页面与 WebSocket 通过同源 `/desktop/<session-id>/` 路径访问；结束窗口时带原 `session_id`，旧票据和 WebSocket 随会话结束撤销。URL 不携带服务令牌，跨站请求会被拒绝。

需要代理时，在同一文件追加 `XHS_PROXY=http://user:pass@proxy:port`。支持 HTTP、HTTPS 和 SOCKS5；无需认证时省略 `user:pass@`。

Compose 的构建参数固定写在 `docker-compose.yml` 的 `build.args` 中，不读取 shell 或 `.env`：本地版本为 `dev`，基础镜像使用 Docker Hub 官方仓库，APT 使用 Ubuntu 官方 HTTPS 软件源，浏览器从官方 GitHub Release 下载。需要调整源码构建的下载地址时，直接修改该文件后运行 `./up.sh`。

内置浏览器为 **fingerprint-chromium 148.0.7778.215**，版本由 `browser/browser_version.txt` 固定，下载文件必须通过仓库 `browser/browser_checksums.txt` 中的 SHA256 校验。镜像已预装浏览器，启动时不再下载；使用已发布镜像无需配置构建参数。


远程服务器可通过 `ssh -L 18060:127.0.0.1:18060 user@server` 转发，再打开本机工作台。自定义端口时相应调整转发命令和访问地址。

容器只公开 `18060`。内部 x11vnc 监听回环 `5900`，websockify/noVNC 监听回环 `6080`，只接受 Go 代理改写的内部 Origin。无需配置桌面来源白名单、VNC 密码或第二条 SSH 转发。Compose 保留 1 GB 共享内存、`cap_drop: ALL`、`DAC_OVERRIDE` 与 `no-new-privileges`；保留该能力以兼容其他 UID 拥有的旧数据目录。

登录弹窗接近全屏显示 noVNC，二维码较小时可切换「原始大小」，按原始像素查看，也可切回「适应窗口」。登录浏览器在容器桌面中以全屏方式运行，不提供浏览器窗口关闭按钮。关闭弹窗或按 Esc 仅收起画面，后台候选浏览器保留，可点击「继续登录」重新连接；请使用「结束登录窗口」结束会话。中文输入使用 **Ctrl+Space** 切换 Rime 简体拼音。未完成的登录会话最长保留 20 分钟，同一共享桌面仅允许一个登录窗口。真实身份与已有绑定一致、未重复绑定且候选 profile 正常关闭落盘后才保存成功，随后弹窗自动关闭。失败、结束或过期不替换原 profile；窗口关闭前先撤销票据和已有 WebSocket，旧 viewer 无法访问后续账号。切换账号按本页和每个请求固定，任务进行中禁止切换，不影响其他标签页。

`DELETE /api/v1/login/cookies` 只清除所选浏览器 Cookie；完整清空使用 `POST /api/v1/accounts/:id/reset-profile`，需要用户确认。

## 维护者：构建与发布

在项目根目录单独构建完整应用镜像：

```bash
tag=$(git rev-parse --short=12 HEAD)
docker build --builder "$(docker context show)" --platform linux/amd64 \
  --build-arg VERSION="$tag" \
  -t "ghcr.io/yukuiii/xiaohongshu-studio:$tag" .
```

本地开发前端可按 [贡献指南](../CONTRIBUTING.md) 用 Node.js 24 构建一次后，分别启动 `go run .` 与 `npm --prefix web run dev`。Vite 的 `5173` 端口将 `/api`、`/desktop`（含 WebSocket）及 `/health` 代理到 Go 的 `18060`；容器生产部署仍只需公开 `18060`。

Docker 发布 workflow 使用当前仓库的 `GITHUB_TOKEN` 和 `packages:write` 权限，推送到 `ghcr.io/<小写仓库所有者>/<小写仓库名>`，只发布版本 tag 与 `latest` 两个标签，均包含完整桌面组件；本地提交标签与发布版本标签共用同一仓库名。仓库需允许 Actions 发布 Packages。发布后可用 `docker compose up -d --pull always --no-build` 直接拉取。容器检查与实际覆盖边界见 [验证说明](desktop/docs/verification.md)。

浏览器资产校验 workflow 默认关闭。维护者设置仓库 Actions Variable `ENABLE_BROWSER_UPDATES=true` 后，才会定时或从默认分支手动校验仓库当前固定版本的官方三平台资产及 SHA256。它只有只读仓库权限，不探测 latest、不自动升级、不提交版本、不创建 Issue。升级需人工确认三平台资产齐全，并一起审阅版本文件和固定校验清单，避免升级到缺少 macOS 资产的版本。
