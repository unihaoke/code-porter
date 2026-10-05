/**
 * 主进程 ↔ 渲染进程 ↔ Go 核心 三方共用的类型定义。
 *
 * 键名一律 snake_case，与 backend/configs/agent.yaml 完全一致——
 * Go 侧的 IPC 层刻意用 yaml 标签做序列化（见 cmd/agent/ipc.go 的 configToMap），
 * 就是为了让界面字段与配置文件一一对应、不用维护第二套命名。
 */

/** Go 核心支持的动作。 */
export type IpcAction =
  | 'config.get'
  | 'config.save'
  | 'agent.start'
  | 'agent.stop'
  | 'agent.status'
  | 'cli.test'
  | 'bot.start'
  | 'bot.stop'
  | 'bot.status'
  | 'bot.test'
  | 'tools.start'
  | 'tools.stop'
  | 'app.quit'

/** Go 核心推送的事件。 */
export type CoreEventName =
  | 'log'
  | 'status'
  | 'health'
  | 'task'
  | 'ready'
  | 'core-error'
  | 'core-exit'

/** 日志级别。 */
export type LogLevel = 'debug' | 'info' | 'warn' | 'error'

/** 一条日志。 */
export interface LogEntry {
  level: LogLevel
  time: string
  msg: string
}

/** 单个 AI 工具在状态里的信息。 */
export interface AiToolStatus {
  model: string
  label: string
  enabled: boolean
  mode: string
  command: string
  /** MCP 模式常驻子进程是否已被「启动本地 AI 工具」拉起（CLI 模式恒为 false）。 */
  running?: boolean
}

/** 支持的 IM 机器人渠道名（与 Go 侧 imbot.Channels 一致）。 */
export type BotChannel = 'feishu' | 'wecom'

/**
 * 单个 IM 渠道的运行/配置状态。
 * 形状渠道无关：飞书 / 企业微信都用同一结构，新增渠道无需改前端类型。
 */
export interface BotRuntimeStatus {
  running: boolean
  enabled: boolean
  configured: boolean
  /** 渠道身份标识（飞书 = App ID，企业微信 = Bot ID）；绝不回传 secret。 */
  credential_id: string
  model: string
  mention_only: boolean
  /** 渠道历史展示键，按渠道二选一出现。 */
  app_id?: string
  bot_id?: string
}

/** 各 IM 渠道状态，键为渠道名；后端始终回传全部已注册渠道。 */
export interface BotsStatus {
  feishu: BotRuntimeStatus
  wecom: BotRuntimeStatus
  [channel: string]: BotRuntimeStatus
}

/** 代理运行状态。 */
export interface AgentStatus {
  running: boolean
  agent_running?: boolean
  version?: string
  agent_id?: string
  agent_name?: string
  has_key?: boolean
  gateway?: string
  log_level?: string
  max_concurrency?: number
  queue_size?: number
  direct_mode?: boolean
  work_dir?: string
  config_path?: string
  ai_tools?: AiToolStatus[]
  /** 本地 AI 工具预热运行时是否已启动（独立于代理 / 机器人）。 */
  tools_running?: boolean
  bots?: BotsStatus
}

/** 单个本地 AI 工具的启动（预热）结果。 */
export interface ToolsStartItem {
  model: string
  label: string
  mode: string
  ok: boolean
  detail: string
}

/** 「启动本地 AI 工具」响应：最新状态 + 各工具预热结果。 */
export interface ToolsStartResult {
  status: AgentStatus
  results: ToolsStartItem[]
}

/** 机器人「测试连接」结果。 */
export interface BotTestResult {
  channel: string
  ok: boolean
  detail: string
  tenant_key?: string
  expire_seconds?: number
}

/** AI 工具健康探测结果。 */
export interface HealthEntry {
  model: string
  available: boolean
  detail: string
}

/** 任务执行事件。 */
export interface TaskEvent {
  task_id: string
  model: string
  phase: 'start' | 'success' | 'failed'
  elapsed_ms?: number
  detail?: string
}

/** CLI 连通性测试的精简结果（供界面渲染）。 */
export interface CliTestItem {
  model: string
  label: string
  status: string
  ok: boolean
  detail: string
  output: string
  work_dir: string
}

