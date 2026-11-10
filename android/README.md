# XFlow Android

手机版汐流。纯 Java 手动打包，不用 gradle——`build.sh` 直接调 `aapt2 / javac / d8 / apksigner`。

## 和桌面版的差别

| | 桌面版 | Android 版 |
|---|---|---|
| 界面 | WebView2 / WebKitGTK + 网页 | 原生 View |
| 直链下载 | Go（httpdl.go） | Java HttpURLConnection |
| 并发 | 自适应（每 3 秒试水，上限 32） | 自适应（同上，上限 16） |
| 断点续传 | ✅ | ✅（`.part` + `.meta`） |
| BT / 磁力 / 种子 | anacrolix/torrent（Go 原生） | 同一套引擎，gomobile 绑成 AAR |
| 浏览器接管 | ✅ 插件 | ❌ |
| 平台 | Windows / Linux | Android，**只支持 arm64** |

## 功能

- 直链多线程分块（Range），**连接数自适应**：起始开 `segN` 个 worker，每 3 秒主动试一批新的，速度涨了继续加、连着两轮没效果就收手
- 分段进度条：主进度下面每段一格，下满变绿，能看出哪段在卡
- 断点续传：暂停/关 App 后重开接着下（进度在 `.meta`）
- BT：磁力链、远程 `.torrent` 链接、本地种子文件都支持；种子**按内容校验**（不看后缀），假种子当场拒
- 删除任务会清掉没下完的 `.part` / BT 半截文件，已完成的成品不碰
- 种子/链接里解析不出种子时自动降级成普通直链下载（能把 `.torrent` 文件本身拿下来）

## 构建

```bash
# 1) 先生成 BT 引擎（仓库根 mobile/，不在 git 里）
cd ../mobile
gomobile bind -target=android/arm64 -androidapi 24 \
    -ldflags=-checklinkname=0 -o ../android/xflowbt.aar .

# 2) 打包
cd ../android
ANDROID_HOME=~/toolchain/android-sdk JAVA_HOME=~/toolchain/jdk-25 ./build.sh
```

需要的东西：

- Android SDK：`build-tools/35.0.0`、`platforms/android-37.0`
- NDK（只要 `libc++_shared.so`，`ANDROID_NDK_HOME` 指向 r26d 即可）
- JDK 17+（`JAVA_HOME`）
- 签名密钥 `xflow.jks`（不进 git；没有会自动生成一个，但换了密钥就不能覆盖安装旧版）

`-ldflags=-checklinkname=0` 是必须的：Go 1.23+ 收紧了 linkname 白名单，`wlynxg/anet` 引用了 `net.zoneCache`，不加会报 `invalid reference to net.zoneCache`。

## 已知限制

- 只有 arm64（`libgojni.so` 只编了 arm64-v8a）
- 不支持浏览器接管（桌面版那套 MV3 插件）
- BT 的完成度数据库放在 App 私有目录 `cache/bt/pc-<hash>`，下载目录里只有你自己下的东西
