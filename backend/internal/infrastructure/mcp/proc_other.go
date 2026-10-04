//go:build !windows

package mcp

import "syscall"

// stdioSysProcAttr 在非 Windows 平台返回 nil（无需特殊处理子进程窗口）。
func stdioSysProcAttr() *syscall.SysProcAttr {
	return nil
}
