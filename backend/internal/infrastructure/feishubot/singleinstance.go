package feishubot

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// 同一 App ID 的长连接如果在多个进程里同时建立（例如开发版与打包版同时运行、
// 手滑开了两个客户端），每条消息会被处理多次、回复也会重复。
//
// FileLock 在配置目录维护一个带心跳的锁文件做「同机单实例」约束：
// 启动时若锁存在且心跳新鲜则拒绝启动并报告占用方 PID；进程正常/异常退出后
// 心跳停止，锁在 staleAfter 后自动失效，不会永久卡死。
//
// 注意：它只能约束「同一台机器」。两台不同机器用同一 App ID 上线仍会重复，
// 那种情况需要在平台侧保证一个应用只部署一处（见 docs/bot-setup.md 排错说明）。

const (
	lockHeartbeat  = 5 * time.Second
	lockStaleAfter = 20 * time.Second
)

// FileLock 机器人单实例锁。
type FileLock struct {
	path string
	appID string

	mu      sync.Mutex
	stopped bool
	stop    chan struct{}
	done    chan struct{}
}

type lockContent struct {
	PID       int    `json:"pid"`
	AppID     string `json:"app_id"`
	Host      string `json:"host"`
	UpdatedAt string `json:"updated_at"`
}

// LockFile 在 dir 下创建/占用锁文件；已被其他存活实例持有时返回错误。
func LockFile(dir, appID string) (*FileLock, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("feishubot: create lock dir: %w", err)
	}
	path := filepath.Join(dir, ".feishu-bot.lock")

	if raw, err := os.ReadFile(path); err == nil {
		var cur lockContent
		if json.Unmarshal(raw, &cur) == nil {
			if t, perr := time.Parse(time.RFC3339, cur.UpdatedAt); perr == nil &&
				time.Since(t) < lockStaleAfter && cur.PID != os.Getpid() {
				return nil, fmt.Errorf(
					"feishubot: 检测到另一个客户端进程（pid=%d, app_id=%s）正在运行该飞书机器人，"+
						"重复连接会导致每条消息被处理多次。请先停止另一个客户端；"+
						"若确认没有其他实例（如上次异常退出），删除锁文件后重试：%s",
					cur.PID, cur.AppID, path)
			}
		}
	}

	l := &FileLock{path: path, appID: appID, stop: make(chan struct{}), done: make(chan struct{})}
	if err := l.write(); err != nil {
		return nil, err
	}
	go l.heartbeat()
	return l, nil
}

// Release 停止心跳并删除锁文件。
func (l *FileLock) Release() {
	l.mu.Lock()
	if l.stopped {
		l.mu.Unlock()
		return
	}
	l.stopped = true
	close(l.stop)
	l.mu.Unlock()
	<-l.done
	_ = os.Remove(l.path)
}

func (l *FileLock) heartbeat() {
	defer close(l.done)
	ticker := time.NewTicker(lockHeartbeat)
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

func (l *FileLock) write() error {
	host, _ := os.Hostname()
	raw, err := json.Marshal(lockContent{
		PID: os.Getpid(), AppID: l.appID, Host: host,
		UpdatedAt: time.Now().Format(time.RFC3339),
	})
	if err != nil {
		return err
	}
	return os.WriteFile(l.path, raw, 0o600)
}
