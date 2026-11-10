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
		buf := make([]byte, 256*1024)
		for {
			n, rerr := resp.Body.Read(buf)
			if n > 0 {
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
				if ctx.Err() != nil {
					setState("paused", "")
				} else {
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
			if err := downloadRange(ctx, j, f, rng[0], rng[1], &total, idx); err != nil {
				return
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

	// 自适应：每 3 秒看一次总速度，还在涨就加 worker
	adaptDone := make(chan struct{})
	go func() {
		defer close(adaptDone)
		t := time.NewTicker(3 * time.Second)
		defer t.Stop()
		var prev, prevDelta int64
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
				// 比"每轮实际下载量"（等价速度），不是累计量的比例
				growing := prevDelta > 0 && float64(delta) > float64(prevDelta)*1.10
				// ★ 这里原来写的是 defer func(){ prevDelta = delta }()，
				// 在循环里 defer 要等函数返回才执行 ⇒ prevDelta 永远 0，
				// growing 永远 false，自适应等于没写。改成普通赋值。
				prevDelta = delta
				if growing && int(a) < maxWorkers {
					add := int32(a)
					if add < 2 {
						add = 2
					}
					for i := int32(0); i < add && atomic.LoadInt32(&active) < maxWorkers; i++ {
						spawn()
					}
					stall = 0
				} else {
					if prevDelta > 0 {
						stall++
					}
					if stall >= 2 && a >= int32(startWorkers) {
						return // 连续两轮速度没长进，认定链路吃满了
					}
				}
			}
		}
	}()

	wg.Wait()
	f.Close()
	if ctx.Err() != nil {
		setState("paused", "")
		return
	}
	select {
	case <-adaptDone:
	default:
	}
	if atomic.LoadInt64(&total) < size {
		setState("error", "连接提前结束，可能被服务器限流")
		return
	}
	finishJob(j, part, finalPath)
}

func downloadRange(ctx context.Context, j *Job, f *os.File, from, to int64, total *int64, idx int) error {
	req, err := http.NewRequestWithContext(ctx, "GET", j.Source, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 LocalDL/1.0")
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", from, to))
	applyHeaders(req, j)
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 206 && resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	buf := make([]byte, 256*1024)
	off := from
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.WriteAt(buf[:n], off); werr != nil {
				return werr
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
				return nil
			}
			return rerr
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
