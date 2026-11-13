// Package testutil 提供测试用的数据目录隔离。
package testutil

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/xpzouying/xiaohongshu-mcp/datadir"
)

// IsolateDataRoot 在临时工作目录中使用固定数据布局。
func IsolateDataRoot(t *testing.T) string {
	t.Helper()
	t.Chdir(t.TempDir())
	root, err := filepath.Abs(datadir.Path())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	return root
}
