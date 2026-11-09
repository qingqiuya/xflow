package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
)

var (
	btOnce   sync.Once
	btClient *torrent.Client
	btErr    error
	btTors   = map[string]*torrent.Torrent{}
	btMu     sync.Mutex
)

// ensureBT 建全局 client。DataDir 只当临时目录用——
// 每个种子真正的落地目录在 buildSpec 里单独指定（见下面那段注释）。
func ensureBT() error {
	btOnce.Do(func() {
		cfg := torrent.NewDefaultClientConfig()
		cfg.Seed = false
		tmp := filepath.Join(os.TempDir(), "xflow-bt")
		_ = os.MkdirAll(tmp, 0777)
		cfg.DataDir = tmp
		btClient, btErr = torrent.NewClient(cfg)
	})
	return btErr
}

// buildSpec 把三种来源统一成 spec，并把存储目录钉死在这个任务的 dir 上。
//
// ★ 为什么不在 client 上设 DataDir：client 是全局单例，DataDir 只有第一份生效，
// 之后换目录的 BT 任务还是会落到第一个目录里。用 spec.Storage 才是按任务隔离的。
func buildSpec(src, dir string) (*torrent.TorrentSpec, error) {
	var spec *torrent.TorrentSpec

	switch {
	case strings.HasPrefix(src, "magnet:?"):
		s, err := torrent.TorrentSpecFromMagnetUri(src)
		if err != nil {
			return nil, fmt.Errorf("磁力链接解析失败：%w", err)
		}
		spec = s

	case strings.HasPrefix(src, "http://"), strings.HasPrefix(src, "https://"):
		// .torrent 本身是个 http 资源，先拉下来（以前这里直接把 URL 当本地路径，
		// AddTorrentFromFile 会去 open 一个叫 "https://..." 的文件，必然失败）
		resp, err := httpClient.Get(src)
		if err != nil {
			return nil, fmt.Errorf("下载种子文件失败：%w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("下载种子文件失败：HTTP %d", resp.StatusCode)
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		if err != nil {
			return nil, fmt.Errorf("读取种子文件失败：%w", err)
		}
		mi, err := metainfo.Load(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("种子文件解析失败：%w", err)
		}
		spec = torrent.TorrentSpecFromMetaInfo(mi)

	default:
		// 本地文件：不靠后缀判断，直接试着按种子解析
		mi, err := metainfo.LoadFromFile(src)
		if err != nil {
			return nil, fmt.Errorf("既不是磁力链接也不是种子文件：%s", src)
		}
		spec = torrent.TorrentSpecFromMetaInfo(mi)
	}

	if err := os.MkdirAll(dir, 0777); err != nil {
		return nil, fmt.Errorf("建目录失败：%w", err)
	}
	spec.Storage = storage.NewFile(dir)
	return spec, nil
}

func startBT(j *Job) {
	dir := j.Dir
	if dir == "" {
		dir = currentDir()
	}
	if err := ensureBT(); err != nil {
		j.mu.Lock()
		j.State, j.Err = "error", err.Error()
		j.mu.Unlock()
		return
	}

	src := j.Source
	btMu.Lock()
	t := btTors[j.ID]
	btMu.Unlock()

	if t == nil {
		spec, err := buildSpec(src, dir)
		if err != nil {
			j.mu.Lock()
			j.State, j.Err = "error", err.Error()
			j.mu.Unlock()
			return
		}
		nt, _, err := btClient.AddTorrentSpec(spec)
		if err != nil {
			j.mu.Lock()
			j.State, j.Err = "error", err.Error()
			j.mu.Unlock()
			return
		}
		t = nt
		btMu.Lock()
		btTors[j.ID] = t
		btMu.Unlock()
	}

	j.mu.Lock()
	j.State, j.Err = "downloading", ""
	j.mu.Unlock()

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
