# xflowmobile —— BT 引擎的 gomobile 绑定

把桌面版的 BT 引擎（Go + anacrolix/torrent）包装成 `gomobile bind` 能吃的形式，
给 Android 用。这样手机版不用重写 BT，直接调同一套引擎。

## 打成 AAR

需要 Go 1.24+、Android NDK、`gomobile`：

```bash
export ANDROID_NDK_HOME=~/toolchain/android-ndk-r26d
export ANDROID_HOME=~/toolchain/android-sdk
go install golang.org/x/mobile/cmd/gomobile@latest
gomobile init

go get -tool golang.org/x/mobile/cmd/gobind

# ★ 必须加这个，否则链接报 "invalid reference to net.zoneCache"
#   anet 用了 //go:linkname，Go 1.23 起默认禁止
export GOFLAGS="-ldflags=-checklinkname=0"

gomobile bind -target=android/arm64 -androidapi 24 -o ../xflowbt.aar .
```

产物 `xflowbt.aar`（16M）里有 `classes.jar` 和 `jni/arm64-v8a/libgojni.so`（32M）。
`build.sh` 会解包它：classes.jar 进 javac 和 d8，libgojni.so 打进 APK 的 `lib/arm64-v8a/`。

## Java 侧 API

```java
import xflowmobile.Xflowmobile;

Xflowmobile.start(cacheDir);                       // 初始化（幂等）
String id  = Xflowmobile.addMagnet(magnetOrPath, dir);
String js  = Xflowmobile.status(id);               // 单个任务 JSON
String all = Xflowmobile.statusAll();              // 全部任务 JSON 数组
Xflowmobile.pause(id);
Xflowmobile.remove(id);
```

gomobile 对导出类型限制多（不支持 chan/func 参数），所以统一走「字符串进、JSON 出」。

## 已知限制

- 只有 arm64（`-target=android/arm64`），armeabi-v7a 没编
- Pause 之后 Resume 是「重新添加同一个 torrent」，靠 anacrolix 的已有数据校验续传，不是真正的断点
