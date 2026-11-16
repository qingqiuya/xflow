package main

// 互传：把 XPort(汐港) 的局域网文件互传搬进 XFlow(汐流)。
//
// 为什么并进来：XFlow 本来就有 mDNS 发现（`_xflow._tcp`，桌面用 zeroconf、
// 手机用 NsdManager）和一套 token 鉴权，互传最缺的就是这两样，其它都是现成的。
//
// 接口（都走 guardMux 的 token 校验，跟下载接口一致）：
//   GET  /api/fs?path=                     列目录（限 allowedRoots）
//   GET  /api/download?path=               下载（支持 Range）
//   POST /api/upload?dir=                  上传（multipart，字段 file）
//   POST /api/send?peer=ip:port&path=&token=  推给某台设备
//   GET  /api/incoming                     待确认列表
//   POST /api/incoming?id=&act=accept|reject
//   GET/POST /api/share                    {"autoAccept":bool,"dir":"收到的文件放哪"}

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type incomingFile struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Size int64  `json:"size"`
	From string `json:"from"`
	Tmp  string `json:"-"`
	When int64  `json:"when"`
}

var (
	shareMu      sync.Mutex
	shareAuto    = true
	shareEnabled = true   // 互传总开关（关掉后互传相关接口一律 403）
	shareDirOv  string // 收到的文件放哪（空 = <下载目录>/收到）
	sharePend    = map[string]*incomingFile{}
	shareOrder   []string
)

func shareToken(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func writeShareJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(v)
}

// shareRecvDir 收到的文件放哪。
func shareRecvDir() string {
	shareMu.Lock()
	ov := shareDirOv
	shareMu.Unlock()
	if ov != "" {
		return ov
	}
	return filepath.Join(currentDir(), "收到")
}

