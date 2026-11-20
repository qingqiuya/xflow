package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

type Job struct {
	ID       string    `json:"id"`
	Kind     string    `json:"kind"` // http / bt
	Source   string    `json:"source"`
	Name     string    `json:"name"`
	Dir      string    `json:"dir"`
	Total    int64     `json:"total"`
	Done     int64     `json:"done"`
	State    string    `json:"state"` // queued downloading paused done error
	Err      string    `json:"err"`
	Created  time.Time `json:"created"`
	Rate     int64     `json:"rate"`
	FilePath string    `json:"file_path"`

	SegN int `json:"seg_n,omitempty"` // 分块并发：1=单线程（不分段）、2..32=固定、0=默认
	// NoGrow 为真时不做自适应增长（固定并发和单线程都置它）。
	// 为什么需要：自适应对下大文件是对的，但小文件或已知快源，
	// 每 3 秒试探多开连接纯是浪费。
	NoGrow   bool      `json:"no_grow,omitempty"`
	Workers  int       `json:"workers,omitempty"` // 当前活跃连接数（自适应）
	Segments []Segment `json:"segments,omitempty"`

	// Headers 是外部（浏览器插件）转进来的请求头：Referer / Cookie 之类。
	// 不带这些，需要登录的站点下回来的是一个 403 页面。
	Headers map[string]string `json:"headers,omitempty"`

	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.Mutex
	lastD  int64
	lastT  time.Time

	// desired 是运行中可调的目标并发数（界面上拖滑块就改它），
	// growGen 每次切「自动/手动」加一，自适应循环看到它变了就重置慢启动状态。
	desired int32
	growGen int32

	// abortErr 非空表示不是因为用户取消才停的（比如某段反复拿不到数据）。
	// 主循环见到它就报 error 而不是 paused。
	abortErr string
}

type Segment struct {
	From int64 `json:"from"`
	To   int64 `json:"to"`
	Done int64 `json:"done"`
}

type Manager struct {
	mu     sync.RWMutex
	jobs   map[string]*Job
	order  []string
	stateF string
	saveMu sync.Mutex
}

func NewManager(statePath string) *Manager {
	m := &Manager{jobs: map[string]*Job{}, stateF: statePath}
	m.load()
	return m
}

func (m *Manager) load() {
	b, err := os.ReadFile(m.stateF)
	if err != nil {
		return
	}
	var list []*Job
	if json.Unmarshal(b, &list) != nil {
		return
	}
	for _, j := range list {
		if j.State == "downloading" {
			j.State = "paused"
		}
		if j.State == "queued" {
			j.State = "paused"
		}
		m.jobs[j.ID] = j
		m.order = append(m.order, j.ID)
	}
}

func (m *Manager) save() {
	m.saveMu.Lock()
	defer m.saveMu.Unlock()
	m.mu.RLock()
	list := make([]*Job, 0, len(m.order))
	for _, id := range m.order {
		if j := m.jobs[id]; j != nil {
			c := *j
			c.ctx, c.cancel = nil, nil
			list = append(list, &c)
		}
	}
	m.mu.RUnlock()
	b, _ := json.MarshalIndent(list, "", "  ")
	tmp := m.stateF + ".tmp"
	os.WriteFile(tmp, b, 0600)
	os.Rename(tmp, m.stateF)
}

func (m *Manager) AddHTTP(url, name, dir string, segN int) (*Job, error) {
	return m.AddHTTPWithHeaders(url, name, dir, segN, nil)
}

// AddHTTPWithHeaders 比 AddHTTP 多一组请求头（浏览器插件接管下载时用）。
func (m *Manager) AddHTTPWithHeaders(url, name, dir string, segN int, headers map[string]string) (*Job, error) {
	return m.AddHTTPEx(url, name, dir, segN, headers, false)
}

// AddHTTPEx 在 AddHTTPWithHeaders 之上再多一个「不自动增长」开关。
// segN=1 表示单线程（直连接收整个响应，不分段）。
func (m *Manager) AddHTTPEx(url, name, dir string, segN int, headers map[string]string, noGrow bool) (*Job, error) {
	if segN <= 0 {
		segN = 16
	}
	if segN > 64 {
		segN = 64
	}
	if name == "" {
		name = "download.bin"
	}
	j := &Job{
		ID:      fmt.Sprintf("%d", time.Now().UnixNano()%1e12),
		Kind:    "http",
		Source:  url,
		Name:    name,
		Dir:     dir,
		State:   "queued",
		SegN:    segN,
		NoGrow:  noGrow,
		Headers: headers,
		Created: time.Now(),
	}
	j.ctx, j.cancel = context.WithCancel(context.Background())
	m.mu.Lock()
	m.jobs[j.ID] = j
	m.order = append(m.order, j.ID)
	m.mu.Unlock()
	m.save()
	go func() {
		startHTTP(j)
		m.save()
	}()
	return j, nil
}

