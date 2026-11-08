package edge

import (
	"unsafe"

	"github.com/pgmsoul/go-webview2/internal/w32"
	"golang.org/x/sys/windows"
)

type iCoreWebView2NewWindowRequestedEventArgsVtbl struct {
	_IUnknownVtbl
	GetUri             ComProc
	PutNewWindow       ComProc
	GetNewWindow       ComProc
	PutHandled         ComProc
	GetHandled         ComProc
	GetIsUserInitiated ComProc
	GetDeferral        ComProc
	GetWindowFeatures  ComProc
}

type ICoreWebView2NewWindowRequestedEventArgs struct {
	vtbl *iCoreWebView2NewWindowRequestedEventArgsVtbl
}

func (i *ICoreWebView2NewWindowRequestedEventArgs) GetUri() (string, error) {
	var uri *uint16
	_, _, err := i.vtbl.GetUri.Call(
		uintptr(unsafe.Pointer(i)),
		uintptr(unsafe.Pointer(&uri)),
	)
	if err != windows.ERROR_SUCCESS {
		return "", err
	}
	s := w32.Utf16PtrToString(uri)
	windows.CoTaskMemFree(unsafe.Pointer(uri))
	return s, nil
}

func (i *ICoreWebView2NewWindowRequestedEventArgs) PutNewWindow(newWindow *ICoreWebView2) error {
	_, _, err := i.vtbl.PutNewWindow.Call(
		uintptr(unsafe.Pointer(i)),
		uintptr(unsafe.Pointer(newWindow)),
	)
	if err != windows.ERROR_SUCCESS {
		return err
	}
	return nil
}

func (i *ICoreWebView2NewWindowRequestedEventArgs) PutHandled(handled bool) error {
	_, _, err := i.vtbl.PutHandled.Call(
		uintptr(unsafe.Pointer(i)),
		uintptr(boolToInt(handled)),
	)
	if err != windows.ERROR_SUCCESS {
		return err
	}
	return nil
}
