// Package lockfile 提供「同机单实例」心跳锁，约束同一 IM 渠道同一身份
// （飞书 App ID / 企业微信 Bot ID 等）在一台机器上只建立一条长连接。
//
// 为什么需要它：IM 平台通常只允许同一身份保持一个有效连接，重复连接要么导致
// 每条消息被处理多次（飞书多连接并存），要么被平台互相踢下线（企微新连接踢旧连接）。
// 开发版与打包版同时运行、手滑开了两个客户端是常见诱因。
//
// 锁文件放在配置目录，文件名带渠道与身份哈希，因此不同渠道（如飞书 + 企微）、
// 同渠道不同账号可以同时运行，互不阻塞。锁内容带心跳：进程正常/异常退出后
// 心跳停止，锁在 staleAfter 后自动失效，不会永久卡死。
//
// 注意：它只能约束「同一台机器」。两台机器用同一身份上线仍会冲突，
// 那种情况需要在平台侧保证一个机器人只部署一处。
package lockfile

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	heartbeatInterval = 5 * time.Second
	staleAfter        = 20 * time.Second
)

// Lock 单实例锁。
type Lock struct {
	path     string
	channel  string
	identity string

	mu      sync.Mutex
	stopped bool
	stop    chan struct{}
	done    chan struct{}
}

type lockContent struct {
	PID       int    `json:"pid"`
	Channel   string `json:"channel"`
	Identity  string `json:"identity"`
	Host      string `json:"host"`
	UpdatedAt string `json:"updated_at"`
}

// Acquire 在 dir 下创建/占用锁；已被其他存活实例持有时返回错误。
//
// channel 为渠道名（feishu/wecom…），identityID 为平台身份标识（App ID/Bot ID），
// 二者共同决定锁文件名：.imbot-<channel>-<sha1(identity)[:12]>.lock。
func Acquire(dir, channel, identityID string) (*Lock, error) {
	if dir == "" {
		return nil, fmt.Errorf("lockfile: dir is empty")
	}
	if channel == "" || identityID == "" {
		return nil, fmt.Errorf("lockfile: channel and identityID are required")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("lockfile: create lock dir: %w", err)
	}
	path := filepath.Join(dir, lockName(channel, identityID))

	l := &Lock{
		path: path, channel: channel, identity: identityID,
		stop: make(chan struct{}), done: make(chan struct{}),
	}
	if err := l.create(); err != nil {
		return nil, err
	}
	go l.heartbeat()
	return l, nil
}

// create 以 O_CREATE|O_EXCL 原子方式占用锁文件，避免「先检查后写入」
// 在跨进程并发下被双双通过（TOCTOU）。锁文件已存在时检查其中心跳：
// 持有者仍存活则拒绝；陈旧 / 损坏 / 本进程残留则删除后重试一次。
func (l *Lock) create() error {
	for attempt := 0; attempt < 2; attempt++ {
		f, err := os.OpenFile(l.path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			return l.writeTo(f)
		}
		if !errors.Is(err, os.ErrExist) {
			return fmt.Errorf("lockfile: create lock: %w", err)
		}
		held, busyErr := l.heldByAliveInstance()
		if held {
			return busyErr
		}
		if rmErr := os.Remove(l.path); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
			return fmt.Errorf("lockfile: remove stale lock: %w", rmErr)
		}
	}
	return fmt.Errorf("lockfile: lock %s is contended, retry later", l.path)
}

// heldByAliveInstance 判断现存锁是否属于另一个存活实例：
// 内容损坏、时间戳陈旧或属于本进程时均返回 false（可接管）。
func (l *Lock) heldByAliveInstance() (bool, error) {
	raw, err := os.ReadFile(l.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("lockfile: read lock: %w", err)
	}
	var cur lockContent
	if json.Unmarshal(raw, &cur) != nil {
		return false, nil
	}
	t, perr := time.Parse(time.RFC3339, cur.UpdatedAt)
	if perr != nil || time.Since(t) >= staleAfter || cur.PID == os.Getpid() {
		return false, nil
	}
	return true, fmt.Errorf(
		"检测到另一个客户端进程（pid=%d, channel=%s, identity=%s）正在运行该机器人，"+
			"重复连接会导致消息重复处理或被平台踢下线。请先停止另一个客户端；"+
			"若确认没有其他实例（如上次异常退出），删除锁文件后重试：%s",
		cur.PID, cur.Channel, cur.Identity, l.path)
}

// lockName 生成锁文件名：身份做哈希，避免原始 ID 直接落文件名，也防特殊字符问题。
func lockName(channel, identityID string) string {
	sum := sha1.Sum([]byte(channel + "\x00" + identityID))
	return ".imbot-" + channel + "-" + hex.EncodeToString(sum[:])[:12] + ".lock"
}

// Release 停止心跳并删除锁文件。仅当确认占用方资源（任务池）已全部结束时调用。
func (l *Lock) Release() {
	l.stopHeartbeat()
	_ = os.Remove(l.path)
}

// Abandon 停止心跳但保留锁文件：用于停机时仍有任务未能在超时内结束的场景。
// 不立即删锁是为了避免另一个实例马上抢锁、与残留任务形成短暂双连接；
// 心跳停止后锁会在 staleAfter 后自然失效。调用后不应再使用该 Lock。
func (l *Lock) Abandon() {
	l.stopHeartbeat()
}

// stopHeartbeat 幂等地停止心跳 goroutine 并等待其退出。
func (l *Lock) stopHeartbeat() {
	l.mu.Lock()
	if l.stopped {
		l.mu.Unlock()
		return
	}
	l.stopped = true
	close(l.stop)
	l.mu.Unlock()
	<-l.done
}

func (l *Lock) heartbeat() {
	defer close(l.done)
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-l.stop:
			return
		case <-ticker.C:
			if err := l.write(); err != nil {
				// 心跳写失败不致命：最坏情况是 stale 后其他实例可抢锁。
				_ = err
			}
		}
	}
}

func (l *Lock) write() error {
	return os.WriteFile(l.path, l.content(), 0o600)
}

// writeTo 写入原子创建出来的锁文件句柄（首次占用）。
func (l *Lock) writeTo(f *os.File) error {
	defer f.Close()
	if _, err := f.Write(l.content()); err != nil {
		return fmt.Errorf("lockfile: write lock: %w", err)
	}
	return nil
}

func (l *Lock) content() []byte {
	host, _ := os.Hostname()
	raw, err := json.Marshal(lockContent{
		PID: os.Getpid(), Channel: l.channel, Identity: l.identity, Host: host,
		UpdatedAt: time.Now().Format(time.RFC3339),
	})
	if err != nil {
		return nil
	}
	return raw
}
