package browser

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/ulikunitz/xz"
)

// 内置浏览器默认从上游 GitHub release 下载，可配置相同目录结构的镜像。
const browserDownloadBase = "https://github.com/adryfish/fingerprint-chromium/releases/download"

// browserVersion 是内置浏览器的版本源，升级时需同步更新固定的上游校验清单。
//
//go:embed browser_version.txt
var browserVersionRaw string

// browserChecksums 固定上游 release 的 SHA256，不信任下载镜像提供的校验文件。
//
//go:embed browser_checksums.txt
var browserChecksums string

var browserVersion = strings.TrimSpace(browserVersionRaw)

func browserURL(name string) (string, error) {
	base := os.Getenv("XHS_BROWSER_DOWNLOAD_BASE")
	if base == "" {
		base = browserDownloadBase
	}
	base = strings.TrimSpace(base)
	u, err := url.Parse(base)
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.Opaque != "" {
		return "", fmt.Errorf("XHS_BROWSER_DOWNLOAD_BASE 必须是有效的 http(s) 绝对地址")
	}
	if u.User != nil || strings.ContainsAny(base, "?#") {
		return "", fmt.Errorf("XHS_BROWSER_DOWNLOAD_BASE 不允许包含用户名、密码、查询参数或片段")
	}
	// 直接拼接保留 GitHub 代理前缀路径中的 https://，避免路径规范化破坏镜像地址。
	return strings.TrimRight(base, "/") + "/" + browserVersion + "/" + name, nil
}

// platformAsset 返回当前 OS/arch 对应的下载文件名与解压后二进制文件名。
// 第三个返回值为 false 表示当前平台无预编译二进制。
func platformAsset() (assetName, binName string, ok bool) {
	switch runtime.GOOS {
	case "darwin":
		if runtime.GOARCH != "arm64" {
			return "", "", false
		}
		return "ungoogled-chromium_" + browserVersion + "-1.1_macos.dmg", "Chromium", true
	case "linux":
		if runtime.GOARCH != "amd64" {
			return "", "", false
		}
		return "ungoogled-chromium-" + browserVersion + "-1-x86_64_linux.tar.xz", "chrome", true
	case "windows":
		if runtime.GOARCH != "amd64" {
			return "", "", false
		}
		return "ungoogled-chromium_" + browserVersion + "-1.1_windows_x64.zip", "chrome.exe", true
	}
	return "", "", false
}

func browserCacheDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "xiaohongshu-mcp", "browser", browserVersion), nil
}

// EnsureBrowser 确保本地存在内置浏览器二进制，返回其路径。
// 已缓存则直接返回；否则下载 → 校验 SHA256 → 解压。当前平台无预编译二进制时返回 error。
func EnsureBrowser() (string, error) {
	asset, binName, ok := platformAsset()
	if !ok {
		return "", fmt.Errorf("当前平台 %s/%s 无预编译浏览器，暂不支持", runtime.GOOS, runtime.GOARCH)
	}

	cacheDir, err := browserCacheDir()
	if err != nil {
		return "", err
	}

	// 已缓存：遍历查找二进制
	if bin := findBinary(cacheDir, binName); bin != "" {
		return bin, nil
	}

	downloadURL, err := browserURL(asset)
	if err != nil {
		return "", err
	}

	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", err
	}

	// 下载（重试 3 次）
	logrus.Infof("首次运行：下载内置浏览器 %s（%s，约 140-190MB，仅一次）...", browserVersion, asset)
	archivePath := filepath.Join(cacheDir, asset)
	var dlErr error
	for attempt := 1; attempt <= 3; attempt++ {
		if dlErr = downloadFile(downloadURL, archivePath); dlErr == nil {
			break
		}
		logrus.Warnf("下载失败（第 %d/3 次）: %v", attempt, dlErr)
		_ = os.Remove(archivePath)
		time.Sleep(2 * time.Second)
	}
	if dlErr != nil {
		return "", fmt.Errorf("下载内置浏览器失败: %w\n"+
			"  本项目只用内置浏览器，缺它不继续。请检查网络后重试；\n"+
			"  离线环境可手动下载 %s，解压到 %s 后重启。", dlErr, downloadURL, cacheDir)
	}
	defer os.Remove(archivePath)

	// 新下载的文件必须与内嵌的上游 SHA256 一致，校验通过后才解压。
	if err := verifySHA256(archivePath, asset); err != nil {
		return "", fmt.Errorf("校验失败: %w", err)
	}

	logrus.Infof("解压内置浏览器 ...")
	if err := extractArchive(archivePath, cacheDir); err != nil {
		return "", fmt.Errorf("解压失败: %w", err)
	}

	bin := findBinary(cacheDir, binName)
	if bin == "" {
		return "", fmt.Errorf("解压后未找到二进制 %s", binName)
	}
	logrus.Infof("内置浏览器就绪: %s", bin)
	return bin, nil
}

