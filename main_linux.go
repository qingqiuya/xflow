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
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	fmt.Println("http://127.0.0.1:" + strconv.Itoa(ln.Addr().(*net.TCPAddr).Port))
	go func() { time.Sleep(time.Hour); os.Exit(0) }()
	http.Serve(ln, mux)
}
