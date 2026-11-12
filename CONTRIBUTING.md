# 贡献指南 | Contributing Guide

感谢你关注红薯创作台！产品入口为浏览器工作台，后端提供 HTTP API。请在提交 PR 前阅读以下规范。

Thank you for your interest in Xiaohongshu Studio! The product is a browser workspace backed by HTTP APIs. Please read this guide before submitting a PR.

---

## 基本流程 | Basic Workflow

1. Fork 本仓库并创建功能分支
2. 在本地完成开发和测试
3. 提交 PR 并填写清晰的描述

在当前源码目录使用 `go run .`，打开 **http://localhost:18060/**。Go 版本以 [go.mod](./go.mod) 为准（当前为 1.27.4）。页面资源位于 `web/`，通过 Go 嵌入，无需单独的前端构建。构建程序使用 `go build -o xiaohongshu-studio .`。

Run `go run .` from the current source directory and open **http://localhost:18060/**. Use the Go version in [go.mod](./go.mod), currently 1.27.4. Files in `web/` are embedded in Go and require no separate frontend build. Build the executable with `go build -o xiaohongshu-studio .`.

页面流程见 [README](./README.md)，接口契约见 [HTTP API 文档](./docs/API.md)。涉及用户行为或接口的修改需同步更新这些文档；不要把未发布的源码功能描述成上游发行包已有功能。

Follow the [README](./README_EN.md) for the page workflow and the [HTTP API documentation](./docs/API.md) for endpoint contracts. Update relevant documentation when behavior changes; do not claim upstream downloads include unpublished source features.

---

## PR 提交规范 | PR Requirements

### 1. 一个 PR 只做一件事 | One PR, One Feature

每个 PR 只包含 **一个功能或一个修复**。多个功能请拆分为多个 PR。

Each PR should contain **only one feature or one fix**. Split multiple features into separate PRs.

### 2. 必须经过验证 | Must Be Verified

**即使代码是 AI 生成的，也必须在本地运行并验证功能正确。** 未经验证的 PR 将直接关闭。

**Even if the code is AI-generated, you must run and verify it locally.** Unverified PRs will be closed.

后端修改运行相关 Go 测试；Web 修改检查页面加载、设置保存、草稿恢复以及相关交互，PR 中记录实际执行的步骤与结果。涉及账号、模型或发布的验证请使用自己的授权配置；不要为演示自动发布真实笔记。发布前的用户确认、服务令牌鉴权和跨站写请求保护必须保留。

Run relevant Go tests for backend changes. For Web changes, check page loading, settings, draft restoration, and affected interactions, and record the actual steps and results in the PR. Use your own authorized account and model settings; do not automatically publish real posts for a demo. Preserve explicit publication confirmation, service-token authentication, and browser-origin protection.

### 3. 提供演示截图/视频 | Provide Demo

PR 中请附上功能演示的 **截图或录屏**，让 Reviewer 快速理解改动效果。

Please attach **screenshots or screen recordings** to demonstrate the feature.

> **隐私提醒：演示中遮挡账号信息、二维码、模型密钥和服务令牌。不要提交 cookies.json、运行日志中的凭证或浏览器本地存储数据。**
>
> **Privacy: Mask account information, QR codes, model keys, and service tokens. Never commit cookies.json, credentials from logs, or browser localStorage data.**

### 4. 禁止大量 JS 注入 | No Excessive JS Injection

后端通过 [go-rod](https://go-rod.github.io/) 自动化操作小红书。**严禁通过大量注入 JavaScript 的方式操作小红书页面元素**，应使用 go-rod 提供的 API。此规范针对外部页面自动化，不限制 `web/` 中工作台自身的正常 JavaScript 交互。

违反此规则的 PR **一律不予合并**。

The backend uses [go-rod](https://go-rod.github.io/) to automate Xiaohongshu. **Do NOT manipulate Xiaohongshu page elements by injecting large amounts of JavaScript.** Use go-rod's API. This rule concerns external-page automation, not ordinary JavaScript interactions within the workspace's own `web/` files.

PRs violating this rule **will NOT be merged**.

### 5. 代码规范 | Code Style

- Go 代码需要格式化（`gofmt`）
- 注释使用中文，专业术语可用英文
- 不要过度设计，保持简洁

---

## 提交 Checklist | PR Checklist

提交前请确认：

- [ ] 代码已在本地运行并验证通过
- [ ] 一个 PR 仅包含一个功能/修复
- [ ] 附上演示截图或录屏（账号信息已打码）
- [ ] 没有大量 JS 注入，使用 go-rod API 操作元素
- [ ] 代码已格式化，注释清晰
- [ ] Web/HTTP 行为变更已更新对应文档，保留草稿、现有登录会话和发布确认
- [ ] 未提交 Cookie、密钥、令牌或个人运行数据
- [ ] 保留 [LICENSE](./LICENSE) 与原作者、上游贡献者署名

---

感谢你的贡献！🎉 | Thanks for contributing!
