#!/bin/bash
# 打 Linux（amd64）的 .deb 安装包。
#
#   ./pack-deb.sh            # 版本从 setup.nsi 读
#   ./pack-deb.sh 1.2.0
#
# 产物：dist/xflow_<版本>_amd64.deb
#
# 装完之后：
#   systemctl --user enable --now xflow     # 开机自启（登录后）
#   xflow --open                            # 或者手动起，顺便开浏览器
set -e
VER=${1:-$(grep -oP 'APPVERSION "\K[0-9.]+' setup.nsi | head -1)}
ARCH=amd64
OUT="dist"
STAGE=$(mktemp -d)
trap 'rm -rf "$STAGE"' EXIT

echo "构建 Linux 二进制 $VER ..."
CGO_ENABLED=0 GOOS=linux GOARCH=$ARCH \
  go build -ldflags "-s -w -X main.Version=$VER" -o "$STAGE/xflow" .

PKG="$STAGE/pkg"
mkdir -p "$PKG/DEBIAN" \
         "$PKG/usr/bin" \
         "$PKG/usr/share/applications" \
         "$PKG/usr/share/icons/hicolor/256x256/apps" \
         "$PKG/usr/share/doc/xflow" \
         "$PKG/lib/systemd/user"

install -m 0755 "$STAGE/xflow" "$PKG/usr/bin/xflow"

# 图标
python3 - "$PKG/usr/share/icons/hicolor/256x256/apps/xflow.png" <<'PY'
import sys
from PIL import Image
im = Image.open("icon.jpg").convert("RGBA")
im.resize((256, 256), Image.LANCZOS).save(sys.argv[1], optimize=True)
PY

cat > "$PKG/usr/share/applications/xflow.desktop" <<'EOF'
[Desktop Entry]
Type=Application
Name=XFlow
Name[zh_CN]=汐流
GenericName=Download Manager
GenericName[zh_CN]=下载器
Comment=Multi-threaded downloader (HTTP + BT)
Comment[zh_CN]=多线程下载器，支持 HTTP 直链和 BT
Exec=xflow --open
Icon=xflow
Terminal=false
Categories=Network;FileTransfer;
Keywords=download;bt;torrent;magnet;
EOF

cat > "$PKG/lib/systemd/user/xflow.service" <<'EOF'
[Unit]
Description=XFlow 汐流下载器
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=/usr/bin/xflow
Restart=on-failure
RestartSec=3
# 下载中途被 systemd 停掉时，让进程有机会把任务状态写盘
KillSignal=SIGTERM
TimeoutStopSec=20

[Install]
WantedBy=default.target
EOF

cp README.md "$PKG/usr/share/doc/xflow/README.md"
gzip -9n "$PKG/usr/share/doc/xflow/README.md"

cat > "$PKG/DEBIAN/control" <<EOF
Package: xflow
Version: $VER
Architecture: $ARCH
Maintainer: qingqiuya <noreply@qingqiuya.top>
Section: net
Priority: optional
Homepage: https://github.com/qingqiuya/xflow
Description: 汐流 XFlow —— 多线程下载器（HTTP + BT）
 一个本地下载服务：分块并发下 HTTP/HTTPS，也能跑 BT 和磁力。
 界面在浏览器里打开（http://127.0.0.1:17777），只监听本机回环。
EOF

cat > "$PKG/DEBIAN/postinst" <<'EOF'
#!/bin/sh
set -e
if [ "$1" = "configure" ]; then
  command -v update-desktop-database >/dev/null 2>&1 && update-desktop-database -q || true
  command -v gtk-update-icon-cache >/dev/null 2>&1 && gtk-update-icon-cache -q -t /usr/share/icons/hicolor || true
  cat <<'MSG'

汐流 XFlow 已安装。

  手动跑一次：      xflow --open
  登录后自启：      systemctl --user enable --now xflow
  开机自启（不等登录）：
      sudo loginctl enable-linger "$USER"

  界面地址：http://127.0.0.1:17777/
  默认下载目录：~/Downloads（可在界面里改）

MSG
fi
EOF

cat > "$PKG/DEBIAN/prerm" <<'EOF'
#!/bin/sh
set -e
if [ "$1" = "remove" ] || [ "$1" = "purge" ]; then
  if command -v systemctl >/dev/null 2>&1; then
    systemctl --user stop xflow.service 2>/dev/null || true
    systemctl --user disable xflow.service 2>/dev/null || true
  fi
fi
EOF
chmod 0755 "$PKG/DEBIAN/postinst" "$PKG/DEBIAN/prerm"

mkdir -p "$OUT"
DEB="$OUT/xflow_${VER}_${ARCH}.deb"
dpkg-deb --root-owner-group --build "$PKG" "$DEB" >/dev/null

echo "产物：$DEB（$(du -h "$DEB" | cut -f1)）"
dpkg-deb --info "$DEB" | sed -n '1,8p'
