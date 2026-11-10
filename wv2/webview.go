//go:build windows
// +build windows

package webview2

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/pgmsoul/go-webview2/internal/w32"
	"github.com/pgmsoul/go-webview2/pkg/edge"

	"golang.org/x/sys/windows"
)

var (
	windowContext     = map[uintptr]interface{}{}
	windowContextSync sync.RWMutex
	nextWndClassSeq   atomic.Uint64
)

func getWindowContext(wnd uintptr) interface{} {
	windowContextSync.RLock()
	defer windowContextSync.RUnlock()
	return windowContext[wnd]
}

func setWindowContext(wnd uintptr, data interface{}) {
	windowContextSync.Lock()
	defer windowContextSync.Unlock()
	windowContext[wnd] = data
}

type browser interface {
	Embed(hwnd uintptr) bool
	Resize()
	Navigate(url string)
	NavigateToString(htmlContent string)
	Init(script string)
	Eval(script string)
	NotifyParentWindowPositionChanged() error
	Focus()
}

type webview struct {
	hwnd       uintptr
	mainthread uintptr
	browser    browser
	autofocus  bool
	secondary  bool
	className  string
	hinstance  windows.Handle
	maxsz      w32.Point
	minsz      w32.Point
	m          sync.Mutex
	bindings   map[string]interface{}
	dispatchq  []func()
	onclose    func()
}

type WindowOptions struct {
	Title   string
	Width   int
	Height  int
	IconId  int
	Default bool
	X       int
	Y       int
	// ClassName 窗口类名前缀；库会为每个窗口追加唯一后缀并独立 RegisterClassExW。
	// 留空则自动生成。
	ClassName string

	// Owner 指定这个窗口的 owner（对应 CreateWindowExW 的 hWndParent）。
	// 在 Windows 上：顶层且「无 owner」的窗口可见时会占任务栏；给了 owner 就不占。
	// 必须在创建时就传进去——事后 SetWindowLongPtr(GWLP_HWNDPARENT) 不生效
	// （窗口已经进过任务栏了）。Wails 那边验证过这套在 Win10/11 都稳。
	Owner uintptr
}

type WebViewOptions struct {
	Window unsafe.Pointer
	Debug  bool

	// DataPath specifies the datapath for the WebView2 runtime to use for the
	// browser instance.
	DataPath string

	// AutoFocus will try to keep the WebView2 widget focused when the window
	// is focused.
	AutoFocus bool

	// WindowOptions customizes the window that is created to embed the
	// WebView2 widget.
	WindowOptions WindowOptions

	// WM_CLOSE
	OnClose func(w WebView)

	// Secondary 辅窗口：关闭时仅结束本窗口消息循环，不 PostQuitMessage 退出进程。
	Secondary bool
}

// New creates a new webview in a new window.
func New(debug bool) WebView { return NewWithOptions(WebViewOptions{Debug: debug}) }

// NewWindow creates a new webview using an existing window.
//
// Deprecated: Use NewWithOptions.
func NewWindow(debug bool, window unsafe.Pointer) WebView {
	return NewWithOptions(WebViewOptions{Debug: debug, Window: window})
}

// NewWithOptions creates a new webview using the provided options.
func NewWithOptions(options WebViewOptions) WebView {
	w := &webview{}
	w.bindings = map[string]interface{}{}
	w.autofocus = options.AutoFocus
	w.secondary = options.Secondary

	chromium := edge.NewChromium()
	chromium.MessageCallback = w.msgcb
	chromium.DataPath = options.DataPath
	chromium.SetPermission(edge.CoreWebView2PermissionKindClipboardRead, edge.CoreWebView2PermissionStateAllow)

	w.browser = chromium
	w.mainthread, _, _ = w32.Kernel32GetCurrentThreadID.Call()
	if options.OnClose != nil {
		w.onclose = func() {
			options.OnClose(w)
		}
	}

	if !w.CreateWithOptions(options.WindowOptions) {
		return nil
	}

	settings, err := chromium.GetSettings()
	if err != nil {
		log.Fatal(err)
	}
	// disable context menu
	err = settings.PutAreDefaultContextMenusEnabled(options.Debug)
	if err != nil {
		log.Fatal(err)
	}
	// disable developer tools
	err = settings.PutAreDevToolsEnabled(options.Debug)
	if err != nil {
		log.Fatal(err)
	}

	return w
}

