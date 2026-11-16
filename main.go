//go:build windows

package main

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	webview2 "github.com/pgmsoul/go-webview2"
)

//go:embed ui.html
var uiFS embed.FS

var mgr *Manager

// trayMode：--tray 启动（窗口隐藏 + 托盘常驻）。
// 这种模式下来新任务时把窗口显示出来 —— 清秋 2026-11-15 要的「默认不占屏幕、
// 下载时才弹出来」，和 Linux 版 1.6.10 的行为对齐。
var trayMode bool

// mainView / servePort：showTrayWindow 需要在窗口重新显示后把页面重新载入一次。
// WebView2 被 SW_HIDE 之后，渲染器会挂起；再 SW_SHOW 回来经常是白屏或花屏
// （2026-11-15 清秋截图那团彩色条纹就是这个）。重载是最省事可靠的修法。
var (
	mainView  webview2.WebView
	servePort int
)

// showTrayWindow 给 HTTP handler 用：把隐藏的主窗口叫出来（找不到就重试几次，
// 因为窗口是主线程异步建的，可能比第一次请求晚）。
func showTrayWindow() {
	if !trayMode {
		return
	}
	go func() {
		for i := 0; i < 20; i++ {
			if findWin() != 0 {
				showMainWindow()
				reloadView()
				return
			}
			time.Sleep(150 * time.Millisecond)
		}
		// 窗口没了（被销毁 / 从没建成功）—— 直接拉一个窗口客户端出来
		spawnWindowClient()
	}()
}

// windowPidPath 记录窗口客户端的 pid，防止连点任务弹一堆窗口。
func windowPidPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".localdl-window.pid")
}

