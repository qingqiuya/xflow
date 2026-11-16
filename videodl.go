package main

// 视频下载：借 yt-dlp 当后端。
//
// 为什么不自己写解析：通用站点（B 站 / YouTube / 推特…）的 API 一直在变，
// yt-dlp 有几万个站在维护。汐流只负责「识别链接 → 起进程 → 把进度接回任务列表」。
// 合并音视频流（DASH）由 yt-dlp 自己调 ffmpeg 做。

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// videoSites 只做「像不像视频站」的粗判，真正决定能不能下的是 yt-dlp。
var videoSites = []string{
	"bilibili.com/video/", "b23.tv/", "youtube.com/watch", "youtu.be/",
	"twitter.com/", "x.com/", "vimeo.com/", "dailymotion.com/",
	"nicovideo.jp/", "douyin.com/", "ixigua.com/", "weibo.com/tv/",
	"twitch.tv/", "tiktok.com/", "instagram.com/",
}

// IsVideoURL 粗判是不是视频页（不是视频直链）。
func IsVideoURL(src string) bool {
	low := strings.ToLower(src)
	if !strings.HasPrefix(low, "http") {
		return false
	}
	for _, s := range videoSites {
		if strings.Contains(low, s) {
			return true
		}
	}
	return false
}

// findYtDlp 找 yt-dlp：先看程序同目录（便携版），再看 PATH。
func findYtDlp() string {
	exe := "yt-dlp"
	if runtime.GOOS == "windows" {
		exe = "yt-dlp.exe"
	}
	if self, err := os.Executable(); err == nil {
		p := filepath.Join(filepath.Dir(self), exe)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	if p, err := exec.LookPath(exe); err == nil {
		return p
	}
	return ""
}

// VideoReady 报告视频下载是否可用（yt-dlp 在不在），给界面提示用。
func VideoReady() (bool, string) {
	y := findYtDlp()
	if y == "" {
		return false, "没找到 yt-dlp（装一个，或把它放到 XFlow 同目录）"
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		if runtime.GOOS == "windows" {
			if _, e2 := exec.LookPath("ffmpeg.exe"); e2 != nil {
				return true, "yt-dlp 有了，但缺 ffmpeg —— 只能下单流（画质/音轨不完整）"
			}
		} else {
			return true, "yt-dlp 有了，但没有 ffmpeg —— DASH 音视频合并不了"
		}
	}
	return true, ""
}

var (
	rePercent = regexp.MustCompile(`(\d{1,3}(?:\.\d+)?)%`)
	reOfSize  = regexp.MustCompile(`of\s+~?\s*([\d.]+)\s*([KMGT]i?B)`)
	reAtRate  = regexp.MustCompile(`at\s+([\d.]+)\s*([KMGT]i?B)/s`)
	reDest    = regexp.MustCompile(`Destination:\s*(.+)$`)
	reMerge   = regexp.MustCompile(`Merging formats into\s+"(.+)"`)
)

func unitToBytes(v float64, unit string) int64 {
	mul := 1.0
	switch strings.ToUpper(strings.TrimSuffix(unit, "B")) {
	case "K", "KI":
		mul = 1024
	case "M", "MI":
		mul = 1024 * 1024
	case "G", "GI":
		mul = 1024 * 1024 * 1024
	case "T", "TI":
		mul = 1024 * 1024 * 1024 * 1024
	}
	return int64(v * mul)
}

// startVideo 跑 yt-dlp，边跑边把进度写回 Job。
func startVideo(j *Job) {
	yt := findYtDlp()
	if yt == "" {
		{ j.State = "error"; j.Err = "没找到 yt-dlp（装一个，或放到 XFlow 同目录）"; return }
		return
	}
	dir := j.Dir
	if dir == "" {
		dir = currentDir()
	}
	os.MkdirAll(dir, 0o755)

	j.State = "downloading"
	outTmpl := filepath.Join(dir, "%(title).150B.%(ext)s")
	args := []string{
		"--no-playlist",
		"--newline",
		"--no-colors",
		"--progress",
		"-o", outTmpl,
		// 有 ffmpeg 就挑最好的画质+音轨再合并；没有就退一张单流
		"-f", "bv*+ba/b",
		"--merge-output-format", "mp4",
		j.Source,
	}

	ctx := j.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	cmd := exec.CommandContext(ctx, yt, args...)
	// Windows 上不弹黑框
	hideWindow(cmd)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		j.State = "error"
		j.Err = "起不了 yt-dlp：" + err.Error()
		return
	}
	cmd.Stderr = cmd.Stdout // 进度有时写 stderr，合并读
	sep := bufio.NewScanner(stdout)
	sep.Buffer(make([]byte, 64<<10), 1<<20)

	var lastFile string
	var lastTotal int64
	go func() {
		for sep.Scan() {
			line := sep.Text()
			if m := reDest.FindStringSubmatch(line); m != nil {
				lastFile = strings.TrimSpace(m[1])
				continue
			}
			if m := reMerge.FindStringSubmatch(line); m != nil {
				lastFile = strings.TrimSpace(m[1])
				j.FilePath = lastFile
				continue
			}
			if !strings.Contains(line, "[download]") {
				continue
			}
			if m := reOfSize.FindStringSubmatch(line); m != nil {
				v, _ := strconv.ParseFloat(m[1], 64)
				lastTotal = unitToBytes(v, m[2])
				j.Total = lastTotal
			}
			if m := rePercent.FindStringSubmatch(line); m != nil {
				p, _ := strconv.ParseFloat(m[1], 64)
				if lastTotal > 0 {
					j.Done = int64(p / 100 * float64(lastTotal))
				}
			}
			if m := reAtRate.FindStringSubmatch(line); m != nil {
				v, _ := strconv.ParseFloat(m[1], 64)
				j.Rate = unitToBytes(v, m[2])
			}
		}
	}()

	start := time.Now()
	if err := cmd.Start(); err != nil {
		j.State = "error"
		j.Err = "起不了 yt-dlp：" + err.Error()
		return
	}
	waitErr := cmd.Wait()
	if ctx.Err() == context.Canceled {
		j.State = "paused"
		return
	}
	if waitErr != nil {
		j.State = "error"
		j.Err = "yt-dlp 失败：" + waitErr.Error()
		return
	}
	// 结束时如果没抓到合并后的路径，就在目录里找最新文件
	if lastFile == "" || !fileExists(lastFile) {
		if f := newestFile(dir, start); f != "" {
			lastFile = f
		}
	}
	j.FilePath = lastFile
	j.Total = maxInt64(j.Done, 1)
	j.Done = j.Total
	j.State = "done"
	fmt.Printf("[视频] %s -> %s\n", j.Source, lastFile)
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func newestFile(dir string, since time.Time) string {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var best string
	var bestT time.Time
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(since.Add(-5 * time.Second)) {
			continue
		}
		if info.ModTime().After(bestT) {
			bestT = info.ModTime()
			best = filepath.Join(dir, e.Name())
		}
	}
	return best
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// AddVideo 建一个视频任务（yt-dlp 后端）。
func (m *Manager) AddVideo(src, name, dir string) *Job {
	if name == "" {
		name = "视频 " + time.Now().Format("01-02 15:04")
	}
	j := &Job{
		ID:      fmt.Sprintf("%d", time.Now().UnixNano()%1e12),
		Kind:    "video",
		Source:  src,
		Name:    name,
		Dir:     dir,
		State:   "queued",
		Created: time.Now(),
	}
	j.ctx, j.cancel = context.WithCancel(context.Background())
	m.mu.Lock()
	m.jobs[j.ID] = j
	m.order = append(m.order, j.ID)
	m.mu.Unlock()
	m.save()
	go func() {
		startVideo(j)
		m.save()
	}()
	return j
}
