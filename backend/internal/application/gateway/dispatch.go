package gateway

import (
	"time"

	"github.com/codeporter/code-porter/internal/application/port"
	"github.com/codeporter/code-porter/internal/domain/task"
)

// toDispatch 把任务聚合适配为下发给 Agent 的传输结构。
func toDispatch(t *task.Task, mcpTimeout time.Duration) port.TaskDispatch {
	return port.TaskDispatch{
		TaskID:         string(t.ID()),
		AgentID:        string(t.AgentID()),
		Model:          t.Model().String(),
		Stream:         t.Stream(),
		Prompt:         t.Request().FlattenPrompt(),
		Messages:       t.Request().Messages,
		Files:          t.Request().Files,
		Operation:      t.Request().Operation.String(),
		WorkDir:        t.Request().WorkDir,
		Temperature:    t.Request().Temperature,
		MaxTokens:      t.Request().MaxTokens,
		Permission:     t.Request().Permission.String(),
		LockToken:      t.LockToken(),
		Attempt:        t.Attempts(),
		CreatedAt:      t.CreatedAt(),
		TimeoutSeconds: int(mcpTimeout.Seconds()),
	}
}
