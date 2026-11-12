# 内嵌桌面镜像验证

默认镜像包含 Go、Chromium、Xvfb、openbox、Fcitx5/Rime、x11vnc 和 websockify/noVNC，唯一部署入口是 `docker/docker-compose.yml`。本地已执行镜像构建、Go 桌面代理回归、容器 smoke 和真实 noVNC 键鼠交互检查；真实账号安全验证由使用者完成，未运行 Grype 安全扫描。

## 覆盖范围

| 项目 | 检查内容 |
| --- | --- |
| 单镜像入口 | 默认命令启动完整服务，镜像只 EXPOSE 18060，运行时仅映射一个回环端口 |
| 桌面输入 | 容器内真实 RFB 键鼠事件验证英文、Shift、Caps Lock、Rime 简体拼音和 Ctrl+Space 切换 |
| 素材与数据 | 宿主新增文件的系统窗口选择、素材只读、其他 UID 的 data 和旧输入法配置迁移 |
| 公开入口 | 工作台首页、HTTP API Bearer 鉴权；`/desktop/<session-id>/vnc.html` 与同会话 WebSocket 拒绝无 ticket、跨站和缺失 Origin 请求 |
| 账号隔离 | 请求账号绑定、Cookie/seed/profile 与图片归属；旧 ticket/连接不能跟随新账号登录会话 |
| profile 持久化 | 本地 fixture 验证 Cookie、localStorage、IndexedDB 重启恢复，候选登录提交/回滚及 session Cookie 不重放旧页面 |
| 发布防重 | 浏览器 Web Locks 协调同账号多标签页，旧草稿保存不覆盖更新后的发布保护；发布回应用本地替身 |
| 内部桌面 | 容器内 noVNC 页面、真实 WebSocket/RFB 握手；ExpectOrigin 拒绝非 Go 上游 Origin 和缺失 Origin |
| 进程恢复 | 组件提前退出、应用退出码、正常重启及 SIGKILL 后显示锁恢复 |
| 指纹行为 | 真实 Linux 浏览器的平台伪装、同 seed 的 Canvas 一致性及不同 seed 的差异 |
| 安全扫描 | 单款完整镜像的 Grype 严重度汇总与 High/Critical 明细，完整 JSON 保存为 artifact |

容器 smoke 不调用 `POST /api/v1/login/browser`，因此不会访问真实登录网站，也不会获取真实登录 ticket。有效 ticket 下的同源 HTTP/WS 代理、过期 ticket 与跨站访问阻断由 Go 代理回归覆盖，不能用容器内 RFB 握手代替该覆盖。真实账号登录、安全验证、发布和互动不属于这些自动检查的覆盖范围。

安全扫描汇总完整镜像的现有告警，不再与另一款基础镜像比较，也不设置新增 GUI 漏洞门禁；报告存在不代表镜像无漏洞，扫描器执行失败仍会使 CI 失败。

## 运行检查

按 [部署指南](../../README.md) 构建唯一应用镜像后，在项目根目录执行（使用 `./up.sh` 输出的提交标签；有未提交改动时标签带 `-dirty`）：

```bash
tag=$(git rev-parse --short=12 HEAD)
python3 docker/desktop/smoke_test.py "ghcr.io/yukuiii/xiaohongshu-studio:${tag}"
```

需要 Docker 和 Python 3。脚本使用隔离容器、临时数据和随机回环端口，结束后清理。HTTP API 检查向 `POST /api/v1/ai/models` 发送无效 JSON：匿名请求应为 401，带正确令牌的请求应为 400，确保实际路由受保护且不会调用模型或小红书。`input_test.py` 由 smoke 复制到容器执行，无需宿主输入法依赖。

独立账号与 profile 回归还可从源码运行（需先构建前端，并已有缓存的内置浏览器）：

```bash
go test -race -tags integration ./browser -run '^TestProfile'
go test -race -tags integration -run '^TestAccount(RuntimeProfilePersistenceLocal|CandidateLoginCommitLocal|ClearCookiesLocal)$' .
```

这些测试使用本地页面，覆盖正常关闭、重启、会话 Cookie、候选 profile 提交及错号回滚；没有桌面的环境会跳过有头分支。本次已在 macOS 及完整 Linux 镜像内执行对应回归。

[Docker CI](../../../.github/workflows/docker-test.yml) 只构建默认完整镜像，运行相同 smoke 检查，并用固定版本、校验下载摘要的 Grype 扫描。原始桌面层的 [历史 CI 记录](https://github.com/xpzouying/xiaohongshu-mcp/actions/runs/41320463382) 仅供来源追溯，不代表当前内嵌桌面已通过验证。

## 原始演示

以下为原始投稿截图，输入法对应切换 Rime 之前的版本，仅供操作效果参考：

- 输入与文件选择：[输入结果](demo-input.png)、[拼音候选](demo-pinyin.png)、[文件窗口](demo-files.png)。
- 小红书公开浏览：[首页](demo-xhs-logged-in.png)、[中文搜索联想](demo-xhs-search-input.png)、[搜索结果](demo-xhs-search-results.png)。
