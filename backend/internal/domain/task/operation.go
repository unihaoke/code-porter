package task

import "strings"

// Operation 代码操作类型，用于 MCP 适配器选择更合适的提示词模板与工具。
type Operation string

const (
	// OperationGenerate 代码生成。
	OperationGenerate Operation = "generate"
	// OperationRefactor 代码重构。
	OperationRefactor Operation = "refactor"
	// OperationDebug 排查缺陷（查 bug / 内存泄漏等）。
	OperationDebug Operation = "debug"
	// OperationExplain 解释代码。
	OperationExplain Operation = "explain"
	// OperationReview 代码评审。
	OperationReview Operation = "review"
	// OperationChat 通用对话（默认）。
	OperationChat Operation = "chat"
)

// ParseOperation 解析操作类型，空值或未知回退为 OperationChat。
func ParseOperation(s string) Operation {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "generate", "code", "gen":
		return OperationGenerate
	case "refactor":
		return OperationRefactor
	case "debug", "bug", "fix":
		return OperationDebug
	case "explain":
		return OperationExplain
	case "review":
		return OperationReview
	}
	return OperationChat
}

// String 返回操作名。
func (o Operation) String() string { return string(o) }
