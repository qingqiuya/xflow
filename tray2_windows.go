//go:build windows

package main

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
	procEnumWindows              = modUser32.NewProc("EnumWindows")
	procGetWindowThreadProcessId = modUser32.NewProc("GetWindowThreadProcessId")
	procIsWindowVisible          = modUser32.NewProc("IsWindowVisible")
	procGetClassName             = modUser32.NewProc("GetClassNameW")
	procGetWindowTextW2          = modUser32.NewProc("GetWindowTextW")
	procGetCurrentProcessId2     = syscall.NewLazyDLL("kernel32.dll").NewProc("GetCurrentProcessId")
	procCreateWindowEx2          = modUser32.NewProc("CreateWindowExW")
	procShowWindow2              = modUser32.NewProc("ShowWindow")
	procSetForeground2           = modUser32.NewProc("SetForegroundWindow")
	procInvalidateRect           = modUser32.NewProc("InvalidateRect")
	procUpdateWindow             = modUser32.NewProc("UpdateWindow")
)

// ownerHwnd 是一个永远不可见的 0x0 窗口（带 WS_EX_TOOLWINDOW）。
// 把它当主窗口的 owner 传进 CreateWindowExW：Windows 不把「有 owner 的窗口」
// 放进任务栏，所以主窗口从出生起就不占任务栏，而且不用给主窗口自己设
// WS_EX_TOOLWINDOW（那会干掉最小化/最大化）。
// 出处：Wails #5001 —— "create the window with a parent window specified...
// the child window will get the benefits of WS_EX_TOOLWINDOW without actually
// needing to use it"，Win10/11 都验证过。
var ownerHwnd uintptr

func makeOwnerWindow() uintptr {
	if ownerHwnd != 0 {
		return ownerHwnd
	}
	cls, _ := syscall.UTF16PtrFromString("STATIC")
	h, _, _ := procCreateWindowEx2.Call(
		0x80, // WS_EX_TOOLWINDOW
		uintptr(unsafe.Pointer(cls)), 0,
		0, // style=0：不显示，也不吃输入
		0, 0, 0, 0,
		0, 0, 0, 0,
	)
	ownerHwnd = h
	return h
}

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

// hideWin 把本进程所有可见的顶层窗口都 SW_HIDE。
// 2026-11-14：只按标题找主窗口藏不干净——WebView2 会留一个只有最小化/最大化/关闭
// 按钮的空框（那是 WebView2 自己的宿主窗口）。改成按 pid 枚举，一个都不留。
func hideWin() {
	logTray("hideWin: 枚举本进程窗口")
	hideAllOwnWindows()
}

func hideAllOwnWindows() int {
	pid := uint32(0)
	p, _, _ := procGetCurrentProcessId2.Call()
	pid = uint32(p)
	n := 0
	cb := syscall.NewCallback(func(h uintptr, _ uintptr) uintptr {
		var wpid uint32
		procGetWindowThreadProcessId.Call(h, uintptr(unsafe.Pointer(&wpid)))
		if wpid != pid {
			return 1
		}
		vis, _, _ := procIsWindowVisible.Call(h)
		if vis == 0 {
			return 1
		}
		// 别把托盘自己的窗口藏了（getlantern/systray 用 SystrayClass）
		var buf [64]uint16
		procGetClassName.Call(h, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
		cls := syscall.UTF16ToString(buf[:])
		if strings.Contains(strings.ToLower(cls), "systray") {
			return 1
		}
		procShowWindow2.Call(h, 0) // SW_HIDE
		logTray(fmt.Sprintf("hideWin: %#x class=%s", h, cls))
		n++
		return 1
	})
	procEnumWindows.Call(cb, 0)
	return n
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
