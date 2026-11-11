# XFlow 第三方组件许可

XFlow 本体以 MIT 许可发布（见 `LICENSE`）。它静态链接了下列第三方 Go 模块，
完整许可原文在 `licenses/` 目录，随每个发布物一起分发。

重新生成：`python3 tools/gen-licenses.py`（依赖 `go list -deps`，反映的是实际编进二进制的模块）。

## 许可分布

- MIT：38 个
- BSD-3-Clause：14 个
- Apache-2.0：9 个
- MPL-2.0：9 个
- BSD-2-Clause：2 个
- ISC：2 个

## 清单

| 模块 | 版本 | 许可 | 源码 |
|---|---|---|---|
| `github.com/alecthomas/atomic` | v0.1.0-alpha2 | MIT | https://github.com/alecthomas/atomic |
| `github.com/anacrolix/btree` | v0.0.0-20251201064447-d86c3fa41bd8 | Apache-2.0 | https://github.com/anacrolix/btree |
| `github.com/anacrolix/chansync` | v0.7.0 | MIT | https://github.com/anacrolix/chansync |
| `github.com/anacrolix/dht/v2` | v2.23.0 | MPL-2.0 | https://github.com/anacrolix/dht/v2 |
| `github.com/anacrolix/envpprof` | v1.4.0 | MIT | https://github.com/anacrolix/envpprof |
| `github.com/anacrolix/generics` | v0.1.1-0.20251125230353-15d98d46693b | MPL-2.0 | https://github.com/anacrolix/generics |
| `github.com/anacrolix/go-libutp` | v1.3.2 | MIT | https://github.com/anacrolix/go-libutp |
| `github.com/anacrolix/log` | v0.17.1-0.20251118025802-918f1157b7bb | MPL-2.0 | https://github.com/anacrolix/log |
| `github.com/anacrolix/missinggo` | v1.3.0 | MIT | https://github.com/anacrolix/missinggo |
| `github.com/anacrolix/missinggo/perf` | v1.0.0 | MIT | https://github.com/anacrolix/missinggo/perf |
| `github.com/anacrolix/missinggo/v2` | v2.10.0 | MIT | https://github.com/anacrolix/missinggo/v2 |
| `github.com/anacrolix/mmsg` | v1.0.1 | MPL-2.0 | https://github.com/anacrolix/mmsg |
| `github.com/anacrolix/multiless` | v0.4.0 | MPL-2.0 | https://github.com/anacrolix/multiless |
| `github.com/anacrolix/stm` | v0.5.0 | MIT | https://github.com/anacrolix/stm |
| `github.com/anacrolix/sync` | v0.5.5-0.20251119100342-d78dd1f686f1 | MPL-2.0 | https://github.com/anacrolix/sync |
| `github.com/anacrolix/torrent` | v1.61.0 | MPL-2.0 | https://github.com/anacrolix/torrent |
| `github.com/anacrolix/upnp` | v0.1.4 | MPL-2.0 | https://github.com/anacrolix/upnp |
| `github.com/bahlo/generic-list-go` | v0.2.0 | BSD-3-Clause | https://github.com/bahlo/generic-list-go |
| `github.com/benbjohnson/immutable` | v0.4.1-0.20221220213129-8932b999621d | MIT | https://github.com/benbjohnson/immutable |
| `github.com/bradfitz/iter` | v0.0.0-20191230175014-e8f45d346db8 | BSD-3-Clause | https://github.com/bradfitz/iter |
| `github.com/cespare/xxhash` | v1.1.0 | MIT | https://github.com/cespare/xxhash |
| `github.com/davecgh/go-spew` | v1.1.1 | ISC | https://github.com/davecgh/go-spew |
| `github.com/dustin/go-humanize` | v1.0.0 | MIT | https://github.com/dustin/go-humanize |
| `github.com/edsrzf/mmap-go` | v1.1.0 | BSD-3-Clause | https://github.com/edsrzf/mmap-go |
| `github.com/go-llsqlite/adapter` | v0.0.0-20230927005056-7f5ce7f0c916 | MPL-2.0 | https://github.com/go-llsqlite/adapter |
| `github.com/go-llsqlite/crawshaw` | v0.5.6-0.20250312230104-194977a03421 | ISC | https://github.com/go-llsqlite/crawshaw |
| `github.com/go-logr/logr` | v1.4.3 | Apache-2.0 | https://github.com/go-logr/logr |
| `github.com/go-logr/stdr` | v1.2.2 | Apache-2.0 | https://github.com/go-logr/stdr |
| `github.com/google/btree` | v1.1.2 | Apache-2.0 | https://github.com/google/btree |
| `github.com/google/go-cmp` | v0.7.0 | BSD-3-Clause | https://github.com/google/go-cmp |
| `github.com/google/uuid` | v1.6.0 | BSD-3-Clause | https://github.com/google/uuid |
| `github.com/gorilla/websocket` | v1.5.0 | BSD-2-Clause | https://github.com/gorilla/websocket |
| `github.com/huandu/xstrings` | v1.3.2 | MIT | https://github.com/huandu/xstrings |
| `github.com/klauspost/cpuid/v2` | v2.2.3 | MIT | https://github.com/klauspost/cpuid/v2 |
| `github.com/mr-tron/base58` | v1.2.0 | MIT | https://github.com/mr-tron/base58 |
| `github.com/multiformats/go-multihash` | v0.2.3 | MIT | https://github.com/multiformats/go-multihash |
| `github.com/multiformats/go-varint` | v0.0.6 | MIT | https://github.com/multiformats/go-varint |
| `github.com/pion/datachannel` | v1.5.9 | MIT | https://github.com/pion/datachannel |
| `github.com/pion/dtls/v3` | v3.0.3 | MIT | https://github.com/pion/dtls/v3 |
| `github.com/pion/ice/v4` | v4.0.2 | MIT | https://github.com/pion/ice/v4 |
| `github.com/pion/interceptor` | v0.1.40 | MIT | https://github.com/pion/interceptor |
| `github.com/pion/logging` | v0.2.3 | MIT | https://github.com/pion/logging |
| `github.com/pion/mdns/v2` | v2.0.7 | MIT | https://github.com/pion/mdns/v2 |
| `github.com/pion/randutil` | v0.1.0 | MIT | https://github.com/pion/randutil |
| `github.com/pion/rtcp` | v1.2.15 | MIT | https://github.com/pion/rtcp |
| `github.com/pion/rtp` | v1.8.18 | MIT | https://github.com/pion/rtp |
| `github.com/pion/sctp` | v1.8.33 | MIT | https://github.com/pion/sctp |
| `github.com/pion/sdp/v3` | v3.0.9 | MIT | https://github.com/pion/sdp/v3 |
| `github.com/pion/srtp/v3` | v3.0.4 | MIT | https://github.com/pion/srtp/v3 |
| `github.com/pion/stun/v3` | v3.0.0 | MIT | https://github.com/pion/stun/v3 |
| `github.com/pion/transport/v3` | v3.0.7 | MIT | https://github.com/pion/transport/v3 |
| `github.com/pion/turn/v4` | v4.0.0 | MIT | https://github.com/pion/turn/v4 |
| `github.com/pion/webrtc/v4` | v4.0.0 | MIT | https://github.com/pion/webrtc/v4 |
| `github.com/pkg/errors` | v0.9.1 | BSD-2-Clause | https://github.com/pkg/errors |
| `github.com/protolambda/ctxlock` | v0.1.0 | MIT | https://github.com/protolambda/ctxlock |
| `github.com/RoaringBitmap/roaring` | v1.2.3 | Apache-2.0 | https://github.com/RoaringBitmap/roaring |
| `github.com/rs/dnscache` | v0.0.0-20211102005908-e0241e321417 | MIT | https://github.com/rs/dnscache |
| `github.com/spaolacci/murmur3` | v1.1.0 | BSD-3-Clause | https://github.com/spaolacci/murmur3 |
| `github.com/tidwall/btree` | v1.8.1 | MIT | https://github.com/tidwall/btree |
| `github.com/webview/webview_go` | v0.0.0-20240831120633-6173450d4dd6 | MIT | https://github.com/webview/webview_go |
| `github.com/wlynxg/anet` | v0.0.3 | BSD-3-Clause | https://github.com/wlynxg/anet |
| `go.etcd.io/bbolt` | v1.3.6 | MIT | https://go.etcd.io/bbolt |
| `go.opentelemetry.io/auto/sdk` | v1.2.1 | Apache-2.0 | https://go.opentelemetry.io/auto/sdk |
| `go.opentelemetry.io/otel` | v1.38.0 | Apache-2.0 | https://go.opentelemetry.io/otel |
| `go.opentelemetry.io/otel/metric` | v1.38.0 | Apache-2.0 | https://go.opentelemetry.io/otel/metric |
| `go.opentelemetry.io/otel/trace` | v1.38.0 | Apache-2.0 | https://go.opentelemetry.io/otel/trace |
| `golang.org/x/crypto` | v0.44.0 | BSD-3-Clause | https://golang.org/x/crypto |
| `golang.org/x/exp` | v0.0.0-20251113190631-e25ba8c21ef6 | BSD-3-Clause | https://golang.org/x/exp |
| `golang.org/x/net` | v0.47.0 | BSD-3-Clause | https://golang.org/x/net |
| `golang.org/x/sync` | v0.18.0 | BSD-3-Clause | https://golang.org/x/sync |
| `golang.org/x/sys` | v0.45.0 | BSD-3-Clause | https://golang.org/x/sys |
| `golang.org/x/text` | v0.31.0 | BSD-3-Clause | https://golang.org/x/text |
| `golang.org/x/time` | v0.14.0 | BSD-3-Clause | https://golang.org/x/time |
| `lukechampine.com/blake3` | v1.1.6 | MIT | https://lukechampine.com/blake3 |

