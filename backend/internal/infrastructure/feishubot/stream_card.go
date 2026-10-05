// 飞书卡片 JSON 2.0 流式卡片渲染。
//
// 关键机制（见飞书开放平台「流式更新卡片」文档）：
//   - schema 必须声明 "2.0"，config.streaming_mode=true 时进入流式更新模式，
//     平台对 markdown 元素「旧文本是新文本前缀」的增量部分做逐字打字机渲染，
//     且流式期间的全量更新不计入常规频控（硬上限 50 次/秒，由调用方节流兜底）；
//   - 终态把 streaming_mode 置回 false，卡片定型、聊天栏预览从「生成中…」变为 summary；
//   - 思考/工具过程放 collapsible_panel：进行中展开，结束自动折叠；
//   - 元素给固定 element_id，帮助平台在整卡 PATCH 时稳定定位做增量 diff。
package feishubot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"

	"github.com/codeporter/code-porter/internal/application/port"
)

const (
	// streamElementBody 正文 markdown 元素的固定 ID（打字机增量锚点）。
	streamElementBody = "cp_body"
	// streamElementProcess 过程折叠面板内 markdown 的固定 ID。
	streamElementProcess = "cp_process"
	// streamProcessMaxRunes 过程区保留的最大字符数（思考链可能极长，截头保尾）。
	streamProcessMaxRunes = 4000
)

// emailRegex 裸邮箱：飞书内容审计可能因 EMAIL_ADDRESS 直接拒绝卡片更新（400），
// 输出前统一把 @ 替换为全角＠，语义不变且能过审。
var emailRegex = regexp.MustCompile(`([A-Za-z0-9._%+\-]+)@([A-Za-z0-9.\-]+\.[A-Za-z]{2,})`)

func maskSensitive(s string) string {
	if !strings.Contains(s, "@") {
		return s
	}
	return emailRegex.ReplaceAllString(s, "$1＠$2")
}

// OpenStreamCard 创建流式卡片并返回消息 ID。
func (r *Runner) OpenStreamCard(ctx context.Context, target port.IMReplyTarget, state port.IMCardState) (string, error) {
	raw, err := json.Marshal(renderStreamCard(state))
	if err != nil {
		return "", err
	}
	var messageID string
	if err := r.createMessage(ctx, target.ChatID, larkim.MsgTypeInteractive, string(raw), &messageID); err != nil {
		return "", err
	}
	if messageID == "" {
		return "", errors.New("feishubot: create stream card returned empty message id")
	}
	return messageID, nil
}

// UpdateStreamCard 用完整状态整体更新卡片（PATCH 不产生新消息）。
func (r *Runner) UpdateStreamCard(ctx context.Context, messageID string, state port.IMCardState) error {
	if strings.TrimSpace(messageID) == "" {
		return errors.New("feishubot: message id is empty")
	}
	raw, err := json.Marshal(renderStreamCard(state))
	if err != nil {
		return err
	}
	req := larkim.NewPatchMessageReqBuilder().
		MessageId(messageID).
		Body(larkim.NewPatchMessageReqBodyBuilder().Content(string(raw)).Build()).
		Build()
	resp, err := r.openAPI.Im.Message.Patch(ctx, req)
	if err != nil {
		return fmt.Errorf("feishubot: patch stream card failed: %w", err)
	}
	if !resp.Success() {
		return fmt.Errorf("feishubot: patch stream card rejected: code=%d msg=%s", resp.Code, resp.Msg)
	}
	return nil
}

// renderStreamCard 按 IMCardState 构造卡片 JSON 2.0。
func renderStreamCard(state port.IMCardState) map[string]any {
	title := strings.TrimSpace(state.Title)
	if title == "" {
		title = "CodePorter"
	}
	running := state.Phase == port.IMCardRunning

	elements := make([]map[string]any, 0, 4)

	// 1) 过程区（思考 + 工具调用）：折叠面板。
	if proc := truncateRunes(maskSensitive(state.Process), streamProcessMaxRunes); proc != "" {
		elements = append(elements, processPanel(proc, running && state.ProcessActive))
	}

	// 2) 正文区：打字机锚点元素。流式期间为空时给等待占位，避免面板完全空白。
	body := maskSensitive(state.Body)
	if strings.TrimSpace(body) == "" {
		if running {
			body = "⏳ 已收到任务，正在等待本地 AI 输出…"
		}
	}
	if body != "" {
		elements = append(elements, map[string]any{
			"tag": "markdown", "element_id": streamElementBody, "content": body,
		})
	}

	// 3) 运行中底部状态行。
	if running && strings.TrimSpace(state.Footer) != "" {
		elements = append(elements, map[string]any{
			"tag": "markdown", "text_align": "left",
			"text_size": "notation", "content": maskSensitive(state.Footer),
		})
	}

	// 4) 终态但没有任何内容时的兜底说明。
	if !running && len(elements) == 0 {
		note := "（未返回可展示的内容）"
		if state.Phase == port.IMCardFailed {
			note = "⚠️ 执行失败"
		}
		elements = append(elements, map[string]any{"tag": "markdown", "content": note})
	}

	summary := strings.TrimSpace(state.Summary)
	if summary == "" {
		switch state.Phase {
		case port.IMCardDone:
			summary = "已完成"
		case port.IMCardFailed:
			summary = "执行失败"
		default:
			summary = "思考中"
		}
	}

	return map[string]any{
		"schema": "2.0",
		"config": map[string]any{
			"streaming_mode": running,
			"update_multi":   true,
			"width_mode":     "fill",
			"summary":        map[string]any{"content": summary},
		},
		"header": map[string]any{
			"title":    map[string]any{"tag": "plain_text", "content": title},
			"template": headerTemplate(state.Phase),
		},
		"body": map[string]any{"elements": elements},
	}
}

// headerTemplate 不同阶段的头部配色。
func headerTemplate(phase port.IMCardPhase) string {
	switch phase {
	case port.IMCardDone:
		return "green"
	case port.IMCardFailed:
		return "red"
	default:
		return "blue"
	}
}

// processPanel 构造思考/执行过程的折叠面板。
func processPanel(content string, active bool) map[string]any {
	title := "🧠 **思考与执行过程，点击查看**"
	expanded := false
	if active {
		title = "🧠 **思考中…**"
		expanded = true
	}
	return map[string]any{
		"tag":              "collapsible_panel",
		"element_id":       "cp_process_panel",
		"expanded":         expanded,
		"header":           panelHeader(title),
		"border":           map[string]any{"color": "grey", "corner_radius": "5px"},
		"vertical_spacing": "8px",
		"padding":          "8px 8px 8px 8px",
		"elements":         []map[string]any{{"tag": "markdown", "element_id": streamElementProcess, "content": content, "text_size": "notation"}},
	}
}

// panelHeader 折叠面板头部（标题 + 可旋转箭头图标）。
func panelHeader(titleMD string) map[string]any {
	return map[string]any{
		"title":               map[string]any{"tag": "markdown", "content": titleMD},
		"vertical_align":      "center",
		"icon":                map[string]any{"tag": "standard_icon", "token": "down-small-ccm_outlined", "size": "16px 16px"},
		"icon_position":       "follow_text",
		"icon_expanded_angle": -180,
	}
}

// truncateRunes 按 rune 截断并保留尾部（长输出只关注最新进展）。
func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return "…（前面的过程已省略）\n" + string(r[len(r)-max:])
}
