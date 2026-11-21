package appupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/mod/semver"
)

type InstallerKind string

const (
	DMG  InstallerKind = "dmg"
	NSIS InstallerKind = "nsis"
	MSI  InstallerKind = "msi"

	repository     = "Yukuiii/xiaohongshu-studio"
	releaseBase    = "https://github.com/" + repository + "/releases/"
	latestManifest = releaseBase + "latest/download/studio-update.json"
	downloadBase   = releaseBase + "download/"
	maxAssetSize   = 1 << 30
)

type Release struct {
	Version string
	Name    string
	Size    int64
	Digest  [sha256.Size]byte
	URL     string
}

var githubClient = &http.Client{CheckRedirect: func(request *http.Request, via []*http.Request) error {
	if len(via) >= 5 || request.URL.Scheme != "https" || request.URL.User != nil || request.URL.Port() != "" {
		return errors.New("GitHub 更新请求跳转无效")
	}
	switch request.URL.Hostname() {
	case "github.com", "release-assets.githubusercontent.com", "objects.githubusercontent.com", "github-releases.githubusercontent.com":
		return nil
	default:
		return errors.New("GitHub 更新请求跳转到未知地址")
	}
}}

func SupportedVersion(version string) bool {
	return semver.IsValid(version) && semver.Canonical(version) == version
}

func validTag(tag string) bool {
	return SupportedVersion(tag) && semver.Prerelease(tag) == ""
}

func assetName(tag string, kind InstallerKind) string {
	name := "Xiaohongshu.Studio_" + strings.TrimPrefix(tag, "v")
	switch kind {
	case DMG:
		return name + "_aarch64.dmg"
	case NSIS:
		return name + "_x64-setup.exe"
	case MSI:
		return name + "_x64_en-US.msi"
	default:
		return ""
	}
}

// Check 返回最新稳定版中与当前安装方式匹配的安装包。
func Check(ctx context.Context, current string, kind InstallerKind) (Release, error) {
	return check(ctx, githubClient, current, kind)
}

func check(ctx context.Context, client *http.Client, current string, kind InstallerKind) (Release, error) {
	if !SupportedVersion(current) || assetName(current, kind) == "" {
		return Release{}, errors.New("当前安装不支持自动更新")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, latestManifest, nil)
	if err != nil {
		return Release{}, err
	}
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return Release{}, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return Release{}, errors.New("最新 GitHub Release 暂未提供桌面更新清单")
	}
	if response.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("读取 GitHub 更新清单失败（HTTP %d）", response.StatusCode)
	}
	var latest struct {
		Version string `json:"version"`
		Assets  map[string]struct {
			Size   int64  `json:"size"`
			Digest string `json:"sha256"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&latest); err != nil {
		return Release{}, fmt.Errorf("解析 GitHub 更新清单: %w", err)
	}
	if !validTag(latest.Version) {
		return Release{}, errors.New("GitHub 更新清单未提供有效的稳定版本")
	}
	if semver.Compare(latest.Version, current) <= 0 {
		return Release{}, nil
	}
	name := assetName(latest.Version, kind)
	asset, ok := latest.Assets[name]
	if !ok {
		return Release{}, errors.New("GitHub Release 缺少当前平台的安装包")
	}
	if asset.Size <= 0 || asset.Size > maxAssetSize {
		return Release{}, errors.New("更新清单中的安装包大小无效")
	}
	var digest [sha256.Size]byte
	if len(asset.Digest) != hex.EncodedLen(len(digest)) {
		return Release{}, errors.New("更新清单未提供有效的安装包 SHA-256")
	}
	if _, err := hex.Decode(digest[:], []byte(asset.Digest)); err != nil {
		return Release{}, errors.New("更新清单未提供有效的安装包 SHA-256")
	}
	return Release{Version: latest.Version, Name: name, Size: asset.Size, Digest: digest, URL: downloadBase + latest.Version + "/" + name}, nil
}

type downloadProgress struct {
	writer io.Writer
	update func(int64)
	bytes  int64
}

func (p *downloadProgress) Write(chunk []byte) (int, error) {
	n, err := p.writer.Write(chunk)
	p.bytes += int64(n)
	if p.update != nil {
		p.update(p.bytes)
	}
	return n, err
}

// Download 流式保存安装包，大小和 SHA-256 均验证通过后才返回文件。
func Download(ctx context.Context, release Release, root string, progress func(int64)) (string, error) {
	return download(ctx, githubClient, release, root, progress)
}

func download(ctx context.Context, client *http.Client, release Release, root string, progress func(int64)) (_ string, err error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, release.URL, nil)
	if err != nil {
		return "", err
	}
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("下载 GitHub 安装包失败（HTTP %d）", response.StatusCode)
	}
	if response.ContentLength >= 0 && response.ContentLength != release.Size {
		return "", errors.New("GitHub 安装包大小与发布记录不符")
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp(root, "package-")
	if err != nil {
		return "", err
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(dir)
		}
	}()
	path := filepath.Join(dir, release.Name)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	writer := downloadProgress{writer: io.MultiWriter(file, hash), update: progress}
	written, copyErr := io.Copy(&writer, io.LimitReader(response.Body, release.Size+1))
	if err := errors.Join(copyErr, file.Close()); err != nil {
		return "", err
	}
	var digest [sha256.Size]byte
	_ = hash.Sum(digest[:0])
	if written != release.Size || digest != release.Digest {
		return "", errors.New("安装包大小或 SHA-256 校验失败，请重新下载")
	}
	return path, nil
}