// spawnWindowClient 起一个 --window-only 子进程把界面弹出来。
func spawnWindowClient() {
	pf := windowPidPath()
	if b, err := os.ReadFile(pf); err == nil {
		var pid int
		fmt.Sscanf(strings.TrimSpace(string(b)), "%d", &pid)
		if pid > 0 && processAlive(pid) {
			return // 已经有一个窗口进程
		}
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	cmd := exec.Command(exe, "--window-only")
	cmd.Env = os.Environ()
	if err := cmd.Start(); err != nil {
		logTray("spawnWindowClient 失败: " + err.Error())
		return
	}
	os.WriteFile(pf, []byte(strconv.Itoa(cmd.Process.Pid)), 0644)
	logTray(fmt.Sprintf("spawnWindowClient: pid=%d", cmd.Process.Pid))
	go cmd.Wait()
}

// runWindowClient 只建窗口连本地服务，窗口关掉就退出（服务不受影响）。
func runWindowClient() {
	port := 17777
	home, _ := os.UserHomeDir()
	if b, err := os.ReadFile(filepath.Join(home, ".localdl-port")); err == nil {
		if p, _ := strconv.Atoi(strings.TrimSpace(string(b))); p > 0 {
			port = p
		}
	}
	w := webview2.NewWithOptions(webview2.WebViewOptions{
		AutoFocus: true,
		DataPath:  filepath.Join(home, ".localdl-webview"),
		WindowOptions: webview2.WindowOptions{
			Title:   "XFlow",
			Width:   1000,
			Height:  700,
			Default: true,
		},
	})
	if w == nil {
		return
	}
	w.Navigate("http://127.0.0.1:" + strconv.Itoa(port) + "/")
	w.Run()
	os.Remove(windowPidPath())
}

func main() {
	defer func() {
		if r := recover(); r != nil {
			home, _ := os.UserHomeDir()
			f, _ := os.Create(filepath.Join(home, "localdl-crash.log"))
			if f != nil {
				fmt.Fprintf(f, "panic: %v\n", r)
				f.Close()
			}
		}
	}()

	// Windows：BT 存储切回非 mmap 模式（见 envboot_windows.go），第一次会 re-exec 一次
	ensureClassicFileIO()

	// WebView2 在 SW_HIDE 再 SW_SHOW 之后经常白屏或花屏（隐藏时 GPU 渲染器把上下文丢了，
	// 恢复时又没重画）。2026-11-15 清秋截图里那团彩色条纹就是这个。
	// 禁掉 GPU 走软件渲染 —— 这 UI 很轻，感知不出差别，但显示/隐藏来回折腾不会坏。
	if os.Getenv("WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS") == "" {
		os.Setenv("WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS", "--disable-gpu --disable-gpu-compositing")
	}

	// --window-only：只建界面窗口连已经在跑的服务（内部用，服务会 spawn 它）。
	// 必须有这个：--tray 建的那个隐藏窗口在某些情况下会没掉（2026-11-15 重启后
	// 实测 FindWindow 找不到、进程 MainWindowHandle=0），光靠 showWin 弹不出来。
	for _, a := range os.Args[1:] {
		if a == "--window-only" {
			runWindowClient()
			return
		}
	}

	// 单实例：只允许一个进程。
	// 以前 alreadyRunning() 写好了却从来没人调用——表面上"只起一个"其实是
	// 第二个实例抢 17777 端口失败 panic 掉的假象（2026-11-10 清秋发现）。
	if alreadyRunning() {
		// 已经有一个在跑：把命令行里的 URL 转给它（插件/浏览器协议拉起的场景），
		// 再把它的窗口叫到前面来，自己退出。
		// 只有真的带着任务来才把窗口叫出来。
		// 以前这里无条件 showWin()，结果是「多一个自启点」就等于「开机必弹窗」——
		// 2026-11-15 查到机器上同时有 Run 键和计划任务两处自启，第二个实例一启动
		// 就把第一个实例藏着的窗口叫出来了。
		if u, n := parseLaunchURL(os.Args[1:]); u != "" {
			forwardToRunning(u, n)
			showMainWindow()
		}
		return
	}

	enableDarkMode()

	// 上次退出前删不掉的 BT 临时文件（见 cleanup.go）：新进程里旧句柄已释放，清一遍
	flushPendingDeletes()

	home, _ := os.UserHomeDir()
	mgr = NewManager(filepath.Join(home, ".localdl.json"))
	mgr.StartTicker()

	mux := http.NewServeMux()
	registerDiscoveryAPI(mux)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		b, _ := uiFS.ReadFile("ui.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(b)
	})
	// 界面右上角显示版本号，也能给脚本探活用
	registerSettingsAPI(mux)

	// 更新检查：去 GitHub 看最新 tag。前端拿到 hasUpdate 就提示。
	mux.HandleFunc("/api/update", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("force") != "" {
			updateMu.Lock()
			updateCached = nil
			updateMu.Unlock()
		}
		json.NewEncoder(w).Encode(checkUpdate())
	})

	// 点更新提示时调这个：把本平台的安装包丢给下载器下到当前目录
	mux.HandleFunc("/api/update/download", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "只收 POST", 405)
			return
		}
		info := checkUpdate()
		if !info.HasUpdate || info.Asset == "" {
			writeJSONErr(w, "没有可下载的更新（"+orDefault(info.Err, "检查一下网络")+"）")
			return
		}
		j, err := mgr.AddHTTP(info.Asset, info.AssetName, currentDir(), 8)
		if err != nil {
			writeJSONErr(w, err.Error())
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
			json.NewDecoder(r.Body).Decode(&d)
			src := strings.TrimSpace(d.Source)
			if src == "" {
				http.Error(w, "链接为空", 400)
				return
			}
			dir := d.Dir
			if dir == "" {
				dir = currentDir()
			}
			switch {
			case strings.HasPrefix(src, "magnet:") || strings.HasSuffix(strings.ToLower(src), ".torrent"):
				mgr.AddBT(src, orDefault(d.Name, "BT 任务"), dir)
			case IsBiliURL(src):
				// B 站：自己解析 playurl + 多候选探测 + 双流下载 + ffmpeg 合并
				mgr.AddBili(src, d.Name, dir)
			case IsVideoURL(src):
				// 其他视频站（YouTube…）：交给 yt-dlp
				mgr.AddVideo(src, d.Name, dir)
			default:
				mgr.AddHTTP(src, orDefault(d.Name, fileNameFromURL(src)), dir, d.Segs)
			}
			showTrayWindow()
			json.NewEncoder(w).Encode(map[string]any{"ok": true})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"jobs": mgr.List()})
	})
	// 浏览器插件用的入口：带 Referer / Cookie 的下载请求。
	// 插件在浏览器扩展上下文里 fetch 127.0.0.1，绕开 CORS 限制靠的是 host_permissions，
	// 但预检还是会来，所以 withCORS 里也放了 Allow-Private-Network。
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
		// B 站（和通用视频站）走原生/yt-dlp，别当普通直链去下
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

		if strings.HasPrefix(src, "magnet:") || strings.HasSuffix(strings.ToLower(src), ".torrent") {
			j := mgr.AddBT(src, orDefault(name, "BT 任务"), dir)
			showTrayWindow()
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
		j, err := mgr.AddHTTPWithHeaders(src, name, dir, d.Segs, h)
		if err != nil {
			json.NewEncoder(w).Encode(map[string]any{"ok": false, "err": err.Error()})
			return
		}
		showTrayWindow()
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "id": j.ID, "name": name})
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
		showTrayWindow()
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
			mgr.Control(id, parts[1])
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})
	mux.HandleFunc("/api/redownload", func(w http.ResponseWriter, r *http.Request) {
		j := mgr.Get(r.URL.Query().Get("id"))
		if j == nil {
			http.Error(w, "没找到", 404)
			return
		}
		dir := currentDir()
		if j.Kind == "http" {
			mgr.AddHTTP(j.Source, j.Name, dir, j.SegN)
		} else {
			mgr.AddBT(j.Source, j.Name, dir)
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})

	mux.HandleFunc("/api/hide", func(w http.ResponseWriter, r *http.Request) {
		hideWin()
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})

	mux.HandleFunc("/api/clear", func(w http.ResponseWriter, r *http.Request) {
		mgr.ClearDone()
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})
	mux.HandleFunc("/api/open", func(w http.ResponseWriter, r *http.Request) {
		j := mgr.Get(r.URL.Query().Get("id"))
		if j == nil {
			http.Error(w, "没找到", 404)
			return
		}
		p := currentDir()
		if j.FilePath != "" {
			p = filepath.Dir(j.FilePath)
		}
		exec.Command("explorer", p).Start()
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})
	mux.HandleFunc("/api/browse", func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Query().Get("path")
		type ent struct {
			Name string `json:"name"`
			Path string `json:"path"`
		}
		var dirs []ent
		parent := ""
		shortcuts := map[string]string{}
		if h, err := os.UserHomeDir(); err == nil {
			for _, kv := range [][2]string{
				{"桌面", filepath.Join(h, "Desktop")},
				{"下载", filepath.Join(h, "Downloads")},
				{"文档", filepath.Join(h, "Documents")},
				{"图片", filepath.Join(h, "Pictures")},
				{"视频", filepath.Join(h, "Videos")},
				{"用户主目录", h},
			} {
				if st, err := os.Stat(kv[1]); err == nil && st.IsDir() {
					shortcuts[kv[0]] = kv[1]
				}
			}
		}
		if p == "" {
			for c := 'A'; c <= 'Z'; c++ {
				d := string(c) + ":\\"
				if st, err := os.Stat(d); err == nil && st.IsDir() {
					dirs = append(dirs, ent{Name: string(c) + ":", Path: d})
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
				if strings.HasPrefix(n, "$") || strings.HasPrefix(n, ".") {
					continue
				}
				dirs = append(dirs, ent{Name: n, Path: filepath.Join(p, n)})
			}
			parent = filepath.Dir(p)
			if len(parent) < len(p) && strings.HasSuffix(p, ":\\") {
				parent = ""
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "path": p, "parent": parent, "dirs": dirs, "current": currentDir(), "shortcuts": shortcuts})
	})

	mux.HandleFunc("/api/setdir", func(w http.ResponseWriter, r *http.Request) {
		var d struct {
			Dir string `json:"dir"`
		}
		json.NewDecoder(r.Body).Decode(&d)
		if d.Dir != "" && isDir(d.Dir) {
			defaultDirOverride = d.Dir
			saveDefaultDir(d.Dir)
			json.NewEncoder(w).Encode(map[string]any{"ok": true})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"ok": false})
	})

	mux.HandleFunc("/api/pickdir_disabled", func(w http.ResponseWriter, r *http.Request) {
		p, err := pickFolder("选择下载目录")
		lg := filepath.Join(mustHome(), "localdl-pickdir.log")
		if err != nil || p == "" || !isDir(p) {
			os.WriteFile(lg, []byte(fmt.Sprintf("err=%v path=%q isdir=%v\n", err, p, isDir(p))), 0644)
			json.NewEncoder(w).Encode(map[string]any{"ok": false, "err": fmt.Sprint(err)})
			return
		}
		os.WriteFile(lg, []byte("ok "+p+"\n"), 0644)
		defaultDirOverride = p
		saveDefaultDir(p)
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "dir": p})
	})

	mux.HandleFunc("/api/dir", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			var d struct {
				Dir string `json:"dir"`
			}
			json.NewDecoder(r.Body).Decode(&d)
			if d.Dir != "" {
				defaultDirOverride = d.Dir
				os.MkdirAll(d.Dir, 0755)
				saveDefaultDir(d.Dir)
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"dir": currentDir()})
	})

	// 固定端口：插件要知道往哪发，随机端口做不到。17777 被占才退回随机。
	// 只听本机回环：别的机器要派任务走 SSH（本机 curl 或 -L 隧道），不对外开端口。
	apiAddr := listenAddr(17777)
	ln, err := net.Listen("tcp", apiAddr)
	if err != nil {
		ln, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			panic(err)
		}
	}
	port := ln.Addr().(*net.TCPAddr).Port
	servePort = port
	currentPort = port
	// 把端口写到文件，方便外部（另一台机器/脚本/插件安装说明）调用 API
	os.WriteFile(filepath.Join(home, ".localdl-port"), []byte(strconv.Itoa(port)), 0644)
	needTok := loadSettings().Listen == "lan"
	setAPIListener(ln, mux)
	// 互传（汐港搬过来的）：/api/fs /api/download /api/upload /api/send /api/incoming /api/share
	registerShare(mux)
	registerClipboard(mux)
	go http.Serve(ln, withCORS(guardMux(mux, needTok)))
	startDiscovery(port, needTok)
	defer stopDiscovery()

	// --headless：只跑 HTTP 服务和下载引擎，不建窗口、不起托盘。
	// 给「ssh / 计划任务 / 无桌面会话」的场景用 —— session 0 里建 WebView2 窗口
	// 会直接 panic（nil pointer），所以这种环境必须跳过窗口。
	// 用法：XFlow.exe --headless [--url <链接>]；停止就 taskkill /IM XFlow.exe /F。
	headless := false
	startHidden := false
	for _, a := range os.Args[1:] {
		switch a {
		case "--tray", "-tray", "--hidden":
			startHidden = true
			trayMode = true // ★ 少了这句：--tray 启动时不会弹窗（2026-11-15 踩到）
		case "--headless", "-headless":
			headless = true
		}
	}
	if headless {
		os.WriteFile(filepath.Join(home, "xflow-headless.log"),
			[]byte(time.Now().Format(time.RFC3339)+" headless 启动，端口 "+strconv.Itoa(port)+"\n"), 0644)
		select {}
	}

	darkenWindowTitle("XFlow")
	startTray()

	// 也认一个标记文件：计划任务的启动参数容易丢（schtasks /change /tr 加不了参数），
	// 有这个文件就静默启动到托盘，不再依赖命令行开关。
	if !startHidden {
		if _, err := os.Stat(filepath.Join(home, ".localdl-tray")); err == nil {
			startHidden = true
			trayMode = true
		}
	}

	w := webview2.NewWithOptions(webview2.WebViewOptions{
		Debug:     false,
		AutoFocus: true,
		DataPath:  filepath.Join(home, ".localdl-webview"),
		WindowOptions: webview2.WindowOptions{
			Title:   "XFlow",
			Width:   1000,
			Height:  700,
			Default: true,
			Hidden:  startHidden, // 自启模式：创建就不显示，不闪
		},
		OnClose: func(w webview2.WebView) {
			// 点 × 不退出，缩到托盘（同时把 WebView2 标记为不可见，免得再显示时白屏）
			hideMainWindowForClose()
		},
	})
	if w != nil {
		mainView = w
	}
	if w == nil {
		// 回退：用浏览器
		exec.Command("rundll32", "url.dll,FileProtocolHandler", "http://127.0.0.1:"+strconv.Itoa(port)).Start()
		select {}
	}
	defer w.Destroy()
	w.Navigate("http://127.0.0.1:" + strconv.Itoa(port))

	w.Run()
}

