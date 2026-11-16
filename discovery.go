package main

// 局域网发现：mDNS（Bonjour）。
//
// 手机端 XFlow 用 Android 的 NsdManager 注册 _xflow._tcp，桌面这边用
// grandcat/zeroconf 做同一件事：一边注册自己，一边浏览别人。
//
// 只有 mDNS 能跨三端稳定工作 —— 手机上的 UDP 组播/广播被系统策略禁了，
// 单播又得先知道对方地址（鸡生蛋）。mDNS 是系统服务，不受那个限制。

import (
	"context"
	"encoding/json"
	"strings"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/grandcat/zeroconf"
)

const xflowService = "_xflow._tcp"

// peerInfo 一台局域网里发现到的 XFlow
type peerInfo struct {
	Name     string `json:"name"`
	IP       string `json:"ip"`
	Port     int    `json:"port"`
	Platform string `json:"platform,omitempty"`
	Ver      string `json:"ver,omitempty"`
	Self     bool   `json:"self,omitempty"`
	Seen     int64  `json:"seen"`
}

var (
	peerMu     sync.RWMutex
	peerList   = map[string]*peerInfo{}
	peerServer *zeroconf.Server
)

func discoveryName() string {
	host, _ := os.Hostname()
	if host == "" {
		host = "desktop"
	}
	return "XFlow@" + host
}

// startDiscovery 注册自己 + 浏览别人。端口是 HTTP API 实际监听的那个。
func startDiscovery(port int, lan bool) {
	if !lan {
		// 只听回环时注册出去也没用（别人连不上），除了给自己看
		// 还是注册上，方便别的主机知道这台机器的存在但连不上时能提示
	}
	name := discoveryName()
	srv, err := zeroconf.Register(name, xflowService, "local.", port,
		[]string{"platform=" + platformTag(), "ver=" + Version}, nil)
	if err == nil {
		peerServer = srv
		fmt.Printf("[发现] 已注册 mDNS: %s port=%d\n", name, port)
	} else {
		fmt.Printf("[发现] 注册失败: %v\n", err)
	}

	go func() {
		resolver, err := zeroconf.NewResolver(nil)
		if err != nil {
			fmt.Printf("[发现] 解析器起不来: %v\n", err)
			return
		}
		entries := make(chan *zeroconf.ServiceEntry, 16)
		go func() {
			for e := range entries {
				ip := ""
				if len(e.AddrIPv4) > 0 {
					ip = pickIPv4(e.AddrIPv4, localIPv4())
				} else if len(e.AddrIPv6) > 0 {
					ip = e.AddrIPv6[0].String()
				}
				if ip == "" {
					continue
				}
				p := &peerInfo{
					Name: unescapeMDNS(e.ServiceInstanceName()),
					IP:   ip,
					Port: e.Port,
					Seen: time.Now().Unix(),
				}
				for _, t := range e.Text {
					for i := 0; i < len(t); i++ {
						if t[i] == '=' {
							k, v := t[:i], t[i+1:]
							if k == "platform" {
								p.Platform = v
							} else if k == "ver" {
								p.Ver = v
							}
							break
						}
					}
				}
				if ip == "127.0.0.1" || ip == "::1" {
					continue
				}
				peerMu.Lock()
				peerList[ip] = p
				peerMu.Unlock()
			}
		}()
		// 每 10 秒重扫一次，掉线的靠 TTL 清掉。
		// Browse 的 ctx 不能是 nil（zeroconf 内部 context.WithCancel(nil) 会 panic）
		ctx := context.Background()
		for {
			if err := resolver.Browse(ctx, xflowService, "local.", entries); err != nil {
				fmt.Printf("[发现] 浏览失败: %v\n", err)
			}
			time.Sleep(10 * time.Second)
		}
	}()
}

// stopDiscovery 退出时注销，免得别人一直看到一台已经不存在的机器
func stopDiscovery() {
	if peerServer != nil {
		peerServer.Shutdown()
		peerServer = nil
	}
}

// snapshotPeers 取当前在线设备（30 秒内有回应）
func snapshotPeers() []*peerInfo {
	now := time.Now().Unix()
	peerMu.Lock()
	defer peerMu.Unlock()
	out := make([]*peerInfo, 0, len(peerList))
	for k, p := range peerList {
		if now-p.Seen > 30 {
			delete(peerList, k)
			continue
		}
		out = append(out, p)
	}
	return out
}

func platformTag() string {
	return runtimeGOOS()
}

// registerDiscoveryAPI /api/peers：给界面和别的机器看
func registerDiscoveryAPI(mux *http.ServeMux) {
	mux.HandleFunc("/api/peers", func(w http.ResponseWriter, r *http.Request) {
		local := localIPv4()
		json.NewEncoder(w).Encode(map[string]any{
			"ok":    true,
			"self":  map[string]any{"name": discoveryName(), "ip": local, "port": servePort},
			"peers": snapshotPeers(),
		})
	})
}

func localIPv4() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	var fallback string
	for _, a := range addrs {
		ipn, ok := a.(*net.IPNet)
		if !ok || ipn.IP.IsLoopback() {
			continue
		}
		ip4 := ipn.IP.To4()
		if ip4 == nil {
			continue
		}
		s := ip4.String()
		// 172.16/12 多半是 docker 网桥，100.64/10 是 tailscale，别当成局域网地址
		if strings.HasPrefix(s, "172.") || strings.HasPrefix(s, "100.") ||
			strings.HasPrefix(s, "169.254.") {
			if fallback == "" {
				fallback = s
			}
			continue
		}
		return s
	}
	return fallback
}


// pickIPv4 从 mDNS 报上来的地址里挑一个能用的。
// 多网卡的机器（比如 G55 有 enp1s0 172.16.0.12 + enp2s0 10.11.11.12）会把所有网卡都播出去，
// 直接取 [0] 可能拿到 docker/旁路口的地址，别人就照那个连过来了。
// 规则：优先和本机主地址同 /24 的；再排除 172./100./169.254.；最后才退回第一个。
func pickIPv4(list []net.IP, prefer string) string {
	if len(list) == 0 {
		return ""
	}
	pref3 := ""
	if p := strings.Split(prefer, "."); len(p) == 4 {
		pref3 = p[0] + "." + p[1] + "." + p[2]
	}
	var fallback string
	for _, ip := range list {
		if ip == nil || ip.To4() == nil {
			continue
		}
		s := ip.To4().String()
		if pref3 != "" && strings.HasPrefix(s, pref3+".") {
			return s
		}
		if strings.HasPrefix(s, "172.") || strings.HasPrefix(s, "100.") || strings.HasPrefix(s, "169.254.") {
			if fallback == "" {
				fallback = s
			}
			continue
		}
		return s
	}
	return fallback
}

// unescapeMDNS：mDNS 里 @ . 会被转义成 \@ \.
func unescapeMDNS(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
			b.WriteByte(s[i])
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

var _ = strconv.Itoa
