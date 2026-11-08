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
	segs := j.SegN
	if segs <= 0 {
		// 自动：按文件大小分档
		switch {
		case size < 8<<20:
			segs = 1
		case size < 64<<20:
			segs = 4
		case size < 256<<20:
			segs = 8
		case size < 2<<30:
			segs = 16
		default:
			segs = 32
		}
	}
	if segs > 64 {
		segs = 64
	}
	// 每段至少 1MB，避免小文件开一堆没用的连接
	if int64(segs) > size/1024/1024 && size > 1024*1024 {
		segs = int(size / 1024 / 1024)
	}
	if segs < 1 {
		segs = 1
	}
	if size < 8*1024*1024 { // 小文件单流
		resp.Body.Close()
		req2, _ := http.NewRequestWithContext(ctx, "GET", j.Source, nil)
		req2.Header.Set("User-Agent", "Mozilla/5.0 LocalDL/1.0")
		r2, err := httpClient.Do(req2)
		if err != nil {
			setState("error", humanErr(err))
			return
		}
		defer r2.Body.Close()
		f.Truncate(0)
		var done int64
		j.mu.Lock()
		j.Total = size
		j.mu.Unlock()
		buf := make([]byte, 256*1024)
		for {
			n, rerr := r2.Body.Read(buf)
			if n > 0 {
				f.Write(buf[:n])
				done += int64(n)
				j.mu.Lock()
				j.Done = done
				j.mu.Unlock()
			}
			if rerr != nil {
				if errors.Is(rerr, io.EOF) {
					break
				}
				if ctx.Err() != nil {
					setState("paused", "")
				} else {
					setState("error", rerr.Error())
				}
				f.Close()
				return
			}
		}
		f.Close()
		finishJob(j, part, finalPath)
		return
	}
	resp.Body.Close()

	if err := f.Truncate(size); err != nil {
		f.Close()
		setState("error", err.Error())
		return
	}
	j.mu.Lock()
	j.Total = size
	j.Done = 0
	j.mu.Unlock()

	var wg sync.WaitGroup
	var total int64
	chunk := size / int64(segs)
	errCh := make(chan error, segs)

	j.mu.Lock()
	j.Segments = make([]Segment, segs)
	for i := 0; i < segs; i++ {
		from := int64(i) * chunk
		to := from + chunk - 1
		if i == segs-1 {
			to = size - 1
		}
		j.Segments[i] = Segment{From: from, To: to}
	}
	j.mu.Unlock()

	for i := 0; i < segs; i++ {
		from := int64(i) * chunk
		to := from + chunk - 1
		if i == segs-1 {
			to = size - 1
		}
		wg.Add(1)
		go func(idx int, from, to int64) {
			defer wg.Done()
			if err := downloadRange(ctx, j, f, from, to, &total, idx); err != nil {
				errCh <- err
			}
		}(i, from, to)
	}
	wg.Wait()
	f.Close()
	select {
	case e := <-errCh:
		if ctx.Err() != nil {
			setState("paused", "")
		} else {
			setState("error", humanErr(e))
		}
		return
	default:
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
