package configs

import "github.com/Yukuiii/xiaohongshu-studio/datadir"

// GetImagesPath 返回图片存储根目录。
func GetImagesPath() string {
	return datadir.Path("uploads")
}
