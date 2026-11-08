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
	procShowWindow2 = modUser32.NewProc("ShowWindow")
	procSetForeground2 = modUser32.NewProc("SetForegroundWindow")
)

func findWin() uintptr {
	tp, _ := syscall.UTF16PtrFromString("XFlow")
	h, _, _ := procFindWindowW.Call(0, uintptr(unsafe.Pointer(tp)))
	return h
}

func showWin() {
	h := findWin()
	logTray(fmt.Sprintf("showWin: hwnd=%#x", h))
	if h != 0 {
		procShowWindow2.Call(h, 5) // SW_SHOW
		procShowWindow2.Call(h, 9) // SW_RESTORE
		procSetForeground2.Call(h)
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
