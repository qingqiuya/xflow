package main

// B 站专用解析（不走 yt-dlp）。
//
// 为什么单独做：yt-dlp 会挑到 mcdn（B 站的 P2P CDN）那个节点，时不时超时。
// B 站自己的 playurl 接口其实一次给了多个候选（baseUrl + 2 个 backupUrl），
// 全是签好的、直接能用 —— 只要挨个探一下挑快的就行（BiliDownload 也是这么干的）。
//
// 流程：BV 号 → view 拿 cid/标题 → playurl(fnval=16) 拿 dash → 挑流 → 探测候选
//      → 视频流 + 音频流分别下载 → ffmpeg -c copy 合并 → 删临时文件。

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const biliUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

// 大小写都认（小写 bv 号也常见：手机上复制出来常被自动小写）；前缀统一还原成 BV
var reBV = regexp.MustCompile(`(?i)(bv[0-9A-Za-z]{10})`)

func biliClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DialContext:           (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
			TLSClientConfig:       &tls.Config{InsecureSkipVerify: false},
			MaxIdleConnsPerHost:   4,
			ResponseHeaderTimeout: 15 * time.Second,
		},
	}
}

func biliGetJSON(url string, out any) error {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", biliUA)
	req.Header.Set("Referer", "https://www.bilibili.com/")
	if ck := BiliCookie(); ck != "" {
		req.Header.Set("Cookie", ck)
	}
	resp, err := biliClient(20 * time.Second).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	return json.Unmarshal(body, out)
}

// IsBiliURL 判断是不是 B 站视频页（也认直接贴进来的 BV 号）。
func IsBiliURL(src string) bool {
	low := strings.ToLower(strings.TrimSpace(src))
	if strings.Contains(low, "bilibili.com/video/") || strings.Contains(low, "b23.tv/") {
		return true
	}
	// 纯 BV 号：BV1GJ411x7h7 / bv1gj411x7h7
	if len(low) == 12 && strings.HasPrefix(low, "bv") {
		for _, c := range low[2:] {
			if (c < '0' || c > '9') && (c < 'a' || c > 'z') {
				return false
			}
		}
		return true
	}
	return false
}

// resolveBvid 从各种 B 站链接里抠出 BV 号（b23.tv 短链先跟一次跳转）。
func resolveBvid(src string) string {
	if m := reBV.FindString(src); m != "" {
		return "BV" + m[2:]
	}
	if strings.Contains(strings.ToLower(src), "b23.tv/") {
		req, err := http.NewRequest("GET", src, nil)
		if err != nil {
			return ""
		}
		req.Header.Set("User-Agent", biliUA)
		c := biliClient(15 * time.Second)
		c.CheckRedirect = func(r *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }
		resp, err := c.Do(req)
		if err != nil {
			return ""
		}
		defer resp.Body.Close()
		if m := reBV.FindString(resp.Header.Get("Location")); m != "" {
			return "BV" + m[2:]
		}
	}
	return ""
}

type biliStream struct {
	ID      int      `json:"id"`
	BaseURL string   `json:"baseUrl"`
	Backup  []string `json:"backupUrl"`
	Codecs  string   `json:"codecs"`
	Width   int      `json:"width"`
	Height  int      `json:"height"`
	Bandw   int      `json:"bandwidth"`
}

func (s biliStream) candidates() []string {
	out := []string{}
	if s.BaseURL != "" {
		out = append(out, s.BaseURL)
	}
	for _, u := range s.Backup {
		if u != "" {
			out = append(out, u)
		}
	}
	return out
}

