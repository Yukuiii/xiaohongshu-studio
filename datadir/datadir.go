// Package datadir 定义持久化数据的固定位置。
package datadir

import "path/filepath"

// Path 返回数据根下的路径。
func Path(elem ...string) string {
	return filepath.Join(append([]string{Root}, elem...)...)
}
