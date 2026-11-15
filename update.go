package main

// 更新检查：去 GitHub 拉 tags，和编译进来的 Version 比。
// 只做「检查 + 提示」，不自动替换文件（换正在运行的 exe 在 Windows 上很麻烦，
// 而且万一新版本有问题用户自己也不好回退）。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const updateReleaseURL = "https://api.github.com/repos/qingqiuya/xflow/releases/latest"
const updatePageURL = "https://github.com/qingqiuya/xflow/releases"

type updateInfo struct {
	Current   string `json:"current"`
	Latest    string `json:"latest"`
	HasUpdate bool   `json:"hasUpdate"`
	URL       string `json:"url,omitempty"`       // release 页面
	Asset     string `json:"asset,omitempty"`     // 本平台安装包直链
	AssetName string `json:"assetName,omitempty"` // 安装包文件名
	Size      int64  `json:"size,omitempty"`
	Portable  bool   `json:"portable,omitempty"` // 调试用：当前是否判成绿色版
	ExePath   string `json:"exe,omitempty"`      // 调试用：os.Executable() 的结果
	Err       string `json:"err,omitempty"`
	Checked   int64  `json:"checked"`
}

var (
	updateMu     sync.Mutex
	updateCached *updateInfo
	updateAt     time.Time
)

// checkUpdate 查一次最新 tag。结果缓存 30 分钟，避免刷 GitHub 的限流。
func checkUpdate() updateInfo {
	updateMu.Lock()
	if updateCached != nil && time.Since(updateAt) < 30*time.Minute {
		v := *updateCached
		updateMu.Unlock()
		return v
	}
	updateMu.Unlock()

	info := updateInfo{Current: Version, Checked: time.Now().Unix()}
	if exe, err := os.Executable(); err == nil {
		info.ExePath = exe
	}
	info.Portable = isPortable()
	c := &http.Client{Timeout: 8 * time.Second}
	req, _ := http.NewRequest("GET", updateReleaseURL, nil)
	req.Header.Set("User-Agent", "XFlow/"+Version)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := c.Do(req)
	if err != nil {
		info.Err = "连不上 GitHub：" + err.Error()
		return info
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		info.Err = "GitHub 返回 " + strconv.Itoa(resp.StatusCode)
		return info
	}
	var rel struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
		Assets  []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
			Size               int64  `json:"size"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		info.Err = "解析 release 失败"
		return info
	}
	latest := strings.TrimPrefix(strings.TrimSpace(rel.TagName), "v")
	info.Latest = latest
	if latest != "" && versionNewer(latest, Version) {
		info.HasUpdate = true
		info.URL = rel.HTMLURL
		if info.URL == "" {
			info.URL = updatePageURL
		}
		// 按平台 + 安装方式挑资产：
		// 装过安装包的（exe 在 %LOCALAPPDATA%\XFlow）→ Setup.exe；
		// 绿色版（exe 放别处）→ Portable.exe，省得给它塞个安装器。
		portable := isPortable()
		for _, a := range rel.Assets {
			name := strings.ToLower(a.Name)
			switch runtime.GOOS {
			case "windows":
				if portable && strings.Contains(name, "portable") {
					info.Asset, info.AssetName, info.Size = a.BrowserDownloadURL, a.Name, a.Size
				} else if !portable && strings.Contains(name, "setup") {
					info.Asset, info.AssetName, info.Size = a.BrowserDownloadURL, a.Name, a.Size
				}
			case "linux":
				if strings.HasSuffix(name, ".deb") {
					info.Asset, info.AssetName, info.Size = a.BrowserDownloadURL, a.Name, a.Size
				}
			}
			if info.Asset != "" {
				break
			}
		}
		// 兜底：没找到对应类型就用任意一个 Windows 资产
		if info.Asset == "" && runtime.GOOS == "windows" {
			for _, a := range rel.Assets {
				if strings.HasSuffix(strings.ToLower(a.Name), ".exe") {
					info.Asset, info.AssetName, info.Size = a.BrowserDownloadURL, a.Name, a.Size
					break
				}
			}
		}
	}

	updateMu.Lock()
	updateCached = &info
	updateAt = time.Now()
	updateMu.Unlock()
	return info
}

// versionNewer 判断 a 是否比 b 新（按点分数字逐段比，"dev" 视为最旧）。
func versionNewer(a, b string) bool {
	pa, pb := splitVer(a), splitVer(b)
	n := len(pa)
	if len(pb) > n {
		n = len(pb)
	}
	for i := 0; i < n; i++ {
		var x, y int
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		if x != y {
			return x > y
		}
	}
	return false
}

func splitVer(v string) []int {
	v = strings.TrimSpace(strings.TrimPrefix(v, "v"))
	if v == "" || v == "dev" {
		return []int{0}
	}
	// 去掉 -beta 之类后缀
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			n = 0
		}
		out = append(out, n)
	}
	return out
}

// writeJSONErr 给 Windows 侧的 handler 用（Linux 那边直接用 json.NewEncoder）。
func writeJSONErr(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]any{"ok": false, "err": msg})
}

// isPortable 判断当前是不是绿色版：exe 不在安装目录（%LOCALAPPDATA%\XFlow）里就是。
func isPortable() bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	local := os.Getenv("LOCALAPPDATA")
	if local == "" {
		return true
	}
	installed := strings.ToLower(filepath.Join(local, "XFlow")) + string(os.PathSeparator)
	return !strings.HasPrefix(strings.ToLower(exe), installed)
}
