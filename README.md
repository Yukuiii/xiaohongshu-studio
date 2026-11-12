# 红薯创作台

[English](./README_EN.md) · [Windows 安装](./docs/windows_guide.md) · [Docker 部署](./docker/README.md) · [HTTP API](./docs/API.md)

在浏览器中完成小红书选题、搜索取材、写稿、配图和确认发布。启动后打开 **http://localhost:18060/**；如果已有管理员部署，请打开管理员提供的工作台地址。

登录使用完整的可视化浏览器：工作台内嵌 noVNC 展示小红书原页面，扫码、手机确认和账号安全验证均可直接看到并处理。默认 Docker 应用将桌面组件与服务运行在同一个容器，只开放 `18060`，不需要另起 noVNC 容器。部署步骤见 [Docker 部署](./docker/README.md)。

## 启动工作台

本创作台需要从**当前仓库源码**运行或构建。上游历史发行包不代表包含本页面。安装 Go（当前 [go.mod](./go.mod) 要求 **1.27.3**），在包含 go.mod 的项目目录打开终端：

```bash
go run .
```

保持终端运行，在浏览器打开 **http://localhost:18060/**。首次启动会安装内置浏览器，请等待终端提示。源码在 macOS/Windows 上运行时，登录会直接弹出本机 Chromium 窗口；如需在工作台页面里操作浏览器，使用上述单应用 Docker 部署。页面资源已嵌入程序，无需安装 Node。

需要构建可执行文件时：

```bash
go build -o xiaohongshu-studio .
./xiaohongshu-studio
```

Windows 具体步骤见 [Windows 安装指南](./docs/windows_guide.md)。使用容器时按 [Docker 部署说明](./docker/README.md) 从当前源码构建。

## 从选题到发布

1. **配置文字模型。** 打开「设置」，填写 OpenAI 兼容服务的接口地址、API Key 和模型名称。例如接口地址为 `https://api.openai.com/v1`；版本路径由服务商决定，程序不会自动补 `/v1`。可以读取模型列表，也可以手动填写名称。
2. **填写服务访问令牌。** 仅当部署者设置了 `AUTH_TOKEN` 时需要填写。它用于访问工作台服务，与模型 API Key 是两项独立配置。
3. **打开登录浏览器。** 在账号入口点击「打开登录浏览器」，在窗口中的真实小红书页面扫码，并按手机提示确认。出现「请通过验证」等账号安全验证时，按页面要求用已登录该账号的小红书 App 扫码验证；验证期间窗口保持打开。成功保存登录状态后，浏览器和登录弹窗自动关闭。
4. **搜索取材并写稿。** 填写选题、搜索关键词和写作要求，开始搜索并生成草稿。工作台会搜索笔记、读取正文并交给文字模型整理。查看素材来源，调整选择后可以重新写稿。
5. **编辑和配图。** 修改标题、正文、话题和画面描述；上传本地图片，或在设置中单独填写支持 OpenAI `images/generations` 的生图服务地址、Key 和模型。未配置生图服务也能上传图片。
6. **预览并确认发布。** 核对事实、表达、图片顺序和可见范围，在发布确认窗口再次确认。生成草稿不会自动发布。

标题最多 20 字（英文和数字按半字计），正文最多 1000 字。图文笔记需要 1–9 张图片，支持 JPEG、PNG、WebP、GIF，每张不超过 10 MiB。超长草稿会保留供编辑，发布前需缩短。

当前页面围绕图文创作。视频发布、评论、点赞、收藏、用户资料等现有能力可通过 [HTTP API](./docs/API.md) 使用。

## 指纹浏览器与下载源

