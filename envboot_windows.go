//go:build windows

package main

import (
	"os"
	"os/exec"
)

// Windows 上 BT 取消后 .part 删不掉的根因是 anacrolix/torrent 的默认存储用 mmap：
//
//	storage/file-io-mmap.go 的 init() 把 defaultFileIo 切成 &mmapFileIo{}
//	storage/file-torrent.go:114  func (fs *fileTorrentImpl) Close() error { return nil }  ← 空实现
//
// 于是 mmap 永不 Unmap，文件句柄要等进程退出才释放（Unix 上 unlink 打开中的文件没问题，
// 所以在 Linux 上完全看不出来）。
//
// 那个包认环境变量 TORRENT_STORAGE_DEFAULT_FILE_IO=classic，切回「每次读写都 open/close」
// 的老实现，就没有长活句柄了。但它的 init 在 main 之前就跑完了，main 里 Setenv 已经晚，
// 所以这里把环境变量设给自己、re-exec 一次 —— 子进程的 storage init 就能读到。
func ensureClassicFileIO() {
	if os.Getenv("TORRENT_STORAGE_DEFAULT_FILE_IO") != "" {
		return // 用户自己指定了，尊重
	}
	cmd := exec.Command(os.Args[0], os.Args[1:]...)
	cmd.Env = append(os.Environ(), "TORRENT_STORAGE_DEFAULT_FILE_IO=classic")
	if err := cmd.Start(); err == nil {
		os.Exit(0)
	}
	// 起不来就按原样继续跑（退回 mmap，宁可慢一点也别启动失败）
}
