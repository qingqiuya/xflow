[![Go](https://github.com/pgmsoul/go-webview2/actions/workflows/go.yml/badge.svg)](https://github.com/pgmsoul/go-webview2/actions/workflows/go.yml) [![Go Report Card](https://goreportcard.com/badge/github.com/pgmsoul/go-webview2)](https://goreportcard.com/report/github.com/pgmsoul/go-webview2) [![Go Reference](https://pkg.go.dev/badge/github.com/pgmsoul/go-webview2.svg)](https://pkg.go.dev/github.com/pgmsoul/go-webview2)

# go-webview2

> **Fork notice:** This repository is a fork of [jchv/go-webview2](https://github.com/jchv/go-webview2). It keeps the same overall API and upstream documentation below, and adds features needed for desktop apps with persistent window state and multiple WebView2 windows.
>
> 中文说明见 [README.zh-CN.md](./README.zh-CN.md)。

---

This package provides an interface for using the Microsoft Edge WebView2 component with Go. It is based on [webview/webview](https://github.com/webview/webview) and provides a compatible API.

Please note that this package only supports Windows, since it provides functionality specific to WebView2. If you wish to use this library for Windows, but use webview/webview for all other operating systems, you could use the [go-webview-selector](https://github.com/jchv/go-webview-selector) package instead. However, you will not be able to use WebView2-specific functionality.

If you wish to build desktop applications in Go using web technologies, please consider [Wails](https://wails.io/). Wails uses go-webview2 internally on Windows (upstream [jchv/go-webview2](https://github.com/jchv/go-webview2)).

## Fork enhancements

Changes on top of [jchv/go-webview2](https://github.com/jchv/go-webview2):

| Feature | Description |
|--------|-------------|
| **`OnClose` callback** | `WebViewOptions.OnClose` runs when the user closes the window (before destroy). Use it to persist window geometry, zoom, etc. |
| **Explicit window placement** | `WindowOptions` supports `X`, `Y`, and `Default` instead of only `Center`. Restore last position/size across sessions. |
| **Zoom factor API** | `GetChromium().GetController().GetZoomFactor()` / `PutZoomFactor()` for UI scale persistence. |
| **`Secondary` windows** | `WebViewOptions.Secondary: true` for extra WebView2 windows. On close, only that window’s message loop exits (`PostThreadMessage(WM_QUIT)`), not the whole process via `PostQuitMessage`. |
| **`NewWindowRequested`** | `target=_blank` and similar requests navigate in the **same** WebView instead of opening the system browser. |
| **Extended demo** | `cmd/demo/main_ext.go` shows saving/restoring window bounds and zoom to `config.json`. |

### Multi-window usage

When running a **second** WebView2 window in another goroutine (e.g. a standalone browser for login / page fetch):

1. Call **`runtime.LockOSThread()`** on that goroutine before `NewWithOptions`, and initialize COM on the same thread (`CoInitializeEx` with STA).
2. Set **`Secondary: true`** so closing the auxiliary window does not quit the primary app.
3. Use a **separate `DataPath`** if you need an isolated cookie/profile (e.g. `%LOCALAPPDATA%/your-app/browser-profile`).

Minimal pattern:

```go
go func() {
    runtime.LockOSThread()
    defer runtime.UnlockOSThread()
    // CoInitializeEx(0, COINIT_APARTMENTTHREADED) on this thread …

    w := webview2.NewWithOptions(webview2.WebViewOptions{
        Secondary: true,
        DataPath:  profileDir,
        WindowOptions: webview2.WindowOptions{Title: "Browser", Width: 1024, Height: 768},
        OnClose: func(w webview2.WebView) { /* save geometry */ },
    })
    w.Navigate("https://example.com")
    w.Run()
}()
```

## Demo

If you are using Windows 10+, the WebView2 runtime should already be installed. If you don't have it installed, you can download and install a copy from Microsoft's website:

[WebView2 runtime](https://developer.microsoft.com/en-us/microsoft-edge/webview2/)

After that, you should be able to run go-webview2 directly:

```bash
go run ./cmd/demo
```

The basic demo is in `cmd/demo/main.go`. For window state persistence, see `cmd/demo/main_ext.go`:

```bash
go run ./cmd/demo/main_ext.go
```

This will use go-winloader to load an embedded copy of WebView2Loader.dll. If you want, you can also provide a newer version of WebView2Loader.dll in the DLL search path and it should be picked up instead. It can be acquired from the WebView2 SDK (which is permissively licensed).

## `WebViewOptions` reference (fork additions)

```go
type WebViewOptions struct {
    Window unsafe.Pointer
    Debug  bool
    DataPath  string      // WebView2 user-data folder (cookies, cache, …)
    AutoFocus bool
    WindowOptions WindowOptions
    OnClose func(w WebView)           // called on WM_CLOSE, before destroy
    Secondary bool                     // auxiliary window; see Multi-window usage
}

type WindowOptions struct {
    Title   string
    Width   uint
    Height  uint
    IconId  uint   // Win32 resource ID; 0 = system default
    Default bool   // true → CW_USEDEFAULT for X/Y
    X       int      // used when Default is false
    Y       int
}
```

## License

Same as upstream unless noted otherwise in this repository. See [LICENSE](./LICENSE).
