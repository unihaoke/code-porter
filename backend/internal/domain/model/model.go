// Package model 定义「本地 AI 编码工具」这一值对象，是任务路由到具体 MCP 适配器的依据。
package model

import (
	"strings"

	"github.com/codeporter/code-porter/pkg/apperr"
)

// Model 本地 AI 工具标识（对应请求体中的 model 字段）。
type Model string

const (
	Trae       Model = "trae"
	ClaudeCode Model = "claude-code"
	CodeBuddy  Model = "codebuddy"
	Codex      Model = "codex"
)

// alias 常用别名，提升外部调用兼容性。
var alias = map[string]Model{
	"trae":          Trae,
	"trae-cn":       Trae,
	"claude":        ClaudeCode,
	"claudecode":    ClaudeCode,
	"claude_code":   ClaudeCode,
	"claude-code":   ClaudeCode,
	"codebuddy":     CodeBuddy,
	"code-buddy":    CodeBuddy,
	"codebuddy-ide": CodeBuddy,
	"codex":         Codex,
	"codex-cli":     Codex,
}

// Parse 解析 model 字符串，未知模型返回 apperr.CodeInvalidParam。
func Parse(s string) (Model, error) {
	key := strings.ToLower(strings.TrimSpace(s))
	if key == "" {
		return "", apperr.Wrap(apperr.CodeInvalidParam, "model is required", nil)
	}
	if m, ok := alias[key]; ok {
		return m, nil
	}
	return "", apperr.Wrap(apperr.CodeInvalidParam, "unsupported model: "+s, nil)
}

// String 返回规范名。
func (m Model) String() string { return string(m) }

// All 返回全部受支持的模型。
func All() []Model { return []Model{Trae, ClaudeCode, CodeBuddy, Codex} }
