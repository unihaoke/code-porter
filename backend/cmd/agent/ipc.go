package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/codeporter/code-porter/internal/application/port"
	"github.com/codeporter/code-porter/internal/domain/model"
	"github.com/codeporter/code-porter/internal/infrastructure/config"
	"github.com/codeporter/code-porter/internal/infrastructure/imbot"
	"github.com/codeporter/code-porter/internal/infrastructure/logging"
	"gopkg.in/yaml.v3"
)

// IPC 模式：Electron（或其他 GUI）作为前端，通过 stdin/stdout 的 JSON 行协议
// 驱动本核心。协议刻意做得很薄：
//
//	请求（前端 → 核心）：{"id":"1","action":"agent.start","params":{...}}
//	响应（核心 → 前端）：{"id":"1","ok":true,"result":{...}}
//	事件（核心 → 前端）：{"event":"log","data":{...}}
//
// stdout 只写协议消息，因此**运行日志必须走事件**，绝不能直接写 stdout。

// IPC 请求动作。
const (
	actConfigGet  = "config.get"
	actConfigSave = "config.save"
	actAgentStart = "agent.start"
	actAgentStop  = "agent.stop"
	actStatus     = "agent.status"
	actCLITest    = "cli.test"
	actBotStart   = "bot.start"
	actBotStop    = "bot.stop"
	actBotStatus  = "bot.status"
	actBotTest    = "bot.test"
	actToolsStart = "tools.start"
	actToolsStop  = "tools.stop"
	actQuit       = "app.quit"
)

// IPC 事件类型。
const (
	evLog    = "log"
	evStatus = "status"
	evHealth = "health"
	evTask   = "task"
)

// ipcRequest 前端发来的请求。
type ipcRequest struct {
	ID     string          `json:"id"`
	Action string          `json:"action"`
	Params json.RawMessage `json:"params,omitempty"`
}

// ipcResponse 核心的响应（与请求 ID 对应）。
type ipcResponse struct {
	ID     string          `json:"id"`
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// ipcEvent 核心主动推送的事件（无 ID）。
type ipcEvent struct {
	Event string `json:"event"`
	Data  any    `json:"data,omitempty"`
}

// ipcLogPayload 日志事件负载。
type ipcLogPayload struct {
	Level  string         `json:"level"`
	Time   string         `json:"time"`
	Msg    string         `json:"msg"`
	Fields map[string]any `json:"fields,omitempty"`
}

// ipcCLITestParams cli.test 的参数。
type ipcCLITestParams struct {
	SkipProbe bool `json:"skip_probe"`
}

// ipcCLITestResult cli.test 的返回。
type ipcCLITestResult struct {
	Results []CLIConnResultSummary `json:"results"`
	OK      int                    `json:"ok"`
	Missing int                    `json:"missing"`
	Warned  int                    `json:"warned"`
	Failed  int                    `json:"failed"`
	Report  string                 `json:"report"`
}

// CLIConnResultSummary 供前端渲染的精简测试结果。
type CLIConnResultSummary struct {
	Model   string `json:"model"`
	Label   string `json:"label"`
	Status  string `json:"status"`
	OK      bool   `json:"ok"`
	Detail  string `json:"detail"`
	Output  string `json:"output"`
	WorkDir string `json:"work_dir"`
}

// ipcServer 把 Service 包装成 JSON 行协议服务。
type ipcServer struct {
	cfgPath string
	cfg     *config.AgentConfig
	log     *logging.Logger

	mu   sync.Mutex
	svc  *Service
	out  io.Writer
	done chan struct{}
	once sync.Once
}

// newIPCServer 构造 IPC 服务。
func newIPCServer(cfgPath string, cfg *config.AgentConfig, log *logging.Logger) *ipcServer {
	return &ipcServer{
		cfgPath: cfgPath,
		cfg:     cfg,
		log:     log,
		out:     os.Stdout,
		done:    make(chan struct{}),
	}
}

// run 读取 stdin 直到 EOF 或收到 app.quit。
func (s *ipcServer) run(ctx context.Context) error {
	// 日志改为写入 stderr，再由本层转成 log 事件推给前端，
	// 这样 stdout 始终只有协议消息，界面才能可靠解析。
	s.mu.Lock()
	logLevel := logging.ParseLevel(s.cfg.Log.Level)
	s.mu.Unlock()
	s.log = logging.New(&ipcLogWriter{s: s, level: logLevel}, logLevel)

	s.send(ipcEvent{Event: "status", Data: s.statusPayload()})
	s.send(ipcEvent{Event: "ready", Data: map[string]any{
		"pid":         os.Getpid(),
		"config_path": s.cfgPath,
	}})

	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var req ipcRequest
		if err := json.Unmarshal(line, &req); err != nil {
			s.respondError("", "请求不是合法 JSON: "+err.Error())
			continue
		}
		if req.Action == actQuit {
			s.respond(req.ID, map[string]any{"bye": true}, nil)
			s.shutdown()
			break
		}
		// 同步派发：保证 config.get/save、agent.start 等有副作用的动作严格有序，
		// 不会出现 start/stop 交叉。耗时操作（stop、cli.test）在 dispatch 内部
		// 自行起 goroutine 并在完成后才回复。
		s.dispatch(ctx, req)
	}
	s.shutdown()
	return nil
}

