import { createHash } from "node:crypto";
import { execFileSync, spawnSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const scriptDir = path.dirname(fileURLToPath(import.meta.url));
const desktopDir = path.resolve(scriptDir, "..");
const repoRoot = path.resolve(desktopDir, "..");
const tauriDir = path.join(desktopDir, "src-tauri");
const binariesDir = path.join(tauriDir, "binaries");
const browserTargetDir = path.join(tauriDir, "resources", "browser");

const args = new Set(process.argv.slice(2));
const buildWeb = args.has("--build-web");
const skipBrowser = args.has("--skip-browser");
const skipSidecar = args.has("--skip-sidecar");

function targetConfig() {
  if (process.platform === "darwin" && process.arch === "arm64") {
    return {
      triple: "aarch64-apple-darwin",
      goos: "darwin",
      goarch: "arm64",
      ext: "",
      asset: (ver) => `ungoogled-chromium_${ver}-1.1_macos.dmg`,
      binaryName: "Chromium",
      cacheBase: path.join(os.homedir(), "Library", "Caches"),
    };
  }
  if (process.platform === "win32" && process.arch === "x64") {
    return {
      triple: "x86_64-pc-windows-msvc",
      goos: "windows",
      goarch: "amd64",
      ext: ".exe",
      asset: (ver) => `ungoogled-chromium_${ver}-1.1_windows_x64.zip`,
      binaryName: "chrome.exe",
      cacheBase:
        process.env.LOCALAPPDATA || path.join(os.homedir(), "AppData", "Local"),
    };
  }
  throw new Error(
    `桌面客户端仅支持 macOS ARM64 与 Windows x64，当前环境为 ${process.platform}/${process.arch}`,
  );
}

function run(cmd, cmdArgs, cwd = repoRoot, extraEnv = {}) {
  const result = spawnSync(cmd, cmdArgs, {
    cwd,
    stdio: "inherit",
    env: { ...process.env, ...extraEnv },
    shell: process.platform === "win32" && cmd === "npm",
  });
  if (result.error) {
    throw result.error;
  }
  if (result.status !== 0) {
    throw new Error(`命令执行失败 (${result.status}): ${cmd} ${cmdArgs.join(" ")}`);
  }
}

function findFileNamed(dir, name) {
  if (!fs.existsSync(dir)) return "";
  const stack = [dir];
  while (stack.length > 0) {
    const current = stack.pop();
    for (const entry of fs.readdirSync(current, { withFileTypes: true })) {
      const fullPath = path.join(current, entry.name);
      if (entry.isDirectory()) {
        stack.push(fullPath);
      } else if (entry.name === name) {
        return fullPath;
      }
    }
  }
  return "";
}

function expectedChecksum(assetName) {
  const raw = fs.readFileSync(
    path.join(repoRoot, "browser", "browser_checksums.txt"),
    "utf8",
  );
  for (const line of raw.split(/\r?\n/)) {
    const parts = line.trim().split(/\s+/);
    if (parts.length === 2 && parts[1] === assetName) {
      return parts[0].toLowerCase();
    }
  }
  throw new Error(`browser_checksums.txt 中未找到 ${assetName}`);
}

async function downloadFile(url, destPath) {
  const response = await fetch(url);
  if (!response.ok) {
    throw new Error(`下载失败 HTTP ${response.status}: ${url}`);
  }
  const buffer = Buffer.from(await response.arrayBuffer());
  fs.writeFileSync(destPath, buffer);
}

function verifySha256(filePath, want) {
  const hash = createHash("sha256");
  hash.update(fs.readFileSync(filePath));
  const got = hash.digest("hex").toLowerCase();
  if (got !== want) {
    throw new Error(`SHA256 校验失败: 期望 ${want}, 实际 ${got}`);
  }
}

function copyDirPreservingSymlinks(src, dest) {
  fs.mkdirSync(dest, { recursive: true });
  if (process.platform === "darwin") {
    for (const entry of fs.readdirSync(src)) {
      execFileSync("cp", ["-a", path.join(src, entry), dest]);
    }
    return;
  }
  fs.cpSync(src, dest, { recursive: true, verbatimSymlinks: true });
}

function extractArchive(archivePath, destDir) {
  fs.mkdirSync(destDir, { recursive: true });
  if (archivePath.endsWith(".dmg")) {
    const mountPoint = fs.mkdtempSync(path.join(os.tmpdir(), "xhs-dmg-"));
    try {
      execFileSync("hdiutil", [
        "attach",
        archivePath,
        "-nobrowse",
        "-mountpoint",
        mountPoint,
      ]);
      const appName = fs
        .readdirSync(mountPoint)
        .find((name) => name.endsWith(".app"));
      if (!appName) {
        throw new Error("DMG 中未找到 .app");
      }
      const dstApp = path.join(destDir, appName);
      execFileSync("cp", ["-a", path.join(mountPoint, appName), dstApp]);
      spawnSync("xattr", ["-dr", "com.apple.quarantine", dstApp], {
        stdio: "ignore",
      });
    } finally {
      spawnSync("hdiutil", ["detach", mountPoint, "-quiet"], {
        stdio: "ignore",
      });
      fs.rmSync(mountPoint, { recursive: true, force: true });
    }
    return;
  }
  if (archivePath.endsWith(".zip")) {
    execFileSync("powershell", [
      "-NoProfile",
      "-Command",
      `Expand-Archive -LiteralPath '${archivePath.replace(/'/g, "''")}' -DestinationPath '${destDir.replace(/'/g, "''")}' -Force`,
    ]);
    return;
  }
  throw new Error(`不支持的浏览器包格式: ${archivePath}`);
}

async function prepareBrowser(cfg) {
  const version = fs
    .readFileSync(path.join(repoRoot, "browser", "browser_version.txt"), "utf8")
    .trim();
  const versionMarker = path.join(browserTargetDir, ".browser-version");
  const macArchive = path.join(browserTargetDir, "chromium.tar.gz");
  const macArchiveSha = path.join(browserTargetDir, "chromium.tar.gz.sha256");
  const macReady =
    cfg.goos === "darwin" &&
    fs.existsSync(macArchive) &&
    fs.existsSync(macArchiveSha);
  const unpackedReady =
    cfg.goos !== "darwin" &&
    Boolean(findFileNamed(browserTargetDir, cfg.binaryName));
  if (
    fs.existsSync(versionMarker) &&
    fs.readFileSync(versionMarker, "utf8").trim() === version &&
    (macReady || unpackedReady)
  ) {
    console.log(`预置指纹浏览器 ${version} 已就绪`);
    return;
  }

  fs.rmSync(browserTargetDir, { recursive: true, force: true });
  fs.mkdirSync(browserTargetDir, { recursive: true });

  // macOS 上 Tauri bundler 复制资源会解引用 .framework 符号链接并触发嵌套 .app 签名问题，
  // 因此打包为保留符号链接的 tar.gz，在桌面端首次启动时原地解压一次。
  const stagingDir =
    cfg.goos === "darwin"
      ? fs.mkdtempSync(path.join(os.tmpdir(), "xhs-browser-stage-"))
      : browserTargetDir;

  try {
    const localCache = path.join(
      cfg.cacheBase,
      "xiaohongshu-mcp",
      "browser",
      version,
    );
    if (findFileNamed(localCache, cfg.binaryName)) {
      console.log(`复用本机缓存的指纹浏览器 ${version}: ${localCache}`);
      copyDirPreservingSymlinks(localCache, stagingDir);
    } else {
      const assetName = cfg.asset(version);
      const wantSha = expectedChecksum(assetName);
      const base = (
        process.env.XHS_BROWSER_DOWNLOAD_BASE ||
        "https://github.com/adryfish/fingerprint-chromium/releases/download"
      ).replace(/\/+$/, "");
      const url = `${base}/${version}/${assetName}`;
      const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), "xhs-browser-dl-"));
      const archivePath = path.join(tmpDir, assetName);
      try {
        console.log(`下载并校验预置指纹浏览器 ${version}: ${url}`);
        await downloadFile(url, archivePath);
        verifySha256(archivePath, wantSha);
        extractArchive(archivePath, stagingDir);
      } finally {
        fs.rmSync(tmpDir, { recursive: true, force: true });
      }
    }

    const bin = findFileNamed(stagingDir, cfg.binaryName);
    if (!bin) {
      throw new Error(`预置浏览器目录中未找到 ${cfg.binaryName}`);
    }

    if (cfg.goos === "darwin") {
      console.log(`打包 macOS 指纹浏览器归档以保留 framework 符号链接: ${macArchive}`);
      execFileSync("tar", ["-czf", macArchive, "-C", stagingDir, "."]);
      const digest = createHash("sha256")
        .update(fs.readFileSync(macArchive))
        .digest("hex");
      fs.writeFileSync(macArchiveSha, `${digest}\n`);
      console.log(`预置指纹浏览器归档已就绪: ${macArchive}`);
    } else {
      console.log(`预置指纹浏览器已就绪: ${bin}`);
    }
    fs.writeFileSync(versionMarker, `${version}\n`);
  } finally {
    if (stagingDir !== browserTargetDir) {
      fs.rmSync(stagingDir, { recursive: true, force: true });
    }
  }
}

function prepareSidecar(cfg) {
  const distIndex = path.join(repoRoot, "web", "dist", "index.html");
  if (buildWeb || !fs.existsSync(distIndex)) {
    if (!fs.existsSync(path.join(repoRoot, "web", "node_modules"))) {
      run("npm", ["--prefix", "web", "ci"]);
    }
    run("npm", ["--prefix", "web", "run", "build"]);
  }

  fs.mkdirSync(binariesDir, { recursive: true });
  const version = process.env.VERSION || "dev";
  const out = path.join(
    binariesDir,
    `xiaohongshu-studio-${cfg.triple}${cfg.ext}`,
  );
  console.log(`构建 Go Sidecar (${cfg.goos}/${cfg.goarch}): ${out}`);
  run(
    "go",
    ["build", `-ldflags=-s -w -X main.version=${version}`, "-o", out, "."],
    repoRoot,
    {
      CGO_ENABLED: "0",
      GOOS: cfg.goos,
      GOARCH: cfg.goarch,
    },
  );
}

const cfg = targetConfig();
if (!skipSidecar) {
  prepareSidecar(cfg);
}
if (!skipBrowser) {
  await prepareBrowser(cfg);
}
