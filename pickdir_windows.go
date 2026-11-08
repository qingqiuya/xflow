//go:build windows

package main

import (
	"fmt"
	"syscall"
	"unsafe"
)

var (
	modOle32   = syscall.NewLazyDLL("ole32.dll")
	procCoInit = modOle32.NewProc("CoInitializeEx")
	procCoUnin = modOle32.NewProc("CoUninitialize")
	procCoCreate = modOle32.NewProc("CoCreateInstance")
	procCoTaskFree = modOle32.NewProc("CoTaskMemFree")
	procGetForeground = modUser32.NewProc("GetForegroundWindow")
)

type guid struct {
	D1 uint32
	D2 uint16
	D3 uint16
	D4 [8]byte
}

var (
	clsidFileOpenDialog = guid{0xDC1C5A9C, 0xE88A, 0x4dde, [8]byte{0xA5, 0xA1, 0x60, 0xF8, 0x2A, 0x20, 0xAE, 0xF7}}
	iidIFileOpenDialog  = guid{0xD57C7288, 0xD4AD, 0x4768, [8]byte{0xBE, 0x02, 0x9D, 0x96, 0x95, 0x32, 0xD9, 0x60}}
)

const (
	clsctxAll              = 0x17
	coinitApartmentThreaded = 0x2
	fosPickFolders         = 0x00000020
	fosForceFileSystem     = 0x00000040
	fosPathMustExist       = 0x00000800
	fosNoChangeDir         = 0x00000008
	sigdnFileSysPath       = 0x80058000
)

func comCall(obj unsafe.Pointer, idx int, args ...uintptr) uintptr {
	vtbl := *(*unsafe.Pointer)(obj)
	fn := *(*uintptr)(unsafe.Pointer(uintptr(vtbl) + uintptr(idx)*unsafe.Sizeof(uintptr(0))))
	all := append([]uintptr{uintptr(obj)}, args...)
	ret, _, _ := syscall.SyscallN(fn, all...)
	return ret
}

func utf16Ptr(s string) *uint16 {
	p, _ := syscall.UTF16PtrFromString(s)
	return p
}

// pickFolder 弹出系统现代文件夹选择框（IFileOpenDialog，跟随系统暗色主题）
func pickFolder(title string) (string, error) {
	hr, _, _ := procCoInit.Call(0, coinitApartmentThreaded)
	if int32(hr) < 0 && int32(hr) != 1 { // S_FALSE(1) 也算成功
		return "", fmt.Errorf("CoInitializeEx 失败 0x%x", hr)
	}
	defer procCoUnin.Call()

	var pfd unsafe.Pointer
	hr, _, _ = procCoCreate.Call(
		uintptr(unsafe.Pointer(&clsidFileOpenDialog)),
		0,
		clsctxAll,
		uintptr(unsafe.Pointer(&iidIFileOpenDialog)),
		uintptr(unsafe.Pointer(&pfd)),
	)
	if int32(hr) < 0 || pfd == nil {
		return "", fmt.Errorf("创建对话框失败 0x%x", hr)
	}
	defer comCall(pfd, 2) // Release

	// GetOptions(10) 读现有，SetOptions(9) 写回
	var opts uint32
	comCall(pfd, 10, uintptr(unsafe.Pointer(&opts)))
	opts |= fosPickFolders | fosForceFileSystem | fosPathMustExist | fosNoChangeDir
	comCall(pfd, 9, uintptr(opts))

	if title != "" {
		if tp := utf16Ptr(title); tp != nil {
			comCall(pfd, 17, uintptr(unsafe.Pointer(tp))) // SetTitle
		}
	}

	owner, _, _ := procGetForeground.Call()
	if r := comCall(pfd, 3, owner); int32(r) != 0 { // Show
		return "", fmt.Errorf("已取消")
	}

	var psi unsafe.Pointer
	if r := comCall(pfd, 20, uintptr(unsafe.Pointer(&psi))); int32(r) < 0 || psi == nil { // GetResult
		return "", fmt.Errorf("拿不到选择结果")
	}
	defer comCall(psi, 2) // Release

	var pw *uint16
	if r := comCall(psi, 5, sigdnFileSysPath, uintptr(unsafe.Pointer(&pw))); int32(r) < 0 || pw == nil { // GetDisplayName
		return "", fmt.Errorf("拿不到路径")
	}
	defer procCoTaskFree.Call(uintptr(unsafe.Pointer(pw)))

	return syscall.UTF16ToString((*[1 << 16]uint16)(unsafe.Pointer(pw))[:]), nil
}
