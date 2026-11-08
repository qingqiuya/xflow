//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

var procCreateMutex = syscall.NewLazyDLL("kernel32.dll").NewProc("CreateMutexW")

// alreadyRunning 用命名互斥体保证只有一个实例
func alreadyRunning() bool {
	name, _ := syscall.UTF16PtrFromString("Local\\XFlow_SingleInstance_Mutex")
	h, _, e := procCreateMutex.Call(0, 0, uintptr(unsafe.Pointer(name)))
	if h == 0 {
		return false
	}
	return e == syscall.ERROR_ALREADY_EXISTS
}
