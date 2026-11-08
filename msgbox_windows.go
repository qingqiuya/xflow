//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

var procMessageBox = syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW")

func walkMsg(title, text string) {
	t, _ := syscall.UTF16PtrFromString(title)
	c, _ := syscall.UTF16PtrFromString(text)
	procMessageBox.Call(0, uintptr(unsafe.Pointer(c)), uintptr(unsafe.Pointer(t)), 0x40) // MB_ICONINFORMATION
}
