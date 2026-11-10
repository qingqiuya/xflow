# 汐流 XFlow

<img src="assets/icon.png" width="140" align="right" alt="汐流">

Windows 桌面下载器：HTTP 多线程分块 + BT/磁力，外加一个接管浏览器下载的扩展。

界面用 WebView2，下载引擎和一个本地 HTTP API 跑在同一个进程里，单文件 exe、免安装依赖。

## 功能

- **HTTP/HTTPS 直链**：分块并发（默认 16 段，自适应）、断点续传、多源择优
- **BT / 磁力**：基于 anacrolix/torrent，支持磁力链接、远程 `.torrent` 地址、**上传本地种子文件**（界面「种子文件」按钮或直接拖进窗口；按内容解析不认后缀）；每个任务下到自己的目录
- **浏览器接管**：插件拦下浏览器下载、带上 Cookie 和 Referer 转给汐流
- **任务历史**：已完成的可重新下载、可清空
- **托盘常驻**：点 × 不退出，缩到托盘
- **本地 API**：`POST /api/add` 可以从脚本或别的程序派任务

## 安装

Release 里有两个 Windows 包，**程序本体是同一个**，区别只在交付方式：

| | `XFlow-Setup.exe`（安装版） | `XFlow.exe`（绿色版） |
|---|---|---|
| 体积 | 6 MB（里面是 LZMA 压过的同一个 exe） | 21 MB，单文件 |
| 怎么用 | 双击安装，建桌面 + 开始菜单快捷方式 | 双击直接跑 |
| 装到哪 | `%LOCALAPPDATA%\XFlow` | 你放哪就在哪 |
| 卸载 | 设置 → 应用，或目录里的 `Uninstall.exe` | 直接删掉 |
| 浏览器插件 | 自带 `ext/` 目录，加载已解压直接选它 | exe 旁边没 `ext/`，接管功能要另外从仓库拿 |
| 管理员权限 | 不需要 | 不需要 |

配置和任务历史都在 `%USERPROFILE%\.localdl*`（端口、上次选的目录、任务记录、WebView 数据），跟 exe 放哪无关——两个版本可以随便切，历史不会丢。

长期用推荐安装版（有快捷方式和卸载项，插件目录也现成）；绿色版适合放 U 盘、或者临时在别的机器上跑。

从 [Releases](https://github.com/qingqiuya/xflow/releases) 下。

### Linux / Debian

```bash
sudo dpkg -i xflow_<版本>_amd64.deb
xflow                                 # 开原生窗口（WebKitGTK）
xflow --headless                      # 只跑服务，用浏览器访问
systemctl --user enable --now xflow   # 登录后自启
```

界面是 WebKitGTK 原生窗口，和 Windows 版的 WebView2 是同一份 `ui.html`。
没装桌面（没有 `DISPLAY`/`WAYLAND_DISPLAY`）时会自动退化成只跑服务。

参数：

```
--port 8899      换端口（只绑 127.0.0.1）
--dir /data/dl   换默认下载目录
--headless       只跑服务
--browser        服务起来后交给系统浏览器，不开原生窗口
```

自己编译 Linux 版要 `libwebkit2gtk-4.1-dev`，并且带上 `pkgconfig/` 那个兼容文件
（Debian 13 只提供 webkit2gtk-4.1，而 webview_go 写死找 4.0）：

```bash
sudo apt install libwebkit2gtk-4.1-dev
PKG_CONFIG_PATH=$PWD/pkgconfig CGO_ENABLED=1 go build -ldflags "-X main.Version=dev" -o xflow .
```

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
main.go          Windows 入口：界面 + HTTP API + 托盘
main_linux.go    Linux 入口：服务 + WebKitGTK 原生窗口
manager.go       任务管理（队列、持久化）
httpdl.go        HTTP 分块下载引擎
btdl.go          BT 引擎
dl/              面板侧下载后端（另一套，服务端用）
ext/             浏览器扩展（MV3）
wv2/             WebView2 绑定的本地副本（含一处窗口关闭的改动）
```

## 已知限制

- 接管不了 `blob:` / `data:` 生成的下载、POST 表单直接吐文件、流媒体和 DRM
- BT 是整包下载，还不支持在种子里只挑几个文件；也不做种（下完就断开）
- 插件在 Chrome/Edge 上只能「加载已解压的扩展」，自签名 crx 会被禁用
- Linux 版用 WebKitGTK 开窗口（不是 WebView2），没有托盘；没图形界面时退化成 `--headless` 纯服务

## 许可

MIT。`wv2/` 是 [jchv/go-webview2](https://github.com/jchv/go-webview2) 的副本，同样是 MIT，版权归原作者。