type rpcMessage struct {
	ID     int               `json:"id"`
	Method string            `json:"method"`
	Params []json.RawMessage `json:"params"`
}

func jsString(v interface{}) string { b, _ := json.Marshal(v); return string(b) }

func (w *webview) msgcb(msg string) {
	d := rpcMessage{}
	if err := json.Unmarshal([]byte(msg), &d); err != nil {
		log.Printf("invalid RPC message: %v", err)
		return
	}

	id := strconv.Itoa(d.ID)
	if res, err := w.callbinding(d); err != nil {
		w.Dispatch(func() {
			w.Eval("window._rpc[" + id + "].reject(" + jsString(err.Error()) + "); window._rpc[" + id + "] = undefined")
		})
	} else if b, err := json.Marshal(res); err != nil {
		w.Dispatch(func() {
			w.Eval("window._rpc[" + id + "].reject(" + jsString(err.Error()) + "); window._rpc[" + id + "] = undefined")
		})
	} else {
		w.Dispatch(func() {
			w.Eval("window._rpc[" + id + "].resolve(" + string(b) + "); window._rpc[" + id + "] = undefined")
		})
	}
}

func (w *webview) callbinding(d rpcMessage) (interface{}, error) {
	w.m.Lock()
	f, ok := w.bindings[d.Method]
	w.m.Unlock()
	if !ok {
		return nil, nil
	}

	v := reflect.ValueOf(f)
	isVariadic := v.Type().IsVariadic()
	numIn := v.Type().NumIn()
	if (isVariadic && len(d.Params) < numIn-1) || (!isVariadic && len(d.Params) != numIn) {
		return nil, errors.New("function arguments mismatch")
	}
	args := []reflect.Value{}
	for i := range d.Params {
		var arg reflect.Value
		if isVariadic && i >= numIn-1 {
			arg = reflect.New(v.Type().In(numIn - 1).Elem())
		} else {
			arg = reflect.New(v.Type().In(i))
		}
		if err := json.Unmarshal(d.Params[i], arg.Interface()); err != nil {
			return nil, err
		}
		args = append(args, arg.Elem())
	}

	errorType := reflect.TypeOf((*error)(nil)).Elem()
	res := v.Call(args)
	switch len(res) {
	case 0:
		// No results from the function, just return nil
		return nil, nil

	case 1:
		// One result may be a value, or an error
		if res[0].Type().Implements(errorType) {
			if res[0].Interface() != nil {
				return nil, res[0].Interface().(error)
			}
			return nil, nil
		}
		return res[0].Interface(), nil

	case 2:
		// Two results: first one is value, second is error
		if !res[1].Type().Implements(errorType) {
			return nil, errors.New("second return value must be an error")
		}
		if res[1].Interface() == nil {
			return res[0].Interface(), nil
		}
		return res[0].Interface(), res[1].Interface().(error)

	default:
		return nil, errors.New("unexpected number of return values")
	}
}

func wndproc(hwnd, msg, wp, lp uintptr) uintptr {
	if w, ok := getWindowContext(hwnd).(*webview); ok {
		switch msg {
		case w32.WMMove, w32.WMMoving:
			_ = w.browser.NotifyParentWindowPositionChanged()
		case w32.WMNCLButtonDown:
			_, _, _ = w32.User32SetFocus.Call(w.hwnd)
			r, _, _ := w32.User32DefWindowProcW.Call(hwnd, msg, wp, lp)
			return r
		case w32.WMSize:
			w.browser.Resize()
		case w32.WMActivate:
			if wp == w32.WAInactive {
				break
			}
			if w.autofocus {
				w.browser.Focus()
			}
		case w32.WMClose:
			// 夏尔改：装了 OnClose 就当"关闭"= 交给回调处理（比如缩到托盘），不销毁窗口
			if w.onclose != nil {
				w.onclose()
				return 0
			}
			_, _, _ = w32.User32DestroyWindow.Call(hwnd)
		case w32.WMDestroy:
			if w.className != "" {
				className, _ := windows.UTF16PtrFromString(w.className)
				_, _, _ = w32.User32UnregisterClassW.Call(
					uintptr(unsafe.Pointer(className)),
					uintptr(w.hinstance),
				)
				w.className = ""
			}
			if w.secondary {
				_, _, _ = w32.User32PostThreadMessageW.Call(w.mainthread, w32.WMQuit, 0, 0)
			} else {
				w.Terminate()
			}
		case w32.WMGetMinMaxInfo:
			lpmmi := (*w32.MinMaxInfo)(unsafe.Pointer(lp))
			if w.maxsz.X > 0 && w.maxsz.Y > 0 {
				lpmmi.PtMaxSize = w.maxsz
				lpmmi.PtMaxTrackSize = w.maxsz
			}
			if w.minsz.X > 0 && w.minsz.Y > 0 {
				lpmmi.PtMinTrackSize = w.minsz
			}
		default:
			r, _, _ := w32.User32DefWindowProcW.Call(hwnd, msg, wp, lp)
			return r
		}
		return 0
	}
	r, _, _ := w32.User32DefWindowProcW.Call(hwnd, msg, wp, lp)
	return r
}

