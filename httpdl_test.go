package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 卡住的连接必须被看门狗掐掉，否则 worker 永远不退出、整个任务假死。
func TestDownloadRangeWatchdog(t *testing.T) {
	old := rangeStallTimeout
	rangeStallTimeout = 300 * time.Millisecond
	defer func() { rangeStallTimeout = old }()

	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Range", "bytes 0-1023/1024")
		w.Header().Set("Content-Length", "1024")
		w.WriteHeader(206)
		_, _ = w.Write([]byte("x"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		// 发了一个字节就卡住，不再吐数据也不断开 —— 模拟 TCP 活着但不干活
		<-block
	}))
	defer srv.Close()
	defer close(block)

	dir := t.TempDir()
	f, err := os.OpenFile(filepath.Join(dir, "t.part"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	job := &Job{Source: srv.URL + "/x.bin"}
	job.ctx = ctx

	var total int64
	start := time.Now()
	off, err := downloadRange(ctx, job, f, 0, 1023, &total, 0)
	spent := time.Since(start)

	if err == nil {
		t.Fatalf("卡住的连接应该报错，实际返回 off=%d err=nil", off)
	}
	if spent > 5*time.Second {
		t.Fatalf("看门狗没生效：等了 %v", spent)
	}
	t.Logf("按预期 %v 后放弃，已收 %d 字节，错误：%v", spent.Round(time.Millisecond), off, err)
}

// 连续失败到上限后，任务状态应该是 error 而不是永远 downloading。
func TestRangeFailuresBecomeError(t *testing.T) {
	old := rangeStallTimeout
	rangeStallTimeout = 150 * time.Millisecond
	defer func() { rangeStallTimeout = old }()

	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Accept-Ranges", "bytes")
		if r.Header.Get("Range") == "" {
			w.Header().Set("Content-Length", "1024")
			w.WriteHeader(200)
			_, _ = w.Write(make([]byte, 1024))
			return
		}
		w.Header().Set("Content-Range", "bytes 0-1023/1024")
		w.Header().Set("Content-Length", "1024")
		w.WriteHeader(206)
		<-block // 永远不吐数据
	}))
	defer srv.Close()
	defer close(block) // 必须在 srv.Close 之后执行，否则 Close 会一直等这些连接

	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	job := &Job{
		ID:     "t1",
		Source: srv.URL + "/big.bin",
		Name:   "big.bin",
		Dir:    dir,
		SegN:   2,
	}
	job.ctx, job.cancel = ctx, cancel

	done := make(chan struct{})
	go func() {
		startHTTP(job)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("任务没有停下（假死）")
	}

	job.mu.Lock()
	state, msg := job.State, job.Err
	job.mu.Unlock()
	if state != "error" {
		t.Fatalf("期望 error，实际 %q（err=%q）", state, msg)
	}
	if msg == "" {
		t.Fatal("error 状态要带原因")
	}
	t.Logf("按预期停下：state=%s err=%s", state, msg)
}

// SourceForge 会拦 Mozilla 前缀的 UA（403），其余站点走浏览器 UA。
func TestUserAgentForSourceForge(t *testing.T) {
	sf := "https://downloads.sourceforge.net/project/xxx/a.zip"
	if ua := userAgentFor(sf); strings.HasPrefix(ua, "Mozilla") {
		t.Fatalf("SourceForge 不该用浏览器 UA: %s", ua)
	}
	if ua := userAgentFor("https://example.com/a.zip"); !strings.HasPrefix(ua, "Mozilla") {
		t.Fatalf("普通站点该用浏览器 UA: %s", ua)
	}
}

// R2/S3 预签名直链把真名放在 query 里，2026-11-19 之前完全没解析。
func TestFileNameFromQuery(t *testing.T) {
	u := "https://edge2.abc.r2.cloudflarestorage.com/oNq/9e66425f" +
		"?response-content-disposition=attachment%3B%20filename%3D%22cn_windows_7_ultimate_with_sp1_x64_dvd_u_677408.dvp%22" +
		"&X-Amz-Signature=abc"
	if got := fileNameFromURL(u); got != "cn_windows_7_ultimate_with_sp1_x64_dvd_u_677408.dvp" {
		t.Fatalf("从 query 取真名失败，得到 %q", got)
	}
	// 没有 filename 参数的（buzzheavier）还是退回路径末段
	if got := fileNameFromURL("https://ts.buzzheavier.com/d/goby1okwv117?v=xyz"); got != "goby1okwv117" {
		t.Fatalf("期望 goby1okwv117，得到 %q", got)
	}
}

func TestSniffExt(t *testing.T) {
	dir := t.TempDir()
	iso := filepath.Join(dir, "a")
	buf := make([]byte, 40000)
	copy(buf[32768:], "CD001")
	os.WriteFile(iso, buf, 0644)
	if got := sniffExt(iso); got != ".iso" {
		t.Fatalf("iso 没认出来，得到 %q", got)
	}
	dvp := filepath.Join(dir, "b")
	os.WriteFile(dvp, append([]byte("DUMBVER\x01"), make([]byte, 100)...), 0644)
	if got := sniffExt(dvp); got != ".dvp" {
		t.Fatalf("dvp 没认出来，得到 %q", got)
	}
	exe := filepath.Join(dir, "c")
	os.WriteFile(exe, []byte("MZ\x90\x00"), 0644)
	if got := sniffExt(exe); got != ".exe" {
		t.Fatalf("exe 没认出来，得到 %q", got)
	}
}
