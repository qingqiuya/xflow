[![Go](https://github.com/pgmsoul/go-webview2/actions/workflows/go.yml/badge.svg)](https://github.com/pgmsoul/go-webview2/actions/workflows/go.yml) [![Go Report Card](https://goreportcard.com/badge/github.com/pgmsoul/go-webview2)](https://goreportcard.com/report/github.com/pgmsoul/go-webview2) [![Go Reference](https://pkg.go.dev/badge/github.com/pgmsoul/go-webview2.svg)](https://pkg.go.dev/github.com/pgmsoul/go-webview2)

# go-webview2

> **Fork 说明：** 本仓库 fork 自 [jchv/go-webview2](https://github.com/jchv/go-webview2)。在保留上游 API 与原有说明的基础上，增加了桌面应用所需的窗口状态持久化、多 WebView2 窗口等能力。
>
> English: [README.md](./README.md)

---

本包为 Go 提供 Microsoft Edge WebView2 组件的封装，基于 [webview/webview](https://github.com/webview/webview)，API 与之兼容。

请注意本包仅支持 Windows（WebView2 为 Windows 专有）。若希望在 Windows 上使用 WebView2、在其他系统上使用 webview/webview，可选用 [go-webview-selector](https://github.com/jchv/go-webview-selector)，但将无法使用 WebView2 专有功能。

若要用 Go + Web 技术构建桌面应用，也可考虑 [Wails](https://wails.io/)。Wails 在 Windows 上内部使用 go-webview2（上游为 [jchv/go-webview2](https://github.com/jchv/go-webview2)）。

## 相对上游的新增能力

在 [jchv/go-webview2](https://github.com/jchv/go-webview2) 基础上的改动：

| 特性 | 说明 |
|------|------|
| **`OnClose` 回调** | `WebViewOptions.OnClose` 在用户关闭窗口时（销毁前）触发，可用于保存窗口位置、缩放等。 |
| **显式窗口位置** | `WindowOptions` 支持 `X`、`Y`、`Default`，替代仅 `Center` 的方式，便于跨会话恢复位置与大小。 |
| **缩放 API** | `GetChromium().GetController().GetZoomFactor()` / `PutZoomFactor()`，用于持久化 UI 缩放。 |
| **`Secondary` 辅窗口** | `WebViewOptions.Secondary: true` 表示辅窗口；关闭时仅结束该窗口的消息循环（`PostThreadMessage(WM_QUIT)`），不会通过 `PostQuitMessage` 退出整个进程。 |
| **`NewWindowRequested`** | 处理 `target=_blank` 等新窗口请求，在**同一** WebView 内导航，而不是打开系统默认浏览器。 |
| **扩展 Demo** | `cmd/demo/main_ext.go` 演示将窗口 bounds 与 zoom 保存到 `config.json`。 |

### 多窗口用法

在**另一个 goroutine** 中运行第二个 WebView2 窗口时（例如独立浏览器窗用于登录、抓取页面）：

1. 在调用 `NewWithOptions` **之前**对该 goroutine 执行 **`runtime.LockOSThread()`**，并在同一线程上初始化 COM（`CoInitializeEx`，STA 模式）。
2. 设置 **`Secondary: true`**，避免关闭辅窗口时连带退出主应用。
3. 如需独立 Cookie/配置，使用**单独的 `DataPath`**（例如 `%LOCALAPPDATA%/your-app/browser-profile`）。

最小示例：

```go
go func() {
    runtime.LockOSThread()
    defer runtime.UnlockOSThread()
    // 在本线程 CoInitializeEx(0, COINIT_APARTMENTTHREADED) …

    w := webview2.NewWithOptions(webview2.WebViewOptions{
        Secondary: true,
        DataPath:  profileDir,
        WindowOptions: webview2.WindowOptions{Title: "Browser", Width: 1024, Height: 768},
        OnClose: func(w webview2.WebView) { /* 保存几何信息 */ },
    })
    w.Navigate("https://example.com")
    w.Run()
}()
```

## Demo

Windows 10 及以上通常已安装 WebView2 运行时。若未安装，可从微软官网下载：

[WebView2 runtime](https://developer.microsoft.com/en-us/microsoft-edge/webview2/)

安装后直接运行：

```bash
go run ./cmd/demo
```

基础示例见 `cmd/demo/main.go`。窗口状态持久化示例见 `cmd/demo/main_ext.go`：

```bash
go run ./cmd/demo/main_ext.go
```

本库通过 go-winloader 加载内嵌的 WebView2Loader.dll。也可在 DLL 搜索路径中放置更新版本的 WebView2Loader.dll（来自 WebView2 SDK，许可宽松）。

## `WebViewOptions` 参考（Fork 新增字段）

```go
type WebViewOptions struct {
    Window unsafe.Pointer
    Debug  bool
    DataPath  string      // WebView2 用户数据目录（Cookie、缓存等）
    AutoFocus bool
    WindowOptions WindowOptions
    OnClose func(w WebView)           // WM_CLOSE 时调用，销毁前
    Secondary bool                     // 辅窗口，见「多窗口用法」
}

type WindowOptions struct {
    Title   string
    Width   uint
    Height  uint
    IconId  uint   // Win32 资源 ID；0 为系统默认图标
    Default bool   // true 时 X/Y 使用 CW_USEDEFAULT
    X       int      // Default 为 false 时生效
    Y       int
}
```

## 许可证

与上游一致，详见本仓库 [LICENSE](./LICENSE)。
