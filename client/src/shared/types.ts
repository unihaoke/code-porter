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
  | 'app.quit'

/** Go 核心推送的事件。 */
export type CoreEventName = 'log' | 'status' | 'health' | 'task' | 'ready'

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
}

/** 代理运行状态。 */
export interface AgentStatus {
  running: boolean
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
  /** 测试本地 AI 连通性。 */
  testCli(skipProbe: boolean): Promise<CliTestResult>
  /** 选择目录（原生对话框）。 */
  pickDirectory(title: string): Promise<string | null>
  /** 打开外部链接。 */
  openExternal(url: string): Promise<void>
  /** 退出应用。 */
  quit(): Promise<void>
  /** 订阅核心事件；返回取消订阅函数。 */
  onEvent(cb: (name: CoreEventName, data: unknown) => void): () => void
  /** 核心进程是否可用。 */
  isCoreReady(): boolean
}
