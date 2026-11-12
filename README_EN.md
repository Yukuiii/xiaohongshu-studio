# Xiaohongshu Studio · 红薯创作台

[中文](./README.md) · [Windows setup](./docs/windows_guide.md) · [Docker deployment](./docker/README.md) · [HTTP API](./docs/API.md)

A Chinese-language browser workspace for researching Xiaohongshu topics, drafting posts, adding images, and confirming publication. Open **http://localhost:18060/** after starting the service, or use the workspace URL supplied by your administrator.

The frontend uses Vite + React. The left sidebar provides Workspace (工作台) for research, drafting, images, and publication; Accounts (账号) for multiple Xiaohongshu accounts and their separate browser data; and Settings (设置) for models and the service token. Add, select, or sign into an account from Accounts using the real sign-in page.

Sign-in uses the complete Xiaohongshu browser page, including phone confirmation and account-security checks. The default Docker application includes Chromium and the noVNC desktop in one container and exposes only port `18060`; no separate noVNC container is needed. See [Docker deployment](./docker/README.md).

## Start the workspace

Run or build from **this repository's current source**. Historical upstream downloads do not establish availability of this workspace. Install Go (**1.27.4**, as specified in [go.mod](./go.mod)) and **Node.js 24**, then build the frontend from the project directory containing go.mod:

```bash
npm --prefix web ci
npm --prefix web run build
go run .
```

Keep the terminal open and visit **http://localhost:18060/**. First startup installs the bundled browser. Running source directly on macOS/Windows opens a visible local Chromium window for sign-in; use the single-application Docker deployment for the browser embedded in the workspace. Go embeds the page and hashed assets from `web/dist/`; the compiled executable and production container require no Node installation at runtime.

To build an executable, refresh the frontend output before compiling Go:

```bash
npm --prefix web ci
npm --prefix web run build
go build -o xiaohongshu-studio .
./xiaohongshu-studio
```

See the [Windows guide](./docs/windows_guide.md) for PowerShell commands. For containers, follow the [Docker guide](./docker/README.md) to build from the current source.

## Frontend development

Install dependencies and build `web/dist/` once using the commands above, then run these in separate terminals:

```bash
# Terminal one: Go API, port 18060 by default
go run .
```

```bash
# Terminal two: Vite with hot updates, port 5173 by default
npm --prefix web run dev
```

Visit **http://localhost:5173/** for development. Vite proxies `/api`, `/desktop` (including WebSocket), and `/health` to `http://127.0.0.1:18060`, so the page, API, and login desktop share a browser origin. Port `5173` serves development; Go serves production on `18060`. After frontend changes, run `npm --prefix web run build` and rebuild or restart Go to refresh the embedded page. Go compilation fails if `web/dist/` is missing.


## From topic to publication

1. **Configure a text model.** Open Settings (设置) and enter an OpenAI-compatible base URL, API key, and model name. Include the provider's version path, for example `https://api.openai.com/v1`; the application does not append `/v1`. Fetch the model list or enter a name manually.
2. **Enter a service token if required.** When the administrator enables `AUTH_TOKEN`, enter that token in Settings. It is separate from the model provider's API key.
3. **Add, select, and sign into an account.** Manage account names, choose an account, or add one in Accounts (账号), then open its real Xiaohongshu sign-in page. Scan and confirm on your phone, including any security checks using the mobile app already signed into that account. Sign-in is saved only after the real identity matches the binding and browser data reaches disk. Failed sign-in preserves the previous browser data. The shared login desktop displays one account at a time.
4. **Research and draft.** Enter a topic, search keywords, and writing instructions. The workspace searches notes, reads their content, and sends the selected sources to your text model. Review source material and change the selection before drafting again.
5. **Edit and add images.** Edit the title, body, tags, and image description. Upload local pictures or configure a separate image provider's URL, key, and model supporting OpenAI `images/generations`. Uploading images works without an image model.
6. **Review and confirm.** Check facts, wording, image order, preview, and visibility. Confirm again in the publication dialog. Generating a draft never publishes it automatically.

The page uses Web Locks to coordinate publication across tabs for the same account. Use a supporting browser over HTTPS or `localhost` / a loopback address. For a remote HTTP deployment, forward the service to your local machine through SSH before opening the workspace.

