package configs

import (
	"path/filepath"
	"testing"
)

func TestGetImagesPath(t *testing.T) {
	t.Run("default falls back to temp dir", func(t *testing.T) {
		t.Setenv("XHS_IMAGES_DIR", "")
		want := filepath.Join(t.TempDir(), ImagesDir)
		// 只校验路径后缀与临时目录规则一致，不依赖固定系统 TempDir。
		got := GetImagesPath()
		if filepath.Base(got) != filepath.Base(want) {
			t.Fatalf("GetImagesPath() = %q, want base %q", got, filepath.Base(want))
		}
	})

	t.Run("uses XHS_IMAGES_DIR when set", func(t *testing.T) {
		custom := filepath.Join(t.TempDir(), "uploads")
		t.Setenv("XHS_IMAGES_DIR", "  "+custom+"  ")
		if got := GetImagesPath(); got != custom {
			t.Fatalf("GetImagesPath() = %q, want %q", got, custom)
		}
	})
}
