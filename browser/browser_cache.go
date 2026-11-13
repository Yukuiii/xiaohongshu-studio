package browser

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/sirupsen/logrus"
)

// profileCacheDirs 只包含可重新生成的缓存，不包含登录态和站点存储。
var profileCacheDirs = []string{
	"Default/Cache",
	"Default/Code Cache",
	"Default/GPUCache",
	"Default/DawnGraphiteCache",
	"Default/DawnWebGPUCache",
	"Default/ShaderCache",
	"Default/GrShaderCache",
	"GrShaderCache",
	"ShaderCache",
	"GraphiteDawnCache",
}

// PruneProfileCaches 删除 Chromium 可重建的缓存；profile 必须已无进程占用。
func PruneProfileCaches(profile string) error {
	if profile == "" {
		return nil
	}
	info, err := os.Lstat(profile)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("profile 不是目录: %s", profile)
	}
	var failures []error
	for _, relative := range profileCacheDirs {
		path := filepath.Join(profile, relative)
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			failures = append(failures, err)
			continue
		}
		// 符号链接只删除链接本身，不跟随到 profile 外部。
		if !info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
			continue
		}
		if err := os.RemoveAll(path); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func pruneProfileCaches(profile string) {
	if err := PruneProfileCaches(profile); err != nil {
		logrus.Warnf("清理浏览器缓存失败 %s: %v", profile, err)
	}
}
