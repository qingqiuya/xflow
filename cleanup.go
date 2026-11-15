package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// 取消 BT 任务后，Windows 上经常删不掉 .part 文件：
// anacrolix/torrent v1.61.0 的 fileTorrentImpl.Close() 是空实现（直接 return nil），
// 文件句柄要等进程退出才由操作系统回收。Unix 上 unlink 打开中的文件没问题，
// Windows 上就是「另一个程序正在使用此文件」—— 重试多久都没用。
//
// 对策：删不掉就记进清单，下次启动时再清（那时旧句柄已随上次进程退出释放）。

var pendingMu sync.Mutex

func pendingDeletePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".localdl-pending-delete.json")
}

func loadPendingDeletes() []string {
	p := pendingDeletePath()
	if p == "" {
		return nil
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	var list []string
	if json.Unmarshal(b, &list) != nil {
		return nil
	}
	return list
}

func savePendingDeletes(list []string) {
	p := pendingDeletePath()
	if p == "" {
		return
	}
	if len(list) == 0 {
		os.Remove(p)
		return
	}
	b, _ := json.MarshalIndent(list, "", "  ")
	tmp := p + ".tmp"
	if os.WriteFile(tmp, b, 0644) == nil {
		os.Rename(tmp, p)
	}
}

// addPendingDelete 记一个当前删不掉的临时文件，留给下次启动清理。
func addPendingDelete(path string) {
	if path == "" {
		return
	}
	pendingMu.Lock()
	defer pendingMu.Unlock()
	list := loadPendingDeletes()
	for _, v := range list {
		if v == path {
			return
		}
	}
	savePendingDeletes(append(list, path))
}

// flushPendingDeletes 启动时清一次上次遗留的临时文件，返回清掉的个数。
func flushPendingDeletes() int {
	pendingMu.Lock()
	defer pendingMu.Unlock()
	list := loadPendingDeletes()
	if len(list) == 0 {
		return 0
	}
	var keep []string
	n := 0
	for _, p := range list {
		if err := os.Remove(p); err == nil || os.IsNotExist(err) {
			os.Remove(p + ".meta")
			n++
			continue
		}
		keep = append(keep, p)
	}
	savePendingDeletes(keep)
	return n
}
