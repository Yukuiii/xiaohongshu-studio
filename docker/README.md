# Docker 部署红薯创作台

默认镜像同时包含工作台、Chromium 和 noVNC 桌面。打开账号弹窗后点击「打开登录浏览器」，即可在工作台内查看真实浏览器并完成登录、安全验证；只运行一个应用容器，不需要单独的桌面镜像或端口。

## 启动

安装并启动 Docker，准备完整的当前项目源码。在项目根目录执行：

```bash
cd docker
docker compose up -d --build
```

打开 [红薯创作台](http://127.0.0.1:18060/)，在账号弹窗内打开登录浏览器，按真实页面完成扫码及账号安全验证，再配置模型、编辑草稿和确认发布。首次构建需要下载 Go 依赖、系统软件包和内置浏览器；系统依赖与浏览器安装在应用复制之前，后续 Go 更改可复用这些缓存层。

镜像和服务名为 `xiaohongshu-studio`，本地镜像标签为 `xiaohongshu-studio:local`。当前内置浏览器镜像支持 `linux/amd64`；Apple Silicon 需要 Docker 的 amd64 模拟支持。

## 数据与素材

- `docker/data/` 保存登录状态、上传图片及运行配置，更新时保留；可用 `XHS_DATA_DIR` 指定已有数据目录的完整路径。
- 工作台上传与生成图片通过 `docker/data/uploads/` 挂载到容器 `/tmp/xiaohongshu_images/`，图片保存在其 `web/` 子目录。重建容器时保留整个 `data/`，不要只备份 `cookies.json`。
- 草稿保存在访问工作台的浏览器本地，换浏览器或清理网站数据不会自动迁移。
- 页面可上传图片；已有图片、视频可放入 `docker/images/`（可用 `XHS_MEDIA_DIR` 指定其他目录），对应容器内只读素材目录 `/app/images/`。桌面文件窗口也有 images 书签。数据与上传目录保持可写。
- 切换已有部署前，先在旧配置所在目录执行 `docker compose down`，再使用新配置启动；保留原数据目录，不要让两个实例同时操作同一账号。
- 镜像内浏览器缓存仍使用 `/app/cache/xiaohongshu-mcp/browser` 这一历史存储身份，与应用探测路径一致，无需因改名重新下载。不要将数据卷覆盖到 `/app/cache`。
- 若使用旧桌面部署的数据，先在旧部署目录停止服务，再将 `XHS_DATA_DIR` 指向原数据目录的完整路径，并保留原素材路径。不要删除或覆盖旧数据，也不要同时运行两个实例。保持原工作台访问地址可以继续使用浏览器本地草稿。

## 日常管理

以下命令在 `docker/` 目录执行：

| 操作 | 命令 |
| --- | --- |
| 查看日志 | `docker compose logs -f` |
| 停止服务并保留数据 | `docker compose down` |
| 更新当前源码后重新构建 | `docker compose up -d --build` |

## 访问令牌、端口与代理

服务默认仅映射到本机回环地址。可在 `docker/.env` 中配置：

```dotenv
AUTH_TOKEN=replace-with-your-own-secret
XHS_WEB_PORT=18060
```

保存后执行 `docker compose up -d`，在工作台填写相同令牌。根页面和健康检查公开，HTTP API 及图片资源需要 `Authorization: Bearer <token>`。未设置 `AUTH_TOKEN` 时不启用 API 令牌鉴权；令牌用于服务访问控制，模型的 API Key 在页面单独配置。登录浏览器由 Go 签发短时 HttpOnly cookie，桌面页面与 WebSocket 通过同源 `/desktop/` 路径访问，不在 URL 传令牌。跨站请求会被拒绝。

需要代理时，在同一文件追加 `XHS_PROXY=http://user:pass@proxy:port`。支持 HTTP、HTTPS 和 SOCKS5；无需认证时省略 `user:pass@`。

构建时默认使用 Ubuntu 官方 HTTPS 软件源。若所在网络下载缓慢，可在 `docker/.env` 指定可信的 HTTPS 镜像源后重新构建，例如：

```dotenv
APT_MIRROR=https://mirrors.huaweicloud.com/ubuntu
APT_SECURITY_MIRROR=https://mirrors.huaweicloud.com/ubuntu
```

这两项仅影响镜像构建，TLS 证书和 APT 软件包签名仍会校验。

内置浏览器保留作者实际使用的 **fingerprint-chromium 148.0.7778.215** 和指纹参数，默认从 [官方 GitHub Release](https://github.com/adryfish/fingerprint-chromium/releases/tag/148.0.7778.215) 下载。版本由 `browser/browser_version.txt` 固定，下载文件必须通过仓库 `browser/browser_checksums.txt` 中的 SHA256 校验；不会从下载源读取校验值，也不会回退到作者 CDN。

若需镜像，可在 `docker/.env` 设置构建变量：

```dotenv
BROWSER_DOWNLOAD_BASE=https://your-mirror.example/fingerprint-chromium/releases/download
```

镜像须提供 `<BROWSER_DOWNLOAD_BASE>/<version>/<archive>` 目录，Linux 文件名为 `ungoogled-chromium-148.0.7778.215-1-x86_64_linux.tar.xz`，内容必须与官方资产一致。也可使用完整 GitHub 代理前缀，例如 `https://your-proxy.example/https://github.com/adryfish/fingerprint-chromium/releases/download`。直接构建时传入 `--build-arg BROWSER_DOWNLOAD_BASE=https://your-mirror.example/fingerprint-chromium/releases/download`，Compose 执行 `docker compose up -d --build`。这是构建下载配置；镜像已预装浏览器，启动不再下载。源码运行时的同结构镜像配置使用 `XHS_BROWSER_DOWNLOAD_BASE`。


远程服务器可通过 `ssh -L 18060:127.0.0.1:18060 user@server` 转发，再打开本机工作台。自定义端口时相应调整转发命令和访问地址。

容器只公开 `18060`。内部 x11vnc 监听回环 `5900`，websockify/noVNC 监听回环 `6080`，只接受 Go 代理改写的内部 Origin。无需配置桌面来源白名单、VNC 密码或第二条 SSH 转发。Compose 保留 1 GB 共享内存、`cap_drop: ALL`、`DAC_OVERRIDE` 与 `no-new-privileges`；保留该能力以兼容其他 UID 拥有的旧数据目录。

中文输入使用 **Ctrl+Space** 切换 Rime 简体拼音。验证期间关闭账号弹窗仅断开画面，后台浏览器保留；成功保存登录信息后，浏览器自动关闭，页面收起弹窗并显示成功。「结束登录窗口」可提前关闭浏览器并保留已保存的数据。未完成会话最长保留 20 分钟。

## 维护者：构建与发布

在项目根目录单独构建完整应用镜像：

```bash
docker build --builder "$(docker context show)" --platform linux/amd64 -t xiaohongshu-studio:local .
```

Docker 发布 workflow 使用当前仓库的 `GITHUB_TOKEN` 和 `packages:write` 权限，推送到 `ghcr.io/<小写仓库所有者>/<小写仓库名>`，只发布版本 tag 与 `latest` 两个标签，均包含完整桌面组件。仓库需允许 Actions 发布 Packages。这里描述的是发布配置，不代表当前分支已有可下载镜像。容器检查与实际覆盖边界见 [验证说明](desktop/docs/verification.md)。

浏览器资产校验 workflow 默认关闭。维护者设置仓库 Actions Variable `ENABLE_BROWSER_UPDATES=true` 后，才会定时或从默认分支手动校验仓库当前固定版本的官方三平台资产及 SHA256。它只有只读仓库权限，不探测 latest、不自动升级、不提交版本、不创建 Issue。升级需人工确认三平台资产齐全，并一起审阅版本文件和固定校验清单，避免升级到缺少 macOS 资产的版本。