// allowedRoots 文件浏览的白名单根：家目录、下载目录、接收目录、常见数据盘。
func allowedRoots() []string {
	roots := []string{mustHome(), currentDir(), shareRecvDir()}
	for _, d := range []string{`D:\`, `E:\`, `F:\`, "/mnt", "/data", "/home", "/media"} {
		if st, err := os.Stat(d); err == nil && st.IsDir() {
			roots = append(roots, filepath.Clean(d))
		}
	}
	return roots
}

func underAllowed(p string) bool {
	p = filepath.Clean(p)
	lp := strings.ToLower(p)
	for _, r := range allowedRoots() {
		lr := strings.ToLower(filepath.Clean(r))
		if lp == lr || strings.HasPrefix(lp, lr+string(os.PathSeparator)) {
			return true
		}
	}
	return false
}

// ---------- 待确认队列 ----------

func shareAddPending(name, from, tmp string, size int64) *incomingFile {
	id := shareToken(6)
	f := &incomingFile{ID: id, Name: name, Size: size, From: from, Tmp: tmp, When: time.Now().Unix()}
	shareMu.Lock()
	sharePend[id] = f
	shareOrder = append(shareOrder, id)
	shareMu.Unlock()
	return f
}

func shareTakePending(id string) *incomingFile {
	shareMu.Lock()
	defer shareMu.Unlock()
	f := sharePend[id]
	if f == nil {
		return nil
	}
	delete(sharePend, id)
	for i, x := range shareOrder {
		if x == id {
			shareOrder = append(shareOrder[:i], shareOrder[i+1:]...)
			break
		}
	}
	return f
}

func sharePendingList() []*incomingFile {
	shareMu.Lock()
	defer shareMu.Unlock()
	out := make([]*incomingFile, 0, len(shareOrder))
	for _, id := range shareOrder {
		if f := sharePend[id]; f != nil {
			out = append(out, f)
		}
	}
	return out
}

// ---------- 路由 ----------

func registerShare(mux *http.ServeMux) {
	mux.HandleFunc("/api/fs", func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Query().Get("path")
		if p == "" {
			p = mustHome()
		}
		p = filepath.Clean(p)
		if !underAllowed(p) {
			writeShareJSON(w, map[string]any{"ok": false, "err": "不在允许范围内：" + p})
			return
		}
		ents, err := os.ReadDir(p)
		if err != nil {
			writeShareJSON(w, map[string]any{"ok": false, "err": err.Error()})
			return
		}
		type item struct {
			Name  string `json:"name"`
			Dir   bool   `json:"dir"`
			Size  int64  `json:"size"`
			MTime int64  `json:"mtime"`
		}
		items := make([]item, 0, len(ents))
		for _, e := range ents {
			it := item{Name: e.Name(), Dir: e.IsDir()}
			if fi, err := e.Info(); err == nil {
				it.Size = fi.Size()
				it.MTime = fi.ModTime().Unix()
			}
			items = append(items, it)
		}
		sort.Slice(items, func(i, j int) bool {
			if items[i].Dir != items[j].Dir {
				return items[i].Dir
			}
			return strings.ToLower(items[i].Name) < strings.ToLower(items[j].Name)
		})
		parent := filepath.Dir(p)
		if parent == p || !underAllowed(parent) {
			parent = ""
		}
		writeShareJSON(w, map[string]any{
			"ok": true, "path": p, "parent": parent, "items": items,
			"dir": shareRecvDir(), "autoAccept": shareAutoVal(),
		})
	})

	mux.HandleFunc("/api/download", func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Query().Get("path")
		if p == "" || !underAllowed(p) {
			http.Error(w, "路径不在允许范围内", 403)
			return
		}
		f, err := os.Open(p)
		if err != nil {
			http.Error(w, err.Error(), 404)
			return
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil || st.IsDir() {
			http.Error(w, "不是文件", 400)
			return
		}
		w.Header().Set("Content-Disposition", "attachment; filename=\""+filepath.Base(p)+"\"")
		http.ServeContent(w, r, filepath.Base(p), st.ModTime(), f)
	})

	mux.HandleFunc("/api/upload", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeShareJSON(w, map[string]any{"ok": false, "err": "要 POST"})
			return
		}
		// 两种口味：
		//   ① multipart/form-data（桌面 → 桌面，字段名 file）
		//   ② 裸 body + X-File-Name 头（手机端用这个 —— 它那套手写 HTTP 处理不了 multipart）
		encHdr := r.Header.Get("X-Enc") == "1"
		nmHdr := r.Header.Get("X-File-Name")
		if encHdr && nmHdr != "" {
			if dec, derr := url.PathUnescape(nmHdr); derr == nil {
				nmHdr = dec
			}
		}
		if nm := nmHdr; nm != "" {
			dir := r.URL.Query().Get("dir")
			if dir == "" || !underAllowed(dir) {
				dir = shareRecvDir()
			}
			os.MkdirAll(dir, 0o755)
			// 拖进来的文件夹：前端带 X-File-Path（相对路径，如 "合集/图/1.png"），
			// 在这里还原目录层级；不合法就退化成只取文件名。
			if rel := r.Header.Get("X-File-Path"); rel != "" {
				if encHdr {
					if dec, derr := url.PathUnescape(rel); derr == nil {
						rel = dec
					}
				}
				if pd, err := safeRelDir(dir, rel); err == nil && pd != "" {
					os.MkdirAll(pd, 0o755)
					dir = pd
					nm = filepath.Base(rel)
				}
			}
			if !shareAutoVal() {
				// 手动同意：先落临时区，等用户点「接收」
				tmp := filepath.Join(os.TempDir(), "xflow-in-"+shareToken(6))
				out, err := os.Create(tmp)
				if err != nil {
					writeShareJSON(w, map[string]any{"ok": false, "err": err.Error()})
					return
				}
				n, err := io.Copy(out, r.Body)
				out.Close()
				if err != nil {
					os.Remove(tmp)
					writeShareJSON(w, map[string]any{"ok": false, "err": err.Error()})
					return
				}
				f := shareAddPending(filepath.Base(nm), r.RemoteAddr, tmp, n)
				writeShareJSON(w, map[string]any{"ok": true, "pending": true, "id": f.ID, "name": f.Name, "size": n})
				return
			}
			dst := filepath.Join(dir, filepath.Base(nm))
			out, err := os.Create(dst)
			if err != nil {
				writeShareJSON(w, map[string]any{"ok": false, "err": err.Error()})
				return
			}
			n, err := io.Copy(out, r.Body)
			out.Close()
			if err != nil {
				os.Remove(dst)
				writeShareJSON(w, map[string]any{"ok": false, "err": err.Error()})
				return
			}
			writeShareJSON(w, map[string]any{"ok": true, "saved": []string{dst}, "size": n, "dir": dir})
			return
		}
		dir := r.URL.Query().Get("dir")
		if dir == "" || !underAllowed(dir) {
			dir = shareRecvDir()
		}
		os.MkdirAll(dir, 0o755)
		if err := r.ParseMultipartForm(64 << 20); err != nil {
			writeShareJSON(w, map[string]any{"ok": false, "err": "表单太大或格式不对: " + err.Error()})
			return
		}
		var saved []string
		for _, fhs := range r.MultipartForm.File {
			for _, fh := range fhs {
				name := filepath.Base(fh.Filename)
				if shareAutoVal() {
					dst := filepath.Join(dir, name)
					if err := saveUpload(fh, dst); err != nil {
						writeShareJSON(w, map[string]any{"ok": false, "err": err.Error()})
						return
					}
					saved = append(saved, dst)
				} else {
					tmp := filepath.Join(os.TempDir(), "xflow-in-"+shareToken(6))
					if err := saveUpload(fh, tmp); err != nil {
						writeShareJSON(w, map[string]any{"ok": false, "err": err.Error()})
						return
					}
					shareAddPending(name, r.RemoteAddr, tmp, fh.Size)
				}
			}
		}
		if len(saved) == 0 {
			writeShareJSON(w, map[string]any{"ok": true, "pending": true, "dir": dir})
			return
		}
		writeShareJSON(w, map[string]any{"ok": true, "saved": saved, "dir": dir})
	})

	// 推给某台设备：POST /api/send?peer=10.11.11.11:17777&path=<本地文件>&token=<对方token>
	mux.HandleFunc("/api/send", func(w http.ResponseWriter, r *http.Request) {
		peer := r.URL.Query().Get("peer")
		p := r.URL.Query().Get("path")
		if peer == "" || p == "" {
			writeShareJSON(w, map[string]any{"ok": false, "err": "要 peer 和 path"})
			return
		}
		if !underAllowed(p) {
			writeShareJSON(w, map[string]any{"ok": false, "err": "路径不在允许范围内"})
			return
		}
		f, err := os.Open(p)
		if err != nil {
			writeShareJSON(w, map[string]any{"ok": false, "err": err.Error()})
			return
		}
		defer f.Close()
		st, _ := f.Stat()
		if st == nil || st.IsDir() {
			writeShareJSON(w, map[string]any{"ok": false, "err": "只能发文件，目录以后再说"})
			return
		}
		// ?raw=1 → 裸 body + X-File-Name（发给手机端用；手机那套手写 HTTP 不解析 multipart）
		if r.URL.Query().Get("raw") == "1" {
			url := "http://" + peer + "/api/upload"
			if t := r.URL.Query().Get("token"); t != "" {
				url += "?token=" + t
			}
			req, err := http.NewRequest(http.MethodPost, url, f)
			if err != nil {
				writeShareJSON(w, map[string]any{"ok": false, "err": err.Error()})
				return
			}
			req.ContentLength = st.Size()
			req.Header.Set("Content-Type", "application/octet-stream")
			req.Header.Set("X-File-Name", filepath.Base(p))
			if t := r.URL.Query().Get("token"); t != "" {
				req.Header.Set("X-API-Token", t)
			}
			client := &http.Client{Timeout: 0}
			resp, err := client.Do(req)
			if err != nil {
				writeShareJSON(w, map[string]any{"ok": false, "err": err.Error()})
				return
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			if resp.StatusCode != 200 {
				writeShareJSON(w, map[string]any{"ok": false, "err": fmt.Sprintf("对方返回 %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))})
				return
			}
			writeShareJSON(w, map[string]any{"ok": true, "peer": peer, "size": st.Size(), "raw": true, "reply": strings.TrimSpace(string(body))})
			return
		}
		pr, pw := io.Pipe()
		mw := multipart.NewWriter(pw)
		go func() {
			defer pw.Close()
			fw, err := mw.CreateFormFile("file", filepath.Base(p))
			if err != nil {
				pw.CloseWithError(err)
				return
			}
			io.Copy(fw, f)
			mw.Close()
		}()
		url := "http://" + peer + "/api/upload"
		if t := r.URL.Query().Get("token"); t != "" {
			url += "?token=" + t
		}
		req, err := http.NewRequest(http.MethodPost, url, pr)
		if err != nil {
			writeShareJSON(w, map[string]any{"ok": false, "err": err.Error()})
			return
		}
		req.Header.Set("Content-Type", mw.FormDataContentType())
		if t := r.URL.Query().Get("token"); t != "" {
			req.Header.Set("X-API-Token", t)
		}
		client := &http.Client{Timeout: 0} // 大文件不设总超时
		resp, err := client.Do(req)
		if err != nil {
			writeShareJSON(w, map[string]any{"ok": false, "err": err.Error()})
			return
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if resp.StatusCode != 200 {
			writeShareJSON(w, map[string]any{"ok": false, "err": fmt.Sprintf("对方返回 %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))})
			return
		}
		writeShareJSON(w, map[string]any{"ok": true, "peer": peer, "size": st.Size(), "reply": strings.TrimSpace(string(body))})
	})

	mux.HandleFunc("/api/incoming", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			writeShareJSON(w, map[string]any{"ok": true, "autoAccept": shareAutoVal(), "dir": shareRecvDir(), "files": sharePendingList()})
			return
		}
		id := r.URL.Query().Get("id")
		act := r.URL.Query().Get("act")
		f := shareTakePending(id)
		if f == nil {
			writeShareJSON(w, map[string]any{"ok": false, "err": "没这个待接收文件"})
			return
		}
		defer os.Remove(f.Tmp)
		if act != "accept" {
			writeShareJSON(w, map[string]any{"ok": true, "act": "reject"})
			return
		}
		dir := shareRecvDir()
		os.MkdirAll(dir, 0o755)
		dst := filepath.Join(dir, filepath.Base(f.Name))
		if err := os.Rename(f.Tmp, dst); err != nil {
			if err2 := copyFileTo(f.Tmp, dst); err2 != nil {
				writeShareJSON(w, map[string]any{"ok": false, "err": err2.Error()})
				return
			}
		}
		writeShareJSON(w, map[string]any{"ok": true, "act": "accept", "path": dst})
	})

	mux.HandleFunc("/api/share", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			writeShareJSON(w, map[string]any{"ok": true, "enabled": shareEnabledVal(),
				"autoAccept": shareAutoVal(), "dir": shareRecvDir()})
			return
		}
		var d struct {
			Enabled    *bool  `json:"enabled"`
			AutoAccept *bool  `json:"autoAccept"`
			Dir        string `json:"dir"`
		}
		if err := json.NewDecoder(r.Body).Decode(&d); err != nil {
			writeShareJSON(w, map[string]any{"ok": false, "err": err.Error()})
			return
		}
		shareMu.Lock()
		if d.Enabled != nil {
			shareEnabled = *d.Enabled
		}
		if d.AutoAccept != nil {
			shareAuto = *d.AutoAccept
		}
		if d.Dir != "" {
			os.MkdirAll(d.Dir, 0o755)
			shareDirOv = d.Dir
		}
		shareMu.Unlock()
		writeShareJSON(w, map[string]any{"ok": true, "enabled": shareEnabledVal(),
			"autoAccept": shareAutoVal(), "dir": shareRecvDir()})
	})
}

func shareEnabledVal() bool {
	shareMu.Lock()
	defer shareMu.Unlock()
	return shareEnabled
}

func shareAutoVal() bool {
	shareMu.Lock()
	defer shareMu.Unlock()
	return shareAuto
}

func saveUpload(fh *multipart.FileHeader, dst string) error {
	src, err := fh.Open()
	if err != nil {
		return err
	}
	defer src.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, src)
	return err
}

func copyFileTo(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

// safeRelDir 把前端给的相对路径（可能带子目录）落成 base 下的真实目录。
// 只允许相对路径：绝对路径、盘符、.. 全部拒绝。返回 "base/子目录"。
func safeRelDir(base, rel string) (string, error) {
	rel = strings.TrimSpace(strings.ReplaceAll(rel, "\\", "/"))
	if rel == "" {
		return "", fmt.Errorf("空路径")
	}
	if strings.HasPrefix(rel, "/") || strings.Contains(rel, ":") {
		return "", fmt.Errorf("不接受绝对路径")
	}
	parts := []string{}
	for _, seg := range strings.Split(rel, "/") {
		seg = strings.TrimSpace(seg)
		if seg == "" || seg == "." || seg == ".." {
			continue
		}
		parts = append(parts, seg)
	}
	if len(parts) == 0 {
		return "", fmt.Errorf("空路径")
	}
	// 最后一段是文件名，去掉
	if len(parts) > 1 {
		parts = parts[:len(parts)-1]
	} else {
		return base, nil
	}
	out := filepath.Join(append([]string{base}, parts...)...)
	// 二次校验：必须在 base 之内
	if rp, err := filepath.Rel(base, out); err != nil || strings.HasPrefix(rp, "..") {
		return "", fmt.Errorf("路径越界")
	}
	return out, nil
}
