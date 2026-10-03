package task

import (
	"strings"
	"time"
)

// Chunk 一段流式输出（MCP → LocalAgent → Gateway → 调用方）。
type Chunk struct {
	// Seq 单调递增序号，用于网关侧去重/排序。
	Seq int `json:"seq"`
	// Content 文本增量。
	Content string `json:"content"`
	// At 产生时间。
	At time.Time `json:"at"`
}

// NewChunk 构造片段。
func NewChunk(seq int, content string) Chunk {
	return Chunk{Seq: seq, Content: content, At: time.Now()}
}

// Chunks 片段集合，支持拼接为完整结果。
type Chunks []Chunk

// Text 按顺序拼接全部内容。
func (cs Chunks) Text() string {
	var b strings.Builder
	for _, c := range cs {
		b.WriteString(c.Content)
	}
	return b.String()
}

// LastSeq 返回最大序号。
func (cs Chunks) LastSeq() int {
	max := 0
	for _, c := range cs {
		if c.Seq > max {
			max = c.Seq
		}
	}
	return max
}
