package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var httpClient = &http.Client{
	Timeout: 0,
	Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		MaxIdleConns:          64,
		MaxIdleConnsPerHost:   32,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		DisableCompression:    true,
	},
}

func fileNameFromURL(u string) string {
	// S3 / Cloudflare R2 的预签名直链没有路径文件名，真名塞在 query 里：
	//   ?response-content-disposition=attachment%3B%20filename%3D%22x.iso%22
	// 2026-11-19：massgrave / buzzheavier 这类源全这样，以前不看一下就退回一串 id。
	if n := fileNameFromQuery(u); n != "" {
		return n
	}
	// 去 query
	if i := strings.IndexAny(u, "?#"); i >= 0 {
		u = u[:i]
	}
	base := filepath.Base(u)
	if base == "." || base == "/" || base == "" {
		return "download.bin"
	}
	base = regexp.MustCompile(`[\\/:*?"<>|]`).ReplaceAllString(base, "_")
	return base
}

// fileNameFromQuery 从 URL 的 query 里挖文件名。
// 只认标准的那几个键，免得把 ?name=xx 这类普通参数当真名。
func fileNameFromQuery(raw string) string {
	pu, err := url.Parse(raw)
	if err != nil || pu.RawQuery == "" {
		return ""
	}
	q := pu.Query()
	for _, k := range []string{"response-content-disposition", "content-disposition"} {
		if v := q.Get(k); v != "" {
			if n := filenameFromDisposition(v); n != "" {
				return n
			}
		}
	}
	return ""
}

// filenameFromDisposition 从 Content-Disposition 里取文件名。
// 支持 filename="x.iso" 和 RFC 5987 的 filename*=UTF-8”x.iso。
// 很多直链（Cloudflare R2、网盘中转、buzzheavier）URL 里只有一串 hash，真名藏在这个头里。
func filenameFromDisposition(cd string) string {
	if cd == "" {
		return ""
	}
	lower := strings.ToLower(cd)
	if i := strings.Index(lower, "filename*="); i >= 0 {
		v := cd[i+len("filename*="):]
		if j := strings.IndexByte(v, ';'); j >= 0 {
			v = v[:j]
		}
		v = strings.TrimSpace(strings.Trim(v, `"`))
		if k := strings.Index(v, "''"); k >= 0 {
			v = v[k+2:]
		}
		if dec, err := url.PathUnescape(v); err == nil && dec != "" {
			return safeFileName(dec)
		}
	}
	if i := strings.Index(lower, "filename="); i >= 0 {
		v := cd[i+len("filename="):]
		if j := strings.IndexByte(v, ';'); j >= 0 {
			v = v[:j]
		}
		v = strings.TrimSpace(strings.Trim(v, `"`))
		if v != "" {
			return safeFileName(v)
		}
	}
	return ""
}

func safeFileName(s string) string {
	s = filepath.Base(strings.ReplaceAll(s, "\\", "/"))
	s = regexp.MustCompile(`[:*?"<>|\x00-\x1f]`).ReplaceAllString(s, "_")
	s = strings.TrimSpace(s)
	if s == "" || s == "." || s == ".." {
		return ""
	}
	return s
}

