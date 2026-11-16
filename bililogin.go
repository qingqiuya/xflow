package main

// 桌面端 B 站扫码登录：拿 SESSDATA 存本地，之后解析接口带着它换 1080P。
//
// 流程跟手机端一致：/x/passport-login/web/qrcode/generate 拿 key+url，
// 界面显示二维码 → 轮询 /poll（86101 未扫 / 86090 待确认 / 0 成功 / 86038 过期），
// 成功后从返回的 data.url 或 Set-Cookie 里取 SESSDATA。

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	qrcode "github.com/skip2/go-qrcode"
)

var (
	biliCkMu sync.RWMutex
	biliCk   string // 内存里的 Cookie 串（SESSDATA=...）
)

// biliCookieFile 存 Cookie 的小文件（在 XFlow 配置目录里）
func biliCookieFile() string {
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		dir = "."
	}
	d := filepath.Join(dir, "XFlow")
	_ = os.MkdirAll(d, 0o700)
	return filepath.Join(d, "bili-cookie.txt")
}

// BiliCookie 返回当前登录 Cookie（没登录返回空串）。
func BiliCookie() string {
	biliCkMu.RLock()
	defer biliCkMu.RUnlock()
	if biliCk != "" {
		return biliCk
	}
	b, err := os.ReadFile(biliCookieFile())
	if err != nil {
		return ""
	}
	s := strings.TrimSpace(string(b))
	if s != "" {
		biliCkMu.Lock()
		biliCk = s
		biliCkMu.Unlock()
	}
	return s
}

func biliSetCookie(ck string) {
	biliCkMu.Lock()
	biliCk = strings.TrimSpace(ck)
	biliCkMu.Unlock()
	if biliCk == "" {
		_ = os.Remove(biliCookieFile())
		return
	}
	_ = os.WriteFile(biliCookieFile(), []byte(biliCk), 0o600)
}

func biliHasSessdata(ck string) bool {
	return strings.Contains(ck, "SESSDATA=")
}

// biliCookieFrom 从 "a=1; b=2" 里挑出需要的几个字段拼成 Cookie 串
func biliCookieFrom(raw string) string {
	var parts []string
	for _, k := range []string{"SESSDATA", "bili_jct", "DedeUserID", "DedeUserID__ckMd5"} {
		if v := cookiePick(raw, k); v != "" {
			parts = append(parts, k+"="+v)
		}
	}
	return strings.Join(parts, "; ")
}

func cookiePick(cookie, key string) string {
	for _, part := range strings.Split(cookie, ";") {
		t := strings.TrimSpace(part)
		if strings.HasPrefix(t, key+"=") {
			return t[len(key)+1:]
		}
	}
	return ""
}

// biliQS 从 URL 查询串里取值（扫码成功的 data.url 带 SESSDATA 等参数）
func biliQS(rawURL, key string) string {
	i := strings.Index(rawURL, "?")
	if i < 0 {
		return ""
	}
	for _, part := range strings.Split(rawURL[i+1:], "&") {
		e := strings.Index(part, "=")
		if e < 0 {
			continue
		}
		if part[:e] == key {
			return part[e+1:]
		}
	}
	return ""
}

func biliWriteJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

type biliQRResp struct {
	Code int    `json:"code"`
	Msg  string `json:"message"`
	Data struct {
		URL       string `json:"url"`
		QRCodeKey string `json:"qrcode_key"`
	} `json:"data"`
}

type biliPollResp struct {
	Code int `json:"code"`
	Data struct {
		URL      string `json:"url"`
		Code     int    `json:"code"`
		Message  string `json:"message"`
		RefreshT string `json:"refresh_token"`
	} `json:"data"`
}

// registerBiliLogin 把登录相关接口挂到 mux 上（两个平台的 main 都调它）。
func registerBiliLogin(mux *http.ServeMux) {
	// 生成二维码
	mux.HandleFunc("/api/bili/qr", func(w http.ResponseWriter, r *http.Request) {
		var res biliQRResp
		if err := biliGetJSON("https://passport.bilibili.com/x/passport-login/web/qrcode/generate", &res); err != nil {
			biliWriteJSON(w, map[string]any{"ok": false, "err": err.Error()})
			return
		}
		if res.Code != 0 || res.Data.URL == "" {
			biliWriteJSON(w, map[string]any{"ok": false, "err": "生成二维码失败"})
			return
		}
		png, err := qrcode.Encode(res.Data.URL, qrcode.Medium, 512)
		if err != nil {
			biliWriteJSON(w, map[string]any{"ok": false, "err": err.Error()})
			return
		}
		biliWriteJSON(w, map[string]any{
			"ok":  true,
			"key": res.Data.QRCodeKey,
			"png": "data:image/png;base64," + base64.StdEncoding.EncodeToString(png),
		})
	})

	// 轮询扫码状态
	mux.HandleFunc("/api/bili/qr/poll", func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Query().Get("key")
		if key == "" {
			biliWriteJSON(w, map[string]any{"ok": false, "err": "key 为空"})
			return
		}
		u := "https://passport.bilibili.com/x/passport-login/web/qrcode/poll?qrcode_key=" + key
		// 自己发一次请求，要拿 Set-Cookie（新版 B 站 Cookie 可能只在这里）
		req, err := http.NewRequest("GET", u, nil)
		if err != nil {
			biliWriteJSON(w, map[string]any{"ok": false, "err": err.Error()})
			return
		}
		req.Header.Set("User-Agent", biliUA)
		req.Header.Set("Referer", "https://www.bilibili.com/")
		resp, err := biliClient(20 * time.Second).Do(req)
		if err != nil {
			biliWriteJSON(w, map[string]any{"ok": false, "err": err.Error()})
			return
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

		var pr biliPollResp
		if err := json.Unmarshal(body, &pr); err != nil {
			biliWriteJSON(w, map[string]any{"ok": false, "err": "返回不是 JSON"})
			return
		}
		st := pr.Data.Code
		if st != 0 {
			biliWriteJSON(w, map[string]any{"ok": true, "status": st, "msg": pr.Data.Message})
			return
		}
		// 成功：先从 data.url 抠，再从 Set-Cookie 兜底
		raw := pr.Data.URL
		if raw != "" {
			raw = "SESSDATA=" + biliQS(raw, "SESSDATA") +
				"; bili_jct=" + biliQS(raw, "bili_jct") +
				"; DedeUserID=" + biliQS(raw, "DedeUserID")
		}
		ck := biliCookieFrom(raw)
		if !biliHasSessdata(ck) {
			var sc []string
			for _, c := range resp.Header.Values("Set-Cookie") {
				if i := strings.Index(c, ";"); i > 0 {
					sc = append(sc, c[:i])
				}
			}
			ck = biliCookieFrom(strings.Join(sc, "; "))
		}
		if !biliHasSessdata(ck) {
			biliWriteJSON(w, map[string]any{"ok": false, "err": "扫码成功但没拿到 SESSDATA"})
			return
		}
		biliSetCookie(ck)
		biliWriteJSON(w, map[string]any{"ok": true, "status": 0, "logged": true})
	})

	// 登录状态
	mux.HandleFunc("/api/bili/status", func(w http.ResponseWriter, r *http.Request) {
		ck := BiliCookie()
		biliWriteJSON(w, map[string]any{"ok": true, "logged": biliHasSessdata(ck)})
	})

	// 退出
	mux.HandleFunc("/api/bili/logout", func(w http.ResponseWriter, r *http.Request) {
		biliSetCookie("")
		biliWriteJSON(w, map[string]any{"ok": true})
	})
}
