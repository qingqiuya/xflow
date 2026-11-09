//go:build !windows

package main

import (
	"bytes"
	"encoding/json"
	"io"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/anacrolix/torrent/metainfo"
)

// Linux 下只用于本地测试下载核心（无 GUI）
var mgr *Manager
func main() {
	home, _ := os.UserHomeDir()
	mgr = NewManager(filepath.Join(home, ".localdl-test.json"))
	mgr.StartTicker()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/jobs", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			var d struct {
				Source string `json:"source"`
				Name   string `json:"name"`
				Segs   int    `json:"segs"`
			}
			json.NewDecoder(r.Body).Decode(&d)
			dir := currentDir()
			if strings.HasPrefix(d.Source, "magnet:") {
				mgr.AddBT(d.Source, d.Name, dir)
			} else {
				if d.Name == "" {
					d.Name = fileNameFromURL(d.Source)
				}
				mgr.AddHTTP(d.Source, d.Name, dir, d.Segs)
			}
			w.Write([]byte(`{"ok":true}`))
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"jobs": mgr.List()})
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
	_ = httptest.NewRecorder
	// 与 Windows 版一致的插件入口，本地测下载链路用
	mux.HandleFunc("/api/add", func(w http.ResponseWriter, r *http.Request) {
		var d struct {
			URL     string `json:"url"`
			Name    string `json:"name"`
			Dir     string `json:"dir"`
			Referer string `json:"referer"`
			Cookie  string `json:"cookie"`
			Segs    int    `json:"segs"`
		}
		if err := json.NewDecoder(r.Body).Decode(&d); err != nil {
			json.NewEncoder(w).Encode(map[string]any{"ok": false, "err": "bad json"})
			return
		}
		dir := d.Dir
		if dir == "" {
			dir = currentDir()
		}
		name := d.Name
		if name == "" {
			name = fileNameFromURL(d.URL)
		}
		if strings.HasPrefix(d.URL, "magnet:") || strings.HasSuffix(strings.ToLower(d.URL), ".torrent") {
			j := mgr.AddBT(d.URL, name, dir)
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
		j, err := mgr.AddHTTPWithHeaders(d.URL, name, dir, d.Segs, h)
		if err != nil {
			json.NewEncoder(w).Encode(map[string]any{"ok": false, "err": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "id": j.ID})
	})
	ln, err := net.Listen("tcp", "127.0.0.1:17777")
	if err != nil {
		ln, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			panic(err)
		}
	}
	fmt.Println("http://127.0.0.1:" + strconv.Itoa(ln.Addr().(*net.TCPAddr).Port))
	go func() { time.Sleep(time.Hour); os.Exit(0) }()
	http.Serve(ln, withCORS(mux))
}