// dispatch 处理单个请求。
func (s *ipcServer) dispatch(ctx context.Context, req ipcRequest) {
	defer func() {
		if r := recover(); r != nil {
			s.respondError(req.ID, fmt.Sprintf("处理 %s 时 panic: %v", req.Action, r))
		}
	}()

	switch req.Action {
	case actConfigGet:
		s.mu.Lock()
		cfg := s.cfg
		s.mu.Unlock()
		m, err := configToMap(cfg)
		if err != nil {
			s.respondError(req.ID, "读取配置失败: "+err.Error())
			return
		}
		s.respond(req.ID, m, nil)

	case actConfigSave:
		incoming, err := mapToConfig(req.Params)
		if err != nil {
			s.respondError(req.ID, "配置格式错误: "+err.Error())
			return
		}
		// MCP.Env 是运行时从 secrets 派生的，不落盘。
		incoming.MCP.Env = nil
		// 旧 token 字段已废弃：界面保存时一律剥除，避免历史配置里的 token
		// 被原样往返写回、永久卡住迁移（即使界面上根本看不到这个字段）。
		incoming.Agent.DeprecatedToken = ""
		if err := config.SaveAgent(s.cfgPath, incoming); err != nil {
			s.respondError(req.ID, "保存失败: "+err.Error())
			return
		}
		s.mu.Lock()
		s.cfg = incoming
		svc := s.svc
		s.mu.Unlock()
		// Service 实例复用、不会随保存重建：必须把新配置同步进去，否则状态展示
		// （ai_tools / bots 启用态）与后续 Start* 仍按旧配置执行。
		if svc != nil {
			svc.UpdateConfig(incoming)
		}
		s.log.Info("agent config saved", port.F("path", s.cfgPath))
		s.send(ipcEvent{Event: "status", Data: s.statusPayload()})
		s.respond(req.ID, map[string]any{"saved": true, "path": s.cfgPath}, nil)

	case actAgentStart:
		s.mu.Lock()
		cur := s.svc
		s.mu.Unlock()
		if cur != nil && cur.Running() {
			s.respondError(req.ID, "代理已在运行")
			return
		}
		s.mu.Lock()
		cfg := s.cfg
		s.mu.Unlock()
		// 复用同一个 Service 实例，避免 Event 钩子与运行实例错位。
		if cur == nil {
			cur = NewService(s.cfgPath, cfg, s.log)
			cur.SetEventHook(s.onEvent)
			s.mu.Lock()
			s.svc = cur
			s.mu.Unlock()
		}
		if err := cur.Start(); err != nil {
			s.respondError(req.ID, "启动失败: "+err.Error())
			return
		}
		s.respond(req.ID, s.statusPayload(), nil)

	case actAgentStop:
		s.mu.Lock()
		cur := s.svc
		s.mu.Unlock()
		if cur == nil || !cur.Running() {
			s.respondError(req.ID, "代理未在运行")
			return
		}
		// 优雅停止可能要等在途任务，异步执行并回报结果。
		go func() {
			cur.Stop()
			s.respond(req.ID, s.statusPayload(), nil)
		}()

	case actStatus:
		s.respond(req.ID, s.statusPayload(), nil)

	case actCLITest:
		var p ipcCLITestParams
		if len(req.Params) > 0 {
			_ = json.Unmarshal(req.Params, &p) // 参数可选，解析失败用默认值
		}
		go func() {
			res := s.runCLITest(ctx, p)
			s.respond(req.ID, res, nil)
		}()

	case actBotStart:
		// 机器人与代理独立：不要求代理已启动。参数可带 channel（feishu/wecom），
		// 不带时启动所有已启用渠道。
		var p struct {
			Channel string `json:"channel"`
		}
		if len(req.Params) > 0 {
			_ = json.Unmarshal(req.Params, &p)
		}
		s.mu.Lock()
		cur := s.svc
		s.mu.Unlock()
		if cur == nil {
			cur = NewService(s.cfgPath, s.cfg, s.log)
			cur.SetEventHook(s.onEvent)
			s.mu.Lock()
			s.svc = cur
			s.mu.Unlock()
		}
		var startErr error
		if strings.TrimSpace(p.Channel) == "" {
			// 聚合启动：逐渠道结果中失败的原因已聚合进 error；成功渠道此刻已在运行。
			_, startErr = cur.StartBots()
		} else {
			startErr = cur.StartBot(p.Channel)
		}
		if startErr != nil {
			s.respondError(req.ID, "机器人启动失败: "+startErr.Error())
			return
		}
		s.respond(req.ID, s.statusPayload(), nil)

	case actBotStop:
		var p struct {
			Channel string `json:"channel"`
		}
		if len(req.Params) > 0 {
			_ = json.Unmarshal(req.Params, &p)
		}
		s.mu.Lock()
		cur := s.svc
		s.mu.Unlock()
		if cur == nil {
			s.respondError(req.ID, "机器人未在运行")
			return
		}
		if strings.TrimSpace(p.Channel) == "" {
			if !cur.BotsRunning() {
				s.respondError(req.ID, "机器人未在运行")
				return
			}
			go func() {
				cur.StopBots()
				s.respond(req.ID, s.statusPayload(), nil)
			}()
		} else {
			if !cur.BotChannelRunning(p.Channel) {
				s.respondError(req.ID, "该渠道机器人未在运行: "+p.Channel)
				return
			}
			go func() {
				cur.StopBot(p.Channel)
				s.respond(req.ID, s.statusPayload(), nil)
			}()
		}

	case actBotStatus:
		s.respond(req.ID, s.statusPayload(), nil)

	case actBotTest:
		// 纯凭证校验，不启动长连接；直接用最新配置构造临时 runner。
		var p struct {
			Channel string `json:"channel"`
		}
		if len(req.Params) > 0 {
			_ = json.Unmarshal(req.Params, &p)
		}
		go func() {
			s.mu.Lock()
			cur := s.svc
			s.mu.Unlock()
			if cur == nil {
				cur = NewService(s.cfgPath, s.cfg, s.log)
				cur.SetEventHook(s.onEvent)
				s.mu.Lock()
				s.svc = cur
				s.mu.Unlock()
			}
			res, err := cur.TestBot(p.Channel)
			if err != nil {
				s.respondError(req.ID, res.Detail+"（"+err.Error()+"）")
				return
			}
			s.respond(req.ID, res, nil)
		}()

	case actToolsStart:
		// 本地 AI 工具与代理 / 机器人都独立：按需创建同一个 Service 实例。
		// 预热要拉起多个子进程并握手，可能耗时数十秒，异步执行后再回复。
		go func() {
			s.mu.Lock()
			cur := s.svc
			s.mu.Unlock()
			if cur == nil {
				cur = NewService(s.cfgPath, s.cfg, s.log)
				cur.SetEventHook(s.onEvent)
				s.mu.Lock()
				s.svc = cur
				s.mu.Unlock()
			}
			if cur.ToolsRunning() {
				s.respondError(req.ID, "本地 AI 工具已在运行")
				return
			}
			results, err := cur.StartTools()
			if err != nil {
				s.respondError(req.ID, "本地 AI 工具启动失败: "+err.Error())
				return
			}
			s.respond(req.ID, map[string]any{
				"status":  s.statusPayload(),
				"results": results,
			}, nil)
		}()

	case actToolsStop:
		s.mu.Lock()
		cur := s.svc
		s.mu.Unlock()
		if cur == nil || !cur.ToolsRunning() {
			s.respondError(req.ID, "本地 AI 工具未在运行")
			return
		}
		go func() {
			cur.StopTools()
			s.respond(req.ID, s.statusPayload(), nil)
		}()

	default:
		s.respondError(req.ID, "未知动作: "+req.Action)
	}
}

