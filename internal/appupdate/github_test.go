package appupdate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type updateTransport func(*http.Request) (*http.Response, error)

func (roundTrip updateTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

func TestCheckReleaseSelectsInstallerAndRejectsUnsafeMetadata(t *testing.T) {
	const tag = "v1.10.0"
	digest := sha256.Sum256([]byte("package"))
	for _, tc := range []struct {
		name, current, asset, actualTag string
		kind                            InstallerKind
		badDigest                       bool
		wantVersion                     string
		wantError                       string
	}{
		{name: "macOS", current: "v1.9.9", asset: "Xiaohongshu.Studio_1.10.0_aarch64.dmg", kind: DMG, wantVersion: tag},
		{name: "Windows NSIS", current: "v1.9.9", asset: "Xiaohongshu.Studio_1.10.0_x64-setup.exe", kind: NSIS, wantVersion: tag},
		{name: "Windows MSI", current: "v1.9.9", asset: "Xiaohongshu.Studio_1.10.0_x64_en-US.msi", kind: MSI, wantVersion: tag},
		{name: "same version", current: tag, kind: DMG},
		{name: "no downgrade", current: "v1.11.0", kind: DMG},
		{name: "no prerelease", current: "v1.9.9", actualTag: "v1.10.0-beta.1", kind: DMG, wantError: "稳定版本"},
		{name: "no short tag", current: "v1.9.9", actualTag: "v2.0", kind: DMG, wantError: "稳定版本"},
		{name: "no wrong installer", current: "v1.9.9", asset: "Xiaohongshu.Studio_1.10.0_x64-setup.exe", kind: MSI, wantError: "缺少"},
		{name: "no path traversal", current: "v1.9.9", asset: "../Xiaohongshu.Studio_1.10.0_aarch64.dmg", kind: DMG, wantError: "缺少"},
		{name: "no missing digest", current: "v1.9.9", asset: "Xiaohongshu.Studio_1.10.0_aarch64.dmg", kind: DMG, badDigest: true, wantError: "SHA-256"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			releaseTag := tc.actualTag
			if releaseTag == "" {
				releaseTag = tag
			}
			checksum := hex.EncodeToString(digest[:])
			if tc.badDigest {
				checksum = ""
			}
			body, err := json.Marshal(map[string]any{
				"version": releaseTag,
				"assets": map[string]any{tc.asset: map[string]any{
					"size": 7, "sha256": checksum, "url": "https://example.com/download",
				}},
			})
			require.NoError(t, err)
			client := &http.Client{Transport: updateTransport(func(req *http.Request) (*http.Response, error) {
				require.Equal(t, latestManifest, req.URL.String())
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
			})}
			release, err := check(t.Context(), client, tc.current, tc.kind)
			if tc.wantError != "" {
				require.ErrorContains(t, err, tc.wantError)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.wantVersion, release.Version)
			if tc.wantVersion != "" {
				require.Equal(t, tc.asset, release.Name)
				require.Equal(t, digest, release.Digest)
				require.Equal(t, downloadBase+releaseTag+"/"+tc.asset, release.URL)
			}
		})
	}
}

func TestDownloadVerifiesBytesAndRemovesBadPackages(t *testing.T) {
	const contents = "installation package"
	const name = "Xiaohongshu.Studio_1.1.0_aarch64.dmg"
	release := Release{Version: "v1.1.0", Name: name, Size: int64(len(contents)), Digest: sha256.Sum256([]byte(contents)), URL: downloadBase + "v1.1.0/" + name}
	for _, body := range []string{contents, "installation Package", "installation", contents + "extra"} {
		t.Run(body, func(t *testing.T) {
			client := &http.Client{Transport: updateTransport(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, ContentLength: -1, Body: io.NopCloser(strings.NewReader(body))}, nil
			})}
			var received int64
			dir := t.TempDir()
			path, err := download(t.Context(), client, release, dir, func(bytes int64) { received = bytes })
			if body != contents {
				require.Error(t, err)
				require.Empty(t, path)
				entries, err := os.ReadDir(dir)
				require.NoError(t, err)
				require.Empty(t, entries, "校验失败后不能留下可安装文件")
				return
			}
			require.NoError(t, err)
			actual, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, contents, string(actual))
			require.Equal(t, release.Size, received)
		})
	}
}
