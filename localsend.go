package main

// LocalSend v2 协议兼容（2026-11-20）
//
// 为什么要做：LocalSend 是局域网互传的事实标准，手机上装它的人比装我们 App 的多。
// 只要按它的协议说话，别人手机上的 LocalSend 就能直接把文件丢进汐流；反过来汐流
// 也能把文件推过去。协议是纯 HTTP + JSON，没有任何服务端依赖。
//
// 实现的端点（接收方向）：
//   GET  /api/localsend/v2/info?fingerprint=xx     报自己是谁
//   POST /api/localsend/v2/register                对面来注册（握手）
//   POST /api/localsend/v2/prepare-upload          发之前先报文件清单，换 token
//   POST /api/localsend/v2/upload?sessionId&fileId&token   真身数据
//   POST /api/localsend/v2/cancel?sessionId        对方取消
//
// 安全边界跟互传一致：只对私有网段开放；「自动接收」关掉时一律 403 让对面重试。
// 端口用 53317（LocalSend 的默认值），跟汐流主端口 17777 互不干扰。

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	lsPort          = 53317
	lsMulticastAddr = "224.0.0.167:53317"
	lsBase          = "/api/localsend/v2"
)

// lsDevice 是协议里的设备描述。
type lsDevice struct {
	Alias       string `json:"alias"`
	Version     string `json:"version"`
	DeviceModel string `json:"deviceModel,omitempty"`
	DeviceType  string `json:"deviceType,omitempty"`
	Fingerprint string `json:"fingerprint"`
	Port        int    `json:"port"`
	Protocol    string `json:"protocol"`
	Download    bool   `json:"download,omitempty"`
	Announce    bool   `json:"announce,omitempty"`
}

type lsFileMeta struct {
	ID       string `json:"id"`
	FileName string `json:"fileName"`
	Size     int64  `json:"size"`
	FileType string `json:"fileType"`
	Sha256   string `json:"sha256,omitempty"`
	Preview  string `json:"preview,omitempty"`
}

type lsSession struct {
	ID      string
	From    string
	Files   map[string]lsFileMeta
	Tokens  map[string]string
	Created time.Time
}

var (
	lsMu        sync.Mutex
	lsSessions  = map[string]*lsSession{}
	lsSelfCache *lsDevice
)

func lsRand(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "xflow"
	}
	return hex.EncodeToString(b)
}

// lsAlias 是显示给别人的设备名：默认用主机名，去掉 XFlow@ 前缀那种啰嗦写法。
func lsAlias() string {
	name := discoveryName()
	name = strings.TrimPrefix(name, "XFlow@")
	if name == "" {
		name = "XFlow"
	}
	return name
}

// lsFingerprintID 是自我识别用的指纹，进程内稳定即可（存到设置里更稳）。
func lsFingerprintID() string {
	lsMu.Lock()
	defer lsMu.Unlock()
	if lsSelfCache != nil {
		return lsSelfCache.Fingerprint
	}
	fp := shareToken(16)
	lsSelfCache = &lsDevice{
		Alias:       lsAlias(),
		Version:     "2.1",
		DeviceModel: lsAlias(),
		DeviceType:  "desktop",
		Fingerprint: fp,
		Port:        lsPort,
		Protocol:    "http",
	}
	return fp
}

func lsSelf() lsDevice {
	lsFingerprintID()
	lsMu.Lock()
	defer lsMu.Unlock()
	d := *lsSelfCache
	return d
}

