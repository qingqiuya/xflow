//go:build windows

package main

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	webview2 "github.com/pgmsoul/go-webview2"
)

//go:embed ui.html
var uiFS embed.FS

var mgr *Manager

func main() {
	defer func() {
		if r := recover(); r != nil {
			home, _ := os.UserHomeDir()
			f, _ := os.Create(filepath.Join(home, "localdl-crash.log"))
			if f != nil {
				fmt.Fprintf(f, "panic: %v\n", r)
				f.Close()
			}
		}
	}()

	enableDarkMode()

	home, _ := os.UserHomeDir()
	mgr = NewManager(filepath.Join(home, ".localdl.json"))
	mgr.StartTicker()

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		b, _ := uiFS.ReadFile("ui.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(b)
	})
	// 界面右上角显示版本号，也能给脚本探活用
	mux.HandleFunc("/api/version", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"version": Version})
	})

	mux.HandleFunc("/api/jobs", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			var d struct {
				Source string `json:"source"`
				Name   string `json:"name"`
				Dir    string `json:"dir"`
				Segs   int    `json:"segs"`
			}
			json.NewDecoder(r.Body).Decode(&d)
			src := strings.TrimSpace(d.Source)
			if src == "" {
				http.Error(w, "链接为空", 400)
				return
			}
			dir := d.Dir
			if dir == "" {
				dir = currentDir()
			}
			if strings.HasPrefix(src, "magnet:") || strings.HasSuffix(strings.ToLower(src), ".torrent") {
				mgr.AddBT(src, orDefault(d.Name, "BT 任务"), dir)
			} else {
				mgr.AddHTTP(src, orDefault(d.Name, fileNameFromURL(src)), dir, d.Segs)
			}
			json.NewEncoder(w).Encode(map[string]any{"ok": true})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"jobs": mgr.List()})
	})
	// 浏览器插件用的入口：带 Referer / Cookie 的下载请求。
	// 插件在浏览器扩展上下文里 fetch 127.0.0.1，绕开 CORS 限制靠的是 host_permissions，
	// 但预检还是会来，所以 withCORS 里也放了 Allow-Private-Network。
	mux.HandleFunc("/api/add", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "只收 POST", 405)
			return
		}
		var d struct {
			URL     string `json:"url"`
			Name    string `json:"name"`
			Dir     string `json:"dir"`
			Referer string `json:"referer"`
			Cookie  string `json:"cookie"`
			Segs    int    `json:"segs"`
		}
		if err := json.NewDecoder(r.Body).Decode(&d); err != nil {
			json.NewEncoder(w).Encode(map[string]any{"ok": false, "err": "请求体不是合法 JSON"})
			return
		}
		src := strings.TrimSpace(d.URL)
		if src == "" {
			json.NewEncoder(w).Encode(map[string]any{"ok": false, "err": "url 为空"})
			return
		}
		dir := d.Dir
		if dir == "" {
			dir = currentDir()
		}
		name := d.Name
		if name == "" {
			name = fileNameFromURL(src)
		}
		if strings.HasPrefix(src, "magnet:") || strings.HasSuffix(strings.ToLower(src), ".torrent") {
			j := mgr.AddBT(src, orDefault(name, "BT 任务"), dir)
			json.NewEncoder(w).Encode(map[string]any{"ok": true, "id": j.ID})
			return
		}
		h := map[string]string{}
		if d.Referer != "" {
			h["Referer"] = d.Referer
		}
		if d.Cookie != "" {
			h["Cookie"] = d.Cookie
		}
		j, err := mgr.AddHTTPWithHeaders(src, name, dir, d.Segs, h)
		if err != nil {
			json.NewEncoder(w).Encode(map[string]any{"ok": false, "err": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "id": j.ID, "name": name})
	})

	// 上传本地 .torrent 文件加任务（界面上「种子文件」按钮走这里）
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
		// 不看后缀，解析一遍内容才算数：改过名的一律能收，假的 .torrent 一律拒
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
		// 存到临时文件给 BT 引擎读；引擎是异步的，这里不能马上删
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
		dir := r.FormValue("dir")
		if dir == "" {
			dir = currentDir()
		}
		j := mgr.AddBT(tmp.Name(), strings.TrimSuffix(name, filepath.Ext(name)), dir)
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "id": j.ID, "name": name})
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
			mgr.Control(id, parts[1])
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})
	mux.HandleFunc("/api/redownload", func(w http.ResponseWriter, r *http.Request) {
		j := mgr.Get(r.URL.Query().Get("id"))
		if j == nil {
			http.Error(w, "没找到", 404)
			return
		}
		dir := currentDir()
		if j.Kind == "http" {
			mgr.AddHTTP(j.Source, j.Name, dir, j.SegN)
		} else {
			mgr.AddBT(j.Source, j.Name, dir)
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})

	mux.HandleFunc("/api/hide", func(w http.ResponseWriter, r *http.Request) {
		hideWin()
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})

	mux.HandleFunc("/api/clear", func(w http.ResponseWriter, r *http.Request) {
		mgr.ClearDone()
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})
	mux.HandleFunc("/api/open", func(w http.ResponseWriter, r *http.Request) {
		j := mgr.Get(r.URL.Query().Get("id"))
		if j == nil {
			http.Error(w, "没找到", 404)
			return
		}
		p := currentDir()
		if j.FilePath != "" {
			p = filepath.Dir(j.FilePath)
		}
		exec.Command("explorer", p).Start()
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})
	mux.HandleFunc("/api/browse", func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Query().Get("path")
		type ent struct {
			Name string `json:"name"`
			Path string `json:"path"`
		}
		var dirs []ent
		parent := ""
		shortcuts := map[string]string{}
		if h, err := os.UserHomeDir(); err == nil {
			for _, kv := range [][2]string{
				{"桌面", filepath.Join(h, "Desktop")},
				{"下载", filepath.Join(h, "Downloads")},
				{"文档", filepath.Join(h, "Documents")},
				{"图片", filepath.Join(h, "Pictures")},
				{"视频", filepath.Join(h, "Videos")},
				{"用户主目录", h},
			} {
				if st, err := os.Stat(kv[1]); err == nil && st.IsDir() {
					shortcuts[kv[0]] = kv[1]
				}
			}
		}
		if p == "" {
			for c := 'A'; c <= 'Z'; c++ {
				d := string(c) + ":\\"
				if st, err := os.Stat(d); err == nil && st.IsDir() {
					dirs = append(dirs, ent{Name: string(c) + ":", Path: d})
				}
			}
		} else {
			ents, err := os.ReadDir(p)
			if err != nil {
				json.NewEncoder(w).Encode(map[string]any{"ok": false, "err": err.Error(), "path": p})
				return
			}
			for _, e := range ents {
				if !e.IsDir() {
					continue
				}
				n := e.Name()
				if strings.HasPrefix(n, "$") || strings.HasPrefix(n, ".") {
					continue
				}
				dirs = append(dirs, ent{Name: n, Path: filepath.Join(p, n)})
			}
			parent = filepath.Dir(p)
			if len(parent) < len(p) && strings.HasSuffix(p, ":\\") {
				parent = ""
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "path": p, "parent": parent, "dirs": dirs, "current": currentDir(), "shortcuts": shortcuts})
	})

	mux.HandleFunc("/api/setdir", func(w http.ResponseWriter, r *http.Request) {
		var d struct {
			Dir string `json:"dir"`
		}
		json.NewDecoder(r.Body).Decode(&d)
		if d.Dir != "" && isDir(d.Dir) {
			defaultDirOverride = d.Dir
			saveDefaultDir(d.Dir)
			json.NewEncoder(w).Encode(map[string]any{"ok": true})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": false})
	})

	mux.HandleFunc("/api/pickdir_disabled", func(w http.ResponseWriter, r *http.Request) {
		p, err := pickFolder("选择下载目录")
		lg := filepath.Join(mustHome(), "localdl-pickdir.log")
		if err != nil || p == "" || !isDir(p) {
			os.WriteFile(lg, []byte(fmt.Sprintf("err=%v path=%q isdir=%v\n", err, p, isDir(p))), 0644)
			json.NewEncoder(w).Encode(map[string]any{"ok": false, "err": fmt.Sprint(err)})
			return
		}
		os.WriteFile(lg, []byte("ok "+p+"\n"), 0644)
		defaultDirOverride = p
		saveDefaultDir(p)
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "dir": p})
	})

	mux.HandleFunc("/api/dir", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			var d struct {
				Dir string `json:"dir"`
			}
			json.NewDecoder(r.Body).Decode(&d)
			if d.Dir != "" {
				defaultDirOverride = d.Dir
				os.MkdirAll(d.Dir, 0755)
				saveDefaultDir(d.Dir)
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"dir": currentDir()})
	})

	// 固定端口：插件要知道往哪发，随机端口做不到。17777 被占才退回随机。
	// 只听本机回环：别的机器要派任务走 SSH（本机 curl 或 -L 隧道），不对外开端口。
	ln, err := net.Listen("tcp", "127.0.0.1:17777")
	if err != nil {
		ln, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			panic(err)
		}
	}
	port := ln.Addr().(*net.TCPAddr).Port
	// 把端口写到文件，方便外部（另一台机器/脚本/插件安装说明）调用 API
	os.WriteFile(filepath.Join(home, ".localdl-port"), []byte(strconv.Itoa(port)), 0644)
	go http.Serve(ln, withCORS(mux))

	darkenWindowTitle("XFlow")
	startTray()

	startHidden := false
	for _, a := range os.Args[1:] {
		if a == "--tray" || a == "-tray" || a == "--hidden" {
			startHidden = true
		}
	}

	w := webview2.NewWithOptions(webview2.WebViewOptions{
		Debug:     false,
		AutoFocus: true,
		DataPath:  filepath.Join(home, ".localdl-webview"),
		WindowOptions: webview2.WindowOptions{
			Title:   "XFlow",
			Width:   1000,
			Height:  700,
			Default: true,
		},
		OnClose: func(w webview2.WebView) {
			// 点 × 不退出，缩到托盘
			hideWin()
		},
	})
	if w == nil {
		// 回退：用浏览器
		exec.Command("rundll32", "url.dll,FileProtocolHandler", "http://127.0.0.1:"+strconv.Itoa(port)).Start()
		select {}
	}
	defer w.Destroy()
	w.Navigate("http://127.0.0.1:" + strconv.Itoa(port))
	attachOwner()

	if startHidden {
		// 自启模式：等窗口建好就藏到托盘，任务栏不留图标
		go func() {
			for i := 0; i < 60; i++ {
				time.Sleep(50 * time.Millisecond)
				if findWin() != 0 {
					// 等首帧画完再藏（不再设 TOOLWINDOW，那会把标题栏的最小化/最大化干掉）
					time.Sleep(200 * time.Millisecond)
					hideWin()
					return
				}
			}
		}()
	}
	w.Run()
}

func orDefault(s, d string) string {
	if strings.TrimSpace(s) == "" {
		return d
	}
	return s
}