// configToMap 把配置转成 map，键名沿用 yaml 标签（snake_case）。
//
// 为什么绕这一圈：AgentConfig 只有 yaml tag、没有 json tag，直接 json.Marshal
// 会得到 Go 字段名（Agent/Gateway/...），与 agent.status 的 snake_case 风格
// 不一致，前端要维护两套键名。走 yaml 标签可让界面字段与配置文件完全对应。
func configToMap(cfg *config.AgentConfig) (map[string]any, error) {
	raw, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	if err := yaml.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	// 旧 token 字段不暴露给界面：否则前端把未知键原样回传，保存时又被写回文件，
	// 用户即使粘贴了新秘钥也会被残留的 token 卡住。
	if agent, ok := out["agent"].(map[string]any); ok {
		delete(agent, "token")
	}
	return out, nil
}

// mapToConfig 把前端送来的对象（键名与 yaml 一致）转回配置结构体。
// JSON 是 YAML 的子集，因此先转回 JSON 字节再用 yaml 解析即可。
func mapToConfig(raw json.RawMessage) (*config.AgentConfig, error) {
	if len(raw) == 0 {
		return nil, errors.New("缺少配置内容")
	}
	cfg := &config.AgentConfig{}
	if err := yaml.Unmarshal(raw, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// runCLITest 执行连通性测试并整理成前端友好的结构。
func (s *ipcServer) runCLITest(ctx context.Context, p ipcCLITestParams) ipcCLITestResult {
	s.mu.Lock()
	cfg := s.cfg
	s.mu.Unlock()
	// 复制一份，避免测试过程中的派生字段污染真实配置。
	local := *cfg

	testCtx, cancel := context.WithTimeout(ctx, 6*time.Minute)
	defer cancel()
	results := TestCLIConnections(testCtx, &local, s.log, CLIConnTestOptions{
		SkipProbe: p.SkipProbe,
		Timeout:   90 * time.Second,
	})

	out := ipcCLITestResult{Results: make([]CLIConnResultSummary, 0, len(results))}
	for _, r := range results {
		label := r.Model.String()
		if acfg := local.MCP.For(r.Model); acfg.Command != "" {
			label = acfg.Command
		}
		out.Results = append(out.Results, CLIConnResultSummary{
			Model: r.Model.String(), Label: label,
			Status: string(r.Status), OK: r.OK(),
			Detail: r.Detail, Output: trim(r.Output, 800), WorkDir: r.WorkDir,
		})
	}
	out.OK, out.Missing, out.Warned, out.Failed = summarizeCLIConn(results)
	out.Report = FormatCLIConnReport(results)
	return out
}

// onEvent 接收 Service 的运行时事件并转成协议事件推给前端。
func (s *ipcServer) onEvent(kind string, data any) {
	s.send(ipcEvent{Event: kind, Data: data})
}

// statusPayload 汇总当前配置与运行状态。
func (s *ipcServer) statusPayload() map[string]any {
	s.mu.Lock()
	cfg := s.cfg
	svc := s.svc
	s.mu.Unlock()
	if cfg == nil {
		return map[string]any{"running": false, "tools_running": false, "bots": map[string]any{}}
	}
	if svc != nil {
		return svc.Status()
	}
	// 尚未启动过：直接基于配置给出静态状态。
	work := strings.TrimSpace(cfg.MCP.WorkDir)
	if work == "" {
		work = defaultClientWorkDir()
	}
	tools := make([]map[string]any, 0, 4)
	for _, m := range model.All() {
		acfg := cfg.MCP.For(m)
		tools = append(tools, map[string]any{
			"model": m.String(), "label": acfg.Command, "enabled": acfg.Enabled,
			"mode": string(acfg.Mode), "command": acfg.Command, "running": false,
		})
	}
	bots := make(map[string]any, len(imbot.Channels()))
	for _, ch := range imbot.Channels() {
		bots[ch] = botStatusItem(cfg, ch, nil)
	}
	return map[string]any{
		"running": false, "agent_running": false, "tools_running": false,
		"version":    "0.2.0",
		"agent_id":   cfg.Agent.ID,
		"agent_name": cfg.Agent.Name,
		"has_key":    cfg.Agent.Key != "",
		"gateway":    cfg.Gateway.Addr,
		"log_level":  cfg.Log.Level, "max_concurrency": cfg.WorkerPool.MaxConcurrency,
		"queue_size": cfg.WorkerPool.QueueSize, "direct_mode": cfg.Direct.Enabled,
		"work_dir": work, "config_path": s.cfgPath, "ai_tools": tools,
		"bots": bots,
	}
}

// shutdown 优雅停止代理与机器人并关闭 done。
func (s *ipcServer) shutdown() {
	s.once.Do(func() {
		s.mu.Lock()
		cur := s.svc
		s.mu.Unlock()
		if cur != nil {
			cur.StopTools()
			cur.StopBots()
			cur.Stop()
		}
		close(s.done)
	})
}

// send 输出一条事件。
func (s *ipcServer) send(ev ipcEvent) {
	raw, err := json.Marshal(ev)
	if err != nil {
		return
	}
	s.writeLine(raw)
}

// respond 输出成功响应。
func (s *ipcServer) respond(id string, result any, _ error) {
	raw, err := json.Marshal(result)
	if err != nil {
		s.respondError(id, "结果序列化失败: "+err.Error())
		return
	}
	s.writeLine(mustJSON(ipcResponse{ID: id, OK: true, Result: raw}))
}

// respondError 输出失败响应。
func (s *ipcServer) respondError(id, msg string) {
	s.writeLine(mustJSON(ipcResponse{ID: id, OK: false, Error: msg}))
}

// writeLine 写一行协议消息（加锁，避免多 goroutine 交叉写坏 stdout）。
func (s *ipcServer) writeLine(raw []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _ = s.out.Write(append(raw, '\n'))
}

// mustJSON 序列化，失败时返回最小错误对象，绝不 panic。
func mustJSON(v any) []byte {
	raw, err := json.Marshal(v)
	if err != nil {
		return []byte(`{"id":"","ok":false,"error":"marshal failed"}`)
	}
	return raw
}

// ipcLogWriter 把日志行转成 log 事件。
//
// logging 包的行格式固定为：<时间戳> <LEVEL> <消息> key=value...
// 这里解析出级别与消息，好让界面能按级别着色；解析不出来就整体当消息。
type ipcLogWriter struct {
	s     *ipcServer
	level logging.Level
}

// knownLevels 日志级别名集合，用于判断第二个字段是否为级别。
var knownLevels = map[string]bool{
	"debug": true, "info": true, "warn": true, "error": true,
}

// Write 实现 io.Writer。
func (w *ipcLogWriter) Write(p []byte) (int, error) {
	for _, line := range splitLines(string(p)) {
		if line == "" {
			continue
		}
		level, msg := splitLogLine(line)
		w.s.send(ipcEvent{Event: evLog, Data: ipcLogPayload{
			Level: level, Time: time.Now().Format("15:04:05.000"), Msg: msg,
		}})
	}
	return len(p), nil
}

// splitLogLine 从行首解析出级别与剩余消息。
// 解析不出正文时回退为整行，避免界面出现「有级别但没有内容」的空日志。
func splitLogLine(line string) (level, msg string) {
	parts := strings.SplitN(line, " ", 3)
	if len(parts) < 2 || !knownLevels[strings.ToLower(parts[1])] {
		return "info", line
	}
	lvl := strings.ToLower(parts[1])
	if len(parts) < 3 || strings.TrimSpace(parts[2]) == "" {
		return lvl, line
	}
	return lvl, parts[2]
}

// splitLines 按行拆分并去掉首尾空白与空行。
//
// 注意：这里必须用 strings.TrimSpace，不能用 CLIConnResult 那个 trim(s, n)——
// 那是「截断并加省略号」的helper，n=0 会把整行截成 "…"，日志内容会全部丢失。
func splitLines(s string) []string {
	out := make([]string, 0, 4)
	for _, line := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(strings.TrimSuffix(line, "\r")); t != "" {
			out = append(out, t)
		}
	}
	return out
}