func (m *Manager) AddBT(src, name, dir string) *Job {
	j := &Job{
		ID:      fmt.Sprintf("%d", time.Now().UnixNano()%1e12),
		Kind:    "bt",
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
		startBT(j)
		m.save()
	}()
	return j
}

func (m *Manager) List() []*Job {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*Job, 0, len(m.order))
	for _, id := range m.order {
		if j := m.jobs[id]; j != nil {
			c := *j
			c.ctx, c.cancel = nil, nil
			out = append(out, &c)
		}
	}
	return out
}

func (m *Manager) Get(id string) *Job {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.jobs[id]
}

// SetThreads 在下载过程中调线程数：segs 1..32，noGrow=false 表示自动增长
// （此时 segs 是上限，从 1 个连接慢启动往上探）。
func (m *Manager) SetThreads(id string, segs int, noGrow bool) error {
	j := m.Get(id)
	if j == nil {
		return fmt.Errorf("没有这个任务")
	}
	if segs < 1 {
		segs = 1
	}
	if segs > 32 {
		segs = 32
	}
	j.mu.Lock()
	wasNoGrow := j.NoGrow
	j.SegN = segs
	j.NoGrow = noGrow
	j.mu.Unlock()

	if noGrow {
		atomic.StoreInt32(&j.desired, int32(segs))
	} else if wasNoGrow {
		// 从手动切回自动：重新从 1 个连接慢启动
		atomic.StoreInt32(&j.desired, 1)
		atomic.AddInt32(&j.growGen, 1)
	} else if d := atomic.LoadInt32(&j.desired); d > int32(segs) {
		// 自动模式下调低上限：多余的连接会在下一块下载完后退场
		atomic.StoreInt32(&j.desired, int32(segs))
	}
	m.save()
	return nil
}

func (m *Manager) Control(id, act string) error {
	j := m.Get(id)
	if j == nil {
		return fmt.Errorf("没有这个任务")
	}
	switch act {
	case "pause", "cancel":
		if j.cancel != nil {
			j.cancel()
		}
		j.State = "paused"
	case "resume":
		if j.State == "downloading" {
			return nil
		}
		j.ctx, j.cancel = context.WithCancel(context.Background())
		j.State = "queued"
		j.Err = ""
		go func() {
			if j.Kind == "http" {
				startHTTP(j)
			} else {
				startBT(j)
			}
			m.save()
		}()
	}
	m.save()
	return nil
}

func (m *Manager) Remove(id string) {
	j := m.Get(id)
	if j != nil && j.cancel != nil {
		j.cancel()
	}
	// 取消后把没下完的临时文件一起清掉：只删 .part，已完成的正式文件不动。
	// BT 任务的文件句柄要等 t.Drop() 走完才真正关闭，所以先等一会儿再删；
	// 还删不掉（anacrolix 的 fileTorrentImpl.Close 是空实现，句柄要等进程退出才释放）
	// 就记账，留给下次启动清理（见 cleanup.go）。
	if j != nil && j.State != "done" && j.FilePath != "" {
		part := j.FilePath + ".part"
		go func() {
			for i := 0; i < 60; i++ {
				time.Sleep(150 * time.Millisecond)
				if err := os.Remove(part); err == nil || os.IsNotExist(err) {
					os.Remove(part + ".meta")
					return
				}
			}
			addPendingDelete(part)
		}()
	}
	m.mu.Lock()
	delete(m.jobs, id)
	for i, v := range m.order {
		if v == id {
			m.order = append(m.order[:i], m.order[i+1:]...)
			break
		}
	}
	m.mu.Unlock()
	m.save()
}

func (m *Manager) ClearDone() {
	m.mu.Lock()
	for id, j := range m.jobs {
		if j.State == "done" || j.State == "error" {
			delete(m.jobs, id)
			for i, v := range m.order {
				if v == id {
					m.order = append(m.order[:i], m.order[i+1:]...)
					break
				}
			}
		}
	}
	m.mu.Unlock()
	m.save()
}

// 每秒刷新速度
func (m *Manager) StartTicker() {
	go func() {
		t := time.NewTicker(time.Second)
		for range t.C {
			m.mu.RLock()
			for _, j := range m.jobs {
				j.mu.Lock()
				now := time.Now()
				if !j.lastT.IsZero() {
					dt := now.Sub(j.lastT).Seconds()
					if dt > 0 {
						j.Rate = int64(float64(j.Done-j.lastD) / dt)
					}
				}
				j.lastD = j.Done
				j.lastT = now
				j.mu.Unlock()
			}
			m.mu.RUnlock()
		}
	}()
}

func defaultDir() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	d := filepath.Join(h, "Downloads")
	os.MkdirAll(d, 0755)
	return d
}
