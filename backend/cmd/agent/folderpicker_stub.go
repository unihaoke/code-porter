//go:build !windows

package main

import "fmt"

// pickFolder 非 Windows 平台不支持目录选择对话框（GUI 本身也仅 Windows）。
func pickFolder(owner uintptr, title, initialDir string) (string, error) {
	return "", fmt.Errorf("目录选择对话框仅在 Windows 客户端中可用")
}
