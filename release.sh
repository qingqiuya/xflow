#!/bin/bash
# 构建并发布 XFlow 到 NAS releases
set -e
VER=${1:-$(grep -oP 'APPVERSION "\K[0-9.]+' setup.nsi | head -1)}
DIR="/mnt/nas/build/releases/xflow/$VER"
echo "构建 $VER ..."
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags "-H windowsgui -s -w" -o XFlow.exe .
rsrc -ico app.ico -manifest app.manifest -o rsrc.syso -arch amd64 2>/dev/null || true
makensis setup.nsi >/dev/null
echo "上传到 NAS:$DIR ..."
ssh user@example.lan "mkdir -p $DIR"
scp -q XFlow.exe XFlow-Setup.exe user@example.lan:$DIR/
ssh user@example.lan "cd $DIR && sha256sum XFlow.exe XFlow-Setup.exe > SHA256SUMS && ls -lh"
echo "发布完成：NAS:$DIR"
