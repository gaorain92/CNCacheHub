package storage

import "os"

// fileExistsOS 是 fileExists 的实现，避免在主测试文件 import os（保持测试文件纯净）。
func fileExistsOS(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
