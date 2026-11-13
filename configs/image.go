package configs

import "github.com/xpzouying/xiaohongshu-mcp/datadir"

// GetImagesPath 返回图片存储根目录。
func GetImagesPath() string {
	return datadir.Path("uploads")
}