Titles allow 20 counted characters (English letters and digits count as half); bodies allow 1,000 characters. Image posts require 1–9 JPEG, PNG, WebP, or GIF files, at most 10 MiB each. Overlong drafts remain editable and must be shortened before publication.

The current page focuses on image posts. Existing video publication, comments, likes, favorites, and profile operations remain available through the [HTTP API](./docs/API.md).

## Fingerprint browser and downloads

The application pins [fingerprint-chromium 148.0.7778.215](https://github.com/adryfish/fingerprint-chromium/releases/tag/148.0.7778.215), retaining platform spoofing, the fixed fingerprint seed, and Cookie settings. New downloads use that project's official GitHub Release instead of the original application's private CDN. Existing browser caches remain usable.

For source launches, `XHS_BROWSER_DOWNLOAD_BASE` selects a compatible mirror serving `<base>/<version>/<official-filename>`. Docker builds use the `BROWSER_DOWNLOAD_BASE` argument or Compose `.env` setting. Every source is checked against the upstream SHA256 pinned in the repository. See [Docker configuration](./docker/README.md).

## Drafts, accounts, and storage

- Settings, service tokens, and model keys remain in browser localStorage under the shared `hongshu-workbench.settings.v1` key. Each account's draft uses `hongshu-workbench.draft.v1:<account-id>` with the existing payload. Publication protection is stored separately in `hongshu-workbench.publish.v1:<account-id>`, so ordinary draft saves cannot overwrite newer publication state from another tab. Old drafts migrate once to the fixed legacy account, preserve existing destination drafts, and retain duplicate prevention for uncertain publication results. Keep the same browser and workspace address; changing either or clearing site data does not restore data automatically. Ports `5173` and `18060` have separate storage.
- The server does not persist model configuration. Selected research material is sent to your configured provider. Models and the service token are shared across the workspace; the token grants access to all accounts, and account slots do not isolate visitors' permissions.
- Each account owns a fixed seed, downloads, and a complete Chromium user-data-dir, including Cookies, localStorage, IndexedDB, Service Workers, cache, and site configuration. `XHS_ACCOUNTS_DIR` sets the root; otherwise it is the hidden `.xhs-accounts/` directory beside the legacy Cookie file. Metadata and profiles live under `<root>/<account-id>/`. Browsers start on demand and are reused between tasks; idle headless instances close after 10 minutes while retaining data. A one-use snapshot inside the profile carries session Cookies across normal shutdowns without reopening old pages; abnormal exits may require sign-in again.
- Selection belongs to the current page, and every request binds its account using `X-XHS-Account-ID`. Switching does not alter other tabs. The workspace prevents switching during a task; each account serializes browser work, while different accounts run independently. The shared desktop supports one sign-in window at a time. A previously verified account can still have an expired site session.
- First initialization creates a fixed default account and imports the old v1/v2 `cookies.json` or `COOKIES_PATH` source once, retaining the original file. The default account preserves a valid migration-time `XHS_FP_SEED`, otherwise its file seed or a new seed. New accounts generate independent seeds. Complete profiles become the session authority thereafter. Removing the default account does not reimport legacy data; migration cannot recover site data older versions never saved.
- Uploaded and generated images use `xiaohongshu_images/web/<account-id>/` in the server's temporary directory. Old images migrate only to the fixed legacy account, retaining source files. Draft references are not image backups; upload again if files are cleared. Docker persists images in `data/uploads/web/` and accounts in `data/accounts/`; stop the service and back up the entire `data/` directory. Profiles are intended for the same machine and runtime environment; sessions are not guaranteed across operating systems.
- Reset browser data clears site data while retaining the account ID, identity binding, seed, drafts, and images. Remove account deletes its browser data, downloads, managed images, and this browser's draft; neither operation is allowed during a task or sign-in. The compatibility `DELETE /api/v1/login/cookies` operation clears only the selected browser's Cookies.

## Offline sign-in maintenance

Stop the service using the same data root, then run this from the source directory, replacing `ACCOUNT_ID` with an ID from Accounts or `GET /api/v1/accounts`:

```bash
go run ./cmd/login -account ACCOUNT_ID
```

The CLI uses the service's `XHS_ACCOUNTS_DIR`, default directory rules, and process lock. Omit `-account` only when exactly one live account exists; zero or multiple accounts require an explicit selection. Sign-in uses a candidate profile and activates it only after verifying identity and shutting down the browser to flush data. Failure preserves the original profile.

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
