#!/bin/bash
# 汐流发版一条龙：编译 → 打包 → tag → push(github+nas) → gh release → 镜像资产到 NAS
#
# 用法： ./release.sh 1.6.32 [--notes "发版说明"]
set -e
VER="${1:-}"
if [ -z "$VER" ]; then echo "用法: ./release.sh <版本号> [--notes 说明]"; exit 1; fi
shift || true
NOTES="汐流 XFlow $VER"
if [ "${1:-}" = "--notes" ]; then NOTES="${2:-$NOTES}"; fi

cd "$(dirname "$0")"
echo "== 1/5 编译 Windows + Linux"
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags "-H windowsgui -s -w -X main.Version=$VER" -o /tmp/XFlow-$VER.exe .
CGO_ENABLED=1 go build -tags webkit2_41 -ldflags "-s -w -X main.Version=$VER" -o /tmp/xflow-linux-$VER .

echo "== 2/5 打包（安装包 / deb / crx / 便携版）"
cp /tmp/XFlow-$VER.exe XFlow.exe
sed -i "s/!define APPVERSION \"[^\"]*\"/!define APPVERSION \"$VER\"/" setup.nsi
sed -i "s/\"version\": \"[^\"]*\"/\"version\": \"$VER\"/" ext/manifest.json
./pack-crx.sh >/dev/null
./pack-deb.sh "$VER" >/dev/null
makensis setup.nsi >/dev/null
cp XFlow-Setup.exe "dist/XFlow-Setup-$VER.exe"
cp "/tmp/XFlow-$VER.exe" "dist/XFlow-portable-$VER.exe"

echo "== 3/5 tag + push"
git add -A && git commit -m "发版 $VER" >/dev/null 2>&1 || true
git tag -f "v$VER"
git push github HEAD && git push nas HEAD && git push -f github "v$VER"

echo "== 4/5 gh release"
gh release create "v$VER" --title "$NOTES" --notes "$NOTES" \
  "dist/XFlow-Setup-$VER.exe" "dist/XFlow-portable-$VER.exe" \
  "dist/xflow_${VER}_amd64.deb" "dist/xflow-ext-$VER.crx"

echo "== 5/5 镜像到 NAS"
~/bin/mirror-xflow-releases.sh "v$VER"
ssh qingqiu@10.11.11.20 "echo v$VER > /mnt/raid6/nas/releases/xflow/LATEST && ln -sfn v$VER /mnt/raid6/nas/releases/xflow/latest"
echo "完成：v$VER"
