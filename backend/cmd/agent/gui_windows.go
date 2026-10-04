//go:build windows

package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
	"golang.org/x/sys/windows"

	"github.com/codeporter/code-porter/internal/application/port"
	"github.com/codeporter/code-porter/internal/infrastructure/config"
	"github.com/codeporter/code-porter/internal/infrastructure/logging"
)

// chanWriter 把日志行推送到带缓冲的 channel，由 UI 线程安全地写入文本框。
type chanWriter struct {
	ch chan string
}

func (w *chanWriter) Write(p []byte) (int, error) {
	s := string(p)
	for _, line := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		if line == "" {
			continue
		}
		select {
		case w.ch <- line + "\n":
		default:
		}
	}
	return len(p), nil
}

// hideConsole 在 Windows 上让进程脱离控制台窗口。
// 即使 exe 是按「控制台子系统」编译（漏加 -H windowsgui），双击运行时 Windows
// 也会附带一个黑框；调用 FreeConsole 可将其关闭，只保留原生 GUI 窗口。
// 若 exe 已是「GUI 子系统」（带 -H windowsgui），本调用无控制台可脱离，安全无副作用。
func hideConsole() {
	kernel32 := windows.NewLazySystemDLL("kernel32.dll")
	if p := kernel32.NewProc("FreeConsole"); p.Find() == nil {
		p.Call()
	}
}