// looksLikeHashName 判断名字是不是一串看不出含义的 id（无扩展名的纯 hex）。
// 是的话优先用 Content-Disposition 给的真名。
func looksLikeHashName(name string) bool {
	if filepath.Ext(name) != "" {
		return false
	}
	if len(name) < 16 {
		return false
	}
	for _, r := range name {
		ok := (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
		if !ok {
			return false
		}
	}
	return true
}

// applyHeaders 把插件转进来的头（Referer / Cookie 等）加到请求上。
// Range 和 User-Agent 属于引擎自己管，不让外部覆盖。
// 两套 UA：SourceForge 会拦以 Mozilla 开头的 UA（直接 403），
// 而大部分正经站点又只认浏览器 UA。按域名分流，403 时再换另一套兜底。
const browserUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

func clientUA() string {
	v := Version
	if v == "" {
		v = "dev"
	}
	return "XFlow/" + v + " (+https://github.com/qingqiuya/xflow)"
}

// userAgentFor：SourceForge 的下载域名明确拦截 Mozilla 前缀的 UA（2026-11-11 实测：
// 同一个链接 Mozilla/5.0 → 403，XFlow/1.6.2 或 curl → 301 → 200）。
// 国内镜像站（清华/中科大/阿里云/华为云等）会拦「声称自己是浏览器」的请求，
// 直接 403 —— 实测同一个 Range 请求：无 UA 或 XFlow UA 返回 206，浏览器 UA 返回 403。
// 所以这些域名也用 clientUA()，别用 browserUA。
var mirrorHosts = []string{
	"tuna.tsinghua.edu.cn", "mirrors.tuna.tsinghua.edu.cn",
	"ustc.edu.cn", "aliyun.com", "huaweicloud.com",
	"mirrors.bfsu.edu.cn", "mirrors.nju.edu.cn", "mirror.sjtu.edu.cn",
	"tencent.com", "doubanio.com",
}

func userAgentFor(rawurl string) string {
	low := strings.ToLower(rawurl)
	// SourceForge 拦的是「Mozilla 前缀」，用 clientUA
	if strings.Contains(low, "sourceforge.net") {
		return clientUA()
	}
	for _, h := range mirrorHosts {
		if strings.Contains(low, h) {
			return clientUA()
		}
	}
	return browserUA
}

func applyHeaders(req *http.Request, j *Job) {
	for k, v := range j.Headers {
		if strings.EqualFold(k, "Range") || strings.EqualFold(k, "User-Agent") || strings.EqualFold(k, "Host") {
			continue
		}
		if v != "" {
			req.Header.Set(k, v)
		}
	}
}

func uniquePath(dir, name string) string {
	p := filepath.Join(dir, name)
	if _, err := os.Stat(p); os.IsNotExist(err) {
		return p
	}
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for i := 1; i < 1000; i++ {
		c := filepath.Join(dir, fmt.Sprintf("%s (%d)%s", base, i, ext))
		if _, err := os.Stat(c); os.IsNotExist(err) {
			return c
		}
	}
	return p
}

const (
	// 同一段连续失败几次就放弃整个任务
	// 2026-11-19：3 → 8。慢速直链（buzzheavier / Cloudflare R2 这类）经常
	// 几十秒不吐数据，3 次就判死太激进，任务动不动就报 error。
	maxRangeAttempts = 8
)

// 一个连接多久没有任何新数据就认定它死了（read 本身可能永远不返回）。
// 用变量而不是常量，方便测试里调小。
// 2026-11-19：30s → 90s，同上，给慢源留出喘气的时间。
var rangeStallTimeout = 90 * time.Second

// withStallWatch 给一次请求加看门狗：超过 timeout 没有任何进展就 cancel。
// 返回的 *atomic.Int64 由调用方每次读到数据时 Store(now)。
func withStallWatch(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc, *atomic.Int64) {
	rctx, cancel := context.WithCancel(ctx)
	var last atomic.Int64
	last.Store(time.Now().UnixNano())
	go func() {
		t := time.NewTicker(timeout / 6)
		defer t.Stop()
		for {
			select {
			case <-rctx.Done():
				return
			case <-t.C:
				if time.Since(time.Unix(0, last.Load())) > timeout {
					cancel()
					return
				}
			}
		}
	}()
	return rctx, cancel, &last
}

func startHTTP(j *Job) {
	ctx := j.ctx
	dir := j.Dir
	if dir == "" {
		dir = defaultDir()
	}
	os.MkdirAll(dir, 0755)
	name := j.Name
	if name == "" {
		name = fileNameFromURL(j.Source)
	}
	finalPath := j.FilePath
	if finalPath == "" {
		finalPath = uniquePath(dir, name)
		j.FilePath = finalPath
	}
	part := finalPath + ".part"

	setState := func(s, e string) {
		j.mu.Lock()
		j.State = s
		j.Err = e
		j.mu.Unlock()
	}
	setState("downloading", "")

	// 探测
	req, err := http.NewRequestWithContext(ctx, "GET", j.Source, nil)
	if err != nil {
		setState("error", err.Error())
		return
	}
	req.Header.Set("User-Agent", userAgentFor(j.Source))
	applyHeaders(req, j)
	resp, err := httpClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			setState("paused", "")
		} else {
			setState("error", humanErr(err))
		}
		return
	}
	// 403 多数是防盗链 / UA 校验：换另一套 UA 重试一次再判死
	if resp.StatusCode == 403 {
		resp.Body.Close()
		alt := browserUA
		if req.Header.Get("User-Agent") == browserUA {
			alt = clientUA()
		}
		if req2, err2 := http.NewRequestWithContext(ctx, "GET", j.Source, nil); err2 == nil {
			req2.Header.Set("User-Agent", alt)
			applyHeaders(req2, j)
			if resp2, err3 := httpClient.Do(req2); err3 == nil {
				resp = resp2
			}
		}
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		setState("error", fmt.Sprintf("HTTP %d（源站拒绝：防盗链或 UA 校验）", resp.StatusCode))
		return
	}

	// 文件名修正：名字看不出类型时，用 Content-Disposition 给的真名。
	// （以前只认 32 位 hex，buzzheavier 那种 11 位 base62（goby1okwv117）就漏了）
	if filepath.Ext(name) == "" {
		if realName := filenameFromDisposition(resp.Header.Get("Content-Disposition")); realName != "" && realName != name {
			if _, serr := os.Stat(part); serr != nil {
				finalPath = uniquePath(dir, realName)
				part = finalPath + ".part"
				j.mu.Lock()
				j.FilePath = finalPath
				j.Name = realName
				j.mu.Unlock()
				name = realName
			}
		}
	}

	size := resp.ContentLength
	supportRange := resp.Header.Get("Accept-Ranges") == "bytes" && size > 0

	f, err := os.OpenFile(part, os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		setState("error", err.Error())
		return
	}

	if !supportRange || size <= 0 || j.SegN == 1 {
		// 单流：服务器不支持 Range、大小未知，或用户/插件指定了单线程
		f.Truncate(0)
		var done int64
		j.mu.Lock()
		j.Total = size
		j.Done = 0
		j.mu.Unlock()
		// 单流也要看门狗：TCP 活着但一个字节不吐时 Read 会永远阻塞
		sctx, scancel, last := withStallWatch(ctx, rangeStallTimeout)
		defer scancel()
		_ = sctx
		buf := make([]byte, 256*1024)
		for {
			n, rerr := resp.Body.Read(buf)
			if n > 0 {
				last.Store(time.Now().UnixNano())
				if _, werr := f.Write(buf[:n]); werr != nil {
					f.Close()
					setState("error", werr.Error())
					return
				}
				done += int64(n)
				j.mu.Lock()
				j.Done = done
				j.mu.Unlock()
			}
			if rerr != nil {
				if errors.Is(rerr, io.EOF) {
					break
				}
				f.Close()
				switch {
				case ctx.Err() != nil:
					setState("paused", "")
				case sctx.Err() != nil:
					setState("error", fmt.Sprintf("%d 秒没收到数据，连接已弃用", int(rangeStallTimeout.Seconds())))
				default:
					setState("error", rerr.Error())
				}
				return
			}
		}
		f.Close()
		finishJob(j, part, finalPath)
		return
	}

	// 多段
	// ===== 自适应 worker 池 =====
	// 文件切成 1MB 的块丢队列，worker 从队列抢；worker 数按实测速度动态增加
	const chunkSize = int64(1 << 20)
	const maxWorkers = 32
	// 起始并发取界面上选的段数（以前这里写死 4，选 16 也不生效）
	startWorkers := j.SegN
	if startWorkers <= 0 {
		startWorkers = 8
	}
	if startWorkers > maxWorkers {
		startWorkers = maxWorkers
	}

	// 续传：读 .part.json 里已完成块的偏移，跳过它们（2026-11-19 补）。
	// 以前只有 .part 没有块状态，resume 时等于从头重下。
	statePath := part + ".json"
	var stateMu sync.Mutex
	doneSet := map[int64]int64{}
	var doneBytes int64
	finished := false
	// 注意：多段下载时 .part 是渐进增长的，大小只在写到最后一块时才等于 size，
	// 所以这里只要求文件存在且非空，真正的校验交给 json 里的 size 字段。
	if st, serr := os.Stat(part); serr == nil && st.Size() > 0 {
		if raw, rerr := os.ReadFile(statePath); rerr == nil {
			var stt struct {
				Size   int64   `json:"size"`
				Chunks []int64 `json:"chunks"`
			}
			if json.Unmarshal(raw, &stt) == nil && stt.Size == size {
				for _, off := range stt.Chunks {
					if off < 0 || off >= size {
						continue
					}
					l := chunkSize
					if off+l > size {
						l = size - off
					}
					doneSet[off] = l
					doneBytes += l
				}
			}
		}
	}
	saveState := func() {
		stateMu.Lock()
		offs := make([]int64, 0, len(doneSet))
		for off := range doneSet {
			offs = append(offs, off)
		}
		stateMu.Unlock()
		sort.Slice(offs, func(a, b int) bool { return offs[a] < offs[b] })
		raw, merr := json.Marshal(struct {
			Size   int64   `json:"size"`
			Chunks []int64 `json:"chunks"`
		}{size, offs})
		if merr != nil {
			return
		}
		tmp := statePath + ".tmp"
		if werr := os.WriteFile(tmp, raw, 0644); werr == nil {
			_ = os.Rename(tmp, statePath)
		}
	}
	markDone := func(off, length int64) {
		stateMu.Lock()
		if _, ok := doneSet[off]; !ok {
			doneSet[off] = length
		}
		stateMu.Unlock()
	}
	defer func() {
		if !finished {
			saveState()
		}
	}()

	ch := make(chan [2]int64, int(size/chunkSize)+2)
	for from := int64(0); from < size; from += chunkSize {
		to := from + chunkSize - 1
		if to >= size {
			to = size - 1
		}
		if _, ok := doneSet[from]; ok {
			continue
		}
		ch <- [2]int64{from, to}
	}
	close(ch)

	j.mu.Lock()
	j.Total = size
	j.Done = doneBytes
	j.Segments = make([]Segment, maxWorkers)
	j.mu.Unlock()

	var (
		wg     sync.WaitGroup
		total  int64
		active int32
		idSeq  int32
	)

	atomic.StoreInt64(&total, doneBytes)
	// 每 5 秒把已完成块落盘，断流/断电都不至于白下
	go func() {
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				saveState()
				return
			case <-t.C:
				saveState()
			}
		}
	}()

	runWorker := func() {
		defer wg.Done()
		defer func() {
			j.mu.Lock()
			j.Workers--
			j.mu.Unlock()
		}()
		idx := int(atomic.AddInt32(&idSeq, 1) - 1)
		if idx >= maxWorkers {
			idx = maxWorkers - 1
		}
		for rng := range ch {
			if ctx.Err() != nil {
				return
			}
			j.mu.Lock()
			j.Segments[idx] = Segment{From: rng[0], To: rng[1]}
			j.mu.Unlock()
			// 一段卡住不能把整个任务拖死：失败就从断点重试，试完就收手、把原因写出去。
			cur := rng[0]
			for attempt := 1; ; attempt++ {
				next, err := downloadRange(ctx, j, f, cur, rng[1], &total, idx)
				cur = next
				if err == nil {
					break
				}
				if ctx.Err() != nil {
					return
				}
				if attempt >= maxRangeAttempts {
					j.mu.Lock()
					j.abortErr = fmt.Sprintf("有一段连续 %d 次拿不到数据（%v），已停下；.part 保留，重试可续传",
						maxRangeAttempts, err)
					j.mu.Unlock()
					if j.cancel != nil {
						j.cancel()
					}
					return
				}
				// 指数退避（0.5s, 1s, 2s, 4s…，封顶 20s），别一失败就猛捶源站
				backoff := time.Duration(1<<uint(attempt)) * 500 * time.Millisecond
				if backoff > 20*time.Second {
					backoff = 20 * time.Second
				}
				time.Sleep(backoff)
			}
			markDone(rng[0], rng[1]-rng[0]+1)
		}
	}

	spawn := func() {
		wg.Add(1)
		atomic.AddInt32(&active, 1)
		j.mu.Lock()
		j.Workers++
		j.mu.Unlock()
		go runWorker()
	}

	n := startWorkers
	if int64(n) > size/chunkSize && size/chunkSize > 0 {
		n = int(size / chunkSize)
		if n < 1 {
			n = 1
		}
	}
	for i := 0; i < n; i++ {
		spawn()
	}

	// 自适应（类似 TCP 慢启动）：每 3 秒**主动**加一批 worker 试水，
	// 下一轮看总速度涨没涨：涨了就继续加，连着两轮没涨就认定吃满了、停手。
	//
	// 之前写的是「等速度自己涨了才加」，但速度只有加了线程才会涨——
	// 判断方向反了，实测 workers 恒等于起始值，等于没自适应（2026-11-10 修）。
	if !j.NoGrow {
		go func() {
			t := time.NewTicker(3 * time.Second)
			defer t.Stop()
			var prev, prevDelta int64
			var probing int32 // 上一轮加了多少个
			stall := 0
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					cur := atomic.LoadInt64(&total)
					delta := cur - prev
					prev = cur
					if cur >= size {
						return
					}
					a := atomic.LoadInt32(&active)
					if a >= maxWorkers {
						return
					}

					if probing > 0 && a > 2 {
						// 上一轮加过线程：看效果。
						// 从 1 个连接起头时前几轮基础太小、速度看不准，先无条件加上去。
						grown := prevDelta > 0 && float64(delta) > float64(prevDelta)*1.05
						if !grown {
							stall++
							if stall >= 2 {
								return // 连着两轮加了没效果，停下
							}
							prevDelta = delta
							probing = 0
							continue
						}
						stall = 0
					}

					// 试探性加一批：从 1 开始，连接越多一轮加得越多
					//（1→2→3→4→6→9→13→19→28→32，约 10 轮到顶）
					add := probing
					if add < 1 {
						add = 1
					}
					if half := atomic.LoadInt32(&active) / 2; half > add {
						add = half
					}
					var spawned int32
					for i := int32(0); i < add && atomic.LoadInt32(&active) < maxWorkers; i++ {
						spawn()
						spawned++
					}
					probing = spawned
					prevDelta = delta
				}
			}
		}()
	}

	wg.Wait()
	f.Close()
	j.mu.Lock()
	abortMsg := j.abortErr
	j.mu.Unlock()
	if abortMsg != "" {
		setState("error", abortMsg)
		return
	}
	if ctx.Err() != nil {
		setState("paused", "")
		return
	}
	select {
	default:
	}
	if atomic.LoadInt64(&total) < size {
		setState("error", "连接提前结束，可能被服务器限流")
		return
	}
	finished = true
	saveState()
	_ = os.Remove(statePath)
	finishJob(j, part, finalPath)
}

