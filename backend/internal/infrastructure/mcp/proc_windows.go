//go:build windows

package mcp

import (
	"syscall"

	"golang.org/x/sys/windows"
)

// stdioSysProcAttr 返回启动 stdio 子进程所需的 SysProcAttr。
// 在 Windows 上设置 CREATE_NO_WINDOW，避免每个 AI 工具子进程（Trae / Claude Code /
// CodeBuddy / Codex 等控制台程序）双击运行客户端时各自弹出一个黑框。
func stdioSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
}
