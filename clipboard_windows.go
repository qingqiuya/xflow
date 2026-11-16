//go:build windows

package main

// Windows 剪贴板（CF_UNICODETEXT）。注意全是自己独立的 proc 变量，
// 别跟 darkmode_windows.go 的 modUser32 打架。

import (
	"fmt"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"
)

var (
	xflowClipUser32   = syscall.NewLazyDLL("user32.dll")
	xflowClipKernel32 = syscall.NewLazyDLL("kernel32.dll")

	procOpenClipboard    = xflowClipUser32.NewProc("OpenClipboard")
	procCloseClipboard   = xflowClipUser32.NewProc("CloseClipboard")
	procEmptyClipboard   = xflowClipUser32.NewProc("EmptyClipboard")
	procSetClipboardData = xflowClipUser32.NewProc("SetClipboardData")
	procGetClipboardData = xflowClipUser32.NewProc("GetClipboardData")
	procIsClipFormat     = xflowClipUser32.NewProc("IsClipboardFormatAvailable")

	procGlobalAlloc  = xflowClipKernel32.NewProc("GlobalAlloc")
	procGlobalLock   = xflowClipKernel32.NewProc("GlobalLock")
	procGlobalUnlock = xflowClipKernel32.NewProc("GlobalUnlock")
	procGlobalFree   = xflowClipKernel32.NewProc("GlobalFree")
)

const (
	xflowCFUnicodeText = 13
	xflowGMEMoveable   = 0x0002
)

func clipboardRead() (string, error) {
	var opened bool
	for i := 0; i < 8; i++ { // 剪贴板常被别的程序占着，试几次
		if r, _, _ := procOpenClipboard.Call(0); r != 0 {
			opened = true
			break
		}
		time.Sleep(40 * time.Millisecond)
	}
	if !opened {
		return "", fmt.Errorf("剪贴板被别的程序占用")
	}
	defer procCloseClipboard.Call()

	if r, _, _ := procIsClipFormat.Call(xflowCFUnicodeText); r == 0 {
		return "", nil // 里面没有文本
	}
	h, _, err := procGetClipboardData.Call(xflowCFUnicodeText)
	if h == 0 {
		return "", fmt.Errorf("GetClipboardData 失败: %v", err)
	}
	ptr, _, _ := procGlobalLock.Call(h)
	if ptr == 0 {
		return "", fmt.Errorf("GlobalLock 失败")
	}
	defer procGlobalUnlock.Call(h)

	// 扫到 UTF-16 的 0 结尾
	var n int
	for n < 8<<20 {
		if *(*uint16)(unsafe.Pointer(ptr + uintptr(n*2))) == 0 {
			break
		}
		n++
	}
	buf := unsafe.Slice((*uint16)(unsafe.Pointer(ptr)), n)
	return string(utf16.Decode(buf)), nil
}

func clipboardWrite(text string) error {
	units := append(utf16.Encode([]rune(text)), 0)
	size := uintptr(len(units) * 2)

	var opened bool
	for i := 0; i < 8; i++ {
		if r, _, _ := procOpenClipboard.Call(0); r != 0 {
			opened = true
			break
		}
		time.Sleep(40 * time.Millisecond)
	}
	if !opened {
		return fmt.Errorf("剪贴板被别的程序占用")
	}
	defer procCloseClipboard.Call()

	procEmptyClipboard.Call()
	h, _, err := procGlobalAlloc.Call(xflowGMEMoveable, size)
	if h == 0 {
		return fmt.Errorf("GlobalAlloc 失败: %v", err)
	}
	ptr, _, _ := procGlobalLock.Call(h)
	if ptr == 0 {
		procGlobalFree.Call(h)
		return fmt.Errorf("GlobalLock 失败")
	}
	dst := unsafe.Slice((*uint16)(unsafe.Pointer(ptr)), len(units))
	copy(dst, units)
	procGlobalUnlock.Call(h)

	if r, _, _ := procSetClipboardData.Call(xflowCFUnicodeText, h); r == 0 {
		procGlobalFree.Call(h)
		return fmt.Errorf("SetClipboardData 失败")
	}
	return nil
}
