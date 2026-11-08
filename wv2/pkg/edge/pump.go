package edge

import (
	"time"
	"unsafe"

	"github.com/pgmsoul/go-webview2/internal/w32"
)

// PumpPendingMessages 处理当前线程待处理的 Win32 消息（供 COM/WebView2 回调在阻塞等待时使用）。
func PumpPendingMessages() bool {
	var msg w32.Msg
	for {
		ret, _, _ := w32.User32PeekMessageW.Call(
			uintptr(unsafe.Pointer(&msg)),
			0,
			0,
			0,
			w32.PMRemove,
		)
		if ret == 0 {
			return true
		}
		if msg.Message == w32.WMQuit {
			return false
		}
		_, _, _ = w32.User32TranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		_, _, _ = w32.User32DispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}
}

func waitChanBool(ch <-chan bool, timeout time.Duration) (bool, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case v := <-ch:
			return v, nil
		default:
		}
		if !PumpPendingMessages() {
			return false, errMessageLoopEnded
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false, errWaitTimeout
}

// WaitWithPump 在超时前泵送 Win32 消息并等待 channel 结果。
func WaitWithPump(ch <-chan bool, timeout time.Duration) (bool, error) {
	return waitChanBool(ch, timeout)
}

// WaitEvalResult 泵送消息并等待 EvalSync 风格的结果 channel。
func WaitEvalResult(ch <-chan evalScriptOutcome, timeout time.Duration) (evalScriptOutcome, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case out := <-ch:
			return out, nil
		default:
		}
		if !PumpPendingMessages() {
			return evalScriptOutcome{}, errMessageLoopEnded
		}
		time.Sleep(5 * time.Millisecond)
	}
	return evalScriptOutcome{}, errWaitTimeout
}