func downloadRange(ctx context.Context, j *Job, f *os.File, from, to int64, total *int64, idx int) (int64, error) {
	// 这一段单独一个可取消的 ctx：TCP 连着但一个字节不吐时，Read 可能永远阻塞。
	rctx, rcancel, last := withStallWatch(ctx, rangeStallTimeout)
	defer rcancel()

	req, err := http.NewRequestWithContext(rctx, "GET", j.Source, nil)
	if err != nil {
		return from, err
	}
	req.Header.Set("User-Agent", userAgentFor(j.Source))
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", from, to))
	applyHeaders(req, j)
	resp, err := httpClient.Do(req)
	if err != nil {
		if rctx.Err() != nil && ctx.Err() == nil {
			return from, fmt.Errorf("%d 秒没有任何数据，连接弃用", int(rangeStallTimeout.Seconds()))
		}
		return from, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 206 && resp.StatusCode != 200 {
		return from, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	buf := make([]byte, 256*1024)
	off := from
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			last.Store(time.Now().UnixNano())
			if _, werr := f.WriteAt(buf[:n], off); werr != nil {
				return off, werr
			}
			off += int64(n)
			d := atomic.AddInt64(total, int64(n))
			j.mu.Lock()
			j.Done = d
			if idx >= 0 && idx < len(j.Segments) {
				j.Segments[idx].Done += int64(n)
			}
			j.mu.Unlock()
		}
		if rerr != nil {
			if errors.Is(rerr, io.EOF) {
				return off, nil
			}
			if rctx.Err() != nil && ctx.Err() == nil {
				return off, fmt.Errorf("%d 秒没有任何数据，连接弃用", int(rangeStallTimeout.Seconds()))
			}
			return off, rerr
		}
	}
}

