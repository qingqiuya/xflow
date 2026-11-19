//go:build !windows

package main

// Linux 版入口：本地服务 + 原生窗口（webkit2gtk，靠 webview_go）。
//
// 和 Windows 版共用同一套下载引擎（manager / httpdl / btdl）和同一份 ui.html，
// 区别只在外壳：那边是 WebView2，这边是 WebKitGTK。
//
// 用法：
//   xflow                  起服务并开窗口
//   xflow --headless       只跑服务（服务器上、或者没图形界面时）
//   xflow --browser        服务起来后交给系统浏览器（不想要原生窗口时）
//   xflow --window-only    只开界面窗口，连已经在跑的服务（服务端自己会拉它）
//   xflow --port 8899      换端口
//   xflow --dir /data/dl   换默认下载目录（写进配置）
//
// 没有 DISPLAY / WAYLAND_DISPLAY 时自动退化成 --headless。

import (
	"bytes"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	webview "github.com/webview/webview_go"
)

//go:embed ui.html
var uiFS embed.FS

var mgr *Manager

// headlessMode / servePort / stateDir 给 ensureWindow 用：
// 服务无界面跑着，有人加任务时才把窗口拉起来。
var (
	headlessMode bool
	servePort    int
	stateDirPath string
)

func main() {
	port := flag.Int("port", 17777, "监听端口（只绑 127.0.0.1）")
	dir := flag.String("dir", "", "默认下载目录")
	headless := flag.Bool("headless", false, "只跑服务，不开界面")
	browser := flag.Bool("browser", false, "用系统浏览器打开，不开原生窗口")
	windowOnly := flag.Bool("window-only", false, "只开窗口，连已经在跑的服务（内部用）")
	flag.Parse()

	// --window-only：不碰 Manager、不监听端口，只把界面连到已有服务上。
	// headless 服务在收到新任务时会 exec 自己这个模式来"弹窗"。
	if *windowOnly {
		runWindowClient(*port)
		return
	}

	stateDir := os.Getenv("XFLOW_STATE")
	if stateDir == "" {
		stateDir = filepath.Join(mustHome(), ".local", "state", "xflow")
	}
	os.MkdirAll(stateDir, 0755)
	stateDirPath = stateDir
	servePort = *port
	headlessMode = *headless || *browser

	if *dir != "" {
		os.MkdirAll(*dir, 0755)
		defaultDirOverride = *dir // 进程内优先，否则多实例会互相踩 .localdl-dir
		saveDefaultDir(*dir)
	}

	mgr = NewManager(filepath.Join(stateDir, "jobs.json"))
	mgr.StartTicker()

	mux := http.NewServeMux()
	registerDiscoveryAPI(mux)

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		b, _ := uiFS.ReadFile("ui.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(b)
	})

	// 界面右上角显示版本号，也能给脚本探活用
	registerSettingsAPI(mux)

	mux.HandleFunc("/api/update", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("force") != "" {
			updateMu.Lock()
			updateCached = nil
			updateMu.Unlock()
		}
		json.NewEncoder(w).Encode(checkUpdate())
	})

	mux.HandleFunc("/api/update/download", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "只收 POST", 405)
			return
		}
		info := checkUpdate()
		if !info.HasUpdate || info.Asset == "" {
			json.NewEncoder(w).Encode(map[string]any{"ok": false, "err": "没有可下载的更新"})
			return
		}
		j, err := mgr.AddHTTP(info.Asset, info.AssetName, currentDir(), 8)
		if err != nil {
			json.NewEncoder(w).Encode(map[string]any{"ok": false, "err": err.Error()})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "id": j.ID, "name": info.AssetName, "version": info.Latest})
	})

	mux.HandleFunc("/icon.png", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "max-age=86400")
		w.Write(iconPNG)
	})

	// 界面背景图（嵌在 exe 里）
	mux.HandleFunc("/bg.jpg", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Cache-Control", "max-age=86400")
		w.Write(bgJPG)
	})

	mux.HandleFunc("/api/version", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"version": Version})
	})

	mux.HandleFunc("/api/jobs", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			var d struct {
				Source string `json:"source"`
				Name   string `json:"name"`
				Dir    string `json:"dir"`
				Segs   int    `json:"segs"`
			}
			if err := json.NewDecoder(r.Body).Decode(&d); err != nil {
				json.NewEncoder(w).Encode(map[string]any{"ok": false, "err": "请求体不是合法 JSON"})
				return
			}
			src := strings.TrimSpace(d.Source)
			if src == "" {
				json.NewEncoder(w).Encode(map[string]any{"ok": false, "err": "链接为空"})
				return
			}
			target := d.Dir
			if target == "" {
				target = currentDir()
			}
			if isBTSource(src) {
				j := mgr.AddBT(src, orDefault(d.Name, "BT 任务"), target)
				ensureWindow()
				json.NewEncoder(w).Encode(map[string]any{"ok": true, "id": j.ID})
				return
			}
			j, err := mgr.AddHTTPWithHeaders(src, orDefault(d.Name, fileNameFromURL(src)), target, d.Segs, nil)
			if err != nil {
				json.NewEncoder(w).Encode(map[string]any{"ok": false, "err": err.Error()})
				return
			}
			ensureWindow()
			json.NewEncoder(w).Encode(map[string]any{"ok": true, "id": j.ID})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"jobs": mgr.List()})
	})

	// 浏览器插件走这个（与 Windows 版同名同参）：插件只认 /api/add + url/name/referer/cookie
	// ★ 2026-11-11：Linux 版原来只有 /api/jobs，插件一调就 404「汐流没接住」
	registerBiliLogin(mux)

	mux.HandleFunc("/api/add", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "只收 POST", 405)
			return
		}
		var d struct {
			URL     string `json:"url"`
			Name    string `json:"name"`
			Dir     string `json:"dir"`
			Referer string `json:"referer"`
			Cookie  string `json:"cookie"`
			Segs    int    `json:"segs"`
		}
		if err := json.NewDecoder(r.Body).Decode(&d); err != nil {
			json.NewEncoder(w).Encode(map[string]any{"ok": false, "err": "请求体不是合法 JSON"})
			return
		}
		src := strings.TrimSpace(d.URL)
		if src == "" {
			json.NewEncoder(w).Encode(map[string]any{"ok": false, "err": "url 为空"})
			return
		}
		dir := d.Dir
		if dir == "" {
			dir = currentDir()
		}
		name := d.Name
		if name == "" {
			name = fileNameFromURL(src)
		}
		if isBTSource(src) {
			j := mgr.AddBT(src, orDefault(name, "BT 任务"), dir)
			ensureWindow()
			json.NewEncoder(w).Encode(map[string]any{"ok": true, "id": j.ID})
			return
		}
		if IsBiliURL(src) {
			j := mgr.AddBili(src, d.Name, dir)
			ensureWindow()
			json.NewEncoder(w).Encode(map[string]any{"ok": true, "id": j.ID, "kind": "video"})
			return
		}
		if IsVideoURL(src) {
			j := mgr.AddVideo(src, d.Name, dir)
			ensureWindow()
			json.NewEncoder(w).Encode(map[string]any{"ok": true, "id": j.ID, "kind": "video"})
			return
		}
		h := map[string]string{}
		if d.Referer != "" {
			h["Referer"] = d.Referer
		}
		if d.Cookie != "" {
			h["Cookie"] = d.Cookie
		}
		j, err := mgr.AddHTTPWithHeaders(src, name, dir, d.Segs, h)
		if err != nil {
			json.NewEncoder(w).Encode(map[string]any{"ok": false, "err": err.Error()})
			return
		}
		ensureWindow()
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "id": j.ID, "name": name})
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
			if err := mgr.Control(id, parts[1]); err != nil {
				json.NewEncoder(w).Encode(map[string]any{"ok": false, "err": err.Error()})
				return
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})

	mux.HandleFunc("/api/clear", func(w http.ResponseWriter, r *http.Request) {
		mgr.ClearDone()
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})

	mux.HandleFunc("/api/redownload", func(w http.ResponseWriter, r *http.Request) {
		j := mgr.Get(r.URL.Query().Get("id"))
		if j == nil {
			http.Error(w, "没找到", 404)
			return
		}
		target := currentDir()
		switch {
		case j.Kind == "video" || IsVideoURL(j.Source):
			mgr.AddVideo(j.Source, j.Name, target)
		case j.Kind == "http":
			mgr.AddHTTP(j.Source, j.Name, target, j.SegN)
		default:
			mgr.AddBT(j.Source, j.Name, target)
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})

	mux.HandleFunc("/api/open", func(w http.ResponseWriter, r *http.Request) {
		j := mgr.Get(r.URL.Query().Get("id"))
		if j == nil || j.FilePath == "" {
			http.Error(w, "没找到", 404)
			return
		}
		exec.Command("xdg-open", filepath.Dir(j.FilePath)).Start()
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})

	mux.HandleFunc("/api/dir", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			var d struct {
				Dir string `json:"dir"`
			}
			json.NewDecoder(r.Body).Decode(&d)
			if d.Dir != "" {
				os.MkdirAll(d.Dir, 0755)
				defaultDirOverride = d.Dir
				saveDefaultDir(d.Dir)
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"dir": currentDir()})
	})

	// 目录选择框的数据源：跟 Windows 版同一个契约（ui.html 读 dirs[].path /
	// shortcuts / parent）。★2026-11-17 之前这里返回的是 items[{name,dir}]，
	// Linux 上「下载目录」对话框永远是空的、点确定还 404 —— 这次补齐。
	mux.HandleFunc("/api/browse", func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimSpace(r.URL.Query().Get("path"))
		type ent struct {
			Name string `json:"name"`
			Path string `json:"path"`
		}
		var dirs []ent
		parent := ""
		shortcuts := map[string]string{}
		h := mustHome()
		for _, kv := range [][2]string{
			{"桌面", filepath.Join(h, "Desktop")},
			{"下载", filepath.Join(h, "Downloads")},
			{"文档", filepath.Join(h, "Documents")},
			{"图片", filepath.Join(h, "Pictures")},
			{"视频", filepath.Join(h, "Videos")},
			{"用户主目录", h},
		} {
			if isDir(kv[1]) {
				shortcuts[kv[0]] = kv[1]
			}
		}
		if p == "" {
			// 空路径 = 根：给「/」+ 常见挂载点的顶层目录（Linux 没有盘符）
			dirs = append(dirs, ent{Name: "/", Path: "/"})
			for _, base := range []string{"/mnt", "/media", "/data"} {
				ents, err := os.ReadDir(base)
				if err != nil {
					continue
				}
				for _, e := range ents {
					if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
						continue
					}
					dirs = append(dirs, ent{Name: base + "/" + e.Name(), Path: filepath.Join(base, e.Name())})
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
				if strings.HasPrefix(n, ".") {
					continue
				}
				dirs = append(dirs, ent{Name: n, Path: filepath.Join(p, n)})
			}
			parent = filepath.Dir(p)
			if parent == "/" {
				parent = ""
			}
		}
		json.NewEncoder(w).Encode(map[string]any{
			"ok": true, "path": p, "parent": parent, "dirs": dirs,
			"current": currentDir(), "shortcuts": shortcuts,
		})
	})

	// 界面里点「确定」用的就是这个（Windows 版一直有，Linux 版漏了）
	mux.HandleFunc("/api/setdir", func(w http.ResponseWriter, r *http.Request) {
		var d struct {
			Dir string `json:"dir"`
		}
		json.NewDecoder(r.Body).Decode(&d)
		if d.Dir != "" && isDir(d.Dir) {
			defaultDirOverride = d.Dir
			saveDefaultDir(d.Dir)
			json.NewEncoder(w).Encode(map[string]any{"ok": true, "dir": d.Dir})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": false, "err": "目录不存在"})
	})

	// 上传本地种子文件：按内容校验，不看后缀
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
		target := r.FormValue("dir")
		if target == "" {
			target = currentDir()
		}
		j := mgr.AddBT(tmp.Name(), name, target)
		ensureWindow()
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "id": j.ID, "name": name})
	})

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "version": Version})
	})

	// 默认只听本机回环；设置里可以改成局域网（会自动带 token 校验）
	//
	// ★2026-11-17：端口被占（典型场景：systemd user 服务已经跑着，用户又点了
	// 桌面图标 / 命令行的 xflow）时，以前直接“监听失败”退出，窗口不出来。
	// 现在先探一下 127.0.0.1:<port>/healthz —— 是自己的就把窗口接上去（等同
	// --window-only），不是才报错。
	hasDisplay := os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != ""
	addr := listenAddr(*port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		if ownServiceListening(*port) {
			// 已经有一份在跑：有图形就把界面接上去，没有就无声退出（systemd 重启
			// / 连点两次图标都不该报错弹窗）。
			fmt.Printf("已有汐流在 %d 端口上跑。\n", *port)
			if hasDisplay {
				runWindowClient(*port)
			}
			return
		}
		fmt.Fprintf(os.Stderr, "监听 %s 失败：%v\n", addr, err)
		os.Exit(1)
	}
	url := "http://" + addr + "/"
	fmt.Printf("汐流 XFlow 已启动：%s\n下载目录：%s\n", url, currentDir())

	// 收到 SIGTERM / Ctrl-C 时把任务状态落盘再退
	go func() {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGTERM, syscall.SIGINT)
		<-ch
		fmt.Println("\n正在退出，保存任务状态…")
		mgr.save()
		os.Exit(0)
	}()

	needTok := loadSettings().Listen == "lan"
	currentPort = *port // 设置页要显示真实端口（重启监听也靠它）
	// 路由必须先注册完再 Serve：设置页切「仅本机/局域网」会重绑监听并起第二个
	// Serve，跟这里并发往同一个 mux 注册就是 map 竞态（2026-11-17 收拾）。
	// 互传（汐港搬过来的）：/api/fs /api/download /api/upload /api/send /api/incoming /api/share
	registerShare(mux)
	registerClipboard(mux)
	setAPIListener(ln, mux)
	go func() {
		// 局域网发现（mDNS）：注册自己 + 浏览手机等设备
		startDiscovery(*port, needTok)
		defer stopDiscovery()
		if err := http.Serve(ln, withCORS(guardMux(mux, needTok))); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}()

	switch {
	case *headless || (!hasDisplay && !*browser):
		fmt.Println("没有图形界面，只跑服务。用浏览器打开上面那个地址就行。")
		select {}
	case *browser:
		exec.Command("xdg-open", url).Start()
		select {}
	default:
		// 原生窗口：webkit2gtk。Run() 会阻塞到窗口关闭。
		w := webview.New(false)
		defer w.Destroy()
		w.SetTitle("汐流 XFlow")
		w.SetSize(1040, 720, webview.HintNone)
		w.Navigate(url)
		w.Run()
		mgr.save()
	}
}

