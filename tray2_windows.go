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
	procEnumWindows    = modUser32.NewProc("EnumWindows")
	procCreateWindowEx = modUser32.NewProc("CreateWindowExW")
	procGetWindowPid2  = modUser32.NewProc("GetWindowThreadProcessId")
	procIsWindowVis    = modUser32.NewProc("IsWindowVisible")
	enumCallbackPtr    uintptr // 回调必须留个引用，被 GC 回收会崩
)

const wsExToolwindow = 0x00000080 // 工具窗口：不进任务栏、不进 Alt+Tab

// GWL_EXSTYLE = -20。go vet 不允许常量 -20 直接转 uintptr，所以用补码当变量存
var gwlExStyle = ^uintptr(19)

// GWLP_HWNDPARENT = -8：给窗口挂 owner 用
var gwlpHwndParent = ^uintptr(7)

// ownerHwnd 是一个不可见的 0x0 STATIC 窗口。
// 微软文档给的替代方案：不设 WS_EX_TOOLWINDOW（那会干掉最小化/最大化），
// 而是「建一个隐藏窗口当可见窗口的 owner」——有 owner 的顶层窗口不进任务栏，
// 标题栏按钮也不受影响。
var ownerHwnd uintptr

func ensureOwner() {
	if ownerHwnd != 0 {
		return
	}
	cls, _ := syscall.UTF16PtrFromString("STATIC")
	h, _, _ := procCreateWindowEx.Call(
		0, uintptr(unsafe.Pointer(cls)), 0,
		0,          // 不带 WS_VISIBLE，创建即不可见
		0, 0, 0, 0, // 0x0
		0, 0, 0, 0,
	)
	if h != 0 {
		ownerHwnd = h
	}
}

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

// attachOwner 给主窗口挂上隐藏 owner，从此它不进任务栏
func attachOwner() {
	ensureOwner()
	if ownerHwnd == 0 {
		return
	}
	if h := findWin(); h != 0 {
		procSetWindowLong2.Call(h, gwlpHwndParent, ownerHwnd)
		logTray(fmt.Sprintf("attachOwner: main=%#x owner=%#x", h, ownerHwnd))
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

// hideWin 把本进程所有「可见的顶层窗口」全藏起来。
//
// 为什么不能只藏 findWin 找到的那个：WebView2 里还有个 Chrome_WidgetWin_1
// 顶层窗口，光藏外壳它照样挂在任务栏上（清秋报的「点 × 之后任务栏还在」）。
// Raymond Chen 的规则：顶层且无主（unowned）的窗口，只要可见就占任务栏。
// 所以按 pid 枚举、逐个 SW_HIDE 才是干净做法。
func hideWin() {
	n := 0
	pid := uint32(os.Getpid())
	enumCallbackPtr = syscall.NewCallback(func(h, l uintptr) uintptr {
		var wpid uint32
		procGetWindowPid2.Call(h, uintptr(unsafe.Pointer(&wpid)))
		if wpid == pid {
			if v, _, _ := procIsWindowVis.Call(h); v != 0 {
				procShowWindow2.Call(h, 6) // SW_HIDE
				n++
			}
		}
		return 1 // 继续枚举
	})
	procEnumWindows.Call(enumCallbackPtr, 0)
	logTray(fmt.Sprintf("hideWin: 藏了 %d 个可见窗口", n))

	// WebView2 在首帧渲染 / Navigate 完成时会把父窗口 ShowWindow 回来，
	// 所以藏一次不够——紧接着蹲 3 秒，谁变可见就再藏谁。
	go func() {
		for i := 0; i < 15; i++ {
			time.Sleep(200 * time.Millisecond)
			rehideVisible()
		}
	}()
}

// rehideVisible 把又冒出来的可见窗口再藏一遍（不打日志，避免刷屏）
func rehideVisible() {
	pid := uint32(os.Getpid())
	cb := syscall.NewCallback(func(h, l uintptr) uintptr {
		var wpid uint32
		procGetWindowPid2.Call(h, uintptr(unsafe.Pointer(&wpid)))
		if wpid == pid {
			if v, _, _ := procIsWindowVis.Call(h); v != 0 {
				procShowWindow2.Call(h, 6)
			}
		}
		return 1
	})
	procEnumWindows.Call(cb, 0)
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
