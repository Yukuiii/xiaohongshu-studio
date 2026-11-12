//go:build integration

package browser

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

// 使用真实 Linux 浏览器验证指纹补丁，防止普通 Chromium 静默忽略专用参数。
func TestFingerprintBrowserSignals(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("部署镜像中的 Linux 指纹回归")
	}
	t.Setenv("COOKIES_PATH", filepath.Join(t.TempDir(), "cookies.json"))
	capture := func(seed int) (string, string) {
		t.Helper()
		b := NewBrowser(true, WithFingerprintSeed(seed))
		defer b.Close()
		page := b.NewPage()
		defer page.Close()
		require.NoError(t, page.SetDocumentContent("<!doctype html><title>local fingerprint fixture</title>"))
		result, err := page.Eval(`() => {
			const canvas = document.createElement("canvas");
			canvas.width = 240; canvas.height = 70;
			const context = canvas.getContext("2d");
			context.fillStyle = "#3478a0";
			context.fillRect(0, 0, 240, 70);
			context.fillStyle = "#f1d060";
			context.font = "20px sans-serif";
			context.fillText("Fingerprint fixture", 8, 40);
			return {platform: navigator.platform, canvas: canvas.toDataURL()};
		}`)
		require.NoError(t, err)
		return result.Value.Get("platform").Str(), result.Value.Get("canvas").Str()
	}
	platform, first := capture(23088)
	require.Equal(t, "Win32", platform, "Linux 上的 Windows 指纹必须真实生效")
	_, sameSeed := capture(23088)
	require.Equal(t, first, sameSeed, "同一账号 seed 应产生一致的 Canvas 指纹")
	_, otherSeed := capture(52066)
	require.NotEqual(t, first, otherSeed, "改变 seed 应改变 Canvas 指纹，不能退化成普通浏览器")
}
