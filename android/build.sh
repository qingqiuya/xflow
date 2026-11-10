#!/bin/bash
# 手工打包 XFlow Android（不用 gradle，直接 aapt2 / javac / d8 / apksigner）。
#
# 前置：
#   1. Android SDK（build-tools + platform）和 JDK 17+：
#        export ANDROID_HOME=~/toolchain/android-sdk        # 里面有 build-tools/35.0.0、platforms/android-37.0
#        export JAVA_HOME=~/toolchain/jdk-25
#   2. BT 引擎的 AAR：仓库根 mobile/ 用 gomobile bind 生成一次（不在 git 里，二进制太大）
#        cd ../mobile
#        gomobile bind -target=android/arm64 -androidapi 24 \
#            -ldflags=-checklinkname=0 -o ../android/xflowbt.aar .
#      （-checklinkname=0 是必须的，否则 anet 会报 invalid reference to net.zoneCache）
#   3. 签名密钥：android/xflow.jks（不在 git 里）。没有就自动生成一个自用的，
#      但换了密钥的包不能覆盖安装旧版。
#
# 产物：android/XFlow.apk
set -e

HERE=$(cd "$(dirname "$0")" && pwd)
APP=$HERE/app/src/main
OUT=$HERE/build
AAR=${XFLOW_AAR:-$HERE/xflowbt.aar}
KS=${XFLOW_KS:-$HERE/xflow.jks}
KSPASS=${XFLOW_KS_PASS:-xflow1234}

JAVA_HOME=${JAVA_HOME:-$HOME/toolchain/jdk-25}
export JAVA_HOME
export PATH=$JAVA_HOME/bin:$PATH
SDK=${ANDROID_HOME:-${ANDROID_SDK_ROOT:-$HOME/toolchain/android-sdk}}
BT=$SDK/build-tools/35.0.0
PLATFORM=$SDK/platforms/android-37.0/android.jar

if [ ! -f "$PLATFORM" ]; then
  echo "找不到 $PLATFORM —— 设 ANDROID_HOME 指向装了 platform android-37.0 的 SDK" >&2
  exit 1
fi
if [ ! -f "$AAR" ]; then
  echo "找不到 $AAR —— 先按上面注释里的 gomobile bind 生成一次（或设 XFLOW_AAR 指到别处）" >&2
  exit 1
fi

rm -rf "$OUT" && mkdir -p "$OUT/gen" "$OUT/classes" "$OUT/dex"

echo "[1/6] 编译资源"
$BT/aapt2 compile --dir $APP/res -o $OUT/res.zip

echo "[2/6] 链接资源 + 生成 R.java"
$BT/aapt2 link -o $OUT/base.apk -I "$PLATFORM" \
  --manifest $APP/AndroidManifest.xml -R $OUT/res.zip \
  --java $OUT/gen --auto-add-overlay

echo "[2.5/6] 解 AAR（BT 引擎）"
rm -rf "$OUT/aar" && mkdir -p "$OUT/aar" && (cd "$OUT/aar" && unzip -q "$AAR")
JAR=$OUT/aar/classes.jar

echo "[3/6] javac"
javac -source 11 -target 11 -encoding UTF-8 -classpath "$PLATFORM:$JAR" -d $OUT/classes \
  $OUT/gen/com/qingqiuya/xflow/R.java $APP/java/com/qingqiuya/xflow/*.java 2>&1 | grep -v "^警告" || true

echo "[4/6] d8 -> dex"
$BT/d8 --lib "$PLATFORM" --min-api 24 --output $OUT/dex $(find $OUT/classes -name "*.class") "$JAR"

echo "[5/6] 打包 apk"
cd $OUT && cp base.apk unsigned.apk && cp dex/classes.dex . && zip -q -j unsigned.apk classes.dex

# Go 的 BT 引擎：libgojni.so 只有 arm64 版。它动态依赖 NDK 的 libc++_shared.so，
# 不一起打进 apk 的话运行时直接 UnsatisfiedLinkError（BT 引擎起不来）。
mkdir -p $OUT/lib/arm64-v8a
cp $OUT/aar/jni/arm64-v8a/libgojni.so $OUT/lib/arm64-v8a/
LIBCXX=${ANDROID_NDK_HOME:-$HOME/toolchain/android-ndk-r26d}/toolchains/llvm/prebuilt/linux-x86_64/sysroot/usr/lib/aarch64-linux-android/libc++_shared.so
if [ -f "$LIBCXX" ]; then
  cp "$LIBCXX" $OUT/lib/arm64-v8a/
else
  echo "!! 找不到 libc++_shared.so（$LIBCXX），BT 引擎会在运行时崩" >&2
  exit 1
fi
zip -q unsigned.apk lib/arm64-v8a/libgojni.so lib/arm64-v8a/libc++_shared.so

echo "[6/6] 对齐 + 签名"
$BT/zipalign -f -p 4 unsigned.apk aligned.apk
if [ ! -f "$KS" ]; then
  echo "没有签名密钥，生成一个新的：$KS" >&2
  keytool -genkeypair -keystore "$KS" -alias xflow -keyalg RSA -keysize 2048 \
    -validity 10000 -storepass "$KSPASS" -keypass "$KSPASS" \
    -dname "CN=XFlow, O=qingqiu" >/dev/null 2>&1
fi
$BT/apksigner sign --ks "$KS" --ks-pass "pass:$KSPASS" --key-pass "pass:$KSPASS" \
  --out "$HERE/XFlow.apk" aligned.apk
echo "== 产物 =="
ls -lh "$HERE/XFlow.apk"
