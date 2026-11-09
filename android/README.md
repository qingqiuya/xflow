# XFlow Android

手机版：纯 Java 实现，不依赖 Go 引擎，也没有 gradle——`build.sh` 直接调 aapt2 / javac / d8 / apksigner。

## 和桌面版的差别

| | 桌面版 | Android 版 |
|---|---|---|
| 下载引擎 | Go（httpdl.go） | Java HttpURLConnection |
| 多线程分块 | ✅ 16 段自适应 | ✅ 最多 8 段 |
| 断点续传 | ✅ | ✅（`.part` + `.meta`） |
| BT / 磁力 | ✅ | ❌ 没有 |
| 界面 | WebView2 / WebKitGTK + 网页 | 原生 View |

BT 没做：Java 生态里没有能直接用的纯 Java BT 实现，接 libtorrent 要走 NDK 编 AAR，量级比整个 App 还大。

## 功能

- 多线程分块下载（Range 请求，最多 8 段并行）
- 断点续传：暂停/关 App 后重开能接着下（进度记在 `.meta`）
- 暂停 / 继续 / 取消
- 前台服务 + WakeLock，灭屏继续跑，通知栏显示进度和速度
- 同时最多 2 个任务，其余排队
- 完成的文件可以「打开」（自带一个极简 ContentProvider 借给别的 app）

## 构建

需要 JDK 17+ 和 Android SDK（build-tools + platform）。

```bash
./build.sh          # 产物 XFlow.apk，签名用 xflow.jks（首次自动生成）
```

`build.sh` 里的路径（`~/toolchain/jdk-25`、`~/toolchain/android-sdk`）按自己环境改。

## 存储位置

`/sdcard/Android/data/com.qingqiu.xflow/files/Download/`

App 私有目录，卸载会被清掉。想搬到公共 Downloads 得走 MediaStore（Android 10+ 的 Scoped Storage），还没做。
