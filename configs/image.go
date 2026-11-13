package configs

import (
	"os"
	"path/filepath"
	"strings"
)

const (
	ImagesDir = "xiaohongshu_images"
)

// GetImagesPath 返回图片存储根目录；优先使用 XHS_IMAGES_DIR。
func GetImagesPath() string {
	if dir := strings.TrimSpace(os.Getenv("XHS_IMAGES_DIR")); dir != "" {
		return dir
	}
	return filepath.Join(os.TempDir(), ImagesDir)
}
