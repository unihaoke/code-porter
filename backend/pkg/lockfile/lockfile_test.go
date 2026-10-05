package lockfile

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

// writeForeignLock 写入一个「其他进程」持有的新鲜锁（测试不起两个进程，
// 直接伪造 pid 与时间戳；生产中同 pid 重入是允许的）。
func writeForeignLock(dir, channel, identity string, pid int) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	raw, err := json.Marshal(lockContent{
		PID: pid, Channel: channel, Identity: identity, Host: "test",
		UpdatedAt: time.Now().Format(time.RFC3339),
	})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, lockName(channel, identity)), raw, 0o600)
}

func TestAcquireExclusive(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "locks")
	if err := writeForeignLock(dir, "wecom", "bot_x", os.Getpid()+100000); err != nil {
		t.Fatal(err)
	}
	// 他进程心跳新鲜时，本实例必须被拒绝。
	if _, err := Acquire(dir, "wecom", "bot_x"); err == nil {
		t.Fatal("second lock must be rejected while foreign instance is alive")
	}
}

func TestAcquireAndRelease(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "locks")
	l1, err := Acquire(dir, "feishu", "cli_a")
	if err != nil {
		t.Fatalf("first lock failed: %v", err)
	}
	l1.Release()
	// 释放后可再次获取。
	l2, err := Acquire(dir, "feishu", "cli_a")
	if err != nil {
		t.Fatalf("re-lock after release failed: %v", err)
	}
	l2.Release()
}

func TestAcquireStaleReclaim(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "locks")
	if err := writeForeignLock(dir, "feishu", "cli_b", os.Getpid()+100000); err != nil {
		t.Fatal(err)
	}
	// 锁内容已过期（持有者崩溃、心跳停止）：应可抢占。
	if err := touchBack(filepath.Join(dir, lockName("feishu", "cli_b")), staleAfter+time.Second); err != nil {
		t.Fatal(err)
	}
	l2, err := Acquire(dir, "feishu", "cli_b")
	if err != nil {
		t.Fatalf("stale lock should be reclaimable: %v", err)
	}
	l2.Release()
}

// TestAcquireDistinctChannelsDifferentFiles 不同渠道/身份使用不同锁文件，可共存。
func TestAcquireDistinctChannelsDifferentFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "locks")
	l1, err := Acquire(dir, "feishu", "cli_a")
	if err != nil {
		t.Fatal(err)
	}
	defer l1.Release()
	l2, err := Acquire(dir, "wecom", "bot_a")
	if err != nil {
		t.Fatalf("different channels must not block each other: %v", err)
	}
	defer l2.Release()
	l3, err := Acquire(dir, "feishu", "cli_other")
	if err != nil {
		t.Fatalf("different identities must not block each other: %v", err)
	}
	defer l3.Release()
}

// TestAcquireRejectsBadArgs 空参数快速失败，不创建任何文件。
func TestAcquireRejectsBadArgs(t *testing.T) {
	if _, err := Acquire(t.TempDir(), "", "id"); err == nil {
		t.Fatal("empty channel must fail")
	}
	if _, err := Acquire(t.TempDir(), "feishu", ""); err == nil {
		t.Fatal("empty identity must fail")
	}
}

// TestAcquireCorruptLockReclaim 锁文件内容损坏时应被当作陈旧锁直接接管。
func TestAcquireCorruptLockReclaim(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "locks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, lockName("wecom", "bot_c"))
	if err := os.WriteFile(path, []byte("{not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := Acquire(dir, "wecom", "bot_c")
	if err != nil {
		t.Fatalf("corrupt lock must be reclaimable: %v", err)
	}
	l.Release()
}

// TestAbandonKeepsFile Abandon 只停心跳不删文件（留给 stale 自然过期），
// 且与 Release 一样可安全重复调用。
func TestAbandonKeepsFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "locks")
	l, err := Acquire(dir, "feishu", "cli_d")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, lockName("feishu", "cli_d"))
	l.Abandon()
	l.Abandon() // 幂等：不 panic、不卡死
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("abandon must keep lock file for stale expiry: %v", err)
	}
}