## 特别说明

### MPL-2.0（Mozilla Public License 2.0）

下列模块以 MPL-2.0 授权，文件级 copyleft：XFlow **未修改** 这些模块的源码，
按要求在此声明其源码可从以下地址获取；如日后本地修改了其中的文件，
被修改的文件将以 MPL-2.0 公开。

- `github.com/anacrolix/dht/v2` —— https://github.com/anacrolix/dht/v2
- `github.com/anacrolix/generics` —— https://github.com/anacrolix/generics
- `github.com/anacrolix/log` —— https://github.com/anacrolix/log
- `github.com/anacrolix/mmsg` —— https://github.com/anacrolix/mmsg
- `github.com/anacrolix/multiless` —— https://github.com/anacrolix/multiless
- `github.com/anacrolix/sync` —— https://github.com/anacrolix/sync
- `github.com/anacrolix/torrent` —— https://github.com/anacrolix/torrent
- `github.com/anacrolix/upnp` —— https://github.com/anacrolix/upnp
- `github.com/go-llsqlite/adapter` —— https://github.com/go-llsqlite/adapter（许可原文取自上游仓库：该版本模块包内没带 LICENSE）

### Apache-2.0

这些模块的 `LICENSE` 原文在 `licenses/`；上游没有 NOTICE 文件的未额外附带。

- `github.com/anacrolix/btree`
- `github.com/go-logr/logr`
- `github.com/go-logr/stdr`
- `github.com/google/btree`
- `github.com/RoaringBitmap/roaring`
- `go.opentelemetry.io/auto/sdk`
- `go.opentelemetry.io/otel`
- `go.opentelemetry.io/otel/metric`
- `go.opentelemetry.io/otel/trace`

### 许可缺失

无。
