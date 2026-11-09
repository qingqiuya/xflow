#!/bin/bash
# 构建 XFlow，并把产物发到发布位置。
#
# 用法:
#   ./release.sh            # 版本号从 setup.nsi 读
#   ./release.sh 1.2.0      # 指定版本号
#
# 发布目标通过环境变量给，没配就只构建不发：
#   XFLOW_RELEASE_HOST   例如 myserver.lan          （ssh/scp 的目标主机）
#   XFLOW_RELEASE_DIR    例如 /srv/releases/xflow   （目标机器上的父目录）
#
# 产物：XFlow.exe（单文件）、XFlow-Setup.exe（NSIS 安装包）、SHA256SUMS
set -e
VER=${1:-$(grep -oP 'APPVERSION "\K[0-9.]+' setup.nsi | head -1)}
HOST="${XFLOW_RELEASE_HOST:-}"
BASE="${XFLOW_RELEASE_DIR:-/srv/releases/xflow}"
DIR="$BASE/$VER"

echo "构建 XFlow $VER ..."
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags "-H windowsgui -s -w" -o XFlow.exe .
rsrc -ico app.ico -manifest app.manifest -o rsrc.syso -arch amd64 2>/dev/null || true
makensis setup.nsi >/dev/null

if [ -z "$HOST" ]; then
  echo "构建完成（没设 XFLOW_RELEASE_HOST，跳过上传）"
  echo "  XFlow.exe         $(du -h XFlow.exe | cut -f1)"
  echo "  XFlow-Setup.exe   $(du -h XFlow-Setup.exe | cut -f1)"
  exit 0
fi

echo "上传到 $HOST:$DIR ..."
ssh "$HOST" "mkdir -p $DIR"
scp -q XFlow.exe XFlow-Setup.exe "$HOST:$DIR/"
ssh "$HOST" "cd $DIR && sha256sum XFlow.exe XFlow-Setup.exe > SHA256SUMS && ls -lh"
echo "发布完成：$HOST:$DIR"
