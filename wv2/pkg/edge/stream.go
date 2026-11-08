package edge

import (
	"fmt"
	"time"
	"unsafe"

	"github.com/pgmsoul/go-webview2/internal/w32"
	"golang.org/x/sys/windows"
)

type iStreamVtbl struct {
	_IUnknownVtbl
	Read  ComProc
	Write ComProc
	Seek  ComProc
	SetSize ComProc
	CopyTo ComProc
	Commit ComProc
	Revert ComProc
	LockRegion ComProc
	UnlockRegion ComProc
	Stat ComProc
	Clone ComProc
}

type iStream struct {
	vtbl *iStreamVtbl
}

const coreWebView2CapturePreviewImageFormatPNG = 0

func (i *ICoreWebView2) CapturePreviewPNG() ([]byte, error) {
	streamPtr, err := w32.SHCreateMemStreamEmpty()
	if err != nil {
		return nil, err
	}
	stream := (*iStream)(unsafe.Pointer(streamPtr))
	_, _, err = i.vtbl.CapturePreview.Call(
		uintptr(unsafe.Pointer(i)),
		uintptr(coreWebView2CapturePreviewImageFormatPNG),
		streamPtr,
	)
	if err != windows.ERROR_SUCCESS {
		return nil, fmt.Errorf("CapturePreview: %w", err)
	}
	return readAllFromIStream(stream)
}

func (i *ICoreWebView2) CallDevToolsProtocolMethodSync(method, paramsJSON string, timeout time.Duration) (string, error) {
	uMethod, err := windows.UTF16PtrFromString(method)
	if err != nil {
		return "", err
	}
	uParams, err := windows.UTF16PtrFromString(paramsJSON)
	if err != nil {
		return "", err
	}
	h := &devToolsHandler{ch: make(chan devToolsOutcome, 1)}
	cb := newICoreWebView2CallDevToolsProtocolMethodCompletedHandler(h)
	_, _, err = i.vtbl.CallDevToolsProtocolMethod.Call(
		uintptr(unsafe.Pointer(i)),
		uintptr(unsafe.Pointer(uMethod)),
		uintptr(unsafe.Pointer(uParams)),
		uintptr(unsafe.Pointer(cb)),
	)
	if err != windows.ERROR_SUCCESS {
		return "", fmt.Errorf("CallDevToolsProtocolMethod: %w", err)
	}
	out, waitErr := WaitDevToolsResult(h.ch, timeout)
	if waitErr != nil {
		return "", waitErr
	}
	if out.err != nil {
		return "", out.err
	}
	return out.json, nil
}

func readAllFromIStream(stream *iStream) ([]byte, error) {
	if stream == nil || stream.vtbl == nil {
		return nil, fmt.Errorf("invalid IStream")
	}
	var stat struct {
		Name          [512]uint16
		Type          uint32
		Size          int64
		ModifiedTime  int64
		AccessTime    int64
		CreateTime    int64
		Mode          uint32
		LocksSupported uint32
		ClsID         windows.GUID
		State         uint32
		Reserved      uint32
	}
	_, _, err := stream.vtbl.Stat.Call(
		uintptr(unsafe.Pointer(stream)),
		uintptr(unsafe.Pointer(&stat)),
		1, // STATFLAG_NONAME
	)
	if err != windows.ERROR_SUCCESS {
		return nil, fmt.Errorf("IStream.Stat: %w", err)
	}
	size := stat.Size
	if size <= 0 {
		return nil, nil
	}
	if size > 64<<20 {
		return nil, fmt.Errorf("IStream too large (%d bytes)", size)
	}
	buf := make([]byte, size)
	var read uint32
	_, _, err = stream.vtbl.Read.Call(
		uintptr(unsafe.Pointer(stream)),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)),
		uintptr(unsafe.Pointer(&read)),
	)
	if err != windows.ERROR_SUCCESS {
		return nil, fmt.Errorf("IStream.Read: %w", err)
	}
	return buf[:read], nil
}