// probeFastest 并发探一遍候选，返回响应最快的那个（Range 请求，1 字节）。
func probeFastest(urls []string) string {
	if len(urls) == 0 {
		return ""
	}
	if len(urls) == 1 {
		return urls[0]
	}
	type res struct {
		url string
		ms  int64
		ok  bool
	}
	ch := make(chan res, len(urls))
	for _, u := range urls {
		go func(u string) {
			start := time.Now()
			req, err := http.NewRequest("GET", u, nil)
			if err != nil {
				ch <- res{u, 1 << 30, false}
				return
			}
			req.Header.Set("User-Agent", biliUA)
			req.Header.Set("Referer", "https://www.bilibili.com/")
			req.Header.Set("Range", "bytes=0-1")
			c := biliClient(6 * time.Second)
			resp, err := c.Do(req)
			if err != nil {
				ch <- res{u, 1 << 30, false}
				return
			}
			io.Copy(io.Discard, io.LimitReader(resp.Body, 64))
			resp.Body.Close()
			ok := resp.StatusCode == 200 || resp.StatusCode == 206
			ch <- res{u, time.Since(start).Milliseconds(), ok}
		}(u)
	}
	var best res
	best.ms = 1 << 30
	for i := 0; i < len(urls); i++ {
		r := <-ch
		if r.ok && r.ms < best.ms {
			best = r
		}
	}
	if best.url == "" {
		return urls[0] // 都探不通就用第一个碰运气
	}
	return best.url
}

// startBili 是 B 站视频任务的主体。
func startBili(j *Job) {
	j.State = "downloading"
	bvid := resolveBvid(j.Source)
	if bvid == "" {
		j.State = "error"
		j.Err = "认不出 BV 号"
		return
	}

	var view struct {
		Code int `json:"code"`
		Msg  string `json:"message"`
		Data struct {
			Title string `json:"title"`
			Cid   int64  `json:"cid"`
		} `json:"data"`
	}
	if err := biliGetJSON("https://api.bilibili.com/x/web-interface/view?bvid="+bvid, &view); err != nil {
		j.State = "error"
		j.Err = "取视频信息失败：" + err.Error()
		return
	}
	if view.Code != 0 {
		j.State = "error"
		j.Err = fmt.Sprintf("B 站返回 %d：%s", view.Code, view.Msg)
		return
	}
	title := strings.TrimSpace(view.Data.Title)
	if title == "" {
		title = bvid
	}
	j.Name = title
	cid := view.Data.Cid

	var pu struct {
		Code int `json:"code"`
		Msg  string `json:"message"`
		Data struct {
			Dash struct {
				Video []biliStream `json:"video"`
				Audio []biliStream `json:"audio"`
			} `json:"dash"`
		} `json:"data"`
	}
	u := fmt.Sprintf("https://api.bilibili.com/x/player/playurl?bvid=%s&cid=%d&qn=112&fnval=16&fourk=1", bvid, cid)
	if err := biliGetJSON(u, &pu); err != nil {
		j.State = "error"
		j.Err = "取播放地址失败：" + err.Error()
		return
	}
	if pu.Code != 0 {
		j.State = "error"
		j.Err = fmt.Sprintf("playurl 返回 %d：%s", pu.Code, pu.Msg)
		return
	}
	if len(pu.Data.Dash.Video) == 0 || len(pu.Data.Dash.Audio) == 0 {
		j.State = "error"
		j.Err = "拿不到 dash 流（可能要登录/Cookie）"
		return
	}

	// 视频：优先 avc（兼容性最好），同编码取分辨率最高的
	videos := pu.Data.Dash.Video
	sort.Slice(videos, func(a, b int) bool {
		ca, cb := codecRank(videos[a].Codecs), codecRank(videos[b].Codecs)
		if ca != cb {
			return ca < cb
		}
		return videos[a].Bandw > videos[b].Bandw
	})
	// 音频：带宽最高的
	audios := pu.Data.Dash.Audio
	sort.Slice(audios, func(a, b int) bool { return audios[a].Bandw > audios[b].Bandw })

	v := videos[0]
	a := audios[0]

	dir := j.Dir
	if dir == "" {
		dir = currentDir()
	}
	os.MkdirAll(dir, 0o755)
	safe := sanitizeFileName(title)
	vPath := filepath.Join(dir, safe+".video.m4s")
	aPath := filepath.Join(dir, safe+".audio.m4s")
	outPath := filepath.Join(dir, safe+".mp4")
	defer os.Remove(vPath)
	defer os.Remove(aPath)

	vURL := probeFastest(v.candidates())
	aURL := probeFastest(a.candidates())

	// 先把总大小探出来（进度条要用）
	vSize := contentLength(vURL)
	aSize := contentLength(aURL)
	if vSize > 0 && aSize > 0 {
		j.Total = vSize + aSize
	}

	var done int64
	var mu sync.Mutex
	add := func(n int64) {
		mu.Lock()
		done += n
		j.Done = done
		mu.Unlock()
	}

	if err := downloadTo(vURL, vPath, add); err != nil {
		j.State = "error"
		j.Err = "视频流下载失败：" + err.Error()
		return
	}
	if err := downloadTo(aURL, aPath, add); err != nil {
		j.State = "error"
		j.Err = "音频流下载失败：" + err.Error()
		return
	}

	j.State = "merging"
	if err := mergeAV(vPath, aPath, outPath); err != nil {
		j.Err = "合并失败（要 ffmpeg）：" + err.Error()
		j.State = "error"
		return
	}
	if st, err := os.Stat(outPath); err == nil {
		j.Total = st.Size()
		j.Done = st.Size()
	}
	j.FilePath = outPath
	j.State = "done"
	fmt.Printf("[B站] %s -> %s\n", bvid, outPath)
}

