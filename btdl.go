package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/anacrolix/torrent"
)

var (
	btOnce   sync.Once
	btClient *torrent.Client
	btErr    error
	btTors   = map[string]*torrent.Torrent{}
	btMu     sync.Mutex
)

func ensureBT(dir string) error {
	btOnce.Do(func() {
		cfg := torrent.NewDefaultClientConfig()
		cfg.DataDir = dir
		cfg.Seed = false
		os.MkdirAll(dir, 0755)
		btClient, btErr = torrent.NewClient(cfg)
	})
	return btErr
}

func startBT(j *Job) {
	dir := j.Dir
	if dir == "" {
		dir = currentDir()
	}
	if err := ensureBT(dir); err != nil {
		j.mu.Lock()
		j.State, j.Err = "error", err.Error()
		j.mu.Unlock()
		return
	}
	j.mu.Lock()
	j.State, j.Err = "downloading", ""
	j.mu.Unlock()

	src := j.Source
	btMu.Lock()
	t := btTors[j.ID]
	btMu.Unlock()

	if t == nil {
		var err error
		switch {
		case len(src) > 8 && src[:8] == "magnet:?":
			t, err = btClient.AddMagnet(src)
		case len(src) > 4 && src[:4] == "http":
			t, err = btClient.AddTorrentFromFile(src)
		default:
			err = fmt.Errorf("BT 源格式不认识")
		}
		if err != nil {
			j.mu.Lock()
			j.State, j.Err = "error", err.Error()
			j.mu.Unlock()
			return
		}
		btMu.Lock()
		btTors[j.ID] = t
		btMu.Unlock()
	}

	select {
	case <-t.GotInfo():
	case <-time.After(3 * time.Minute):
		j.mu.Lock()
		j.State, j.Err = "error", "3 分钟内没拿到种子信息"
		j.mu.Unlock()
		return
	case <-j.ctx.Done():
		j.mu.Lock()
		j.State = "paused"
		j.mu.Unlock()
		return
	}

	info := t.Info()
	j.mu.Lock()
	j.Name = info.Name
	j.Total = info.TotalLength()
	j.FilePath = filepath.Join(dir, info.Name)
	j.mu.Unlock()

	t.DownloadAll()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-j.ctx.Done():
			t.Drop()
			btMu.Lock()
			delete(btTors, j.ID)
			btMu.Unlock()
			j.mu.Lock()
			if j.State != "error" {
				j.State = "paused"
			}
			j.mu.Unlock()
			return
		case <-tick.C:
			done := t.BytesCompleted()
			j.mu.Lock()
			j.Done = done
			j.mu.Unlock()
			if j.Total > 0 && done >= j.Total {
				j.mu.Lock()
				j.State, j.Done = "done", j.Total
				j.mu.Unlock()
				t.Drop()
				return
			}
		}
	}
}
