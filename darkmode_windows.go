//go:build windows

package main

import (
	"syscall"
	"time"
	"unsafe"
)

var (
	modUxTheme = syscall.NewLazyDLL("uxtheme.dll")
	modDwmapi  = syscall.NewLazyDLL("dwmapi.dll")
	modUser32  = syscall.NewLazyDLL("user32.dll")

	procSetPreferredAppMode = modUxTheme.NewProc("#135")
	procFlushMenuThemes     = modUxTheme.NewProc("#104")
	procAllowDarkForWindow  = modUxTheme.NewProc("#133")
	procDwmSetAttr          = modDwmapi.NewProc("DwmSetWindowAttribute")
	procFindWindowW         = modUser32.NewProc("FindWindowW")
)

// enableDarkMode 让进程的 Win32 对话框/菜单跟随系统深色
func enableDarkMode() {
	if p := procSetPreferredAppMode; p.Find() == nil {
		p.Call(uintptr(1)) // AllowDark
	}
	if p := procFlushMenuThemes; p.Find() == nil {
		p.Call()
	}
}

// darkenWindowTitle 把窗口标题栏刷成深色（异步等窗口出现）
func darkenWindowTitle(title string) {
	go func() {
		tp, _ := syscall.UTF16PtrFromString(title)
		for i := 0; i < 60; i++ {
			time.Sleep(100 * time.Millisecond)
			hwnd, _, _ := procFindWindowW.Call(0, uintptr(unsafe.Pointer(tp)))
			if hwnd == 0 {
				continue
			}
			if p := procAllowDarkForWindow; p.Find() == nil {
				p.Call(hwnd, 1)
			}
			dark := int32(1)
			// DWMWA_USE_IMMERSIVE_DARK_MODE = 20（旧版 Win10 是 19，两个都试）
			procDwmSetAttr.Call(hwnd, 20, uintptr(unsafe.Pointer(&dark)), 4)
			procDwmSetAttr.Call(hwnd, 19, uintptr(unsafe.Pointer(&dark)), 4)
			return
		}
	}()
}
