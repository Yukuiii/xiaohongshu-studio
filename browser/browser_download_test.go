package browser

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type browserDownloadTransport func(*http.Request) (*http.Response, error)

func (f browserDownloadTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func isolateBrowserDownload(t *testing.T) {
	t.Helper()
	cacheRoot := t.TempDir()
	t.Setenv("HOME", cacheRoot)
	t.Setenv("XDG_CACHE_HOME", cacheRoot)
	t.Setenv("LocalAppData", cacheRoot)
}

func browserReleaseForTest(t *testing.T) (asset, checksum, binary string) {
	t.Helper()
	switch {
	case runtime.GOOS == "darwin" && runtime.GOARCH == "arm64":
		return "ungoogled-chromium_148.0.7778.215-1.1_macos.dmg", "b72f091e2e1a7583eed389c4b8e3534ed355e568af8c8bbf8fc30a25e23ca679", "Chromium"
	case runtime.GOOS == "linux" && runtime.GOARCH == "amd64":
		return "ungoogled-chromium-148.0.7778.215-1-x86_64_linux.tar.xz", "70d239830332e5820aa34dfcb284161cac0429eee25da642830afe04bda717f4", "chrome"
	case runtime.GOOS == "windows" && runtime.GOARCH == "amd64":
		return "ungoogled-chromium_148.0.7778.215-1.1_windows_x64.zip", "9ef3f471b7a6641b4224532522b29141ce3746e27d55788d88e2fd951f362579", "chrome.exe"
	default:
		t.Skip("当前平台没有内置浏览器 release")
		return "", "", ""
	}
}

func interceptBrowserDownloads(t *testing.T, requests *[]string) {
	t.Helper()
	previous := http.DefaultTransport
	http.DefaultTransport = browserDownloadTransport(func(req *http.Request) (*http.Response, error) {
		*requests = append(*requests, req.URL.String())
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("untrusted mirror archive")),
			Request:    req,
		}, nil
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
}

func TestEnsureBrowserUsesOfficialOrMirrorAndPinnedChecksum(t *testing.T) {
	asset, checksum, binary := browserReleaseForTest(t)
	tests := []struct {
		name string
		base string
		want string
	}{
		{
			name: "official by default",
			want: "https://github.com/adryfish/fingerprint-chromium/releases/download",
		},
		{
			name: "mirror directory with trailing slashes",
			base: "https://mirror.example/browser/releases///",
			want: "https://mirror.example/browser/releases",
		},
		{
			name: "full GitHub proxy prefix",
			base: "https://mirror.example/https://github.com/adryfish/fingerprint-chromium/releases/download/",
			want: "https://mirror.example/https://github.com/adryfish/fingerprint-chromium/releases/download",
		},
		{
			name: "HTTP mirror",
			base: "http://mirror.example/browser",
			want: "http://mirror.example/browser",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isolateBrowserDownload(t)
			t.Setenv("XHS_BROWSER_DOWNLOAD_BASE", tt.base)
			var requests []string
			interceptBrowserDownloads(t, &requests)

			path, err := EnsureBrowser()
			if path != "" || err == nil || !strings.Contains(err.Error(), "SHA256 不匹配") || !strings.Contains(err.Error(), checksum) {
				t.Fatalf("EnsureBrowser() = (%q, %v), want rejection against pinned upstream checksum %s", path, err, checksum)
			}
			wantURL := tt.want + "/148.0.7778.215/" + asset
			if len(requests) != 1 || requests[0] != wantURL {
				t.Fatalf("download requests = %q, want only %q; checksum must not be fetched from the mirror", requests, wantURL)
			}
			cacheDir, err := browserCacheDir()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(cacheDir, asset)); !os.IsNotExist(err) {
				t.Fatalf("untrusted archive was retained: %v", err)
			}
			if path := findBinary(cacheDir, binary); path != "" {
				t.Fatalf("untrusted archive produced a cached browser: %s", path)
			}
		})
	}
}

func TestEnsureBrowserRejectsInvalidSourceWithoutLeakingCredentials(t *testing.T) {
	browserReleaseForTest(t)
	sources := []string{
		"https://private-user:private-password@mirror.example/download",
		"https://private-user:private-password%ZZ@mirror.example/download",
		"https://mirror.example/download?token=query-secret",
		"https://mirror.example/download#fragment-secret",
		"https://mirror.example/download?",
		"https://mirror.example/download#",
		"file:///private/browser",
		"ftp://mirror.example/download",
		"//mirror.example/download",
		"https:///download",
		"https:opaque-secret",
		"https://mirror.example/%ZZ",
		"   ",
	}
	for _, source := range sources {
		t.Run(source, func(t *testing.T) {
			isolateBrowserDownload(t)
			t.Setenv("XHS_BROWSER_DOWNLOAD_BASE", source)
			var requests []string
			interceptBrowserDownloads(t, &requests)

			path, err := EnsureBrowser()
			if path != "" || err == nil || !strings.Contains(err.Error(), "XHS_BROWSER_DOWNLOAD_BASE") {
				t.Fatalf("EnsureBrowser() = (%q, %v), want a clear source configuration error", path, err)
			}
			for _, secret := range []string{source, "private-user", "private-password", "query-secret", "fragment-secret", "opaque-secret"} {
				if strings.Contains(err.Error(), secret) {
					t.Fatalf("source configuration error leaked a URL or credential: %q", err.Error())
				}
			}
			if len(requests) != 0 {
				t.Fatalf("invalid source made network requests: %q", requests)
			}
			cacheDir, err := browserCacheDir()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(cacheDir); !os.IsNotExist(err) {
				t.Fatalf("invalid source changed the browser cache: %v", err)
			}
		})
	}
}

func TestEnsureBrowserPreservesExistingVersionCache(t *testing.T) {
	_, _, binary := browserReleaseForTest(t)
	isolateBrowserDownload(t)
	t.Setenv("XHS_BROWSER_DOWNLOAD_BASE", "https://private-user:private-password@unused.example/download")
	var requests []string
	interceptBrowserDownloads(t, &requests)

	base, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	cacheDir := filepath.Join(base, "xiaohongshu-mcp", "browser", "148.0.7778.215")
	cachedBinary := filepath.Join(cacheDir, "legacy", "nested", binary)
	if err := os.MkdirAll(filepath.Dir(cachedBinary), 0o755); err != nil {
		t.Fatal(err)
	}
	contents := []byte("existing fingerprint browser")
	if err := os.WriteFile(cachedBinary, contents, 0o755); err != nil {
		t.Fatal(err)
	}
	legacyFile := filepath.Join(cacheDir, "existing-cache-data")
	if err := os.WriteFile(legacyFile, []byte("preserve me"), 0o600); err != nil {
		t.Fatal(err)
	}

	path, err := EnsureBrowser()
	if err != nil || path != cachedBinary {
		t.Fatalf("EnsureBrowser() = (%q, %v), want existing cached browser %q", path, err, cachedBinary)
	}
	if len(requests) != 0 {
		t.Fatalf("existing cache caused network requests: %q", requests)
	}
	got, err := os.ReadFile(cachedBinary)
	if err != nil || string(got) != string(contents) {
		t.Fatalf("existing browser changed: contents %q, error %v", got, err)
	}
	got, err = os.ReadFile(legacyFile)
	if err != nil || string(got) != "preserve me" {
		t.Fatalf("existing cache data changed: contents %q, error %v", got, err)
	}
}