/** CLI 连通性测试响应。 */
export interface CliTestResult {
  results: CliTestItem[]
  ok: number
  missing: number
  warned: number
  failed: number
  report: string
}

/** 飞书机器人配置（与 agent.yaml 的 bots.feishu 一致）。 */
export interface FeishuBotConfig {
  enabled: boolean
  app_id: string
  app_secret: string
  model: string
  mention_only: boolean
  system_prompt: string
}

/** 企业微信智能机器人配置（与 agent.yaml 的 bots.wecom 一致，API 长连接模式）。 */
export interface WeComBotConfig {
  enabled: boolean
  bot_id: string
  secret: string
  model: string
  mention_only: boolean
  system_prompt: string
}

/** 本地 IM 机器人配置，每个渠道一个独立开关。 */
export interface BotsConfig {
  feishu: FeishuBotConfig
  wecom: WeComBotConfig
  [channel: string]: FeishuBotConfig | WeComBotConfig
}

/** 配置对象的通用形状（键名与 agent.yaml 一致）。 */
export interface AgentConfig {
  agent: { id: string; key: string; name: string }
  gateway: { addr: string; timeout: string; insecure_tls: boolean }
  worker_pool: { max_concurrency: number; queue_size: number }
  direct: { enabled: boolean; [k: string]: unknown }
  mcp: {
    work_dir: string
    claude_code: ToolConfig
    codex: ToolConfig
    trae: ToolConfig
    codebuddy: ToolConfig
    health_timeout: string
    [k: string]: unknown
  }
  bots?: BotsConfig
  secrets: { anthropic_api_key: string; openai_api_key: string }
  log: { level: string }
  [k: string]: unknown
}

/** 单个工具的配置。 */
export interface ToolConfig {
  enabled: boolean
  mode: string
  command: string
  args: string[]
  work_dir: string
  request_timeout: string
  cli: {
    args: string[]
    prompt_via_stdin: boolean
    model: string
    permission_mode: string
    max_turns: number
    extra_args: string[]
    output_format: string
    result_path: string
    include_stderr: boolean
  }
  [k: string]: unknown
}

/** preload 暴露给渲染进程的 API。 */
export interface CodeporterApi {
  /** 读取当前配置。 */
  getConfig(): Promise<AgentConfig>
  /** 保存配置。 */
  saveConfig(cfg: AgentConfig): Promise<{ saved: boolean; path: string }>
  /** 启动代理。 */
  start(): Promise<AgentStatus>
  /** 停止代理。 */
  stop(): Promise<AgentStatus>
  /** 读取状态。 */
  status(): Promise<AgentStatus>
  /** 启动 IM 机器人（与代理独立）；带 channel 时只启动该渠道，不带启动全部已启用渠道。 */
  startBots(channel?: string): Promise<AgentStatus>
  /** 停止 IM 机器人；带 channel 时只停该渠道，不带停止全部渠道。 */
  stopBots(channel?: string): Promise<AgentStatus>
  /** 测试机器人凭证/连通性（不建立长连接）。 */
  testBot(channel?: string): Promise<BotTestResult>
  /** 启动（预热）配置中已启用的本地 AI 工具，独立于代理 / 机器人。 */
  startTools(): Promise<ToolsStartResult>
  /** 停止本地 AI 工具预热运行时。 */
  stopTools(): Promise<AgentStatus>
  /** 测试本地 AI 连通性。 */
  testCli(skipProbe: boolean): Promise<CliTestResult>
  /** 选择目录（原生对话框）。 */
  pickDirectory(title: string): Promise<string | null>
  /** 打开外部链接。 */
  openExternal(url: string): Promise<void>
  /** 用系统文件管理器打开本地目录（如配置所在文件夹）；成功返回空串，失败返回错误信息。 */
  openPath(target: string): Promise<string>
  /** 退出应用。 */
  quit(): Promise<void>
  /** 订阅核心事件；返回取消订阅函数。 */
  onEvent(cb: (name: CoreEventName, data: unknown) => void): () => void
  /** 核心进程是否可用。 */
  isCoreReady(): boolean
}
