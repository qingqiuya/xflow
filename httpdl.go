package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
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

// applyHeaders 把插件转进来的头（Referer / Cookie 等）加到请求上。
// Range 和 User-Agent 属于引擎自己管，不让外部覆盖。
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
	maxRangeAttempts = 3
)

// 一个连接多久没有任何新数据就认定它死了（read 本身可能永远不返回）。
// 用变量而不是常量，方便测试里调小。
var rangeStallTimeout = 30 * time.Second

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
	req.Header.Set("User-Agent", "Mozilla/5.0 LocalDL/1.0")
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
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		setState("error", fmt.Sprintf("HTTP %d", resp.StatusCode))
		return
	}

	size := resp.ContentLength
	supportRange := resp.Header.Get("Accept-Ranges") == "bytes" && size > 0

	f, err := os.OpenFile(part, os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		setState("error", err.Error())
		return
	}

	if !supportRange || size <= 0 {
		// 单流
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

	ch := make(chan [2]int64, (size/chunkSize)+2)
	for from := int64(0); from < size; from += chunkSize {
		to := from + chunkSize - 1
		if to >= size {
			to = size - 1
		}
		ch <- [2]int64{from, to}
	}
	close(ch)

	j.mu.Lock()
	j.Total = size
	j.Done = 0
	j.Segments = make([]Segment, maxWorkers)
	j.mu.Unlock()

	var (
		wg     sync.WaitGroup
		total  int64
		active int32
		idSeq  int32
	)

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
				time.Sleep(time.Duration(attempt) * 700 * time.Millisecond)
			}
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

				if probing > 0 {
					// 上一轮加过线程：看效果
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

				// 试探性加一批：不低于 2 个，最多加到上限
				add := probing
				if add < 2 {
					add = 2
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
	req.Header.Set("User-Agent", "Mozilla/5.0 LocalDL/1.0")
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
	}
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
