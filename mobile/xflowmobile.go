// Package xflowmobile 把汐流的 BT 引擎包成 gomobile 能绑定的形式，给 Android 用。
//
// gomobile 对导出类型限制很多（不支持 chan/func 参数、泛型等），所以这里
// 统一用「字符串进、JSON 出」的笨办法，Java 那边用 org.json 解。
package xflowmobile

import (
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
)

type task struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Dir   string `json:"dir"`
	Total int64  `json:"total"`
	Done  int64  `json:"done"`
	State string `json:"state"` // queued / downloading / done / error / paused
	Err   string `json:"err"`
	Speed int64  `json:"speed"`

	t        *torrent.Torrent
	stop     chan struct{}
	lastDone int64
	lastT    time.Time
	mu       sync.Mutex
}

var (
	cli   *torrent.Client
	cliMu sync.Mutex
	tasks = map[string]*task{}
	tkMu  sync.Mutex
	seq   int64
	// metaRoot 是 Start 传进来的引擎目录。BT 自己的数据库（完成度 .torrent.db）
	// 必须放这里，不能落到用户看得见的下载目录里。
	metaRoot string
)

// Start 初始化 BT 客户端。dataDir 是 .torrent 元数据的临时落点，
// 真正的下载目录每个任务自己指定。
func Start(dataDir string) error {
	cliMu.Lock()
	defer cliMu.Unlock()
	if cli != nil {
		return nil
	}
	if dataDir == "" {
		dataDir = "/data/data/com.qingqiu.xflow/cache/bt"
	}
	metaRoot = dataDir
	cfg := torrent.NewDefaultClientConfig()
	cfg.DataDir = dataDir
	cfg.Seed = false
	c, err := torrent.NewClient(cfg)
	if err != nil {
		return err
	}
	cli = c
	return nil
}

func client() (*torrent.Client, error) {
	cliMu.Lock()
	defer cliMu.Unlock()
	if cli == nil {
		return nil, fmt.Errorf("还没 Start")
	}
	return cli, nil
}

// AddMagnet 加一个磁力/种子文件任务，返回任务 id。
// dir 是下载落地目录；src 支持 magnet: / http(s) .torrent / 本地路径。
func AddMagnet(src, dir string) (string, error) {
	c, err := client()
	if err != nil {
		return "", err
	}
	spec, err := buildSpec(src, dir)
	if err != nil {
		return "", err
	}
	t, _, err := c.AddTorrentSpec(spec)
	if err != nil {
		return "", err
	}

	id := fmt.Sprintf("bt%d", atomic.AddInt64(&seq, 1))
	tk := &task{ID: id, Dir: dir, State: "queued", t: t, stop: make(chan struct{})}
	tk.lastT = time.Now()
	tkMu.Lock()
	tasks[id] = tk
	tkMu.Unlock()

	go run(tk)
	return id, nil
}

func buildSpec(src, dir string) (*torrent.TorrentSpec, error) {
	var spec *torrent.TorrentSpec
	switch {
	case len(src) > 8 && src[:8] == "magnet:?":
		s, err := torrent.TorrentSpecFromMagnetUri(src)
		if err != nil {
			return nil, fmt.Errorf("磁力链接解析失败：%w", err)
		}
		spec = s
	default:
		mi, err := loadMetaInfo(src)
		if err != nil {
			return nil, fmt.Errorf("不是可用的种子来源：%w", err)
		}
		spec = torrent.TorrentSpecFromMetaInfo(mi)
	}
	// 每个任务用自己的存储根，别都塞到 client 的 DataDir 里。
	// 完成度数据库（.torrent.db / -shm / -wal）要指到引擎目录：默认那套会直接
	// 生成在下载目录里，用户看得见的文件夹就脏了。按任务算个名字，避免多任务
	// 抢同一个 bolt 库。
	key := fmt.Sprintf("%x", sha1.Sum([]byte(dir+"|"+src)))[:16]
	pc, err := storage.NewBoltPieceCompletion(filepath.Join(metaRoot, "pc-"+key))
	if err != nil {
		pc = storage.NewMapPieceCompletion() // 建库失败就退化成内存，功能不受影响
	}
	spec.Storage = storage.NewFileWithCompletion(dir, pc)
	return spec, nil
}

