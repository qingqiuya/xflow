package edge

import (
	"fmt"

	"github.com/pgmsoul/go-webview2/internal/w32"
)

type _ICoreWebView2ExecuteScriptCompletedHandlerVtbl struct {
	_IUnknownVtbl
	Invoke ComProc
}

type iCoreWebView2ExecuteScriptCompletedHandler struct {
	vtbl *_ICoreWebView2ExecuteScriptCompletedHandlerVtbl
	impl _ICoreWebView2ExecuteScriptCompletedHandlerImpl
}

func _ICoreWebView2ExecuteScriptCompletedHandlerIUnknownQueryInterface(this *iCoreWebView2ExecuteScriptCompletedHandler, refiid, object uintptr) uintptr {
	return this.impl.QueryInterface(refiid, object)
}

func _ICoreWebView2ExecuteScriptCompletedHandlerIUnknownAddRef(this *iCoreWebView2ExecuteScriptCompletedHandler) uintptr {
	return this.impl.AddRef()
}

func _ICoreWebView2ExecuteScriptCompletedHandlerIUnknownRelease(this *iCoreWebView2ExecuteScriptCompletedHandler) uintptr {
	return this.impl.Release()
}

func _ICoreWebView2ExecuteScriptCompletedHandlerInvoke(this *iCoreWebView2ExecuteScriptCompletedHandler, errorCode uintptr, result *uint16) uintptr {
	return this.impl.ExecuteScriptCompleted(errorCode, result)
}

type _ICoreWebView2ExecuteScriptCompletedHandlerImpl interface {
	_IUnknownImpl
	ExecuteScriptCompleted(errorCode uintptr, result *uint16) uintptr
}

var _ICoreWebView2ExecuteScriptCompletedHandlerFn = _ICoreWebView2ExecuteScriptCompletedHandlerVtbl{
	_IUnknownVtbl{
		NewComProc(_ICoreWebView2ExecuteScriptCompletedHandlerIUnknownQueryInterface),
		NewComProc(_ICoreWebView2ExecuteScriptCompletedHandlerIUnknownAddRef),
		NewComProc(_ICoreWebView2ExecuteScriptCompletedHandlerIUnknownRelease),
	},
	NewComProc(_ICoreWebView2ExecuteScriptCompletedHandlerInvoke),
}

func newICoreWebView2ExecuteScriptCompletedHandler(impl _ICoreWebView2ExecuteScriptCompletedHandlerImpl) *iCoreWebView2ExecuteScriptCompletedHandler {
	return &iCoreWebView2ExecuteScriptCompletedHandler{
		vtbl: &_ICoreWebView2ExecuteScriptCompletedHandlerFn,
		impl: impl,
	}
}

// evalScriptHandler 单次 ExecuteScript 回调。
type evalScriptHandler struct {
	ch chan evalScriptOutcome
}

type evalScriptOutcome struct {
	result string
	err    error
}

func (h *evalScriptHandler) QueryInterface(_, _ uintptr) uintptr { return 0 }
func (h *evalScriptHandler) AddRef() uintptr                     { return 1 }
func (h *evalScriptHandler) Release() uintptr                    { return 1 }

func (h *evalScriptHandler) ExecuteScriptCompleted(errorCode uintptr, result *uint16) uintptr {
	out := evalScriptOutcome{}
	if int32(errorCode) < 0 {
		out.err = fmt.Errorf("ExecuteScript failed: %08x", errorCode)
	} else if result != nil {
		out.result = w32.Utf16PtrToString(result)
	}
	select {
	case h.ch <- out:
	default:
	}
	return 0
}
