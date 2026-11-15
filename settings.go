package main

// 对外接口设置：监听范围 + 访问令牌。
// 默认只绑 127.0.0.1（本机），这是最安全的；想让局域网别的机器用，
// 把 listen 改成 "lan"，并给一个 token —— 否则同网段任何人都能往你硬盘里塞文件。

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type apiSettings struct {
	Listen string `json:"listen"` // "local"(默认,127.0.0.1) / "lan"(0.0.0.0)
	Token  string `json:"token"`  // 空 = 不校验（只在本机模式下允许为空）
}

var (
	settingsMu   sync.Mutex
	cachedSetttings *apiSettings
)

func settingsPath() string {
	return filepath.Join(mustHome(), ".localdl-settings.json")
}

func loadSettings() apiSettings {
	settingsMu.Lock()
	defer settingsMu.Unlock()
	if cachedSetttings != nil {
		return *cachedSetttings
	}
	s := apiSettings{Listen: "local"}
	if b, err := os.ReadFile(settingsPath()); err == nil {
		json.Unmarshal(b, &s)
	}
	if s.Listen == "" {
		s.Listen = "local"
	}
	// 局域网模式但没 token：自动生成一个，避免裸奔
	if s.Listen == "lan" && s.Token == "" {
		s.Token = randomToken()
		saveSettingsLocked(s)
	}
	cachedSetttings = &s
	return s
}

func saveSettings(s apiSettings) error {
	settingsMu.Lock()
	defer settingsMu.Unlock()
	return saveSettingsLocked(s)
}

func saveSettingsLocked(s apiSettings) error {
	b, _ := json.MarshalIndent(s, "", "  ")
	p := settingsPath()
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0600); err != nil {
		return err
	}
	if err := os.Rename(tmp, p); err != nil {
		return err
	}
	cachedSetttings = &s
	return nil
}

func randomToken() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// listenAddr 返回实际绑定的地址。
func listenAddr(port int) string {
	s := loadSettings()
	host := "127.0.0.1"
	if s.Listen == "lan" {
		host = "0.0.0.0"
	}
	return net.JoinHostPort(host, itoa(port))
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [8]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// lanIP 找本机的局域网 IPv4（给设置页显示"别人该连哪个地址"）。
func lanIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok && !ipn.IP.IsLoopback() {
			if ip4 := ipn.IP.To4(); ip4 != nil {
				return ip4.String()
			}
		}
	}
	return ""
}

// authOK 检查 token。token 为空时不校验（本机模式）。
//
// 规则刻意分开：
//   - 「写」接口（加任务、下更新）→ 必须带 token（局域网模式下）
//   - 「读」接口和页面 → 放行，免得本机开页面还要拼 token
func authOK(r *http.Request, needToken bool) bool {
	if !needToken {
		return true
	}
	s := loadSettings()
	if s.Token == "" {
		return true
	}
	got := r.Header.Get("X-Token")
	if got == "" {
		got = r.URL.Query().Get("token")
	}
	if got == "" {
		// 也认 Authorization: Bearer xxx
		if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
			got = strings.TrimPrefix(h, "Bearer ")
		}
	}
	return got == s.Token
}

// guardMux 包一层：局域网模式下，「写」接口要 token。读接口和页面放行。
func guardMux(mux *http.ServeMux, needToken bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		write := r.Method == "POST" || r.Method == "PUT" || r.Method == "DELETE" || r.Method == "PATCH"
		if write && !authOK(r, needToken) {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(401)
			json.NewEncoder(w).Encode(map[string]any{"ok": false, "err": "需要 token（请求头 X-Token）"})
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// registerSettingsAPI 注册 /api/settings（读取/修改监听范围与 token）。
func registerSettingsAPI(mux *http.ServeMux) {
	mux.HandleFunc("/api/settings", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			var d struct {
				Listen string `json:"listen"`
				Token  string `json:"token"`
				Rotate bool   `json:"rotate"` // true = 重新生成 token
			}
			if err := json.NewDecoder(r.Body).Decode(&d); err != nil {
				json.NewEncoder(w).Encode(map[string]any{"ok": false, "err": "请求体不是合法 JSON"})
				return
			}
			s := loadSettings()
			if d.Listen == "local" || d.Listen == "lan" {
				s.Listen = d.Listen
			}
			if d.Rotate {
				s.Token = randomToken()
			} else if d.Token != "" {
				s.Token = d.Token
			}
			if s.Listen == "lan" && s.Token == "" {
				s.Token = randomToken()
			}
			if s.Listen == "local" {
				// 本机模式不需要 token，清掉免得本机页面也用不了
				s.Token = ""
			}
			if err := saveSettings(s); err != nil {
				json.NewEncoder(w).Encode(map[string]any{"ok": false, "err": err.Error()})
				return
			}
			json.NewEncoder(w).Encode(map[string]any{
				"ok": true, "settings": s, "needRestart": true,
				"lanIP": lanIP(), "port": currentPort,
			})
			return
		}
		s := loadSettings()
		json.NewEncoder(w).Encode(map[string]any{
			"ok": true, "settings": s, "lanIP": lanIP(), "port": currentPort,
		})
	})
}

// currentPort 记录实际监听端口（随机回退时也能告诉前端）。
var currentPort = 17777
