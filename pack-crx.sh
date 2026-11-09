#!/bin/bash
# 把 ext/ 打成 .crx（自签名），顺便算 sha256。
#
# 用法：
#   ./pack-crx.sh                    # 用 ~/localdl/ext，输出到 ~/localdl/dist
#   ./pack-crx.sh <扩展目录> <输出目录>
#
# 私钥固定放在 ~/localdl/ext-key.pem：**这个文件不能丢**，
# 丢了再打包出来的扩展 ID 就变了（浏览器当成另一个扩展，设置全丢）。
# 它不进 git（.gitignore 里有）。

set -e
EXT_DIR="${1:-$HOME/localdl/ext}"
OUT_DIR="${2:-$HOME/localdl/dist}"
KEY="$HOME/localdl/ext-key.pem"
CHROME="${CHROME:-chromium}"

[ -f "$EXT_DIR/manifest.json" ] || { echo "不是扩展目录：$EXT_DIR"; exit 1; }
command -v "$CHROME" >/dev/null || { echo "找不到 $CHROME（用 CHROME=google-chrome 指定）"; exit 1; }

VER=$(python3 -c "import json;print(json.load(open('$EXT_DIR/manifest.json'))['version'])")
NAME=$(python3 -c "import json;print(json.load(open('$EXT_DIR/manifest.json'))['name'])")
BASE="$(basename "$EXT_DIR")"
mkdir -p "$OUT_DIR"

# chrome 会把结果写到扩展目录的兄弟位置：<dir>.crx / <dir>.pem
rm -f "$EXT_DIR.crx" "$EXT_DIR.pem"
if [ -f "$KEY" ]; then
  "$CHROME" --pack-extension="$EXT_DIR" --pack-extension-key="$KEY" --no-message-box >/dev/null 2>&1 || true
else
  echo "==> 首次打包，生成私钥 $KEY（务必保存好）"
  "$CHROME" --pack-extension="$EXT_DIR" --no-message-box >/dev/null 2>&1 || true
  [ -f "$EXT_DIR.pem" ] && mv "$EXT_DIR.pem" "$KEY" && chmod 600 "$KEY"
fi

[ -f "$EXT_DIR.crx" ] || { echo "打包失败：没生成 $EXT_DIR.crx"; exit 1; }

OUT="$OUT_DIR/xflow-ext-$VER.crx"
mv "$EXT_DIR.crx" "$OUT"
chmod 644 "$OUT"

# 扩展 ID = 公钥 sha256 前 16 字节转 a-p，算出来方便核对
if [ -f "$KEY" ]; then
  ID=$(openssl rsa -in "$KEY" -pubout -outform DER 2>/dev/null \
       | sha256sum | cut -c1-32 \
       | tr '0-9a-f' 'a-p')
  echo "扩展 ID: $ID"
fi

echo "产物: $OUT ($(stat -c%s "$OUT") 字节)"
echo "版本: $VER  名称: $NAME"
(cd "$OUT_DIR" && sha256sum "$(basename "$OUT")" | tee "$(basename "$OUT").sha256")
