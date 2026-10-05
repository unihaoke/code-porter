package feishubot

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// touchBack 把锁内容里的心跳时间置为过去，模拟持有者崩溃后心跳停止。
func touchBack(path string, age time.Duration) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var c lockContent
	if err := json.Unmarshal(raw, &c); err != nil {
		return err
	}
	c.UpdatedAt = time.Now().Add(-age).Format(time.RFC3339)
	out, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o600)
}

// writeForeignLock 写入一个「其他进程」持有的新鲜锁（测试不能真的起两个进程，
// 直接伪造 pid 与时间戳；生产中同 pid 重入是允许的）。
func writeForeignLock(dir string, pid int) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	raw, err := json.Marshal(lockContent{
		PID: pid, AppID: "cli_x", Host: "test",
		UpdatedAt: time.Now().Format(time.RFC3339),
	})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, ".feishu-bot.lock"), raw, 0o600)
}

func TestFileLockExclusive(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "locks")
	if err := writeForeignLock(dir, os.Getpid()+100000); err != nil {
		t.Fatal(err)
	}
	// 他进程心跳新鲜时，本实例必须被拒绝。
	if _, err := LockFile(dir, "cli_a"); err == nil {
		t.Fatal("second lock must be rejected while foreign instance is alive")
	}
}

func TestFileLockAcquireAndRelease(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "locks")
	l1, err := LockFile(dir, "cli_a")
	if err != nil {
		t.Fatalf("first lock failed: %v", err)
	}
	l1.Release()
	// 释放后可再次获取。
	l2, err := LockFile(dir, "cli_a")
	if err != nil {
		t.Fatalf("re-lock after release failed: %v", err)
	}
	l2.Release()
}

func TestFileLockStaleReclaim(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "locks")
	if err := writeForeignLock(dir, os.Getpid()+100000); err != nil {
		t.Fatal(err)
	}
	// 锁内容已过期（持有者崩溃、心跳停止）：应可抢占。
	if err := touchBack(filepath.Join(dir, ".feishu-bot.lock"), lockStaleAfter+time.Second); err != nil {
		t.Fatal(err)
	}
	l2, err := LockFile(dir, "cli_b")
	if err != nil {
		t.Fatalf("stale lock should be reclaimable: %v", err)
	}
	l2.Release()
}
