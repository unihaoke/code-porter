//go:build windows

package mcp

import (
	"os/exec"
	"strconv"
	"syscall"

	"golang.org/x/sys/windows"
)

// stdioSysProcAttr 返回启动 stdio 子进程所需的 SysProcAttr。
// 在 Windows 上设置 CREATE_NO_WINDOW，避免每个 AI 工具子进程（Trae / Claude Code /
// CodeBuddy / Codex 等控制台程序）双击运行客户端时各自弹出一个黑框。
func stdioSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
}

// killProcessTree 终止指定进程及其所有子孙进程。
//
// 必要性：标准库在 context 取消时只 kill 直接子进程。而 codex 这类 CLI 是
// 「启动器 → 真正的工作进程」两级结构（node codex.js 会再派生 codex.exe），
// 孙进程存活时会继续持有继承来的 stdout/stderr 管道句柄，使父进程侧读管道
// 永远等不到 EOF —— 表现为任务停止后 inflight 计数不归零。
//
// taskkill 是 Windows 自带命令：/T 终止进程树，/F 强制结束。
// 这是 best-effort 清理：进程若已退出会返回非 0，此时视为成功。
func killProcessTree(pid int) error {
	if pid <= 0 {
		return nil
	}
	cmd := exec.Command("taskkill", "/PID", strconv.Itoa(pid), "/T", "/F")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
	if err := cmd.Run(); err != nil {
		// 进程可能已经正常退出，或权限不足；交由调用方的 WaitDelay 兜底。
		return err
	}
	return nil
}
