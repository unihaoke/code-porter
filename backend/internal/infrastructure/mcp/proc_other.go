//go:build !windows

package mcp

import "syscall"

// stdioSysProcAttr 在非 Windows 平台返回 nil（无需特殊处理子进程窗口）。
func stdioSysProcAttr() *syscall.SysProcAttr {
	return nil
}

// killProcessTree 终止指定进程及其子进程组（非 Windows 平台）。
//
// 子进程由 exec 启动时位于同一进程组，对进程组取负号发送 SIGKILL 即可整组终止，
// 避免孙进程（如 codex.exe）继续持有管道句柄导致 Wait 永久阻塞。
// 这是 best-effort 清理：进程若已退出会返回 ESRCH，此时视为成功。
func killProcessTree(pid int) error {
	if pid <= 0 {
		return nil
	}
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		return err
	}
	return nil
}
