package webview2

import (
	"unsafe"

	"github.com/pgmsoul/go-webview2/pkg/edge"
)

// This is copied from webview/webview.
// The documentation is included for convenience.

// Hint is used to configure window sizing and resizing behavior.
type Hint uint32

const (
	// HintNone specifies that width and height are default size
	HintNone Hint = iota

	// HintFixed specifies that window size can not be changed by a user
	HintFixed

	// HintMin specifies that width and height are minimum bounds
	HintMin

	// HintMax specifies that width and height are maximum bounds
	HintMax
)

// HookCallback is the type for the callback function that is executed when a JavaScript hook is called.
type HookCallback func(chromium *edge.Chromium, args []any)

// WindowPosition holds window position and size.
type WindowPosition struct {
	X, Y          int
	Width, Height int
	Hints         Hint
	// NoSize is used in SetPosition, and specifies that width and height will be ignored.
	NoSize bool
	// NoMove is used in SetPosition, and specifies that x and y will be ignored.
	NoMove bool
}

// WebView is the interface for the webview.
type WebView interface {

	// Run runs the main loop until it's terminated. After this function exits -
	// you must destroy the webview.
	Run()

	// Terminate stops the main loop. It is safe to call this function from
	// a background thread.
	Terminate()

	// Dispatch posts a function to be executed on the main thread. You normally
	// do not need to call this function, unless you want to tweak the native
	// window.
	Dispatch(f func())

	// Destroy destroys a webview and closes the native window.
	Destroy()

	// Window returns a native window handle pointer. When using GTK backend the
	// pointer is GtkWindow pointer, when using Cocoa backend the pointer is
	// NSWindow pointer, when using Win32 backend the pointer is HWND pointer.
	Window() unsafe.Pointer

	// SetTitle updates the title of the native window. Must be called from the UI
	// thread.
	SetTitle(title string)

	// SetPosition updates native window size and position.
	SetPosition(wp WindowPosition)

	// GetPosition returns the current size and position of the window.
	GetPosition() (x, y, width, height int)

	// SetSize updates native window size. See Hint constants.
	SetSize(w int, h int, hint Hint)

	// GetSize returns the current size.
	GetSize() (width, height int)

	// Navigate navigates webview to the given URL. URL may be a data URI, i.e.
	// "data:text/text,<html>...</html>". It is often ok not to url-encode it
	// properly, webview will re-encode it for you.
	Navigate(url string)

	// SetHtml sets the webview HTML directly.
	// The origin of the page is `about:blank`.
	SetHtml(html string)

	// Init injects JavaScript code at the initialization of the new page. Every
	// time the webview will open a new page - this initialization code will
	// be executed. It is guaranteed that code is executed before window.onload.
	Init(js string)

	// Eval evaluates arbitrary JavaScript code. Evaluation happens asynchronously,
	// also the result of the expression is ignored. Use RPC bindings if you want
	// to receive notifications about the results of the evaluation.
	Eval(js string)

	// SetInitHook injects JavaScript code when the page is initialized, and registers a callback function.
	// When the JavaScript code calls the hook function, the callback function will be executed.
	// The callback function can accept parameters of any basic type, slice, or map.
	// For example, when the frontend executes `__hook__(123, "hello")`, the `args` in the Go layer will receive `[123, "hello"]`.
	//
	// Parameters:
	//   js: The JavaScript code to be injected.
	//   hook: The name of the hook function.
	//   onHook: The callback function to be executed when the hook function is called.
	SetInitHook(js, hook string, onHook HookCallback)

	// SetOnLoadHook sets a callback function to be executed when the browser page is initialized.
	//
	// Parameters:
	//   onLoad: The callback function to be executed when the page is loaded.
	SetOnLoadHook(onLoad func(chromium *edge.Chromium))

	// Bind binds a callback function so that it will appear under the given name
	// as a global JavaScript function. Internally it uses webview_init().
	// Callback receives a request string and a user-provided argument pointer.
	// Request string is a JSON array of all the arguments passed to the
	// JavaScript function.
	//
	// f must be a function
	// f must return either value and error or just error
	Bind(name string, f interface{}) error

	GetChromium() *edge.Chromium
}
