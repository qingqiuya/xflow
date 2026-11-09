//go:build !windows

package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
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
