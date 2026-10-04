package gateway

import (
	"context"

	"github.com/codeporter/code-porter/internal/application/port"
	"github.com/codeporter/code-porter/internal/domain/agent"
	"github.com/codeporter/code-porter/internal/domain/model"
	"github.com/codeporter/code-porter/internal/domain/task"
	"github.com/codeporter/code-porter/internal/domain/user"
)

// ChatCommand 网页对话请求。
type ChatCommand struct {
	// OwnerID 租户（登录用户）。
	OwnerID user.ID
	// APIKeyID 调用方标识（网页端传 "web"）。
	APIKeyID string
	// AgentID 目标 Agent，空表示默认。
	AgentID agent.ID
	// Model 目标本地 AI 工具，空表示使用网关默认。
	Model model.Model
	// Messages 完整对话历史（多轮由前端维护并全量回传）。
	Messages []task.Message
	// Mode 投递通路。
	Mode task.DeliveryMode
	// Stream 是否流式。
	Stream bool
	// WorkDir 本地工作目录。
	WorkDir string
	// Temperature 采样温度。
	Temperature *float64
	// MaxTokens 最大输出长度。
	MaxTokens int
}

// ChatUseCase 网页对话用例：把控制台 / 网页端的对话请求翻译成任务。
//
// 它刻意做得很薄——只负责补齐默认模型与通路，
// 真正的路由、入队、事件订阅全部复用 SubmitTaskUseCase，避免两套逻辑漂移。
type ChatUseCase struct {
	submit       *SubmitTaskUseCase
	defaultModel model.Model
	log          port.Logger
}

// NewChatUseCase 构造用例。
func NewChatUseCase(submit *SubmitTaskUseCase, defaultModel model.Model, log port.Logger) *ChatUseCase {
	return &ChatUseCase{
		submit:       submit,
		defaultModel: defaultModel,
		log:          log.With(port.F("uc", "chat")),
	}
}

// Execute 提交一次网页对话。
func (u *ChatUseCase) Execute(ctx context.Context, cmd ChatCommand) (*SubmitTaskResult, error) {
	m := cmd.Model
	if m == "" {
		m = u.defaultModel
	}
	if m == "" {
		m = model.ClaudeCode
	}
	mode := cmd.Mode
	if mode == "" {
		mode = task.ModePull
	}
	return u.submit.Execute(ctx, SubmitTaskCommand{
		OwnerID:     cmd.OwnerID,
		APIKeyID:    cmd.APIKeyID,
		AgentID:     cmd.AgentID,
		Model:       m,
		Messages:    cmd.Messages,
		Mode:        mode,
		Stream:      cmd.Stream,
		WorkDir:     cmd.WorkDir,
		Temperature: cmd.Temperature,
		MaxTokens:   cmd.MaxTokens,
	})
}
