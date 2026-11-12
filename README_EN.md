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

This project builds on the browser automation and HTTP functionality of [xpzouying/xiaohongshu-mcp](https://github.com/xpzouying/xiaohongshu-mcp). The current product is Xiaohongshu Studio (红薯创作台). Original attribution is **Copyright 2025 xpzouying**. Licensed under the [Apache License 2.0](./LICENSE). The Go module path keeps the upstream identifier for compatibility with existing internal imports.

See the [contributing guide](./CONTRIBUTING.md) for improvements.

## 📄 License

This project is open source under the [Apache License 2.0](LICENSE).

You are free to use, modify and distribute this project, including for commercial purposes, as long as you keep the original copyright notice and license file. The [LICENSE](LICENSE) file is the authoritative source for the full terms.

Contributions submitted to this project are licensed under the same license by default.
