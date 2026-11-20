package main

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// 浏览器接管插件的「一键引导」。
//
// 为什么需要这个：扩展必须由用户在扩展页手动「加载已解压的扩展程序」——
// Chromium 从 2025 年起不接受商店外的 crx，也不允许第三方软件静默安装
// （注册表外部扩展 / force-list 都会被判「不受信任」停用）。
// 所以能做的只有：把目录扒拉给他、把浏览器扩展页打开，剩下一指点用户点。
//
// 界面右上角「安装浏览器插件」按钮 → 引导页 → 调这里。

// extDirPath 返回安装目录下的 ext（浏览器插件目录）；找不到返回空串。
func extDirPath() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	p := filepath.Join(filepath.Dir(exe), "ext")
	if st, e := os.Stat(p); e == nil && st.IsDir() {
		return p
	}
	return ""
}

// openPath 用系统默认方式打开一个目录（Windows 是资源管理器）。
func openPath(dir string) bool {
	if dir == "" {
		return false
	}
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("explorer", dir)
	} else {
		cmd = exec.Command("xdg-open", dir)
	}
	return cmd.Start() == nil
}

// openBrowserExtensions 打开 Edge/Chrome 的扩展管理页，返回打开了哪个（空串=没找到）。
func openBrowserExtensions() string {
	if runtime.GOOS == "windows" {
		cands := []struct{ exe, url string }{
			{`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`, "edge://extensions"},
			{`C:\Program Files\Microsoft\Edge\Application\msedge.exe`, "edge://extensions"},
			{`C:\Program Files\Google\Chrome\Application\chrome.exe`, "chrome://extensions"},
			{`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`, "chrome://extensions"},
		}
		for _, c := range cands {
			if _, err := os.Stat(c.exe); err != nil {
				continue
			}
			cmd := exec.Command(c.exe, "--new-window", c.url)
			if cmd.Start() == nil {
				return filepath.Base(c.exe) + " " + c.url
			}
		}
		return ""
	}
	for _, b := range []string{"chromium", "chromium-browser", "google-chrome", "microsoft-edge"} {
		if _, err := exec.LookPath(b); err != nil {
			continue
		}
		cmd := exec.Command(b, "--new-window", "chrome://extensions")
		if cmd.Start() == nil {
			return b + " chrome://extensions"
		}
	}
	return ""
}

// registerInstallExt 挂上 /api/installext：打开插件目录 + 浏览器扩展页。
// 前端引导页点「开始安装」时调它；只查目录（?open=0）时不弹任何窗口。
func registerInstallExt(mux *http.ServeMux) {
	mux.HandleFunc("/api/installext", func(w http.ResponseWriter, r *http.Request) {
		dir := extDirPath()
		opened := false
		browser := ""
		if r.URL.Query().Get("open") != "0" {
			opened = openPath(dir)
			browser = openBrowserExtensions()
		}
		json.NewEncoder(w).Encode(map[string]any{
			"ok":      dir != "",
			"dir":     dir,
			"opened":  opened,
			"browser": browser,
		})
	})
}
