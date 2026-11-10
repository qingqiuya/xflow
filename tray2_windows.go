//go:build windows

package main

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"time"
	"unsafe"

	"github.com/getlantern/systray"
)

//go:embed tray.ico
var trayIconBytes []byte

// 2026-11-10 教训：为了「不占任务栏」我试了 WS_EX_TOOLWINDOW（会干掉最小化/
// 最大化）、隐藏 owner 窗口、按 pid 枚举全部 SW_HIDE、常驻 watcher —— 一个比一个
// 复杂，结果越修越坏（窗口空白、藏了又冒出来、显示不出来）。
// 清秋说「还不如默认显示任务栏」，所以这里回到最朴素的做法：
//   · 点 × 或点「隐藏到托盘」→ SW_HIDE（窗口不在屏幕上，任务栏图标也会跟着消失）
//   · 点「显示窗口」→ SW_SHOW + SW_RESTORE + 置前 + 强制重绘
// 不碰任何扩展样式、不玩 owner、不装 watcher。

var (
	procShowWindow2    = modUser32.NewProc("ShowWindow")
	procSetForeground2 = modUser32.NewProc("SetForegroundWindow")
	procInvalidateRect = modUser32.NewProc("InvalidateRect")
	procUpdateWindow   = modUser32.NewProc("UpdateWindow")
)

func findWin() uintptr {
	tp, _ := syscall.UTF16PtrFromString("XFlow")
	h, _, _ := procFindWindowW.Call(0, uintptr(unsafe.Pointer(tp)))
	return h
}

func showWin() {
	h := findWin()
	logTray(fmt.Sprintf("showWin: hwnd=%#x", h))
	if h == 0 {
		return
	}
	procShowWindow2.Call(h, 5) // SW_SHOW
	procShowWindow2.Call(h, 9) // SW_RESTORE
	procSetForeground2.Call(h)
	// 藏起来的时候 WebView2 会停渲染，回来后踹一脚让它重画
	procInvalidateRect.Call(h, 0, 1)
	procUpdateWindow.Call(h)
}

func hideWin() {
	h := findWin()
	logTray(fmt.Sprintf("hideWin: hwnd=%#x", h))
	if h != 0 {
		procShowWindow2.Call(h, 6) // SW_HIDE
	}
}

func logTray(msg string) {
	h, _ := os.UserHomeDir()
	f, err := os.OpenFile(filepath.Join(h, "xflow-tray.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err == nil {
		fmt.Fprintf(f, "%s %s\n", time.Now().Format("15:04:05"), msg)
		f.Close()
	}
}

// startTray 用独立的消息循环做托盘，不碰 WebView2 的窗口过程
func startTray() {
	go func() {
		runtime.LockOSThread()
		systray.Run(func() {
			systray.SetIcon(trayIconBytes)
			systray.SetTitle("XFlow")
			systray.SetTooltip("XFlow 下载器")
			mShow := systray.AddMenuItem("显示窗口", "把窗口叫回来")
			mHide := systray.AddMenuItem("隐藏到托盘", "藏起来继续下载")
			systray.AddSeparator()
			mQuit := systray.AddMenuItem("退出 XFlow", "退出下载器")
			go func() {
				for {
					select {
					case <-mShow.ClickedCh:
						showWin()
					case <-mHide.ClickedCh:
						hideWin()
					case <-mQuit.ClickedCh:
						systray.Quit()
						os.Exit(0)
					}
				}
			}()
		}, func() {})
	}()
}
