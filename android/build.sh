#!/bin/bash
set -e
export JAVA_HOME=$HOME/toolchain/jdk-25
export PATH=$JAVA_HOME/bin:$PATH
SDK=$HOME/toolchain/android-sdk
BT=$SDK/build-tools/35.0.0
PLATFORM=$SDK/platforms/android-37.0/android.jar
APP=$HOME/xflow-android/app/src/main
OUT=$HOME/xflow-android/build
rm -rf $OUT && mkdir -p $OUT/gen $OUT/classes $OUT/dex

echo "[1/6] 编译资源"
$BT/aapt2 compile --dir $APP/res -o $OUT/res.zip

echo "[2/6] 链接资源 + 生成 R.java"
$BT/aapt2 link -o $OUT/base.apk -I $PLATFORM \
  --manifest $APP/AndroidManifest.xml -R $OUT/res.zip \
  --java $OUT/gen --auto-add-overlay

echo "[3/6] javac"
javac -source 11 -target 11 -encoding UTF-8 -classpath $PLATFORM -d $OUT/classes \
  $OUT/gen/com/qingqiu/xflow/R.java $APP/java/com/qingqiu/xflow/*.java 2>&1 | grep -v "^警告" || true

echo "[4/6] d8 -> dex"
$BT/d8 --lib $PLATFORM --min-api 24 --output $OUT/dex $(find $OUT/classes -name "*.class")

echo "[5/6] 打包 apk"
cd $OUT && cp base.apk unsigned.apk && cp dex/classes.dex . && zip -q -j unsigned.apk classes.dex

echo "[6/6] 对齐 + 签名"
$BT/zipalign -f 4 unsigned.apk aligned.apk
KS=$HOME/xflow-android/xflow.jks
if [ ! -f $KS ]; then
  keytool -genkeypair -keystore $KS -alias xflow -keyalg RSA -keysize 2048 \
    -validity 10000 -storepass xflow1234 -keypass xflow1234 \
    -dname "CN=XFlow, O=qingqiu" >/dev/null 2>&1
fi
$BT/apksigner sign --ks $KS --ks-pass pass:xflow1234 --key-pass pass:xflow1234 \
  --out $HOME/xflow-android/XFlow.apk aligned.apk
echo "== 产物 =="
ls -lh $HOME/xflow-android/XFlow.apk
