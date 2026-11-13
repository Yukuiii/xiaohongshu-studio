package configs

import (
	"path/filepath"
	"testing"

	"github.com/xpzouying/xiaohongshu-mcp/internal/testutil"
)

func TestGetImagesPath(t *testing.T) {
	root := testutil.IsolateDataRoot(t)
	t.Setenv("XHS_IMAGES_DIR", filepath.Join(t.TempDir(), "ignored"))
	want := filepath.Join(root, "uploads")
	got, err := filepath.Abs(GetImagesPath())
	if err != nil || got != want {
		t.Fatalf("GetImagesPath() = (%q, %v), want %q", got, err, want)
	}
}