func codecRank(c string) int {
	c = strings.ToLower(c)
	switch {
	case strings.HasPrefix(c, "avc"):
		return 0 // 兼容性最好
	case strings.HasPrefix(c, "hev"), strings.HasPrefix(c, "hvc"):
		return 1
	case strings.HasPrefix(c, "av01"):
		return 2
	}
	return 3
}

func sanitizeFileName(s string) string {
	rep := strings.NewReplacer("/", "_", "\\", "_", ":", "_", "*", "_", "?", "_",
		"\"", "_", "<", "_", ">", "_", "|", "_", "\n", " ", "\r", " ")
	out := strings.TrimSpace(rep.Replace(s))
	if len([]rune(out)) > 100 {
		out = string([]rune(out)[:100])
	}
	if out == "" {
		out = "bili-video"
	}
	return out
}

func contentLength(url string) int64 {
	req, _ := http.NewRequest("GET", url, nil)
	if req == nil {
		return 0
	}
	req.Header.Set("User-Agent", biliUA)
	req.Header.Set("Referer", "https://www.bilibili.com/")
	req.Header.Set("Range", "bytes=0-0")
	resp, err := biliClient(8 * time.Second).Do(req)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	if cr := resp.Header.Get("Content-Range"); cr != "" {
		if i := strings.LastIndex(cr, "/"); i > 0 {
			var n int64
			fmt.Sscanf(cr[i+1:], "%d", &n)
			return n
		}
	}
	return resp.ContentLength
}

func downloadTo(url, dst string, onChunk func(int64)) error {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", biliUA)
	req.Header.Set("Referer", "https://www.bilibili.com/")
	resp, err := biliClient(0).Do(req) // 0 = 不设总超时
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 && resp.StatusCode != 206 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()
	buf := make([]byte, 256*1024)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				return werr
			}
			onChunk(int64(n))
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func mergeAV(vPath, aPath, outPath string) error {
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		return fmt.Errorf("没找到 ffmpeg")
	}
	args := []string{
		"-y", "-loglevel", "error",
		"-i", vPath, "-i", aPath,
		"-c", "copy", "-movflags", "+faststart",
		outPath,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, ff, args...)
	hideWindow(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// AddBili 建一个 B 站视频任务（自己解析，不走 yt-dlp）。
func (m *Manager) AddBili(src, name, dir string) *Job {
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
		startBili(j)
		m.save()
	}()
	return j
}
