package main

import (
	"encoding/json"
	"os"
	"unsafe"

	"github.com/pgmsoul/go-webview2"
	"golang.org/x/sys/windows"
)

// Config 应用配置，保存窗口位置、大小和缩放等信息
type Config struct {
	Width   int     `json:"width"`
	Height  int     `json:"height"`
	UIScale float64 `json:"ui_scale"`
	X       int     `json:"x"`
	Y       int     `json:"y"`
}

const (
	configFile = "config.json"
	title      = "WebView2 App"
)

// loadConfig 从 JSON 文件加载配置，文件不存在时返回零值 Config
func loadConfig() *Config {
	cfg := &Config{}
	data, err := os.ReadFile(configFile)
	if err != nil {
		return cfg
	}
	json.Unmarshal(data, cfg)
	return cfg
}

// saveConfig 将配置保存为 JSON 文件
func saveConfig(cfg *Config) {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return
	}
	os.WriteFile(configFile, data, 0644)
}

func main() {
	cfg := loadConfig()

	// 默认值保护
	if cfg.Width <= 0 {
		cfg.Width = 1280
	}
	if cfg.Height <= 0 {
		cfg.Height = 800
	}
	if cfg.UIScale < 0.1 {
		cfg.UIScale = 1.0
	}

	defPos := cfg.X <= 0 || cfg.Y <= 0
	w := webview2.NewWithOptions(webview2.WebViewOptions{
		AutoFocus: true,
		WindowOptions: webview2.WindowOptions{
			Title:   title,
			IconId:  1, //id 需要和 winres/winres.json 里的图标 ID 一致。
			Width:   uint(cfg.Width),
			Height:  uint(cfg.Height),
			Default: defPos,
			X:       cfg.X,
			Y:       cfg.Y,
		},
		OnClose: func(w webview2.WebView) {
			zoom, _ := w.GetChromium().GetController().GetZoomFactor()
			cfg.UIScale = zoom
			// 获取还原后的窗口位置和大小（最大化时 GetWindowRect 会错，用 rcNormalPosition）
			hwnd := w.Window()
			type point struct{ X, Y int32 }
			type rect struct{ Left, Top, Right, Bottom int32 }
			var wp struct {
				Length           uint32
				Flags            uint32
				ShowCmd          uint32
				PtMinPosition    point
				PtMaxPosition    point
				RcNormalPosition rect
			}
			wp.Length = uint32(unsafe.Sizeof(wp))
			procGetWindowPlacement := windows.NewLazyDLL("user32.dll").NewProc("GetWindowPlacement")
			ret, _, _ := procGetWindowPlacement.Call(
				uintptr(hwnd),
				uintptr(unsafe.Pointer(&wp)),
			)
			if ret != 0 {
				r := wp.RcNormalPosition
				cfg.X = int(r.Left)
				cfg.Y = int(r.Top)
				cfg.Width = int(r.Right - r.Left)
				cfg.Height = int(r.Bottom - r.Top)
			}
			//jufmt.Yellow.Println("保存配置", *cfg)

			// 保存配置
			saveConfig(cfg)
		},
	})
	if w == nil {
		panic("创建 webview 失败")
	}
	defer w.Destroy()

	// 恢复窗口位置

	// 页面加载完成后设置缩放
	w.Bind("__ready__", func() {
		w.Dispatch(func() {
			chromium := w.GetChromium()
			if chromium != nil {
				ctrl := chromium.GetController()
				if ctrl != nil {
					//jufmt.Yellow.Println("set zoom", cfg.UIScale)
					ctrl.PutZoomFactor(cfg.UIScale)
				}
			}
		})
	})

	w.Init(`window.addEventListener('load', () => { __ready__(); })`)

	w.Navigate("https://www.baidu.com")
	w.Run()
}
