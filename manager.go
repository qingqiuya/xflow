package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
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

	SegN     int       `json:"seg_n,omitempty"` // 并发分块数（0=默认）
	Segments []Segment `json:"segments,omitempty"`

	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.Mutex
	lastD  int64
	lastT  time.Time
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
