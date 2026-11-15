//go:build !windows

package main

// 非 Windows 不需要：Unix 上删除打开中的文件没问题，mmap 也不影响。
func ensureClassicFileIO() {}
