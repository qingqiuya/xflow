package main

import (
	"os"
	"path/filepath"
	"strings"
)

var defaultDirOverride string

func mustHome() string {
	h, _ := os.UserHomeDir()
	return h
}

func currentDir() string {
	if defaultDirOverride != "" {
		return defaultDirOverride
	}
	if b, err := os.ReadFile(filepath.Join(mustHome(), ".localdl-dir")); err == nil {
		if d := strings.TrimSpace(string(b)); d != "" {
			return d
		}
	}
	d := filepath.Join(mustHome(), "Downloads")
	os.MkdirAll(d, 0755)
	return d
}

func saveDefaultDir(d string) {
	os.WriteFile(filepath.Join(mustHome(), ".localdl-dir"), []byte(d), 0600)
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}
