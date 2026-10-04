package http

// QueueStats 运维/控制台读取队列快照所需的最小能力。
type QueueStats interface {
	Snapshot() map[string]int
}

// connCounter WebSocket 连接计数。
type connCounter interface {
	ConnCount() int
}