浏览器固定为 [fingerprint-chromium 148.0.7778.215](https://github.com/adryfish/fingerprint-chromium/releases/tag/148.0.7778.215)，保留原有平台伪装、固定指纹 seed 和 Cookie 配置。默认从该项目官方 GitHub Release 下载，不再依赖原项目作者的自建 CDN。已有浏览器缓存可以继续使用。

源码启动时，可用 `XHS_BROWSER_DOWNLOAD_BASE` 指定兼容镜像，目录须保持 `<基址>/<版本>/<官方文件名>`；Docker 构建使用同名含义的 `BROWSER_DOWNLOAD_BASE` 构建参数或 `.env` 配置。所有来源都必须通过仓库固定的上游 SHA256 校验。部署示例见 [Docker 说明](./docker/README.md)。

## 草稿、账号和数据

- 配置、服务令牌、模型密钥和草稿保存在当前浏览器的 localStorage 中；更换浏览器或清除站点数据后不会自动恢复。在共用电脑上可通过设置清除配置，草稿会保留。
- 服务端不持久化模型配置；写稿时会把选中的素材发送给你配置的模型服务。
- 所有访问者共用服务的小红书登录账号，没有独立用户账户隔离。登录会话保存在服务端 Cookie 文件中，默认使用工作目录的 `cookies.json`，具体路径取决于部署配置。
- 上传和生成的图片保存在服务端系统临时目录的 `xiaohongshu_images/web` 下。草稿中的图片引用不能替代图片文件；系统清理临时目录后需重新上传。默认 Docker 配置将图片持久化到宿主的 `data/uploads/web/`，重建容器时请保留整个数据目录，详见 [Docker 部署说明](./docker/README.md)。

## 常见问题

**页面打不开：** 确认服务终端仍在运行，使用的是启动日志中的端口；默认地址是 http://localhost:18060/。

**提示未授权：** 在设置中填写部署者提供的服务令牌。页面公开可打开，业务接口和图片仍受令牌保护。

**模型请求失败：** 检查地址、Key 和模型名称是否属于同一服务；包含服务商需要的版本路径，生图模型需支持对应的图片接口。

**扫码后仍未登录：** 查看账号弹窗中的真实浏览器画面。普通扫码后还需手机确认，部分账号会出现额外安全验证；验证未完成前不会标记为登录成功。二维码过期时在小红书原页面内刷新；未完成的会话最长保留 20 分钟，也可主动结束。登录信息保存成功后会自动关闭浏览器。

部署时让页面和 API 使用同一地址；浏览器跨站写请求会被拒绝。服务鉴权配置见 [HTTP API 文档](./docs/API.md)。

## 来源与许可证

本项目基于 [xpzouying/xiaohongshu-mcp](https://github.com/xpzouying/xiaohongshu-mcp) 的浏览器自动化与 HTTP 功能改造，当前产品名为「红薯创作台」（xiaohongshu-studio）。原作者版权为 **Copyright 2025 xpzouying**，保留上游贡献者署名、[Apache License 2.0](./LICENSE) 和[历史捐赠记录](./DONATIONS.md)。Go module 路径保留上游标识以记录代码来源。

欢迎按[贡献指南](./CONTRIBUTING.md)提交改进。

<a id="contributors-"></a>

## 🙏 致谢贡献者 ✨

感谢以下所有为本项目做出贡献的朋友！（排名不分先后）

<!-- ALL-CONTRIBUTORS-LIST:START - Do not remove or modify this section -->
<!-- prettier-ignore-start -->
<!-- markdownlint-disable -->
<table>
  <tbody>
    <tr>
      <td align="center" valign="top" width="14.28%"><a href="https://haha.ai"><img src="https://avatars.githubusercontent.com/u/3946563?v=4&size=100" width="100px" height="100px" alt="zy"/><br /><sub><b>zy</b></sub></a><br /><a href="https://github.com/xpzouying/xiaohongshu-mcp/commits?author=xpzouying" title="Code">💻</a> <a href="#ideas-xpzouying" title="Ideas, Planning, & Feedback">🤔</a> <a href="https://github.com/xpzouying/xiaohongshu-mcp/commits?author=xpzouying" title="Documentation">📖</a> <a href="#design-xpzouying" title="Design">🎨</a> <a href="#maintenance-xpzouying" title="Maintenance">🚧</a> <a href="#infra-xpzouying" title="Infrastructure (Hosting, Build-Tools, etc)">🚇</a> <a href="https://github.com/xpzouying/xiaohongshu-mcp/pulls?q=is%3Apr+reviewed-by%3Axpzouying" title="Reviewed Pull Requests">👀</a></td>
      <td align="center" valign="top" width="14.28%"><a href="http://www.hwbuluo.com"><img src="https://avatars.githubusercontent.com/u/1271815?v=4&size=100" width="100px" height="100px" alt="clearwater"/><br /><sub><b>clearwater</b></sub></a><br /><a href="https://github.com/xpzouying/xiaohongshu-mcp/commits?author=esperyong" title="Code">💻</a></td>
      <td align="center" valign="top" width="14.28%"><a href="https://github.com/laryzhong"><img src="https://avatars.githubusercontent.com/u/47939471?v=4&size=100" width="100px" height="100px" alt="Zhongpeng"/><br /><sub><b>Zhongpeng</b></sub></a><br /><a href="https://github.com/xpzouying/xiaohongshu-mcp/commits?author=laryzhong" title="Code">💻</a></td>
      <td align="center" valign="top" width="14.28%"><a href="https://github.com/DTDucas"><img src="https://avatars.githubusercontent.com/u/105262836?v=4&size=100" width="100px" height="100px" alt="Duong Tran"/><br /><sub><b>Duong Tran</b></sub></a><br /><a href="https://github.com/xpzouying/xiaohongshu-mcp/commits?author=DTDucas" title="Code">💻</a></td>
      <td align="center" valign="top" width="14.28%"><a href="https://github.com/Angiin"><img src="https://avatars.githubusercontent.com/u/17389304?v=4&size=100" width="100px" height="100px" alt="Angiin"/><br /><sub><b>Angiin</b></sub></a><br /><a href="https://github.com/xpzouying/xiaohongshu-mcp/commits?author=Angiin" title="Code">💻</a></td>
      <td align="center" valign="top" width="14.28%"><a href="https://github.com/muhenan"><img src="https://avatars.githubusercontent.com/u/43441941?v=4&size=100" width="100px" height="100px" alt="Henan Mu"/><br /><sub><b>Henan Mu</b></sub></a><br /><a href="https://github.com/xpzouying/xiaohongshu-mcp/commits?author=muhenan" title="Code">💻</a></td>
      <td align="center" valign="top" width="14.28%"><a href="https://github.com/chengazhen"><img src="https://avatars.githubusercontent.com/u/52627267?v=4&size=100" width="100px" height="100px" alt="Journey"/><br /><sub><b>Journey</b></sub></a><br /><a href="https://github.com/xpzouying/xiaohongshu-mcp/commits?author=chengazhen" title="Code">💻</a></td>
    </tr>
    <tr>
      <td align="center" valign="top" width="14.28%"><a href="https://github.com/eveyuyi"><img src="https://avatars.githubusercontent.com/u/69026872?v=4&size=100" width="100px" height="100px" alt="Eve Yu"/><br /><sub><b>Eve Yu</b></sub></a><br /><a href="https://github.com/xpzouying/xiaohongshu-mcp/commits?author=eveyuyi" title="Code">💻</a></td>
      <td align="center" valign="top" width="14.28%"><a href="https://github.com/CooperGuo"><img src="https://avatars.githubusercontent.com/u/183056602?v=4&size=100" width="100px" height="100px" alt="CooperGuo"/><br /><sub><b>CooperGuo</b></sub></a><br /><a href="https://github.com/xpzouying/xiaohongshu-mcp/commits?author=CooperGuo" title="Code">💻</a></td>
      <td align="center" valign="top" width="14.28%"><a href="https://biboyqg.github.io/"><img src="https://avatars.githubusercontent.com/u/125724218?v=4&size=100" width="100px" height="100px" alt="Banghao Chi"/><br /><sub><b>Banghao Chi</b></sub></a><br /><a href="https://github.com/xpzouying/xiaohongshu-mcp/commits?author=BiboyQG" title="Code">💻</a></td>
      <td align="center" valign="top" width="14.28%"><a href="https://github.com/varz1"><img src="https://avatars.githubusercontent.com/u/60377372?v=4&size=100" width="100px" height="100px" alt="varz1"/><br /><sub><b>varz1</b></sub></a><br /><a href="https://github.com/xpzouying/xiaohongshu-mcp/commits?author=varz1" title="Code">💻</a></td>
      <td align="center" valign="top" width="14.28%"><a href="https://google.meloguan.site"><img src="https://avatars.githubusercontent.com/u/62586556?v=4&size=100" width="100px" height="100px" alt="Melo Y Guan"/><br /><sub><b>Melo Y Guan</b></sub></a><br /><a href="https://github.com/xpzouying/xiaohongshu-mcp/commits?author=Meloyg" title="Code">💻</a></td>
      <td align="center" valign="top" width="14.28%"><a href="https://github.com/lmxdawn"><img src="https://avatars.githubusercontent.com/u/21293193?v=4&size=100" width="100px" height="100px" alt="lmxdawn"/><br /><sub><b>lmxdawn</b></sub></a><br /><a href="https://github.com/xpzouying/xiaohongshu-mcp/commits?author=lmxdawn" title="Code">💻</a></td>
      <td align="center" valign="top" width="14.28%"><a href="https://github.com/haikow"><img src="https://avatars.githubusercontent.com/u/22428382?v=4&size=100" width="100px" height="100px" alt="haikow"/><br /><sub><b>haikow</b></sub></a><br /><a href="https://github.com/xpzouying/xiaohongshu-mcp/commits?author=haikow" title="Code">💻</a></td>
    </tr>
    <tr>
      <td align="center" valign="top" width="14.28%"><a href="https://carlo-blog.aiju.fun/"><img src="https://avatars.githubusercontent.com/u/18513362?v=4&size=100" width="100px" height="100px" alt="Carlo"/><br /><sub><b>Carlo</b></sub></a><br /><a href="https://github.com/xpzouying/xiaohongshu-mcp/commits?author=a67793581" title="Code">💻</a></td>
      <td align="center" valign="top" width="14.28%"><a href="https://github.com/hrz394943230"><img src="https://avatars.githubusercontent.com/u/28583005?v=4&size=100" width="100px" height="100px" alt="hrz"/><br /><sub><b>hrz</b></sub></a><br /><a href="https://github.com/xpzouying/xiaohongshu-mcp/commits?author=hrz394943230" title="Code">💻</a></td>
      <td align="center" valign="top" width="14.28%"><a href="https://github.com/ctrlz526"><img src="https://avatars.githubusercontent.com/u/143257420?v=4&size=100" width="100px" height="100px" alt="Ctrlz"/><br /><sub><b>Ctrlz</b></sub></a><br /><a href="https://github.com/xpzouying/xiaohongshu-mcp/commits?author=ctrlz526" title="Code">💻</a></td>
      <td align="center" valign="top" width="14.28%"><a href="https://github.com/flippancy"><img src="https://avatars.githubusercontent.com/u/6467703?v=4&size=100" width="100px" height="100px" alt="flippancy"/><br /><sub><b>flippancy</b></sub></a><br /><a href="https://github.com/xpzouying/xiaohongshu-mcp/commits?author=flippancy" title="Code">💻</a></td>
      <td align="center" valign="top" width="14.28%"><a href="https://github.com/Infinityay"><img src="https://avatars.githubusercontent.com/u/103165980?v=4&size=100" width="100px" height="100px" alt="Yuhang Lu"/><br /><sub><b>Yuhang Lu</b></sub></a><br /><a href="https://github.com/xpzouying/xiaohongshu-mcp/commits?author=Infinityay" title="Code">💻</a></td>
      <td align="center" valign="top" width="14.28%"><a href="https://triepod.ai"><img src="https://avatars.githubusercontent.com/u/199543909?v=4&size=100" width="100px" height="100px" alt="Bryan Thompson"/><br /><sub><b>Bryan Thompson</b></sub></a><br /><a href="https://github.com/xpzouying/xiaohongshu-mcp/commits?author=bryankthompson" title="Code">💻</a></td>
      <td align="center" valign="top" width="14.28%"><a href="http://www.megvii.com"><img src="https://avatars.githubusercontent.com/u/7806992?v=4&size=100" width="100px" height="100px" alt="tan jun"/><br /><sub><b>tan jun</b></sub></a><br /><a href="https://github.com/xpzouying/xiaohongshu-mcp/commits?author=tanxxjun321" title="Code">💻</a></td>
    </tr>
    <tr>
      <td align="center" valign="top" width="14.28%"><a href="https://github.com/coldmountein"><img src="https://avatars.githubusercontent.com/u/95873096?v=4&size=100" width="100px" height="100px" alt="coldmountain"/><br /><sub><b>coldmountain</b></sub></a><br /><a href="https://github.com/xpzouying/xiaohongshu-mcp/commits?author=coldmountein" title="Code">💻</a></td>
      <td align="center" valign="top" width="14.28%"><a href="https://blog.litpp.com/"><img src="https://avatars.githubusercontent.com/u/44826388?v=4&size=100" width="100px" height="100px" alt="mamage"/><br /><sub><b>mamage</b></sub></a><br /><a href="https://github.com/xpzouying/xiaohongshu-mcp/commits?author=yqdaddy" title="Code">💻</a> <a href="https://github.com/xpzouying/xiaohongshu-mcp/commits?author=yqdaddy" title="Documentation">📖</a></td>
      <td align="center" valign="top" width="14.28%"><a href="https://runyang.vercel.app/"><img src="https://avatars.githubusercontent.com/u/54588936?v=4&size=100" width="100px" height="100px" alt="Runyang YOU"/><br /><sub><b>Runyang YOU</b></sub></a><br /><a href="https://github.com/xpzouying/xiaohongshu-mcp/commits?author=YRYangang" title="Code">💻</a> <a href="https://github.com/xpzouying/xiaohongshu-mcp/commits?author=YRYangang" title="Documentation">📖</a></td>
      <td align="center" valign="top" width="14.28%"><a href="https://www.hnfnu.edu.cn/"><img src="https://avatars.githubusercontent.com/u/134906805?v=4&size=100" width="100px" height="100px" alt="e0_7"/><br /><sub><b>e0_7</b></sub></a><br /><a href="https://github.com/xpzouying/xiaohongshu-mcp/commits?author=Daily-AC" title="Code">💻</a> <a href="https://github.com/xpzouying/xiaohongshu-mcp/commits?author=Daily-AC" title="Documentation">📖</a></td>
      <td align="center" valign="top" width="14.28%"><a href="https://github.com/prehisle"><img src="https://avatars.githubusercontent.com/u/2081344?v=4&size=100" width="100px" height="100px" alt="prehisle"/><br /><sub><b>prehisle</b></sub></a><br /><a href="https://github.com/xpzouying/xiaohongshu-mcp/commits?author=prehisle" title="Code">💻</a> <a href="https://github.com/xpzouying/xiaohongshu-mcp/commits?author=prehisle" title="Documentation">📖</a></td>
      <td align="center" valign="top" width="14.28%"><a href="https://github.com/blablabiu"><img src="https://avatars.githubusercontent.com/u/123888078?v=4&size=100" width="100px" height="100px" alt="Xinhao Chen"/><br /><sub><b>Xinhao Chen</b></sub></a><br /><a href="https://github.com/xpzouying/xiaohongshu-mcp/commits?author=blablabiu" title="Code">💻</a> <a href="https://github.com/xpzouying/xiaohongshu-mcp/commits?author=blablabiu" title="Documentation">📖</a></td>
      <td align="center" valign="top" width="14.28%"><a href="https://github.com/qiuxsgit"><img src="https://avatars.githubusercontent.com/u/15036686?v=4&size=100" width="100px" height="100px" alt="qiuxsgit"/><br /><sub><b>qiuxsgit</b></sub></a><br /><a href="https://github.com/xpzouying/xiaohongshu-mcp/commits?author=qiuxsgit" title="Code">💻</a></td>
    </tr>
    <tr>
      <td align="center" valign="top" width="14.28%"><a href="https://github.com/ZhuYichuan"><img src="https://avatars.githubusercontent.com/u/7954801?v=4&size=100" width="100px" height="100px" alt="openlts"/><br /><sub><b>openlts</b></sub></a><br /><a href="https://github.com/xpzouying/xiaohongshu-mcp/commits?author=ZhuYichuan" title="Code">💻</a></td>
      <td align="center" valign="top" width="14.28%"><a href="https://github.com/galois1983"><img src="https://avatars.githubusercontent.com/u/80469442?v=4&size=100" width="100px" height="100px" alt="galois1983"/><br /><sub><b>galois1983</b></sub></a><br /><a href="https://github.com/xpzouying/xiaohongshu-mcp/commits?author=galois1983" title="Code">💻</a></td>
      <td align="center" valign="top" width="14.28%"><a href="https://github.com/xPeiPeix"><img src="https://avatars.githubusercontent.com/u/42157266?v=4&size=100" width="100px" height="100px" alt="Peip"/><br /><sub><b>Peip</b></sub></a><br /><a href="https://github.com/xpzouying/xiaohongshu-mcp/commits?author=xPeiPeix" title="Code">💻</a></td>
      <td align="center" valign="top" width="14.28%"><a href="https://github.com/zhenzoo"><img src="https://avatars.githubusercontent.com/u/273501489?v=4&size=100" width="100px" height="100px" alt="zhenzoo"/><br /><sub><b>zhenzoo</b></sub></a><br /><a href="https://github.com/xpzouying/xiaohongshu-mcp/commits?author=zhenzoo" title="Code">💻</a></td>
      <td align="center" valign="top" width="14.28%"><a href="https://github.com/eeeggplant"><img src="https://avatars.githubusercontent.com/u/73321941?v=4&size=100" width="100px" height="100px" alt="eggplant"/><br /><sub><b>eggplant</b></sub></a><br /><a href="https://github.com/xpzouying/xiaohongshu-mcp/commits?author=eeeggplant" title="Code">💻</a></td>
      <td align="center" valign="top" width="14.28%"><a href="https://github.com/Iams4kura"><img src="https://avatars.githubusercontent.com/u/126048986?v=4&size=100" width="100px" height="100px" alt="s4kura"/><br /><sub><b>s4kura</b></sub></a><br /><a href="https://github.com/xpzouying/xiaohongshu-mcp/commits?author=Iams4kura" title="Code">💻</a></td>
      <td align="center" valign="top" width="14.28%"><a href="https://github.com/Matrixqlc"><img src="https://avatars.githubusercontent.com/u/129405195?v=4&size=100" width="100px" height="100px" alt="Matrixqlc"/><br /><sub><b>Matrixqlc</b></sub></a><br /><a href="https://github.com/xpzouying/xiaohongshu-mcp/commits?author=Matrixqlc" title="Code">💻</a></td>
    </tr>
    <tr>
      <td align="center" valign="top" width="14.28%"><a href="https://www.zhihu.com/people/lim0-34"><img src="https://avatars.githubusercontent.com/u/39004676?v=4&size=100" width="100px" height="100px" alt="Qu Yuli"/><br /><sub><b>Qu Yuli</b></sub></a><br /><a href="https://github.com/xpzouying/xiaohongshu-mcp/commits?author=DLLXW" title="Code">💻</a></td>
      <td align="center" valign="top" width="14.28%"><a href="https://github.com/ao-lj-ao"><img src="https://avatars.githubusercontent.com/u/287673796?v=4&size=100" width="100px" height="100px" alt="ao-lj-ao"/><br /><sub><b>ao-lj-ao</b></sub></a><br /><a href="https://github.com/xpzouying/xiaohongshu-mcp/commits?author=ao-lj-ao" title="Code">💻</a></td>
      <td align="center" valign="top" width="14.28%"><a href="https://github.com/CNQQC"><img src="https://avatars.githubusercontent.com/u/243505512?v=4&size=100" width="100px" height="100px" alt="CNQQC"/><br /><sub><b>CNQQC</b></sub></a><br /><a href="https://github.com/xpzouying/xiaohongshu-mcp/commits?author=CNQQC" title="Code">💻</a></td>
      <td align="center" valign="top" width="14.28%"><a href="https://github.com/ACCLE123"><img src="https://avatars.githubusercontent.com/u/95094975?v=4&size=100" width="100px" height="100px" alt="ACCLE123"/><br /><sub><b>ACCLE123</b></sub></a><br /><a href="https://github.com/xpzouying/xiaohongshu-mcp/commits?author=ACCLE123" title="Code">💻</a></td>
      <td align="center" valign="top" width="14.28%"><a href="https://github.com/SamYunquChen"><img src="https://avatars.githubusercontent.com/u/106165995?v=4&size=100" width="100px" height="100px" alt="SamYunquChen"/><br /><sub><b>SamYunquChen</b></sub></a><br /><a href="https://github.com/xpzouying/xiaohongshu-mcp/commits?author=SamYunquChen" title="Code">💻</a></td>
      <td align="center" valign="top" width="14.28%"><a href="https://github.com/kalivid"><img src="https://avatars.githubusercontent.com/u/22721966?v=4&size=100" width="100px" height="100px" alt="kalivid"/><br /><sub><b>kalivid</b></sub></a><br /><a href="https://github.com/xpzouying/xiaohongshu-mcp/commits?author=kalivid" title="Code">💻</a></td>
    </tr>
  </tbody>
</table>

<!-- markdownlint-restore -->
<!-- prettier-ignore-end -->

<!-- ALL-CONTRIBUTORS-LIST:END -->

### ✨ 特别感谢

<table>
  <tbody>
    <tr>
      <td align="center" valign="top" width="20%"><a href="https://github.com/wanpengxie"><img src="https://avatars.githubusercontent.com/wanpengxie" width="130px;" alt="wanpengxie"/><br /><sub><b>@wanpengxie</b></sub></a></td>
      <td align="center" valign="top" width="20%"><a href="https://github.com/tanxxjun321"><img src="https://avatars.githubusercontent.com/u/7806992?v=4" width="130px;" alt="tanxxjun321"/><br /><sub><b>@tanxxjun321</b></sub></a></td>
      <td align="center" valign="top" width="20%"><a href="https://github.com/Angiin"><img src="https://avatars.githubusercontent.com/u/17389304?v=4" width="130px;" alt="Angiin"/><br /><sub><b>@Angiin</b></sub></a></td>
    </tr>
  </tbody>
</table>

本项目遵循 [all-contributors](https://github.com/all-contributors/all-contributors) 规范。欢迎任何形式的贡献！

## 📄 许可证

本项目采用 [Apache License 2.0](LICENSE) 开源。

你可以自由地使用、修改和分发本项目，包括用于商业用途，只需保留原始的版权声明和许可证文件。详细条款以 [LICENSE](LICENSE) 文件为准。

向本项目提交的贡献，默认按同一许可证授权。
