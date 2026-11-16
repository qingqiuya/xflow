package main

// 剪贴板互传：GET /api/clipboard 取，POST /api/clipboard {"text":"..."} 放。
// 平台实现在 clipboard_windows.go / clipboard_linux.go。

import (
	"encoding/json"
	"net/http"
)

func registerClipboard(mux *http.ServeMux) {
	mux.HandleFunc("/api/clipboard", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			t, err := clipboardRead()
			if err != nil {
				writeJSONErr(w, err.Error())
				return
			}
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			json.NewEncoder(w).Encode(map[string]any{"ok": true, "text": t})
			return
		}
		var d struct {
			Text string `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&d); err != nil {
			writeJSONErr(w, "bad json")
			return
		}
		if err := clipboardWrite(d.Text); err != nil {
			writeJSONErr(w, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "len": len([]rune(d.Text))})
	})
}
