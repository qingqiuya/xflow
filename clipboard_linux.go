//go:build !windows

package main

// Linux 剪贴板：借 xclip（X11）。没装就明确报错，别静默返回空串。

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func clipboardRead() (string, error) {
	if _, err := exec.LookPath("xclip"); err != nil {
		return "", fmt.Errorf("缺 xclip，装一下：sudo apt install xclip")
	}
	cmd := exec.Command("xclip", "-selection", "clipboard", "-o")
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		if os.Getenv("DISPLAY") == "" {
			return "", fmt.Errorf("没有图形环境（DISPLAY 为空）")
		}
		if strings.Contains(err.Error(), "exit status") {
			return "", nil // 剪贴板是空的
		}
		return "", fmt.Errorf("读剪贴板失败：%v %s", err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

func clipboardWrite(text string) error {
	if _, err := exec.LookPath("xclip"); err != nil {
		return fmt.Errorf("缺 xclip，装一下：sudo apt install xclip")
	}
	cmd := exec.Command("xclip", "-selection", "clipboard", "-i")
	cmd.Stdin = strings.NewReader(text)
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		if os.Getenv("DISPLAY") == "" {
			return fmt.Errorf("没有图形环境（DISPLAY 为空）")
		}
		return fmt.Errorf("写剪贴板失败：%v %s", err, strings.TrimSpace(errb.String()))
	}
	return nil
}