func (w *webview) Create(debug bool, window unsafe.Pointer) bool {
	// This function signature stopped making sense a long time ago.
	// It is but legacy cruft at this point.
	return w.CreateWithOptions(WindowOptions{})
}

func allocWndClassName(prefix string, secondary bool) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		if secondary {
			prefix = "WebView2Secondary"
		} else {
			prefix = "WebView2"
		}
	}
	return fmt.Sprintf("%s_%d", prefix, nextWndClassSeq.Add(1))
}

func (w *webview) CreateWithOptions(opts WindowOptions) bool {
	var hinstance windows.Handle
	if err := windows.GetModuleHandleEx(0, nil, &hinstance); err != nil {
		return false
	}
	w.hinstance = hinstance

	var icon uintptr
	if opts.IconId == 0 {
		icow, _, _ := w32.User32GetSystemMetrics.Call(w32.SystemMetricsCxIcon)
		icoh, _, _ := w32.User32GetSystemMetrics.Call(w32.SystemMetricsCyIcon)
		icon, _, _ = w32.User32LoadImageW.Call(uintptr(hinstance), 32512, icow, icoh, 0)
	} else {
		icon, _, _ = w32.User32LoadImageW.Call(uintptr(hinstance), uintptr(opts.IconId), 1, 0, 0, w32.LR_DEFAULTSIZE|w32.LR_SHARED)
	}

	classStr := allocWndClassName(opts.ClassName, w.secondary)
	w.className = classStr
	className, _ := windows.UTF16PtrFromString(classStr)
	wc := w32.WndClassExW{
		CbSize:        uint32(unsafe.Sizeof(w32.WndClassExW{})),
		HInstance:     hinstance,
		LpszClassName: className,
		HIcon:         windows.Handle(icon),
		HIconSm:       windows.Handle(icon),
		LpfnWndProc:   windows.NewCallback(wndproc),
	}
	atom, _, _ := w32.User32RegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	if atom == 0 {
		log.Printf("RegisterClassExW failed for %q", classStr)
		return false
	}

	windowName, _ := windows.UTF16PtrFromString(opts.Title)

	windowWidth := opts.Width
	if windowWidth == 0 {
		windowWidth = 640
	}
	windowHeight := opts.Height
	if windowHeight == 0 {
		windowHeight = 480
	}

	var posX, posY uint
	if opts.Default {
		posX = w32.CW_USEDEFAULT
		posY = w32.CW_USEDEFAULT
	} else {
		posX = uint(opts.X)
		posY = uint(opts.Y)
	}

	w.hwnd, _, _ = w32.User32CreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(windowName)),
		0xCF0000, // WS_OVERLAPPEDWINDOW
		uintptr(posX),
		uintptr(posY),
		uintptr(windowWidth),
		uintptr(windowHeight),
		opts.Owner, // hWndParent：有 owner 的窗口不进任务栏
		0,
		uintptr(hinstance),
		0,
	)
	if w.hwnd == 0 {
		className, _ := windows.UTF16PtrFromString(classStr)
		_, _, _ = w32.User32UnregisterClassW.Call(uintptr(unsafe.Pointer(className)), uintptr(hinstance))
		w.className = ""
		return false
	}
	setWindowContext(w.hwnd, w)

	_, _, _ = w32.User32ShowWindow.Call(w.hwnd, w32.SWShow)
	_, _, _ = w32.User32UpdateWindow.Call(w.hwnd)
	_, _, _ = w32.User32SetFocus.Call(w.hwnd)

	if !w.browser.Embed(w.hwnd) {
		return false
	}
	w.browser.Resize()
	return true
}