// runGUI 在 Windows 上弹出原生配置窗口，双击 codeporter-agent.exe 即进入此路径。
func runGUI(configPath string) error {
	hideConsole()

	cfg, err := loadConfig(configPath)
	if err != nil {
		return err
	}

	logCh := make(chan string, 1024)
	writer := &chanWriter{ch: logCh}
	log := logging.New(writer, logging.ParseLevel(cfg.Log.Level))

	var (
		mw        *walk.MainWindow
		inGwAddr  *walk.LineEdit
		inAgentID *walk.LineEdit
		inKey     *walk.LineEdit
		inName    *walk.LineEdit
		inAnth    *walk.LineEdit
		inOpen    *walk.LineEdit
		cmbLevel  *walk.ComboBox
		chkDirect *walk.CheckBox
		spinConc  *walk.NumberEdit
		inWorkDir *walk.LineEdit

		chkFeishu        *walk.CheckBox
		inFeishuAppID    *walk.LineEdit
		inFeishuSecret   *walk.LineEdit
		chkFeishuMention *walk.CheckBox
		cmbFeishuModel   *walk.ComboBox

		btnTest *walk.PushButton
		logEdit *walk.TextEdit
	)

	var svc *Service
	var svcMu sync.Mutex

	// 把控件当前值写回配置（仅覆盖 GUI 管理的字段，其余保持原样）。
	applyWidgets := func() {
		cfg.Gateway.Addr = strings.TrimSpace(inGwAddr.Text())
		cfg.Agent.ID = strings.TrimSpace(inAgentID.Text())
		cfg.Agent.Key = inKey.Text()
		cfg.Agent.Name = strings.TrimSpace(inName.Text())
		cfg.Log.Level = cmbLevel.Text()
		cfg.Secrets.AnthropicAPIKey = inAnth.Text()
		cfg.Secrets.OpenAIAPIKey = inOpen.Text()
		cfg.Direct.Enabled = chkDirect.Checked()
		cfg.WorkerPool.MaxConcurrency = int(spinConc.Value())
		cfg.MCP.WorkDir = strings.TrimSpace(inWorkDir.Text())
		// 飞书机器人（GUI 管理字段）。
		cfg.Bots.Feishu.Enabled = chkFeishu.Checked()
		cfg.Bots.Feishu.AppID = strings.TrimSpace(inFeishuAppID.Text())
		cfg.Bots.Feishu.AppSecret = inFeishuSecret.Text()
		cfg.Bots.Feishu.MentionOnly = chkFeishuMention.Checked()
		if m := cmbFeishuModel.Text(); m == "自动（claude-code）" {
			cfg.Bots.Feishu.Model = ""
		} else {
			cfg.Bots.Feishu.Model = m
		}
		// MCP.Env 是运行时从 secrets 派生的，不持久化到 yaml。
		cfg.MCP.Env = nil
	}

	saveConfig := func() error {
		applyWidgets()
		if err := config.SaveAgent(configPath, cfg); err != nil {
			return err
		}
		log.Info("agent config saved", port.F("path", configPath))
		return nil
	}

	startAgent := func() {
		svcMu.Lock()
		running := svc != nil && svc.Running()
		svcMu.Unlock()
		if running {
			walk.MsgBox(mw, "提示", "代理已在运行。修改配置请先点「停止」。", walk.MsgBoxOK)
			return
		}
		if err := saveConfig(); err != nil {
			walk.MsgBox(mw, "保存失败", err.Error(), walk.MsgBoxIconError)
			return
		}
		svcMu.Lock()
		svc = NewService(configPath, cfg, log)
		svcMu.Unlock()
		if err := svc.Start(); err != nil {
			walk.MsgBox(mw, "启动失败", err.Error(), walk.MsgBoxIconError)
			return
		}
		walk.MsgBox(mw, "已启动", "CodePorter 本地代理已启动，运行日志见下方。", walk.MsgBoxOK)
	}

	stopAgent := func() {
		svcMu.Lock()
		cur := svc
		svcMu.Unlock()
		if cur == nil || !cur.Running() {
			walk.MsgBox(mw, "提示", "代理未运行。", walk.MsgBoxOK)
			return
		}
		go func() {
			cur.Stop()
			walk.MsgBox(mw, "已停止", "CodePorter 本地代理已停止。", walk.MsgBoxOK)
		}()
	}

	// testCLI 后台执行连通性测试：先做安装检测，再对已启用工具发起一次极短的真实调用。
	// 全程在 goroutine 中进行，绝不阻塞 UI 线程。
	testCLI := func() {
		// 用界面上当前的配置快照来测，改了参数不用先保存也能测。
		snapshot := *cfg
		applyWidgets()
		snapshot = *cfg

		btnTest.SetEnabled(false)
		btnTest.SetText("测试中…")
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			results := TestCLIConnections(ctx, &snapshot, log, CLIConnTestOptions{
				Timeout: 90 * time.Second,
				Probe:   cliTestProbePrompt,
			})
			ok, missing, warned, failed := summarizeCLIConn(results)
			summary := FormatCLIConnReport(results)
			mw.Synchronize(func() {
				for _, line := range strings.Split(strings.TrimRight(summary, "\r\n"), "\n") {
					log.Info(line)
				}
				btnTest.SetEnabled(true)
				btnTest.SetText("测试 CLI 连接")
				var msg string
				switch {
				case ok > 0 && failed == 0 && missing == 0 && warned == 0:
					msg = fmt.Sprintf("全部正常：%d 个工具可用。", ok)
				default:
					msg = fmt.Sprintf("可用 %d 个。\r\n未安装 %d · 需处理 %d · 失败 %d\r\n\r\n"+
						"「额度/配额不足」说明调用链已通，只是账号额度用尽（无需配置密钥）。\r\n"+
						"详细信息见下方运行日志。", ok, missing, warned, failed)
				}
				icon := walk.MsgBoxIconWarning
				if ok > 0 && failed == 0 && missing == 0 && warned == 0 {
					icon = walk.MsgBoxIconInformation
				}
				walk.MsgBox(mw, "本地 AI 连通性测试", msg, icon)
			})
		}()
	}

	builder := MainWindow{
		AssignTo: &mw,
		Title:    "CodePorter 本地代理",
		MinSize:  Size{Width: 660, Height: 620},
		Layout:   VBox{Spacing: 8, Margins: Margins{Left: 12, Top: 12, Right: 12, Bottom: 12}},
		Children: []Widget{
			GroupBox{
				Title:  "网关连接",
				Layout: Grid{Columns: 2, Spacing: 8, Margins: Margins{Left: 10, Top: 10, Right: 10, Bottom: 10}},
				Children: []Widget{
					Label{Text: "网关地址:"},
					LineEdit{AssignTo: &inGwAddr, Text: cfg.Gateway.Addr},
					Label{Text: "连接秘钥（控制台创建）:"},
					LineEdit{AssignTo: &inKey, Text: cfg.Agent.Key, PasswordMode: true},
					Label{Text: "机器名:"},
					LineEdit{AssignTo: &inName, Text: cfg.Agent.Name},
					Label{Text: "实例 ID（自动生成，勿改）:"},
					LineEdit{AssignTo: &inAgentID, Text: cfg.Agent.ID, ReadOnly: true},
					Label{Text: "日志级别:"},
					ComboBox{AssignTo: &cmbLevel, Value: cfg.Log.Level, Editable: false,
						Model: []string{"debug", "info", "warn", "error"}},
				},
			},
			GroupBox{
				Title:  "AI 密钥（可选：CLI 模式通常无需填写）",
				Layout: Grid{Columns: 2, Spacing: 8, Margins: Margins{Left: 10, Top: 10, Right: 10, Bottom: 10}},
				Children: []Widget{
					Label{Text: "Anthropic API Key:"},
					LineEdit{AssignTo: &inAnth, Text: cfg.Secrets.AnthropicAPIKey},
					Label{Text: "OpenAI API Key:"},
					LineEdit{AssignTo: &inOpen, Text: cfg.Secrets.OpenAIAPIKey},
					Label{Text: ""},
					Label{
						Text: "留空即可：mode=cli 时 Claude Code / Codex 等走本机登录态（订阅），不需密钥。" +
							"仅当使用 --bare 或显式指定第三方 API Key 时才填。",
					},
				},
			},
			GroupBox{
				Title:  "运行参数",
				Layout: Grid{Columns: 2, Spacing: 8, Margins: Margins{Left: 10, Top: 10, Right: 10, Bottom: 10}},
				Children: []Widget{
					Label{Text: "最大并发任务数:"},
					NumberEdit{AssignTo: &spinConc, Value: float64(cfg.WorkerPool.MaxConcurrency),
						MinValue: 1, MaxValue: 16, SpinButtonsVisible: true},
					CheckBox{AssignTo: &chkDirect, Text: "启用 SSE 直连模式（低延迟交互式对话）",
						Checked: cfg.Direct.Enabled, ColumnSpan: 2},
				},
			},
			GroupBox{
				Title:  "本地 AI（CLI 在此目录下工作）",
				Layout: Grid{Columns: 2, Spacing: 8, Margins: Margins{Left: 10, Top: 10, Right: 10, Bottom: 10}},
				Children: []Widget{
					Label{Text: "工作目录:"},
					Composite{
						Layout: HBox{Spacing: 6},
						Children: []Widget{
							LineEdit{AssignTo: &inWorkDir, Text: cfg.MCP.WorkDir, StretchFactor: 1},
							PushButton{Text: "浏览…", OnClicked: func() {
								cur := strings.TrimSpace(inWorkDir.Text())
								dir, err := pickFolder(0, "选择 AI CLI 的工作目录", cur)
								if err != nil {
									walk.MsgBox(mw, "选择失败", err.Error(), walk.MsgBoxIconError)
									return
								}
								if dir == "" {
									return // 用户取消
								}
								inWorkDir.SetText(dir)
								log.Info("已选择工作目录: " + dir)
							}},
							PushButton{Text: "默认", OnClicked: func() {
								inWorkDir.SetText("")
								log.Info("工作目录已清空，将使用客户端 exe 所在目录: " + defaultClientWorkDir())
							}},
						},
					},
					Label{Text: ""},
					Label{
						Text: "留空则使用客户端 exe 所在目录。Claude Code / Codex 会在此目录中读写文件、执行命令。",
					},
				},
			},
			GroupBox{
				Title:  "飞书机器人（可选：长连接直连，无需公网域名）",
				Layout: Grid{Columns: 2, Spacing: 8, Margins: Margins{Left: 10, Top: 10, Right: 10, Bottom: 10}},
				Children: []Widget{
					CheckBox{AssignTo: &chkFeishu, Text: "启用飞书机器人（消息直接在本机处理并回复）",
						Checked: cfg.Bots.Feishu.Enabled, ColumnSpan: 2},
					Label{Text: "App ID:"},
					LineEdit{AssignTo: &inFeishuAppID, Text: cfg.Bots.Feishu.AppID},
					Label{Text: "App Secret:"},
					LineEdit{AssignTo: &inFeishuSecret, Text: cfg.Bots.Feishu.AppSecret, PasswordMode: true},
					Label{Text: "处理模型:"},
					ComboBox{AssignTo: &cmbFeishuModel, Editable: false,
						Value: feishuModelLabel(cfg.Bots.Feishu.Model),
						Model: []string{"自动（claude-code）", "claude-code", "trae", "codebuddy", "codex"}},
					CheckBox{AssignTo: &chkFeishuMention, Text: "群聊中仅响应 @机器人 的消息（私聊始终响应）",
						Checked: cfg.Bots.Feishu.MentionOnly, ColumnSpan: 2},
					Label{Text: ""},
					Label{
						Text: "前置：飞书开放平台企业自建应用开启「机器人」能力，事件订阅选择" +
							"「使用长连接接收事件」并添加 im.message.receive_v1，然后发布版本。",
					},
				},
			},
			Composite{
				Layout: HBox{Spacing: 8},
				Children: []Widget{
					PushButton{AssignTo: &btnTest, Text: "测试 CLI 连接", OnClicked: testCLI},
					PushButton{Text: "保存并启动", OnClicked: startAgent},
					PushButton{Text: "停止", OnClicked: stopAgent},
					PushButton{Text: "仅保存配置", OnClicked: func() {
						if err := saveConfig(); err != nil {
							walk.MsgBox(mw, "保存失败", err.Error(), walk.MsgBoxIconError)
							return
						}
						walk.MsgBox(mw, "已保存", "配置已写入 "+configPath, walk.MsgBoxOK)
					}},
					HSpacer{},
					PushButton{Text: "退出", OnClicked: func() { mw.Close() }},
				},
			},
			Label{Text: "运行日志"},
			TextEdit{
				AssignTo:      &logEdit,
				ReadOnly:      true,
				VScroll:       true,
				StretchFactor: 1,
				MinSize:       Size{Height: 140},
				Text: "提示：填好网关地址与令牌 → 可先点「测试 CLI 连接」验证本地 AI → 再点「保存并启动」。\r\n" +
					"AI 密钥留空即可（CLI 走本机登录态）。\r\n",
			},
		},
	}

	// 先用 Create 构建窗口（此时子控件指针已就绪），再绑定事件与密码掩码，最后进入消息循环。
	if err := builder.Create(); err != nil {
		return err
	}

	// 密钥输入框以密码形式显示。
	inKey.SetPasswordMode(true)
	inAnth.SetPasswordMode(true)
	inOpen.SetPasswordMode(true)

	// 关闭窗口时尽量优雅停止代理（后台执行，避免阻塞 UI 线程）。
	mw.Closing().Attach(func(_ *bool, _ walk.CloseReason) {
		svcMu.Lock()
		cur := svc
		svcMu.Unlock()
		if cur != nil {
			go cur.Stop()
		}
	})

	// 把日志 channel 的行安全地刷新到文本框（必须在 UI 线程执行）。
	go func() {
		for line := range logCh {
			l := line
			mw.Synchronize(func() {
				if logEdit != nil {
					logEdit.AppendText(l)
				}
			})
		}
	}()

	mw.Run()
	return nil
}

// feishuModelLabel 配置中的模型值到 GUI 下拉文案的映射（空值=自动）。
func feishuModelLabel(m string) string {
	if m == "" {
		return "自动（claude-code）"
	}
	return m
}
