package edge

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"
)

const screenshotTimeout = 30 * time.Second

const cdpCaptureViewport = `{"format":"png","fromSurface":true}`
const cdpCaptureFullPage = `{"format":"png","captureBeyondViewport":true,"fromSurface":true}`

// CaptureScreenshotViewport 截取当前 WebView 可视区域（CDP Page.captureScreenshot，等同 DevTools 普通截图）。
func (e *Chromium) CaptureScreenshotViewport() ([]byte, error) {
	return e.captureScreenshotCDP(cdpCaptureViewport)
}

// CaptureScreenshotFullPage 使用 DevTools Page.captureScreenshot 截取整页（等同 Edge 开发者工具「捕获完整大小截图」）。
func (e *Chromium) CaptureScreenshotFullPage() ([]byte, error) {
	return e.captureScreenshotCDP(cdpCaptureFullPage)
}

func (e *Chromium) captureScreenshotCDP(paramsJSON string) ([]byte, error) {
	if e.webview == nil {
		return nil, fmt.Errorf("webview not ready")
	}
	raw, err := e.webview.CallDevToolsProtocolMethodSync(
		"Page.captureScreenshot",
		paramsJSON,
		screenshotTimeout,
	)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Data string `json:"data"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return nil, fmt.Errorf("parse captureScreenshot result: %w", err)
	}
	if payload.Data == "" {
		return nil, fmt.Errorf("captureScreenshot returned empty data (raw=%s)", truncateForErr(raw, 256))
	}
	png, err := base64.StdEncoding.DecodeString(payload.Data)
	if err != nil {
		return nil, fmt.Errorf("decode screenshot: %w", err)
	}
	return png, nil
}

func truncateForErr(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

func WaitDevToolsResult(ch <-chan devToolsOutcome, timeout time.Duration) (devToolsOutcome, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case out := <-ch:
			return out, nil
		default:
		}
		if !PumpPendingMessages() {
			return devToolsOutcome{}, errMessageLoopEnded
		}
		time.Sleep(5 * time.Millisecond)
	}
	return devToolsOutcome{}, errWaitTimeout
}