func finishJob(j *Job, part, final string) {
	j.mu.Lock()
	j.Done = j.Total
	j.State = "done"
	j.Rate = 0
	j.mu.Unlock()
	if err := os.Rename(part, final); err != nil {
		j.mu.Lock()
		j.State = "error"
		j.Err = err.Error()
		j.mu.Unlock()
		return
	}
	// 下完了，续传状态就没用了：删掉 part+ ".json"（多段下载的块记录）和它的临时文件。
	// 以前漏了这一步，每个多段任务下完都在下载目录里多留一个 xxx.part.json。
	_ = os.Remove(part + ".json")
	_ = os.Remove(part + ".json.tmp")
	// 名字里看不出类型（没扩展名）时，按文件头补一个 —— 不然下完是个没后缀的裸文件，
	// Windows 双击不认识，人也不知道这是 iso 还是补丁包。
	if filepath.Ext(final) == "" {
		if ext := sniffExt(final); ext != "" {
			newPath := final + ext
			if _, err := os.Stat(newPath); os.IsNotExist(err) {
				if err := os.Rename(final, newPath); err == nil {
					j.mu.Lock()
					j.FilePath = newPath
					j.Name = filepath.Base(newPath)
					j.mu.Unlock()
				}
			}
		}
	}
}

// sniffExt 按文件头猜扩展名。只做最有把握的几种：
// exe(MZ) / dvp(DUMBVER) / iso(偏移 32768 的 CD001) / zip / 7z / rar / gz / pdf。
func sniffExt(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	head := make([]byte, 8)
	n, _ := io.ReadFull(f, head)
	head = head[:n]
	switch {
	case len(head) >= 2 && head[0] == 'M' && head[1] == 'Z':
		return ".exe"
	case len(head) >= 8 && string(head[:8]) == "DUMBVER\x01":
		return ".dvp"
	case len(head) >= 4 && string(head[:4]) == "PK\x03\x04":
		return ".zip"
	case len(head) >= 6 && string(head[:6]) == "7z\xbc\xaf\x27\x1c":
		return ".7z"
	case len(head) >= 4 && string(head[:4]) == "Rar!":
		return ".rar"
	case len(head) >= 3 && head[0] == 0x1f && head[1] == 0x8b && head[2] == 0x08:
		return ".gz"
	case len(head) >= 4 && string(head[:4]) == "%PDF":
		return ".pdf"
	}
	// ISO9660：主卷描述符在扇区 16（32768），开头是 "CD001"
	if fi, err := f.Stat(); err == nil && fi.Size() > 32773 {
		buf := make([]byte, 5)
		if _, err := f.ReadAt(buf, 32768); err == nil && string(buf) == "CD001" {
			return ".iso"
		}
	}
	return ""
}

func humanErr(err error) string {
	s := err.Error()
	switch {
	case strings.Contains(s, "no such host"):
		return "域名解析失败"
	case strings.Contains(s, "connection refused"):
		return "连接被拒绝"
	case strings.Contains(s, "timeout"):
		return "连接超时"
	case strings.Contains(s, "certificate"):
		return "证书错误：" + s
	}
	return s
}
