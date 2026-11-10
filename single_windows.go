//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

var procCreateMutex = syscall.NewLazyDLL("kernel32.dll").NewProc("CreateMutexW")

// alreadyRunning 用命名互斥体保证只有一个实例。
// 用 Global\ 而不是 Local\：Local 是按登录会话隔离的，
// 从 SSH（session 0）和桌面（session 1）各起一次都能成功，就会出现多个托盘图标。
func alreadyRunning() bool {
	name, _ := syscall.UTF16PtrFromString("Global\\XFlow_SingleInstance_Mutex")
	h, _, e := procCreateMutex.Call(0, 0, uintptr(unsafe.Pointer(name)))
	if h == 0 {
		return false
	}
	return e == syscall.ERROR_ALREADY_EXISTS
}