// verifySHA256 使用随程序发布的固定上游 SHA256 校验下载内容。
func verifySHA256(archivePath, asset string) error {
	want, err := expectedSHA(asset)
	if err != nil {
		return err
	}
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, want) {
		return fmt.Errorf("%s SHA256 不匹配：期望 %s，实际 %s", asset, want, got)
	}
	return nil
}

func expectedSHA(asset string) (string, error) {
	sc := bufio.NewScanner(strings.NewReader(browserChecksums))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 2 && fields[1] == asset {
			hash, err := hex.DecodeString(fields[0])
			if err != nil || len(hash) != sha256.Size {
				return "", fmt.Errorf("内嵌 SHA256 清单中 %s 的校验值无效", asset)
			}
			return fields[0], nil
		}
	}
	if err := sc.Err(); err != nil {
		return "", fmt.Errorf("读取内嵌 SHA256 清单失败: %w", err)
	}
	return "", fmt.Errorf("内嵌 SHA256 清单中未找到 %s", asset)
}

func findBinary(dir, binName string) string {
	var found string
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if filepath.Base(path) == binName {
			found = path
			return io.EOF // 提前结束
		}
		return nil
	})
	if found != "" {
		if err := os.Chmod(found, 0o755); err != nil {
			logrus.Debugf("chmod %s: %v", found, err)
		}
	}
	return found
}

func downloadFile(url, dst string) error {
	resp, err := (&http.Client{Timeout: 10 * time.Minute}).Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, url)
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, resp.Body)
	return err
}

func extractArchive(archivePath, destDir string) error {
	switch {
	case strings.HasSuffix(archivePath, ".tar.xz"):
		return extractTarXz(archivePath, destDir)
	case strings.HasSuffix(archivePath, ".zip"):
		return extractZip(archivePath, destDir)
	case strings.HasSuffix(archivePath, ".dmg"):
		return extractDmg(archivePath, destDir)
	}
	return fmt.Errorf("不支持的压缩格式: %s", archivePath)
}

func extractTarXz(archivePath, destDir string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()
	xzr, err := xz.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(xzr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		target := filepath.Join(destDir, hdr.Name)
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode))
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return err
			}
			out.Close()
		case tar.TypeSymlink:
			_ = os.MkdirAll(filepath.Dir(target), 0o755)
			_ = os.Symlink(hdr.Linkname, target)
		}
	}
	return nil
}

func extractZip(archivePath, destDir string) error {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, zf := range zr.File {
		target := filepath.Join(destDir, zf.Name)
		if zf.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		rc, err := zf.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, zf.Mode())
		if err != nil {
			rc.Close()
			return err
		}
		_, err = io.Copy(out, rc)
		out.Close()
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func extractDmg(archivePath, destDir string) error {
	mountPoint, err := os.MkdirTemp("", "bx-dmg-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(mountPoint)

	if out, err := exec.Command("hdiutil", "attach", archivePath, "-nobrowse", "-mountpoint", mountPoint).CombinedOutput(); err != nil {
		return fmt.Errorf("hdiutil attach: %v: %s", err, out)
	}
	defer exec.Command("hdiutil", "detach", mountPoint, "-quiet").Run()

	var appPath string
	entries, _ := os.ReadDir(mountPoint)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".app") {
			appPath = filepath.Join(mountPoint, e.Name())
			break
		}
	}
	if appPath == "" {
		return fmt.Errorf("dmg 内未找到 .app")
	}
	dstApp := filepath.Join(destDir, filepath.Base(appPath))
	if out, err := exec.Command("cp", "-R", appPath, dstApp).CombinedOutput(); err != nil {
		return fmt.Errorf("拷贝 .app: %v: %s", err, out)
	}
	_ = exec.Command("xattr", "-dr", "com.apple.quarantine", dstApp).Run()
	return nil
}
