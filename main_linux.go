//go:build !windows

package main

// Linux 版入口：本地服务 + 原生窗口（webkit2gtk，靠 webview_go）。
//
// 和 Windows 版共用同一套下载引擎（manager / httpdl / btdl）和同一份 ui.html，
// 区别只在外壳：那边是 WebView2，这边是 WebKitGTK。
//
// 用法：
//   xflow                  起服务并开窗口
//   xflow --headless       只跑服务（服务器上、或者没图形界面时）
//   xflow --browser        服务起来后交给系统浏览器（不想要原生窗口时）
//   xflow --port 8899      换端口
//   xflow --dir /data/dl   换默认下载目录（写进配置）
//
// 没有 DISPLAY / WAYLAND_DISPLAY 时自动退化成 --headless。

import (
	"bytes"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/anacrolix/torrent/metainfo"
	webview "github.com/webview/webview_go"
)

//go:embed ui.html
var uiFS embed.FS

var mgr *Manager

func main() {
	port := flag.Int("port", 17777, "监听端口（只绑 127.0.0.1）")
	dir := flag.String("dir", "", "默认下载目录")
	headless := flag.Bool("headless", false, "只跑服务，不开界面")
	browser := flag.Bool("browser", false, "用系统浏览器打开，不开原生窗口")
	flag.Parse()

	stateDir := os.Getenv("XFLOW_STATE")
	if stateDir == "" {
		stateDir = filepath.Join(mustHome(), ".local", "state", "xflow")
	}
	os.MkdirAll(stateDir, 0755)

	if *dir != "" {
		os.MkdirAll(*dir, 0755)
		saveDefaultDir(*dir)
	}

	mgr = NewManager(filepath.Join(stateDir, "jobs.json"))
	mgr.StartTicker()

	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		b, _ := uiFS.ReadFile("ui.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(b)
	})

	mux.HandleFunc("/api/jobs", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			var d struct {
				Source string `json:"source"`
				Name   string `json:"name"`
				Dir    string `json:"dir"`
				Segs   int    `json:"segs"`
			}
			if err := json.NewDecoder(r.Body).Decode(&d); err != nil {
				json.NewEncoder(w).Encode(map[string]any{"ok": false, "err": "请求体不是合法 JSON"})
				return
			}
			src := strings.TrimSpace(d.Source)
			if src == "" {
				json.NewEncoder(w).Encode(map[string]any{"ok": false, "err": "链接为空"})
				return
			}
			target := d.Dir
			if target == "" {
				target = currentDir()
			}
			if isBTSource(src) {
				j := mgr.AddBT(src, orDefault(d.Name, "BT 任务"), target)
				json.NewEncoder(w).Encode(map[string]any{"ok": true, "id": j.ID})
				return
			}
			j, err := mgr.AddHTTPWithHeaders(src, orDefault(d.Name, fileNameFromURL(src)), target, d.Segs, nil)
			if err != nil {
				json.NewEncoder(w).Encode(map[string]any{"ok": false, "err": err.Error()})
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"ok": true, "id": j.ID})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"jobs": mgr.List()})
	})

	mux.HandleFunc("/api/job/", func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/job/"), "/")
		id := parts[0]
		if r.Method == "DELETE" {
			mgr.Remove(id)
			json.NewEncoder(w).Encode(map[string]any{"ok": true})
			return
		}
		if len(parts) > 1 {
			if err := mgr.Control(id, parts[1]); err != nil {
				json.NewEncoder(w).Encode(map[string]any{"ok": false, "err": err.Error()})
				return
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})

	mux.HandleFunc("/api/clear", func(w http.ResponseWriter, r *http.Request) {
		mgr.ClearDone()
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})

	mux.HandleFunc("/api/redownload", func(w http.ResponseWriter, r *http.Request) {
		j := mgr.Get(r.URL.Query().Get("id"))
		if j == nil {
			http.Error(w, "没找到", 404)
			return
		}
		target := currentDir()
		if j.Kind == "http" {
			mgr.AddHTTP(j.Source, j.Name, target, j.SegN)
		} else {
			mgr.AddBT(j.Source, j.Name, target)
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})

	mux.HandleFunc("/api/open", func(w http.ResponseWriter, r *http.Request) {
		j := mgr.Get(r.URL.Query().Get("id"))
		if j == nil || j.FilePath == "" {
			http.Error(w, "没找到", 404)
			return
		}
		exec.Command("xdg-open", filepath.Dir(j.FilePath)).Start()
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})

	mux.HandleFunc("/api/dir", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			var d struct{ Dir string `json:"dir"` }
			json.NewDecoder(r.Body).Decode(&d)
			if d.Dir != "" {
				os.MkdirAll(d.Dir, 0755)
				defaultDirOverride = d.Dir
				saveDefaultDir(d.Dir)
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"dir": currentDir()})
	})

	mux.HandleFunc("/api/browse", func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Query().Get("path")
		if p == "" {
			p = mustHome()
		}
		ents, err := os.ReadDir(p)
		if err != nil {
			json.NewEncoder(w).Encode(map[string]any{"ok": false, "err": err.Error()})
			return
		}
		type item struct {
			Name string `json:"name"`
			Dir  bool   `json:"dir"`
		}
		var items []item
		for _, e := range ents {
			items = append(items, item{Name: e.Name(), Dir: e.IsDir()})
		}
		json.NewEncoder(w).Encode(map[string]any{
			"ok": true, "path": p, "parent": filepath.Dir(p), "items": items,
		})
	})

	// 上传本地种子文件：按内容校验，不看后缀
	mux.HandleFunc("/api/torrent", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "只收 POST", 405)
			return
		}
		if err := r.ParseMultipartForm(16 << 20); err != nil {
			json.NewEncoder(w).Encode(map[string]any{"ok": false, "err": "表单解析失败：" + err.Error()})
			return
		}
		f, hdr, err := r.FormFile("file")
		if err != nil {
			json.NewEncoder(w).Encode(map[string]any{"ok": false, "err": "没收到文件"})
			return
		}
		defer f.Close()
		name := filepath.Base(hdr.Filename)
		raw, err := io.ReadAll(io.LimitReader(f, 32<<20))
		if err != nil {
			json.NewEncoder(w).Encode(map[string]any{"ok": false, "err": "读文件失败：" + err.Error()})
			return
		}
		mi, err := metainfo.Load(bytes.NewReader(raw))
		if err != nil {
			json.NewEncoder(w).Encode(map[string]any{"ok": false, "err": "这不是合法的种子文件（bencode 解析失败）"})
			return
		}
		if info, ierr := mi.UnmarshalInfo(); ierr == nil && info.Name != "" {
			name = info.Name
		}
		tmp, err := os.CreateTemp("", "xflow-*.torrent")
		if err != nil {
			json.NewEncoder(w).Encode(map[string]any{"ok": false, "err": "写临时文件失败：" + err.Error()})
			return
		}
		if _, err := tmp.Write(raw); err != nil {
			tmp.Close()
			json.NewEncoder(w).Encode(map[string]any{"ok": false, "err": "保存种子失败：" + err.Error()})
			return
		}
		tmp.Close()
		target := r.FormValue("dir")
		if target == "" {
			target = currentDir()
		}
		j := mgr.AddBT(tmp.Name(), name, target)
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "id": j.ID, "name": name})
	})

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "version": Version})
	})

	// 只听本机回环：要用别处的机器管理，走 SSH 隧道
	addr := "127.0.0.1:" + strconv.Itoa(*port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "监听 %s 失败：%v\n", addr, err)
		os.Exit(1)
	}
	url := "http://" + addr + "/"
	fmt.Printf("汐流 XFlow 已启动：%s\n下载目录：%s\n", url, currentDir())

	// 收到 SIGTERM / Ctrl-C 时把任务状态落盘再退
	go func() {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGTERM, syscall.SIGINT)
		<-ch
		fmt.Println("\n正在退出，保存任务状态…")
		mgr.save()
		os.Exit(0)
	}()

	go func() {
		if err := http.Serve(ln, withCORS(mux)); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}()

	hasDisplay := os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != ""

	switch {
	case *headless || (!hasDisplay && !*browser):
		fmt.Println("没有图形界面，只跑服务。用浏览器打开上面那个地址就行。")
		select {}
	case *browser:
		exec.Command("xdg-open", url).Start()
		select {}
	default:
		// 原生窗口：webkit2gtk。Run() 会阻塞到窗口关闭。
		w := webview.New(false)
		defer w.Destroy()
		w.SetTitle("汐流 XFlow")
		w.SetSize(1040, 720, webview.HintNone)
		w.Navigate(url)
		w.Run()
		mgr.save()
	}
}

func orDefault(s, d string) string {
	if strings.TrimSpace(s) == "" {
		return d
	}
	return s
}

func isBTSource(src string) bool {
	if strings.HasPrefix(src, "magnet:?") {
		return true
	}
	low := strings.ToLower(src)
	return strings.HasSuffix(low, ".torrent")
}
