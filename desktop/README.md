# 红薯创作台桌面端（Tauri v2）

基于 Tauri v2 的桌面客户端，支持 **macOS (Apple Silicon / ARM64)** 与 **Windows (x64)**。安装包内预打包 Go 后端服务（内嵌 `web/dist` 前端）与 `fingerprint-chromium` 指纹浏览器，开箱即用，无需在运行期额外下载浏览器或配置运行环境。

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
```

## 运行期数据目录

桌面端默认将账号、数据库、图片上传与日志保存在系统应用本地数据目录中（与源码或 Docker 部署相互隔离）：

- **macOS**：`~/Library/Application Support/com.xiaohongshu.studio/`
- **Windows**：`%LOCALAPPDATA%\com.xiaohongshu.studio\`

目录内主要包含：

- `accounts/`：各账号独立浏览器 Profile
- `studio.db`：SQLite 数据库
- `uploads/`：本地上传与生成图片缓存（对应 `XHS_IMAGES_DIR`）
- `studio.log`：后端标准错误日志
- `browser/current/`（macOS 首次启动从内置 `chromium.tar.gz` 原地解压一次）
