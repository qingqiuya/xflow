# 汐流 XFlow

<img src="assets/icon.png" width="140" align="right" alt="汐流">

Windows 桌面下载器：HTTP 多线程分块 + BT/磁力，外加一个接管浏览器下载的扩展。

界面用 WebView2，下载引擎和一个本地 HTTP API 跑在同一个进程里，单文件 exe、免安装依赖。

## 功能

- **HTTP/HTTPS 直链**：分块并发（默认 16 段，自适应）、断点续传、多源择优
- **BT / 磁力**：基于 anacrolix/torrent
- **浏览器接管**：插件拦下浏览器下载、带上 Cookie 和 Referer 转给汐流
- **任务历史**：已完成的可重新下载、可清空
- **托盘常驻**：点 × 不退出，缩到托盘
- **本地 API**：`POST /api/add` 可以从脚本或别的程序派任务

## 安装

从 [Releases](https://github.com/qingqiuya/xflow/releases) 下 `XFlow-Setup.exe`，装到 `%LOCALAPPDATA%\XFlow`，不需要管理员权限。

### 浏览器插件

安装目录下的 `ext/` 是扩展本体：

1. `chrome://extensions` 或 `edge://extensions`
2. 打开「开发者模式」
3. 「加载已解压的扩展程序」→ 选 `%LOCALAPPDATA%\XFlow\ext`

**这个目录不能删**，删了浏览器下次启动扩展就没了（解压加载是引用，不是拷贝）。

## 本地 API

汐流只监听 `127.0.0.1:17777`，不对外开放。端口写在 `%USERPROFILE%\.localdl-port`。

```bash
curl -X POST http://127.0.0.1:17777/api/add \
  -H "Content-Type: application/json" \
  -d '{"url":"https://example.com/big.iso","name":"big.iso","segs":16}'
```

| 字段 | 说明 |
|---|---|
| `url` | 必填，http/https/magnet |
| `name` | 保存文件名，不填从 URL 推 |
| `dir` | 保存目录，不填用界面上的默认目录 |
| `segs` | 并发段数，0 或省略 = 16，上限 64 |
| `referer` / `cookie` | 可选，需要登录的站点用得上 |

返回 `{"ok":true,"id":"..."}`。

## 从源码构建

需要 Go 1.21+、`makensis`（打安装包）、`rsrc`（可选，生成图标资源）。

```bash
# 只构建
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 \
  go build -ldflags "-H windowsgui -s -w" -o XFlow.exe .

# 构建 + 打安装包
./release.sh 1.2.0

# 插件打包成 crx（自签名，Windows 上装不上，见 CRX-安装说明.md）
./pack-crx.sh
```

`release.sh` 要想顺带发布，设两个环境变量：`XFLOW_RELEASE_HOST` 和 `XFLOW_RELEASE_DIR`。

## 目录

```
main.go          界面 + HTTP API + 托盘
manager.go       任务管理（队列、持久化）
httpdl.go        HTTP 分块下载引擎
btdl.go          BT 引擎
dl/              面板侧下载后端（另一套，服务端用）
ext/             浏览器扩展（MV3）
wv2/             WebView2 绑定的本地副本（含一处窗口关闭的改动）
```

## 已知限制

- 接管不了 `blob:` / `data:` 生成的下载、POST 表单直接吐文件、流媒体和 DRM
- 插件在 Chrome/Edge 上只能「加载已解压的扩展」，自签名 crx 会被禁用
- 只在 Windows 上验证过；Linux 下有一份无界面的测试用 `main_linux.go`

## 许可

MIT。`wv2/` 是 [jchv/go-webview2](https://github.com/jchv/go-webview2) 的副本，同样是 MIT，版权归原作者。