func (w *webview) Destroy() {
	_, _, _ = w32.User32PostMessageW.Call(w.hwnd, w32.WMClose, 0, 0)
}

func (w *webview) Run() {
	var msg w32.Msg
	for {
		_, _, _ = w32.User32GetMessageW.Call(
			uintptr(unsafe.Pointer(&msg)),
			0,
			0,
			0,
		)
		if msg.Message == w32.WMApp {
			w.m.Lock()
			q := append([]func(){}, w.dispatchq...)
			w.dispatchq = []func(){}
			w.m.Unlock()
			for _, v := range q {
				v()
			}
		} else if msg.Message == w32.WMQuit {
			return
		}
		r, _, _ := w32.User32GetAncestor.Call(uintptr(msg.Hwnd), w32.GARoot)
		r, _, _ = w32.User32IsDialogMessage.Call(r, uintptr(unsafe.Pointer(&msg)))
		if r != 0 {
			continue
		}
		_, _, _ = w32.User32TranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		_, _, _ = w32.User32DispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}
}

func (w *webview) Terminate() {
	_, _, _ = w32.User32PostQuitMessage.Call(0)
}

func (w *webview) Window() unsafe.Pointer {
	return unsafe.Pointer(w.hwnd)
}

func (w *webview) Navigate(url string) {
	w.browser.Navigate(url)
}

func (w *webview) SetHtml(html string) {
	w.browser.NavigateToString(html)
}

func (w *webview) SetTitle(title string) {
	_title, err := windows.UTF16FromString(title)
	if err != nil {
		_title, _ = windows.UTF16FromString("")
	}
	_, _, _ = w32.User32SetWindowTextW.Call(w.hwnd, uintptr(unsafe.Pointer(&_title[0])))
}

// SetPosition supports setting the window's position (X, Y) and size (Width, Height) simultaneously or separately.
func (w *webview) SetPosition(wp WindowPosition) {
	index := w32.GWLStyle
	style := w32.GetWindowLong(w.hwnd, index)
	hints := wp.Hints
	x, y, width, height := wp.X, wp.Y, wp.Width, wp.Height
	if hints == HintFixed {
		style &^= w32.WSThickFrame | w32.WSMaximizeBox
	} else {
		style |= w32.WSThickFrame | w32.WSMaximizeBox
	}
	w32.SetWindowLong(w.hwnd, index, style)

	if hints == HintMax {
		w.maxsz.X = int32(width)
		w.maxsz.Y = int32(height)
	} else if hints == HintMin {
		w.minsz.X = int32(width)
		w.minsz.Y = int32(height)
	} else {
		// 1. Initialize the base flag: add SWPFrameChanged to ensure that the style modification takes effect immediately.
		flags := uintptr(w32.SWPNoZOrder | w32.SWPNoActivate | w32.SWPFrameChanged)

		var finalX, finalY, finalWidth, finalHeight uintptr

		// 2. Handle position (X, Y)
		if wp.NoMove {
			// The user "only wants to change the size, not the position", add SWPNoMove to tell Windows to ignore X and Y.
			flags |= w32.SWPNoMove
		} else {
			finalX = uintptr(x)
			finalY = uintptr(y)
		}

		// 3. Handle size (Width, Height)
		if wp.NoSize {
			// The user "only wants to change the position, not the size", add SWPNoSize to tell Windows to ignore width and height.
			flags |= w32.SWPNoSize
		} else {
			// If you want to change the size, still use the original AdjustWindowRect to prevent the title bar from eating up the client area size.
			r := w32.Rect{}
			r.Left = 0
			r.Top = 0
			r.Right = int32(width)
			r.Bottom = int32(height)
			_, _, _ = w32.User32AdjustWindowRect.Call(uintptr(unsafe.Pointer(&r)), w32.WSOverlappedWindow, 0)

			finalWidth = uintptr(r.Right - r.Left)
			finalHeight = uintptr(r.Bottom - r.Top)

			// If you are also moving the position, you need to add the Left offset calculated by AdjustWindowRect to prevent misalignment.
			if (flags & w32.SWPNoMove) == 0 {
				finalX = uintptr(int(finalX) + int(r.Left))
				finalY = uintptr(int(finalY) + int(r.Top))
			}
		}

		// 4. Precisely feed the calculated parameters to SetWindowPos.
		_, _, _ = w32.User32SetWindowPos.Call(
			w.hwnd, 0,
			finalX, finalY,
			finalWidth, finalHeight,
			flags,
		)

		// 5. If the size has changed, notify the browser kernel to redraw.
		if (flags & w32.SWPNoSize) == 0 {
			w.browser.Resize()
		}
	}
}
func (w *webview) GetPosition() (px, py, width, height int) {
	var wp w32.WindowPlacement
	wp.Length = uint32(unsafe.Sizeof(wp))
	res, _, _ := w32.User32GetWindowPlacement.Call(w.hwnd, uintptr(unsafe.Pointer(&wp)))
	if res != 0 {
		r := wp.RcNormalPosition
		return int(r.Left), int(r.Top), int(r.Right - r.Left), int(r.Bottom - r.Top)
	}
	var r w32.Rect
	_, _, _ = w32.User32GetWindowRect.Call(w.hwnd, uintptr(unsafe.Pointer(&r)))
	return int(r.Left), int(r.Top), int(r.Right - r.Left), int(r.Bottom - r.Top)
}

