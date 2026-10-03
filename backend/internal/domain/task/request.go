package task

import "strings"

// Message 标准化对话消息（领域内部表示，与传输层 DTO 解耦）。
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// CodeFile 代码上下文文件（MVP 只读，不落盘）。
type CodeFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// Request 任务的标准化入参，是 MCP 适配层唯一依赖的输入结构。
type Request struct {
	// Prompt 扁平化后的完整提示词。
	Prompt string `json:"prompt"`
	// Messages 原始多轮消息。
	Messages []Message `json:"messages,omitempty"`
	// Files 附带的代码上下文。
	Files []CodeFile `json:"files,omitempty"`
	// Operation 操作类型。
	Operation Operation `json:"operation"`
	// WorkDir 本地工作目录，供 MCP 工具定位项目。
	WorkDir string `json:"work_dir,omitempty"`
	// Temperature 采样温度，nil 表示由 AI 工具自行决定。
	Temperature *float64 `json:"temperature,omitempty"`
	// MaxTokens 最大输出长度。
	MaxTokens int `json:"max_tokens,omitempty"`
}

// FlattenPrompt 当 Prompt 为空时，用 Messages 拼接出提示词。
func (r Request) FlattenPrompt() string {
	if strings.TrimSpace(r.Prompt) != "" {
		return r.Prompt
	}
	var b strings.Builder
	for _, m := range r.Messages {
		if m.Content == "" {
			continue
		}
		b.WriteString(m.Role)
		b.WriteString(": ")
		b.WriteString(m.Content)
		b.WriteString("\n")
	}
	return b.String()
}
