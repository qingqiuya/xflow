# 汐流 XFlow HTTP API

> 适用于 1.6.31+。服务只监听你选的地址；`local` 模式下别的机器要连得走 SSH 隧道
> （`ssh -L 17777:127.0.0.1:17777 qingqiu@<机器>`）。

基础地址：`http://127.0.0.1:17777`
（设置里改成「局域网」后可用 `http://<局域网IP>:17777`）

## 鉴权

默认「仅本机」模式不需要令牌。
切到「局域网」后，**写接口**（POST / PUT / DELETE）必须带令牌，三种写法都认：

```
-H "X-Token: <令牌>"
?token=<令牌>
-H "Authorization: Bearer <令牌>"
```

读接口（`GET /api/jobs`、`/api/version`、`/api/update`）不需要令牌。

错误统一返回 `{"ok":false,"err":"..."}`；鉴权失败是 HTTP 401。

---

## 加下载任务

### POST /api/add（浏览器插件走这个）

```bash
curl -X POST http://127.0.0.1:17777/api/add \
  -H "Content-Type: application/json" \
  -H "X-Token: <令牌>" \
  -d '{
    "url": "https://example.com/a.iso",
    "name": "a.iso",
    "dir": "D:\\Downloads",
    "referer": "",
    "cookie": "",
    "segs": 16
  }'
```

| 字段 | 必填 | 说明 |
|---|---|---|
| url | ✅ | http/https 直链，或 `magnet:?xt=...` |
| name | | 文件名，留空自动从 URL 猜 |
| dir | | 落地目录，留空用当前下载目录 |
| referer / cookie | | 防盗链站点用 |
| segs | | 分段数，默认 16 |

返回：`{"ok":true,"id":"<任务id>","name":"a.iso"}`

### POST /api/jobs

同样能加任务，参数名换成 `source`：

```bash
curl -X POST http://127.0.0.1:17777/api/jobs \
  -H "Content-Type: application/json" \
  -d '{"source":"magnet:?xt=urn:btih:...","name":"","dir":"","segs":16}'
```

### POST /api/torrent

上传 `.torrent` 文件（multipart，字段名 `file`，可带 `dir`）：

```bash
curl -X POST http://127.0.0.1:17777/api/torrent \
  -H "X-Token: <令牌>" \
  -F "file=@/path/to/x.torrent" -F "dir=D:\\Downloads"
```

---

## 看任务

```bash
curl http://127.0.0.1:17777/api/jobs      # 全部任务
curl http://127.0.0.1:17777/api/version   # {"version":"1.6.31"}
curl http://127.0.0.1:17777/healthz       # 探活
```

任务对象主要字段：

```
id  kind(http|bt)  name  source  dir  total  done  state  rate  err  file_path  seg_n
```

`state` 取值：`queued` `downloading` `paused` `done` `error`

---

## 控制任务

```bash
curl -X POST   http://127.0.0.1:17777/api/job/<id>/pause     # 暂停
curl -X POST   http://127.0.0.1:17777/api/job/<id>/resume    # 继续
curl -X DELETE http://127.0.0.1:17777/api/job/<id>           # 删除（连 .part 一起清）
curl          "http://127.0.0.1:17777/api/redownload?id=<id>" # 重新下载
curl          "http://127.0.0.1:17777/api/open?id=<id>"       # 打开所在文件夹
curl          "http://127.0.0.1:17777/api/clear"              # 清空已完成
```

---

## 下载目录

```bash
curl http://127.0.0.1:17777/api/dir                     # 查当前目录
curl -X POST http://127.0.0.1:17777/api/dir \
  -H "Content-Type: application/json" -d '{"dir":"D:\\Downloads"}'

curl "http://127.0.0.1:17777/api/browse?path=D:\\"      # 列目录
# → {"ok":true,"path":"...","parent":"...","items":[{"name":"x","dir":true}]}
```

---

## 更新

```bash
curl http://127.0.0.1:17777/api/update
# → {"current":"1.6.31","latest":"1.6.31","hasUpdate":false,
#    "asset":"...","assetName":"XFlow-Setup-1.6.31.exe","size":6690695}

curl -X POST http://127.0.0.1:17777/api/update/download   # 让下载器去下安装包/便携版
```

更新时会按安装方式选资产：exe 在 `%LOCALAPPDATA%\XFlow` 下 → 下载 `Setup`；否则 → `Portable`。

---

## 设置（监听范围 / 令牌）

```bash
curl http://127.0.0.1:17777/api/settings
# → {"ok":true,"settings":{"listen":"local","token":""},
#    "lanIP":"10.11.11.147","port":17777}

# 切局域网（自动生成令牌；立即生效，不用重启）
curl -X POST http://127.0.0.1:17777/api/settings \
  -H "Content-Type: application/json" -d '{"listen":"lan"}'

# 换一个令牌
curl -X POST http://127.0.0.1:17777/api/settings \
  -H "Content-Type: application/json" -d '{"listen":"lan","rotate":true}'

# 切回仅本机（这个请求本身要带当前令牌）
curl -X POST "http://127.0.0.1:17777/api/settings?token=<令牌>" \
  -H "Content-Type: application/json" -d '{"listen":"local"}'
```

---

## 其他

```bash
curl http://127.0.0.1:17777/api/hide      # 窗口缩到托盘
curl http://127.0.0.1:17777/bg.jpg        # 界面背景图
curl http://127.0.0.1:17777/icon.png      # 图标
```

---

## 典型用法

```bash
TOKEN=你设置的令牌

# 1) 远程派一个任务（局域网模式）
curl -X POST http://10.11.11.147:17777/api/add \
  -H "Content-Type: application/json" -H "X-Token: $TOKEN" \
  -d '{"url":"https://mirrors.tuna.tsinghua.edu.cn/debian-cd/current/amd64/iso-cd/debian-13.7.0-amd64-netinst.iso"}'

# 2) 看进度
curl http://10.11.11.147:17777/api/jobs | grep -o '"state":"[a-z]*"'

# 3) 挂着看速度
watch -n 2 'curl -s http://10.11.11.147:17777/api/jobs | head -c 400'
```