// ownServiceListening 探一下这个端口上是不是已经在跑的汐流（自己的服务）。
func ownServiceListening(port int) bool {
	c := http.Client{Timeout: 800 * time.Millisecond}
	resp, err := c.Get("http://127.0.0.1:" + strconv.Itoa(port) + "/healthz")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return false
	}
	var v struct {
		OK      bool   `json:"ok"`
		Version string `json:"version"`
	}
	return json.NewDecoder(resp.Body).Decode(&v) == nil && v.OK
}

func orDefault(s, d string) string {
	if strings.TrimSpace(s) == "" {
		return d
	}
	return s
}

func isBTSource(src string) bool {
	if strings.HasPrefix(src, "magnet:?") {
		return true
	}
	low := strings.ToLower(src)
	return strings.HasSuffix(low, ".torrent")
}

// windowPidFile 记录界面窗口客户端的 pid，用来避免重复弹窗。
func windowPidFile() string {
	if stateDirPath == "" {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".local", "state", "xflow", "window.pid")
	}
	return filepath.Join(stateDirPath, "window.pid")
}

// runWindowClient 只开窗口连已有服务；窗口关掉就退出，服务不受影响。
func runWindowClient(port int) {
	url := "http://127.0.0.1:" + strconv.Itoa(port) + "/"
	w := webview.New(false)
	defer w.Destroy()
	w.SetTitle("汐流 XFlow")
	w.SetSize(1040, 720, webview.HintNone)
	w.Navigate(url)
	w.Run()
	os.Remove(windowPidFile())
}

// ensureWindow 在有界面能力的机器上，收到第一个新任务时把窗口拉起来。
// 已经在开着的就什么都不做（靠 window.pid + kill -0 判断）。
func ensureWindow() {
	if !headlessMode {
		return // 本来就是窗口模式
	}
	if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		return // 真没图形环境
	}
	pidFile := windowPidFile()
	if b, err := os.ReadFile(pidFile); err == nil {
		if pid, _ := strconv.Atoi(strings.TrimSpace(string(b))); pid > 0 {
			if syscall.Kill(pid, 0) == nil {
				return // 窗口已经在
			}
		}
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	cmd := exec.Command(exe, "--window-only", "--port", strconv.Itoa(servePort))
	cmd.Env = os.Environ()
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "拉起窗口失败：%v\n", err)
		return
	}
	os.WriteFile(pidFile, []byte(strconv.Itoa(cmd.Process.Pid)), 0644)
	go cmd.Wait()
}
