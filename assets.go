package main

import _ "embed"

// 界面背景图（清秋 2026-11-16 提供的猫耳少女壁纸，压成 58KB JPEG）
//
//go:embed assets/bg.jpg
var bgJPG []byte

// 界面左上角的小图标（和 exe 图标同一张）
//
//go:embed assets/icon.png
var iconPNG []byte