// loadMetaInfo 支持本地 .torrent 文件和 http(s) 的 .torrent 链接（桌面版 1.1.4 起就是这样）。
// 以前 Android 端直接 metainfo.LoadFromFile，传 URL 进来会报 "no such file or directory"。
func loadMetaInfo(src string) (*metainfo.MetaInfo, error) {
	if strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://") {
		client := &http.Client{Timeout: 30 * time.Second}
		resp, err := client.Get(src)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("下载种子失败：HTTP %d", resp.StatusCode)
		}
		return metainfo.Load(io.LimitReader(resp.Body, 32<<20))
	}
	return metainfo.LoadFromFile(src)
}

func run(tk *task) {
	tk.mu.Lock()
	tk.State = "downloading"
	tk.mu.Unlock()

	select {
	case <-tk.t.GotInfo():
	case <-time.After(3 * time.Minute):
		tk.mu.Lock()
		tk.State, tk.Err = "error", "3 分钟内没拿到种子信息"
		tk.mu.Unlock()
		return
	case <-tk.stop:
		tk.mu.Lock()
		tk.State = "paused"
		tk.mu.Unlock()
		return
	}

	info := tk.t.Info()
	tk.mu.Lock()
	tk.Name = info.Name
	tk.Total = info.TotalLength()
	tk.mu.Unlock()

	tk.t.DownloadAll()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-tk.stop:
			tk.t.Drop()
			tk.mu.Lock()
			if tk.State != "error" {
				tk.State = "paused"
			}
			tk.mu.Unlock()
			return
		case <-tick.C:
			done := tk.t.BytesCompleted()
			tk.mu.Lock()
			now := time.Now()
			if el := now.Sub(tk.lastT).Seconds(); el >= 0.8 {
				tk.Speed = int64(float64(done-tk.lastDone) / el)
				tk.lastDone = done
				tk.lastT = now
			}
			tk.Done = done
			if tk.Total > 0 && done >= tk.Total {
				tk.State, tk.Done = "done", tk.Total
				tk.mu.Unlock()
				tk.t.Drop()
				return
			}
			tk.mu.Unlock()
		}
	}
}

// Status 返回单个任务的 JSON（找不到返回 "{}"）
func Status(id string) string {
	tkMu.Lock()
	tk := tasks[id]
	tkMu.Unlock()
	if tk == nil {
		return "{}"
	}
	return mustJSON(tk)
}

// StatusAll 返回所有任务的 JSON 数组
func StatusAll() string {
	tkMu.Lock()
	list := make([]*task, 0, len(tasks))
	for _, t := range tasks {
		list = append(list, t)
	}
	tkMu.Unlock()
	out := make([]string, 0, len(list))
	for _, t := range list {
		out = append(out, mustJSON(t))
	}
	b, _ := json.Marshal(json.RawMessage("[" + join(out) + "]"))
	return string(b)
}

func join(ss []string) string {
	r := ""
	for i, s := range ss {
		if i > 0 {
			r += ","
		}
		r += s
	}
	return r
}

func mustJSON(tk *task) string {
	tk.mu.Lock()
	defer tk.mu.Unlock()
	b, err := json.Marshal(map[string]any{
		"id": tk.ID, "name": tk.Name, "dir": tk.Dir,
		"total": tk.Total, "done": tk.Done,
		"state": tk.State, "err": tk.Err, "speed": tk.Speed,
	})
	if err != nil {
		return "{}"
	}
	return string(b)
}

// Pause 停下（保留 .part 和已下数据，目前不支持续传，加了会重新校验）
func Pause(id string) {
	tkMu.Lock()
	tk := tasks[id]
	tkMu.Unlock()
	if tk == nil {
		return
	}
	close(tk.stop)
}

// Remove 删任务（不删文件）
func Remove(id string) {
	tkMu.Lock()
	tk := tasks[id]
	delete(tasks, id)
	tkMu.Unlock()
	if tk == nil {
		return
	}
	select {
	case <-tk.stop:
	default:
		close(tk.stop)
	}
}
