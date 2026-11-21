# 红薯创作台桌面端（Tauri v2）

基于 Tauri v2 的桌面客户端，支持 **macOS (Apple Silicon / ARM64)** 与 **Windows (x64)**。安装包内预打包 Go 后端服务（内嵌 `web/dist` 前端）与 `fingerprint-chromium` 指纹浏览器，开箱即用，无需在运行期额外下载浏览器或配置运行环境。

桌面端扫码登录会打开本机内置 Chromium 窗口，noVNC 由 Docker 后端提供。切到登录浏览器或最小化客户端时，正在打开或结束的登录窗口会继续完成；后台暂停进度轮询，返回客户端后恢复。

桌面窗口、启动页和工作台参考 [Muse](https://muse.ai/) 的界面风格，并随系统切换深浅外观，标题栏、表单和浮层保持一致。下拉选项保留键盘选择与表单校验，任务状态在两种主题下均以独立颜色区分。

发布结果提供「打开笔记」和「复制链接」；「打开笔记」会使用系统默认浏览器，笔记的访问仍遵循小红书的登录和可见范围设置。

## 应用更新

桌面客户端会在启动时和运行期间每 6 小时检查 GitHub Release 的最新稳定版。在「设置 → 应用更新」可以手动检查、下载和安装。下载完成后会校验安装包大小和 SHA-256。确认安装时需要等待发布及定时任务结束，应用随后会退出、安装并重新打开。

macOS 使用已安装目录中的 `.app`，Windows 沿用原来的 NSIS 或 MSI 安装方式。升级继续使用原来的账号、草稿和任务数据。旧版首次升级可在发布页下载对应安装包覆盖安装。[GitHub Releases](https://github.com/Yukuiii/xiaohongshu-studio/releases)

稳定版发布自动生成 `studio-update.json`，记录 tag、三个桌面安装包的大小和 SHA-256，和安装包一起上传到 GitHub Release。客户端从同一 Release 读取清单和安装包。

## 目录结构

- `package.json`：桌面端构建与开发脚本（`@tauri-apps/cli`）
- `scripts/prepare.mjs`：准备 Go Sidecar 与预打包指纹浏览器资源（下载并校验 `browser/browser_checksums.txt` SHA256，或复用本机缓存）
- `loading/index.html`：启动加载与错误兜底页面
- `src-tauri/`：Tauri v2 Rust 宿主工程（托盘常驻、Sidecar 生命周期管理、`127.0.0.1:18060` 端口冲突自动回退、应用数据目录隔离）

## 环境要求

- Node.js 24+
- Go 1.27+
- Rust stable toolchain（含 Cargo）
- Windows 还需 Microsoft Edge WebView2 Runtime（Windows 11 默认内置）

## 本地开发与打包

在仓库根目录执行：

```bash
# 安装依赖
npm --prefix web ci
npm --prefix desktop ci

# 仅准备 Sidecar 与预置浏览器资源
npm --prefix desktop run prepare:assets

# 启动桌面端开发模式
npm --prefix desktop run dev

# 构建安装包（macOS 生成 .app/.dmg；Windows 生成 NSIS 与 MSI 安装包）
npm --prefix desktop run build

# 指定安装包版本；v 前缀会自动去掉，未设置时使用 tauri.conf.json 的 0.1.0
VERSION=v1.2.3 npm --prefix desktop run build
```

## 运行期数据目录

桌面端默认将账号、数据库、图片上传与日志保存在系统应用本地数据目录中（与源码或 Docker 部署相互隔离）：

- **macOS**：`~/Library/Application Support/com.xiaohongshu.studio/`
- **Windows**：`%LOCALAPPDATA%\com.xiaohongshu.studio\`

目录内主要包含：

- `accounts/`：各账号独立浏览器 Profile
- `studio.db`：账号、草稿、模型配置、发布保护及后台任务记录
- `uploads/`：本地上传与生成图片
- `studio.log`：后端标准错误日志
- `browser/current/`（仅 macOS，首次启动从内置 `chromium.tar.gz` 原地解压一次）

Windows 使用安装包内、与应用 exe 同级的 `browser/`。桌面数据沿用上述路径，升级无需搬迁数据。
