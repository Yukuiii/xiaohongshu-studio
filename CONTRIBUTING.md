# 贡献指南 | Contributing Guide

感谢你关注红薯创作台！产品入口为浏览器工作台，后端提供 HTTP API。请在提交 PR 前阅读以下规范。

Thank you for your interest in Xiaohongshu Studio! The product is a browser workspace backed by HTTP APIs. Please read this guide before submitting a PR.

---

## 基本流程 | Basic Workflow

1. Fork 本仓库并创建功能分支
2. 在本地完成开发和测试
3. 提交 PR 并填写清晰的描述

前端使用 Vite + React，Go 版本以 [go.mod](./go.mod) 为准（当前为 1.27.4），前端构建使用 Node.js 24。在当前源码目录先安装依赖并生成产物，再运行 Go：

The frontend uses Vite + React. Use the Go version in [go.mod](./go.mod), currently 1.27.4, and Node.js 24 for frontend builds. Install dependencies and generate assets before running Go:

```bash
npm --prefix web ci
npm --prefix web run build
go run .
```

生产入口为 **http://localhost:18060/**。Go 嵌入 `web/dist/index.html`、`web/dist/assets/` 和 `prompt/` 中的模型模板；缺少构建资源时编译失败。更新前端后重新执行 `npm --prefix web run build`，再运行 `go build -o xiaohongshu-studio .`。编译后的程序无需 Node；不要提交 `web/node_modules/` 或 `web/dist/`，依赖变化必须同步提交 `web/package-lock.json`。

The production entry is **http://localhost:18060/**. Go embeds `web/dist/index.html`, `web/dist/assets/`, and the model templates in `prompt/`; missing build resources fail compilation. After frontend changes, run `npm --prefix web run build` before `go build -o xiaohongshu-studio .`. The executable requires no Node at runtime. Do not commit `web/node_modules/` or `web/dist/`; commit `web/package-lock.json` with dependency changes.

开发时先完成一次前端构建，在一个终端运行 `go run .`（18060），另一个终端运行 `npm --prefix web run dev`，访问 **http://localhost:5173/**。Vite 将 `/api`、`/desktop`（含 WebSocket）和 `/health` 代理到 `http://127.0.0.1:18060`。根 Go 包的构建、测试和发版任务均须先执行上述 npm 安装与构建；Docker 通过多阶段构建完成同样流程。

For development, build the frontend once, run `go run .` on port 18060 in one terminal and `npm --prefix web run dev` in another, then visit **http://localhost:5173/**. Vite proxies `/api`, `/desktop` (including WebSocket), and `/health` to `http://127.0.0.1:18060`. Build, test, and release jobs involving the root Go package must run the npm installation and build first; Docker performs these steps in its multistage build.

页面包含「工作台」「任务」「定时任务」「账号」「设置」五个导航。每个账号使用独立的完整 Chromium profile、固定 seed、下载目录及托管图片目录；正常关闭不能删除 user-data-dir。源码数据固定在当前工作目录的 `data/`，Docker 为 `/app/data`，桌面端直接使用系统应用数据目录；数据根包含 `accounts/`、`studio.db` 和 `uploads/`。账号业务请求通过 `X-XHS-Account-ID` 固定，定时配置保存自己的账号快照，不能维护服务全局当前账号；同账号浏览器操作串行，headless 实例空闲 10 分钟后关闭并保留数据。同一数据根使用 `accounts.Store` 进程锁，禁止多个服务实例同时使用。

The navigation includes Workspace, Tasks, Scheduled tasks, Accounts, and Settings. Each account owns a complete Chromium profile, fixed seed, download directory, and managed images. Normal shutdown must retain user-data-dir. Source runs use `data/` under the working directory, Docker uses `/app/data`, and desktop builds use the application data directory. Bind account requests with `X-XHS-Account-ID`; schedules retain their own account snapshots. Do not maintain a globally selected account. Serialize browser operations per account, retain idle profiles, and use the `accounts.Store` lock to prevent shared data roots across processes.

账号、模型配置、草稿、发布防重、发布任务、定时配置及执行历史保存在 `studio.db` 的 `accounts`、`settings`、`drafts`、`publish_protection`、`publish_tasks`、`scheduled_jobs` 和 `schedule_runs` 表中。模型配置通过 `/api/v1/settings` 管理，API Key 不返回浏览器；工作台访问令牌使用浏览器键 `hongshu-workbench.token.v1`。旧 `hongshu-workbench.settings.v1` 只向未初始化服务端迁移，成功后移除；已有或已清空的配置不能被旧浏览器覆盖。草稿和发布保护使用 revision 协调并发更新；任务创建和结束原子更新保护。定时执行保留配置与内容快照，重试只针对明确失败或跳过的记录，结果未知时不能重发。全新数据库不创建默认账号，也不自动导入旧 JSON、Cookie 文件或 localStorage 草稿。切换账号前保存草稿、取消旧请求并丢弃迟到结果。服务令牌授权整个工作台，账号槽位不构成访问者权限隔离。

Persist accounts, models, drafts, publication protection, publication tasks, schedules, and execution history in the `accounts`, `settings`, `drafts`, `publish_protection`, `publish_tasks`, `scheduled_jobs`, and `schedule_runs` tables. Manage models through `/api/v1/settings` without returning API keys. Keep the service token under `hongshu-workbench.token.v1`; legacy model settings migrate only into an uninitialized server. Coordinate drafts and protection with revisions, update protection atomically with publication tasks, and preserve scheduled configuration and content snapshots. Retry only failed or skipped runs, never uncertain publications. A fresh database does not import legacy accounts, Cookies, or browser drafts. Save before switching accounts, cancel old requests, and ignore late results. A service token authorizes the whole workspace.

