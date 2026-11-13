package main

import (
	"testing"

	"github.com/xpzouying/xiaohongshu-mcp/internal/testutil"
)

// isolateDataRoot 让当前测试使用独立的固定数据目录。
func isolateDataRoot(t *testing.T) string {
	t.Helper()
	return testutil.IsolateDataRoot(t)
}
