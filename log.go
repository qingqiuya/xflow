package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// 运行日志：写到 ~/xflow.log（Windows 上是 C:\Users\<你>\xflow.log）。
//
// 为什么需要它：这个程序是 windowsgui 的，没有控制台，原来所有 fmt.Printf
// 全是黑洞 —— 下载出问题只能靠猜（2026-11-20 清秋「你看看9950的日志」，
// 结果那个 xflow-tray.log 只记窗口显隐，一条下载相关的都没有）。
//
// 规矩：只在任务生命周期这种低频地方写，绝不在每个数据块里写；
// 超过 5MB 轮转成 xflow.log.old，不删旧内容。

var (
	appLogMu   sync.Mutex
	appLogPath string
)

func appLog(format string, args ...any) {
	appLogMu.Lock()
	defer appLogMu.Unlock()

	if appLogPath == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return
		}
		appLogPath = filepath.Join(h, "xflow.log")
	}
	if st, err := os.Stat(appLogPath); err == nil && st.Size() > 5<<20 {
		_ = os.Rename(appLogPath, appLogPath+".old")
	}
	f, err := os.OpenFile(appLogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	msg := format
	if len(args) > 0 {
		msg = fmt.Sprintf(format, args...)
	}
	fmt.Fprintf(f, "%s %s\n", time.Now().Format("01-02 15:04:05"), msg)
}
