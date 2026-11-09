package main

import (
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// 对外（内网）开放下载接口的保护：只认白名单里的来源网段。
//
// 为什么要这个：绑 127.0.0.1 时只有本机能连，插件够用；但要从别的机器
// （比如家里的 G55）派任务，就得监听内网网卡 —— 那台机器一开，全网段都能往
// 下载器里塞任务、下载目录也能被写。所以加一道来源白名单，默认只放本机和 G55。
//
// 白名单文件：~/.localdl-allow，一行一个 CIDR，# 开头是注释。
// 不存在时自动生成一份默认的。

var allowNets []*net.IPNet

const defaultAllow = `# 允许访问 XFlow 下载接口的来源，一行一个 CIDR
# 本机（浏览器插件、界面自己走这个）
127.0.0.1/32
::1/128
# 家里的 G55（夏尔那台）
203.0.113.10/32
`

func loadAllowList() {
	home, _ := os.UserHomeDir()
	p := filepath.Join(home, ".localdl-allow")
	b, err := os.ReadFile(p)
	if err != nil {
		_ = os.WriteFile(p, []byte(defaultAllow), 0600)
		b = []byte(defaultAllow)
	}
	allowNets = nil
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.Contains(line, "/") {
			line += "/32"
		}
		if _, n, e := net.ParseCIDR(line); e == nil {
			allowNets = append(allowNets, n)
		}
	}
}

func sourceAllowed(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	if ip == nil {
		return false
	}
	for _, n := range allowNets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// withGuard 挡住白名单之外的来源。接口返回 JSON，其他路径给 403。
func withGuard(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !sourceAllowed(r) {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"ok":false,"err":"来源不在 ~/.localdl-allow 白名单里"}`))
				return
			}
			http.Error(w, "403 来源不允许", http.StatusForbidden)
			return
		}
		h.ServeHTTP(w, r)
	})
}