func orDefault(s, d string) string {
	if strings.TrimSpace(s) == "" {
		return d
	}
	return s
}

// parseLaunchURL 从命令行里抠出要下载的 URL。
// 支持三种形态：xflow://add?url=...&name=... / --url <url> / 裸 http(s) URL
func parseLaunchURL(args []string) (string, string) {
	for i, a := range args {
		a = strings.TrimSpace(a)
		low := strings.ToLower(a)
		if strings.HasPrefix(low, "xflow://") {
			if u, err := url.Parse(a); err == nil {
				q := u.Query()
				return q.Get("url"), q.Get("name")
			}
		}
		if (a == "--url" || a == "-url" || a == "--add") && i+1 < len(args) {
			return args[i+1], ""
		}
	}
	for _, a := range args {
		if strings.HasPrefix(a, "http://") || strings.HasPrefix(a, "https://") {
			return a, ""
		}
	}
	return "", ""
}

// forwardToRunning 把任务 POST 给已经在跑的那个实例（端口写在 .localdl-port）
func forwardToRunning(rawurl, name string) bool {
	home, _ := os.UserHomeDir()
	b, err := os.ReadFile(filepath.Join(home, ".localdl-port"))
	if err != nil {
		return false
	}
	port := strings.TrimSpace(string(b))
	if port == "" {
		return false
	}
	body, _ := json.Marshal(map[string]any{"url": rawurl, "name": name})
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Post("http://127.0.0.1:"+port+"/api/add", "application/json", bytes.NewReader(body))
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == 200
}

// showMainWindow / hideMainWindow：窗口显隐的同时同步 WebView2 的 IsVisible，
// 不然再显示回来就是白屏/花屏。
func showMainWindow() {
	showWin()
	if mainView != nil {
		mainView.Dispatch(func() { mainView.SetVisible(true) })
	}
}

func hideMainWindow() {
	if mainView != nil {
		mainView.Dispatch(func() { mainView.SetVisible(false) })
	}
	hideWin()
}

// reloadView 让界面重新载入一次（隐藏后再显示时 WebView2 常白屏，重载最稳）。
func reloadView() {
	if mainView == nil || servePort == 0 {
		return
	}
	url := "http://127.0.0.1:" + strconv.Itoa(servePort) + "/"
	mainView.Dispatch(func() {
		mainView.Navigate(url)
	})
}

// ensureWindow Windows 版本身就是窗口程序，不需要像 Linux 那样按需拉起窗口。
// （main_linux.go 里那份是给 headless 模式用的，win 版只保留签名一致。）
func ensureWindow() {}
