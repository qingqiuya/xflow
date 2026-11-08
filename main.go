//go:build windows

package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	webview2 "github.com/pgmsoul/go-webview2"
)

//go:embed ui.html
var uiFS embed.FS

var mgr *Manager
var defaultDirOverride string

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
	mux.HandleFunc("/api/jobs", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			var d struct {
				Source string `json:"source"`
				Name   string `json:"name"`
				Dir    string `json:"dir"`
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
				mgr.AddHTTP(src, orDefault(d.Name, fileNameFromURL(src)), dir)
			}
			json.NewEncoder(w).Encode(map[string]any{"ok": true})
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
			mgr.AddHTTP(j.Source, j.Name, dir)
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
		var d struct{ Dir string `json:"dir"` }
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
			var d struct{ Dir string `json:"dir"` }
			json.NewDecoder(r.Body).Decode(&d)
			if d.Dir != "" {
				defaultDirOverride = d.Dir
				os.MkdirAll(d.Dir, 0755)
				saveDefaultDir(d.Dir)
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"dir": currentDir()})
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	// 把端口写到文件，方便外部（另一台机器/脚本）调用 API
	os.WriteFile(filepath.Join(home, ".localdl-port"), []byte(strconv.Itoa(port)), 0644)
	go http.Serve(ln, mux)

	darkenWindowTitle("XFlow")
	startTray()

	w := webview2.NewWithOptions(webview2.WebViewOptions{
		Debug:     false,
		AutoFocus: true,
		DataPath:  filepath.Join(home, ".localdl-webview"),
		WindowOptions: webview2.WindowOptions{
			Title:  "XFlow",
			Width:  1000,
			Height: 700,
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
	w.Run()
}

func orDefault(s, d string) string {
	if strings.TrimSpace(s) == "" {
		return d
	}
	return s
}

func currentDir() string {
	if defaultDirOverride != "" {
		return defaultDirOverride
	}
	if b, err := os.ReadFile(filepath.Join(mustHome(), ".localdl-dir")); err == nil {
		if d := strings.TrimSpace(string(b)); d != "" {
			return d
		}
	}
	d := filepath.Join(mustHome(), "Downloads")
	os.MkdirAll(d, 0755)
	return d
}

func mustHome() string {
	h, _ := os.UserHomeDir()
	return h
}

func saveDefaultDir(d string) {
	os.WriteFile(filepath.Join(mustHome(), ".localdl-dir"), []byte(d), 0600)
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

var _ = time.Second
