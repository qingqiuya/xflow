//go:build windows

package main

import (
	"os"
	"path/filepath"
	"syscall"
	"time"
	"unsafe"
)

var (
	modShell32 = syscall.NewLazyDLL("shell32.dll")
	modKernel32 = syscall.NewLazyDLL("kernel32.dll")

	procShellNotifyIcon = modShell32.NewProc("Shell_NotifyIconW")
	procSetWindowLongPtr = modUser32.NewProc("SetWindowLongPtrW")
	procGetWindowLongPtr = modUser32.NewProc("GetWindowLongPtrW")
	procCallWindowProc   = modUser32.NewProc("CallWindowProcW")
	procShowWindow       = modUser32.NewProc("ShowWindow")
	procSetForeground    = modUser32.NewProc("SetForegroundWindow")
	procCreatePopupMenu  = modUser32.NewProc("CreatePopupMenu")
	procAppendMenuW      = modUser32.NewProc("AppendMenuW")
	procTrackPopupMenu   = modUser32.NewProc("TrackPopupMenu")
	procDestroyMenu      = modUser32.NewProc("DestroyMenu")
	procGetCursorPos     = modUser32.NewProc("GetCursorPos")
	procLoadImageW       = modUser32.NewProc("LoadImageW")
	procDestroyIcon      = modUser32.NewProc("DestroyIcon")
	procGetModuleName    = modKernel32.NewProc("GetModuleFileNameW")
	procPostQuitMessage  = modUser32.NewProc("PostQuitMessage")
)

const (
	nimAdd    = 0
	nimDelete = 2
	nifMessage = 0x1
	nifIcon    = 0x2
	nifTip     = 0x4
	wmTray    = 0x0400 + 100 // WM_USER+100
	wmClose   = 0x0010
	wmCommand = 0x0111
	wmRButtonUp = 0x0205
	wmLButtonDbl = 0x0203
	swHide    = 6
	swShow    = 5
	mfString  = 0x0
	imageIcon = 1
	lrLoadFromFile = 0x10
	idiApplication = 32512
	tpmRightButton = 0x2
	tpmReturnCmd   = 0x100
)

type notifyIconData struct {
	CbSize           uint32
	Hwnd             uintptr
	UID              uint32
	UFlags           uint32
	UCallbackMessage uint32
	HIcon            uintptr
	SzTip            [128]uint16
	DwState          uint32
	DwStateMask      uint32
	SzInfo           [256]uint16
	UVersion         uint32
	SzInfoTitle      [64]uint16
	DwInfoFlags      uint32
	GuidItem         [16]byte
	HBalloonIcon     uintptr
}

type point struct{ X, Y int32 }

var (
	trayHwnd   uintptr
	trayIcon   uintptr
	origProc   uintptr
	trayAdded  bool
	idShow     = 1001
	idExit     = 1002
)

func exeDir() string {
	buf := make([]uint16, 1024)
	n, _, _ := procGetModuleName.Call(0, uintptr(unsafe.Pointer(&buf[0])), 1023)
	if n == 0 {
		return "."
	}
	return filepath.Dir(syscall.UTF16ToString(buf[:n]))
}

func loadTrayIcon() uintptr {
	p := filepath.Join(exeDir(), "app.ico")
	if _, err := os.Stat(p); err == nil {
		ip, _ := syscall.UTF16PtrFromString(p)
		h, _, _ := procLoadImageW.Call(0, uintptr(unsafe.Pointer(ip)), imageIcon, 16, 16, lrLoadFromFile)
		if h != 0 {
			return h
		}
	}
	h, _, _ := procLoadImageW.Call(0, idiApplication, imageIcon, 16, 16, 0)
	return h
}

func addTray(hwnd uintptr) bool {
	if trayAdded {
		return true
	}
	if trayIcon == 0 {
		trayIcon = loadTrayIcon()
	}
	var nid notifyIconData
	nid.CbSize = uint32(unsafe.Sizeof(nid))
	nid.Hwnd = hwnd
	nid.UID = 1
	nid.UFlags = nifMessage | nifIcon | nifTip
	nid.UCallbackMessage = wmTray
	nid.HIcon = trayIcon
	tip, _ := syscall.UTF16FromString("XFlow 下载器")
	copy(nid.SzTip[:], tip)
	r, _, _ := procShellNotifyIcon.Call(nimAdd, uintptr(unsafe.Pointer(&nid)))
	trayAdded = r != 0
	return trayAdded
}

func delTray() {
	if !trayAdded || trayHwnd == 0 {
		return
	}
	var nid notifyIconData
	nid.CbSize = uint32(unsafe.Sizeof(nid))
	nid.Hwnd = trayHwnd
	nid.UID = 1
	procShellNotifyIcon.Call(nimDelete, uintptr(unsafe.Pointer(&nid)))
	trayAdded = false
}

func showTrayMenu(hwnd uintptr) {
	menu, _, _ := procCreatePopupMenu.Call()
	if menu == 0 {
		return
	}
	defer procDestroyMenu.Call(menu)
	s1, _ := syscall.UTF16PtrFromString("显示 XFlow")
	s2, _ := syscall.UTF16PtrFromString("退出")
	procAppendMenuW.Call(menu, mfString, uintptr(idShow), uintptr(unsafe.Pointer(s1)))
	procAppendMenuW.Call(menu, mfString, uintptr(idExit), uintptr(unsafe.Pointer(s2)))
	var pt point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	procSetForeground.Call(hwnd)
	cmd, _, _ := procTrackPopupMenu.Call(menu, tpmRightButton|tpmReturnCmd, uintptr(pt.X), uintptr(pt.Y), 0, hwnd, 0)
	switch int(cmd) {
	case idShow:
		procShowWindow.Call(hwnd, swShow)
	case idExit:
		delTray()
		procShowWindow.Call(hwnd, swHide)
		os.Exit(0)
	}
}

var wndProcCb uintptr

func installTray(title string) {
	go func() {
		tp, _ := syscall.UTF16PtrFromString(title)
		for i := 0; i < 100; i++ {
			time.Sleep(100 * time.Millisecond)
			hwnd, _, _ := procFindWindowW.Call(0, uintptr(unsafe.Pointer(tp)))
			if hwnd == 0 {
				continue
			}
			trayHwnd = hwnd
			if wndProcCb == 0 {
				wndProcCb = syscall.NewCallback(func(h, msg, wp, lp uintptr) uintptr {
					switch msg {
					case wmClose:
						procShowWindow.Call(h, swHide)
						return 0
					case wmTray:
						if lp == wmRButtonUp {
							showTrayMenu(h)
						} else if lp == wmLButtonDbl {
							procShowWindow.Call(h, swShow)
							procSetForeground.Call(h)
						}
						return 0
					}
					r, _, _ := procCallWindowProc.Call(origProc, h, msg, wp, lp)
					return r
				})
			}
			// 先读原窗口过程，再替换（顺序反了会丢消息 -> 白屏）
			old, _, _ := procGetWindowLongPtr.Call(hwnd, ^uintptr(15)) // GWLP_WNDPROC = -16
			if old == 0 {
				return
			}
			origProc = old
			procSetWindowLongPtr.Call(hwnd, ^uintptr(15), wndProcCb)
			addTray(hwnd)
			return
		}
	}()
}
