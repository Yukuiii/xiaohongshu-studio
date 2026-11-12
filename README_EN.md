# Xiaohongshu Studio · 红薯创作台

[中文](./README.md) · [Windows setup](./docs/windows_guide.md) · [Docker deployment](./docker/README.md) · [HTTP API](./docs/API.md)

A Chinese-language browser workspace for researching Xiaohongshu topics, drafting posts, adding images, and confirming publication. Open **http://localhost:18060/** after starting the service, or use the workspace URL supplied by your administrator.

Sign-in uses the complete Xiaohongshu browser page, including phone confirmation and account-security checks. The default Docker application includes Chromium and the noVNC desktop in one container and exposes only port `18060`; no separate noVNC container is needed. See [Docker deployment](./docker/README.md).

## Start the workspace

Run or build from **this repository's current source**. Historical upstream downloads do not establish availability of this workspace. Install Go (**1.27.4**, as specified in [go.mod](./go.mod)), then run this in the project directory containing go.mod:

```bash
go run .
```

Keep the terminal open and visit **http://localhost:18060/**. First startup installs the bundled browser. Running source directly on macOS/Windows opens a visible local Chromium window for sign-in; use the single-application Docker deployment for the browser embedded in the workspace. Frontend assets are embedded and need no Node installation.

To build an executable:

```bash
go build -o xiaohongshu-studio .
./xiaohongshu-studio
```

See the [Windows guide](./docs/windows_guide.md) for PowerShell commands. For containers, follow the [Docker guide](./docker/README.md) to build from the current source.

## From topic to publication

1. **Configure a text model.** Open Settings (设置) and enter an OpenAI-compatible base URL, API key, and model name. Include the provider's version path, for example `https://api.openai.com/v1`; the application does not append `/v1`. Fetch the model list or enter a name manually.
2. **Enter a service token if required.** When the administrator enables `AUTH_TOKEN`, enter that token in Settings. It is separate from the model provider's API key.
3. **Open the login browser.** Use the account panel to open the real Xiaohongshu page, scan the code, and confirm on your phone. Complete any additional account-security verification using the mobile app already signed into that account. The browser stays open during verification; after the login information is saved successfully, the browser and account dialog close automatically.
4. **Research and draft.** Enter a topic, search keywords, and writing instructions. The workspace searches notes, reads their content, and sends the selected sources to your text model. Review source material and change the selection before drafting again.
5. **Edit and add images.** Edit the title, body, tags, and image description. Upload local pictures or configure a separate image provider's URL, key, and model supporting OpenAI `images/generations`. Uploading images works without an image model.
6. **Review and confirm.** Check facts, wording, image order, preview, and visibility. Confirm again in the publication dialog. Generating a draft never publishes it automatically.

Titles allow 20 counted characters (English letters and digits count as half); bodies allow 1,000 characters. Image posts require 1–9 JPEG, PNG, WebP, or GIF files, at most 10 MiB each. Overlong drafts remain editable and must be shortened before publication.

The current page focuses on image posts. Existing video publication, comments, likes, favorites, and profile operations remain available through the [HTTP API](./docs/API.md).

## Fingerprint browser and downloads

The application pins [fingerprint-chromium 148.0.7778.215](https://github.com/adryfish/fingerprint-chromium/releases/tag/148.0.7778.215), retaining platform spoofing, the fixed fingerprint seed, and Cookie settings. New downloads use that project's official GitHub Release instead of the original application's private CDN. Existing browser caches remain usable.

For source launches, `XHS_BROWSER_DOWNLOAD_BASE` selects a compatible mirror serving `<base>/<version>/<official-filename>`. Docker builds use the `BROWSER_DOWNLOAD_BASE` argument or Compose `.env` setting. Every source is checked against the upstream SHA256 pinned in the repository. See [Docker configuration](./docker/README.md).

## Drafts, accounts, and storage

- Settings, service tokens, model keys, and drafts use the current browser's localStorage. They do not follow you to another browser and are lost when site data is cleared. Clear configuration in Settings on shared computers; that action preserves the draft.
- The server does not persist model configuration. Selected research material is sent to your configured model provider for drafting.
- Everyone using one service shares its Xiaohongshu account; there are no isolated user accounts. Login sessions live in the server's Cookie file, normally `cookies.json` in the working directory, depending on deployment configuration.
- Uploaded and generated images live under `xiaohongshu_images/web` in the server's system temporary directory. Saved draft references are not backups of image files; upload again if temporary files are removed. The default Docker configuration persists images under the host's `data/uploads/web/`; retain the entire data directory when rebuilding containers. See [Docker deployment](./docker/README.md).

## Troubleshooting

**Cannot open the page:** Keep the service running and check its configured port. The default URL is http://localhost:18060/.

**Unauthorized requests:** Enter the administrator's service token in Settings. The page is public, while protected API calls and image reads require authentication.

**Model requests fail:** Check the provider URL, required version path, key, and model name. Image generation needs a model supporting the image endpoint.

**Sign-in does not complete:** Inspect the actual browser inside the account panel. Scanning can require phone confirmation and additional account-security verification. Refresh an expired QR code in the Xiaohongshu page itself. Pending sessions remain available for up to 20 minutes and can be ended manually; a successful saved login closes the browser automatically.

Serve the page and API from the same origin; cross-site browser write requests are rejected. See the [HTTP API guide](./docs/API.md) for service authentication.

## Origin and license

This project builds on the browser automation and HTTP functionality of [xpzouying/xiaohongshu-mcp](https://github.com/xpzouying/xiaohongshu-mcp). The current product is Xiaohongshu Studio (红薯创作台). Original attribution is **Copyright 2025 xpzouying**. The [Apache License 2.0](./LICENSE), upstream contributor credits, and [historical donation records](./DONATIONS.md) are retained. The Go module path keeps the upstream source identifier.

See the [contributing guide](./CONTRIBUTING.md) for improvements.

<a id="contributors-"></a>

## 🙏 Thanks to Contributors ✨

Thanks to all friends who have contributed to this project! (In no particular order)

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

### ✨ Special Thanks

<table>
  <tbody>
    <tr>
      <td align="center" valign="top" width="20%"><a href="https://github.com/wanpengxie"><img src="https://avatars.githubusercontent.com/wanpengxie" width="130px;" alt="wanpengxie"/><br /><sub><b>@wanpengxie</b></sub></a></td>
      <td align="center" valign="top" width="20%"><a href="https://github.com/tanxxjun321"><img src="https://avatars.githubusercontent.com/u/7806992?v=4" width="130px;" alt="tanxxjun321"/><br /><sub><b>@tanxxjun321</b></sub></a></td>
      <td align="center" valign="top" width="20%"><a href="https://github.com/Angiin"><img src="https://avatars.githubusercontent.com/u/17389304?v=4" width="130px;" alt="Angiin"/><br /><sub><b>@Angiin</b></sub></a></td>
    </tr>
  </tbody>
</table>

This project follows the [all-contributors](https://github.com/all-contributors/all-contributors) specification. Contributions of any kind welcome!

## 📄 License

This project is open source under the [Apache License 2.0](LICENSE).

You are free to use, modify and distribute this project, including for commercial purposes, as long as you keep the original copyright notice and license file. The [LICENSE](LICENSE) file is the authoritative source for the full terms.

Contributions submitted to this project are licensed under the same license by default.
