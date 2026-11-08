package edge

import (
	"fmt"

	"github.com/pgmsoul/go-webview2/internal/w32"
)

type _ICoreWebView2CallDevToolsProtocolMethodCompletedHandlerVtbl struct {
	_IUnknownVtbl
	Invoke ComProc
}

type iCoreWebView2CallDevToolsProtocolMethodCompletedHandler struct {
	vtbl *_ICoreWebView2CallDevToolsProtocolMethodCompletedHandlerVtbl
	impl _ICoreWebView2CallDevToolsProtocolMethodCompletedHandlerImpl
}

func _ICoreWebView2CallDevToolsProtocolMethodCompletedHandlerIUnknownQueryInterface(this *iCoreWebView2CallDevToolsProtocolMethodCompletedHandler, refiid, object uintptr) uintptr {
	return this.impl.QueryInterface(refiid, object)
}

func _ICoreWebView2CallDevToolsProtocolMethodCompletedHandlerIUnknownAddRef(this *iCoreWebView2CallDevToolsProtocolMethodCompletedHandler) uintptr {
	return this.impl.AddRef()
}

func _ICoreWebView2CallDevToolsProtocolMethodCompletedHandlerIUnknownRelease(this *iCoreWebView2CallDevToolsProtocolMethodCompletedHandler) uintptr {
	return this.impl.Release()
}

func _ICoreWebView2CallDevToolsProtocolMethodCompletedHandlerInvoke(this *iCoreWebView2CallDevToolsProtocolMethodCompletedHandler, errorCode uintptr, returnObjectAsJson *uint16) uintptr {
	return this.impl.DevToolsProtocolMethodCompleted(errorCode, returnObjectAsJson)
}

type _ICoreWebView2CallDevToolsProtocolMethodCompletedHandlerImpl interface {
	_IUnknownImpl
	DevToolsProtocolMethodCompleted(errorCode uintptr, returnObjectAsJson *uint16) uintptr
}

var _ICoreWebView2CallDevToolsProtocolMethodCompletedHandlerFn = _ICoreWebView2CallDevToolsProtocolMethodCompletedHandlerVtbl{
	_IUnknownVtbl{
		NewComProc(_ICoreWebView2CallDevToolsProtocolMethodCompletedHandlerIUnknownQueryInterface),
		NewComProc(_ICoreWebView2CallDevToolsProtocolMethodCompletedHandlerIUnknownAddRef),
		NewComProc(_ICoreWebView2CallDevToolsProtocolMethodCompletedHandlerIUnknownRelease),
	},
	NewComProc(_ICoreWebView2CallDevToolsProtocolMethodCompletedHandlerInvoke),
}

func newICoreWebView2CallDevToolsProtocolMethodCompletedHandler(impl _ICoreWebView2CallDevToolsProtocolMethodCompletedHandlerImpl) *iCoreWebView2CallDevToolsProtocolMethodCompletedHandler {
	return &iCoreWebView2CallDevToolsProtocolMethodCompletedHandler{
		vtbl: &_ICoreWebView2CallDevToolsProtocolMethodCompletedHandlerFn,
		impl: impl,
	}
}

type devToolsHandler struct {
	ch chan devToolsOutcome
}

type devToolsOutcome struct {
	json string
	err  error
}

func (h *devToolsHandler) QueryInterface(_, _ uintptr) uintptr { return 0 }
func (h *devToolsHandler) AddRef() uintptr                     { return 1 }
func (h *devToolsHandler) Release() uintptr                    { return 1 }

func (h *devToolsHandler) DevToolsProtocolMethodCompleted(errorCode uintptr, returnObjectAsJson *uint16) uintptr {
	out := devToolsOutcome{}
	if int32(errorCode) < 0 {
		out.err = fmt.Errorf("CallDevToolsProtocolMethod failed: %08x", errorCode)
	} else if returnObjectAsJson != nil {
		out.json = w32.Utf16PtrToString(returnObjectAsJson)
	}
	select {
	case h.ch <- out:
	default:
	}
	return 0
}
