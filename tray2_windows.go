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

var (
	procShowWindow2    = modUser32.NewProc("ShowWindow")
	procSetForeground2 = modUser32.NewProc("SetForegroundWindow")
	procGetWindowLong2 = modUser32.NewProc("GetWindowLongPtrW")
	procSetWindowLong2 = modUser32.NewProc("SetWindowLongPtrW")
	procInvalidateRect = modUser32.NewProc("InvalidateRect")
	procUpdateWindow   = modUser32.NewProc("UpdateWindow")
)

const wsExToolwindow = 0x00000080 // 工具窗口：不进任务栏、不进 Alt+Tab

// GWL_EXSTYLE = -20。go vet 不允许常量 -20 直接转 uintptr，所以用补码当变量存
var gwlExStyle = ^uintptr(19)

func findWin() uintptr {
	tp, _ := syscall.UTF16PtrFromString("XFlow")
	h, _, _ := procFindWindowW.Call(0, uintptr(unsafe.Pointer(tp)))
	return h
}

func showWin() {
	h := findWin()
	logTray(fmt.Sprintf("showWin: hwnd=%#x", h))
	if h != 0 {
		// 不再碰 TOOLWINDOW：设了之后标题栏只剩 ×（没有最小化/最大化），
		// 摘掉又得靠「隐藏再显示」才刷新，来回折腾还容易把 WebView2 弄成白屏。
		// 隐藏时只 SW_HIDE 就够了——隐藏的窗口本来就不在任务栏。
		clearToolWindow()
		procShowWindow2.Call(h, 5) // SW_SHOW
		procShowWindow2.Call(h, 9) // SW_RESTORE
		procSetForeground2.Call(h)
		// 隐藏期间 WebView2 会停渲染，回来时强制重画一遍，否则一片空白
		procInvalidateRect.Call(h, 0, 1)
		procUpdateWindow.Call(h)
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

// clearToolWindow 恢复成普通窗口（有最小化/最大化、进任务栏）
func clearToolWindow() {
	h := findWin()
	if h != 0 {
		ex, _, _ := procGetWindowLong2.Call(h, gwlExStyle)
		procSetWindowLong2.Call(h, gwlExStyle, ex&^wsExToolwindow)
	}
}

// markToolWindow 只设扩展样式，不动可见性
func markToolWindow() {
	h := findWin()
	if h != 0 {
		ex, _, _ := procGetWindowLong2.Call(h, gwlExStyle)
		procSetWindowLong2.Call(h, gwlExStyle, ex|wsExToolwindow)
	}
}

func hideWin() {
	h := findWin()
	logTray(fmt.Sprintf("hideWin: hwnd=%#x", h))
	if h != 0 {
		procShowWindow2.Call(h, 6) // SW_HIDE
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
						osExit()
					}
				}
			}()
		}, func() {})
	}()
}

func osExit() {
	syscall.Exit(0)
}