// startLocalSend 起 LocalSend 兼容层：TCP 53317 + UDP 组播发现。
// 端口被真 LocalSend 占了就安静退出（两者不能共存，谁先起谁用）。
func startLocalSend() {
	mux := http.NewServeMux()

	mux.HandleFunc(lsBase+"/info", func(w http.ResponseWriter, r *http.Request) {
		if !lsPrivatePeer(r) {
			http.Error(w, "only private network", http.StatusForbidden)
			return
		}
		writeShareJSON(w, lsSelf())
	})

	mux.HandleFunc(lsBase+"/register", func(w http.ResponseWriter, r *http.Request) {
		if !lsPrivatePeer(r) {
			http.Error(w, "only private network", http.StatusForbidden)
			return
		}
		var d lsDevice
		json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&d)
		if d.Fingerprint != "" && d.Fingerprint == lsFingerprintID() {
			http.Error(w, "self-discovered", http.StatusPreconditionFailed)
			return
		}
		if d.Alias != "" {
			lsRememberPeer(d)
		}
		writeShareJSON(w, lsSelf())
	})

	// 发之前先报清单：换每个文件的 token。自动接收关着就直接 403。
	mux.HandleFunc(lsBase+"/prepare-upload", func(w http.ResponseWriter, r *http.Request) {
		if !lsPrivatePeer(r) {
			http.Error(w, "only private network", http.StatusForbidden)
			return
		}
		if !shareAutoVal() {
			http.Error(w, "接收方没开自动接收", http.StatusForbidden)
			return
		}
		var req struct {
			Info  lsDevice              `json:"info"`
			Files map[string]lsFileMeta `json:"files"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&req); err != nil {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}
		if len(req.Files) == 0 {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		s := &lsSession{
			ID:      lsRand(8),
			From:    req.Info.Alias,
			Files:   map[string]lsFileMeta{},
			Tokens:  map[string]string{},
			Created: time.Now(),
		}
		tokens := map[string]string{}
		for id, f := range req.Files {
			s.Files[id] = f
			tok := lsRand(8)
			s.Tokens[id] = tok
			tokens[id] = tok
		}
		lsMu.Lock()
		// 清掉 10 分钟前的旧会话，别无限涨
		for id, old := range lsSessions {
			if time.Since(old.Created) > 10*time.Minute {
				delete(lsSessions, id)
			}
		}
		lsSessions[s.ID] = s
		lsMu.Unlock()

		appLog("localsend prepare: from=%q files=%d session=%s", req.Info.Alias, len(req.Files), s.ID)
		writeShareJSON(w, map[string]any{"sessionId": s.ID, "files": tokens})
	})

	// 真身数据：一个文件一次请求，可以并行。
	mux.HandleFunc(lsBase+"/upload", func(w http.ResponseWriter, r *http.Request) {
		if !lsPrivatePeer(r) {
			http.Error(w, "only private network", http.StatusForbidden)
			return
		}
		q := r.URL.Query()
		sid, fid, tok := q.Get("sessionId"), q.Get("fileId"), q.Get("token")
		lsMu.Lock()
		s := lsSessions[sid]
		var meta lsFileMeta
		var want string
		if s != nil {
			meta = s.Files[fid]
			want = s.Tokens[fid]
		}
		lsMu.Unlock()
		if s == nil {
			http.Error(w, "unknown session", http.StatusForbidden)
			return
		}
		if want == "" || tok != want {
			http.Error(w, "invalid token", http.StatusForbidden)
			return
		}

		dir := shareRecvDir()
		os.MkdirAll(dir, 0o755)
		name := filepath.Base(meta.FileName)
		if name == "" || name == "." || name == string(filepath.Separator) {
			name = "localsend-" + lsRand(4)
		}
		dst := filepath.Join(dir, name)
		// 重名就加序号，别覆盖用户已有文件
		for i := 1; ; i++ {
			if _, err := os.Stat(dst); os.IsNotExist(err) {
				break
			}
			ext := filepath.Ext(name)
			base := strings.TrimSuffix(name, ext)
			dst = filepath.Join(dir, base+" ("+itoa(i)+")"+ext)
			if i > 999 {
				break
			}
		}
		f, err := os.Create(dst)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		n, cerr := io.Copy(f, r.Body)
		f.Close()
		if cerr != nil {
			os.Remove(dst)
			http.Error(w, "write failed: "+cerr.Error(), http.StatusInternalServerError)
			return
		}
		appLog("localsend 收到 %q（%d 字节，来自 %q）", name, n, s.From)
		w.WriteHeader(http.StatusOK)
	})

	mux.HandleFunc(lsBase+"/cancel", func(w http.ResponseWriter, r *http.Request) {
		sid := r.URL.Query().Get("sessionId")
		lsMu.Lock()
		delete(lsSessions, sid)
		lsMu.Unlock()
		w.WriteHeader(http.StatusOK)
	})

	srv := &http.Server{
		Addr:              "0.0.0.0:" + itoa(lsPort),
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	ln, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		appLog("localsend: 端口 %d 被占，兼容层不启用（可能装过真 LocalSend）", lsPort)
		return
	}
	go func() {
		if err := srv.Serve(ln); err != nil {
			appLog("localsend: HTTP 服务退出：%v", err)
		}
	}()
	appLog("localsend: 兼容层已启动 %s（设备名 %q）", srv.Addr, lsAlias())

	go lsMulticastLoop()
}

// lsRememberPeer 记下对方（供以后做发送端用，现在只是写日志）。
func lsRememberPeer(d lsDevice) {
	appLog("localsend peer: %q %s:%d fp=%s", d.Alias, "", d.Port, d.Fingerprint)
}

// lsPrivatePeer 只放行私有网段/回环（跟互传的规矩一致）。
func lsPrivatePeer(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	if ip.IsLoopback() {
		return true
	}
	if ip.IsPrivate() {
		return true
	}
	// 100.64/10（Tailscale / CGNAT）也算局域网
	if ip4 := ip.To4(); ip4 != nil && ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127 {
		return true
	}
	return false
}

// lsMulticastLoop 周期性广播自己 + 收别人的广播。
func lsMulticastLoop() {
	addr, err := net.ResolveUDPAddr("udp4", lsMulticastAddr)
	if err != nil {
		return
	}
	conn, err := net.ListenMulticastUDP("udp4", nil, addr)
	if err != nil {
		appLog("localsend: 组播监听失败（%v），发现功能不可用", err)
		return
	}
	defer conn.Close()

	// 每 30 秒喊一嗓子；别人收到会来注册
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		send := func() {
			d := lsSelf()
			d.Announce = true
			b, _ := json.Marshal(d)
			if c, err := net.DialUDP("udp4", nil, addr); err == nil {
				c.Write(b)
				c.Close()
			}
		}
		send()
		for range t.C {
			send()
		}
	}()

	buf := make([]byte, 64<<10)
	for {
		n, src, err := conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		var d lsDevice
		if json.Unmarshal(buf[:n], &d) != nil || d.Fingerprint == "" {
			continue
		}
		if d.Fingerprint == lsFingerprintID() {
			continue // 自己喊的
		}
		if !d.Announce {
			continue
		}
		// 对方在喊：先按协议回一个 /register，失败再回 UDP
		go lsRegisterBack(d, src)
	}
}

func lsRegisterBack(d lsDevice, src *net.UDPAddr) {
	self := lsSelf()
	host := src.IP.String()
	port := d.Port
	if port == 0 {
		port = lsPort
	}
	url := "http://" + net.JoinHostPort(host, itoa(port)) + lsBase + "/register"
	body, _ := json.Marshal(self)
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Post(url, "application/json", strings.NewReader(string(body)))
	if err == nil {
		resp.Body.Close()
		if resp.StatusCode == 200 {
			appLog("localsend: 已向 %q 注册（%s）", d.Alias, host)
			return
		}
	}
	// 退回 UDP 应答
	d.Announce = false
	b, _ := json.Marshal(d)
	if c, err := net.DialUDP("udp4", nil, src); err == nil {
		c.Write(b)
		c.Close()
	}
}
