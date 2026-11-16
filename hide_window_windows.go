//go:build windows

package main

import (
	"os/exec"
	"syscall"
)

// hideWindow 让子进程别弹黑框（yt-dlp / ffmpeg）。
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}