重新登录必须复制已关闭的 active profile 到候选目录，确认真实身份一致、未重复绑定，再正常关闭候选浏览器并原子提交指针。失败不能替换旧 profile。桌面授权绑定账号与 `session_id`，关闭旧窗口时撤销 Cookie 票据及现有 WebSocket；同一 noVNC 桌面只允许一个登录窗口。

For sign-in, copy the stopped active profile into a candidate, verify the real identity and duplicate bindings, then shut down the candidate normally before atomically committing the pointer. Failure must preserve the old profile. Bind desktop authorization to the account and `session_id`, revoke tickets and existing WebSockets when closing a window, and allow one login window at a time on the shared noVNC desktop.

页面流程见 [README](./README.md)，接口契约见 [HTTP API 文档](./docs/API.md)。涉及用户行为或接口的修改需同步更新这些文档；不要把未发布的源码功能描述成上游发行包已有功能。

Follow the [README](./README_EN.md) for the page workflow and the [HTTP API documentation](./docs/API.md) for endpoint contracts. Update relevant documentation when behavior changes; do not claim upstream downloads include unpublished source features.

## 提示词维护 | Prompt templates

模型提示词统一维护在 `prompt/*.md`：`compose-system.md` 和 `compose-user.md` 用于手动工作台的选题、要求和完整参考；`schedule-system.md` 和 `schedule-user.md` 用于定时任务的关键词自由创作，不调用搜索或正文读取；`image.md` 定义一张主图的生成要求。模板通过 `{{变量}}` 单次替换，不重新解释关键词或参考正文中的双花括号。

两个写稿系统模板均支持 `publication_type`、`title_limit`、`content_limit`；手动写稿用户模板支持 `topic`、`instructions`、`sources`，定时写稿用户模板支持 `keyword` 和可选的 `instructions`。上述用户模板变量已 JSON 编码，不要再添加引号；生图模板支持 `title`、`content`、`image_prompt`。配置和密钥不得进入模板。模板使用 `go:embed` 编译进程序，修改后重新构建后端；桌面和 Docker 沿用相同模板。

Maintain prompts in `prompt/*.md` using `{{variable}}` placeholders. The `compose-*` templates handle manual writing with references; `schedule-*` templates handle keywords and optional writing instructions without searches. User-template values are JSON encoded before substitution, and input content is never interpreted as a template. Keep credentials outside prompts. Templates are embedded with `go:embed`, so rebuild the backend, desktop sidecar, or Docker image after changes.


---

## PR 提交规范 | PR Requirements

### 1. 一个 PR 只做一件事 | One PR, One Feature

每个 PR 只包含 **一个功能或一个修复**。多个功能请拆分为多个 PR。

Each PR should contain **only one feature or one fix**. Split multiple features into separate PRs.

### 2. 必须经过验证 | Must Be Verified

**即使代码是 AI 生成的，也必须在本地运行并验证功能正确。** 未经验证的 PR 将直接关闭。

**Even if the code is AI-generated, you must run and verify it locally.** Unverified PRs will be closed.

后端修改运行相关 Go 测试；Web 修改检查页面加载、设置保存、草稿恢复以及相关交互，PR 中记录实际执行的步骤与结果。涉及账号、模型或发布的验证使用授权配置，不要为演示真实发帖。手动发布保留确认；定时任务在用户启用或点击重试后可自动发布，保留服务令牌鉴权、账号身份检查和跨站写请求保护。

Run relevant Go tests and verify affected Web interactions, recording the actual results. Use authorized account and model configurations and do not publish real posts for demonstrations. Preserve manual publication confirmation; schedules may publish automatically after the user enables them or requests a retry. Retain service-token authentication, account identity checks, and browser-origin protection.

多账号回归应覆盖 A/B 完整 profile 重启隔离、固定 seed、候选身份错配或重复不提交、空库不导入旧数据、SQLite 重启持久化、墓碑不复活、进程锁、空闲关闭与删除竞态，以及切换时迟到请求、草稿和未知发布结果保护。先执行 `npm --prefix web test` 和 `npm --prefix web run build`，再对修改文件 `gofmt`，运行 `go build ./...`、`go vet ./...`、`go test ./...`、`go test -race ./...` 和 `go test -tags integration -run '^$' ./...`。真实浏览器测试与人工扫码分别记录覆盖范围；测试通过不代表真实账号登录或发布已验收。

Multi-account regressions should cover A/B profile persistence and isolation, fixed seeds, rejected identity mismatches and duplicate bindings, fresh databases ignoring legacy data, SQLite persistence across restarts, tombstones, process locks, idle shutdown and removal races, and late requests, drafts, and uncertain publication results while switching. Run `npm --prefix web test` and `npm --prefix web run build`, format changed Go files, then run `go build ./...`, `go vet ./...`, `go test ./...`, `go test -race ./...`, and `go test -tags integration -run '^$' ./...`. Record real-browser coverage and manual sign-in separately; passing tests does not establish acceptance of real account sign-in or publication.

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
- [ ] 保留 [LICENSE](./LICENSE)、原作者版权声明与上游来源说明

---

感谢你的贡献！🎉 | Thanks for contributing!