func (w *webview) SetSize(width int, height int, hints Hint) {
	w.SetPosition(WindowPosition{Width: width, Height: height, Hints: hints, NoMove: true})
}

func (w *webview) GetSize() (width, height int) {
	_, _, width, height = w.GetPosition()
	return
}

func (w *webview) Init(js string) {
	w.browser.Init(js)
}

func (w *webview) Eval(js string) {
	w.browser.Eval(js)
}

func (w *webview) Dispatch(f func()) {
	w.m.Lock()
	w.dispatchq = append(w.dispatchq, f)
	w.m.Unlock()
	_, _, _ = w32.User32PostThreadMessageW.Call(w.mainthread, w32.WMApp, 0, 0)
}

func (w *webview) Bind(name string, f interface{}) error {
	v := reflect.ValueOf(f)
	if v.Kind() != reflect.Func {
		return errors.New("only functions can be bound")
	}
	if n := v.Type().NumOut(); n > 2 {
		return errors.New("function may only return a value or a value+error")
	}
	w.m.Lock()
	w.bindings[name] = f
	w.m.Unlock()

	w.Init("(function() { var name = " + jsString(name) + ";" + `
		var RPC = window._rpc = (window._rpc || {nextSeq: 1});
		window[name] = function() {
		  var seq = RPC.nextSeq++;
		  var promise = new Promise(function(resolve, reject) {
			RPC[seq] = {
			  resolve: resolve,
			  reject: reject,
			};
		  });
		  window.external.invoke(JSON.stringify({
			id: seq,
			method: name,
			params: Array.prototype.slice.call(arguments),
		  }));
		  return promise;
		}
	})()`)

	return nil
}

func (w *webview) GetChromium() *edge.Chromium {
	if c, ok := w.browser.(*edge.Chromium); ok {
		return c
	}
	return nil
}

func (w *webview) SetOnLoadHook(onLoad func(chromium *edge.Chromium)) {
	w.SetInitHook(`window.addEventListener('load', () => { __onload__(); })`, "__onload__",
		func(chromium *edge.Chromium, args []any) {
			if onLoad != nil {
				onLoad(chromium)
			}
		})
}

func (w *webview) SetInitHook(js, hook string, onHook HookCallback) {
	w.Bind(hook, func(args ...any) {
		w.Dispatch(func() {
			chromium := w.GetChromium()
			if chromium != nil {
				if onHook != nil {
					onHook(chromium, args)
				}
			}
		})
	})
	w.Init(js)
}
