// 与后端 api/bot.go、console.go 响应结构对应的类型定义。

export type BotChannel = 'feishu' | 'wecom'

export interface Bot {
  id: string
  name: string
  channel: BotChannel
  channel_name: string
  enabled: boolean
  model: string
  mode: string
  agent_id: string
  webhook_url: string
  has_secret: boolean
  has_token: boolean
  has_aes_key: boolean
  system_prompt: string
  mention_only: boolean
  callback_url: string
  can_receive: boolean
  can_reply: boolean
  created_at: number
  updated_at: number
}

export interface BotInput {
  name: string
  channel: BotChannel
  model?: string
  mode?: string
  agent_id?: string
  webhook_url?: string
  secret?: string
  token?: string
  aes_key?: string
  system_prompt?: string
  mention_only?: boolean
  enabled?: boolean
}

export interface AgentHealth {
  cpu_percent: number
  mem_percent: number
  inflight: number
  queued: number
  max_concurrency: number
  hostname: string
  os: string
  updated_at: string
  mcps?: { model: string; available: boolean; detail?: string }[]
}

export interface Agent {
  id: string
  name: string
  status: 'online' | 'offline' | 'busy'
  last_heartbeat: number
  queued: number
  max_concurrency: number
  health: AgentHealth
  owner_id?: string
  owner?: string
}

export interface TaskItem {
  id: string
  agent_id: string
  model: string
  mode: string
  status: string
  attempts: number
  created_at: number
  updated_at: number
  chunks: number
  error: string
  source: string
  prompt: string
}

export interface ModelInfo {
  model: string
  available: boolean
  agents: { agent: string; available: boolean; detail?: string }[]
}

export interface Overview {
  agents: number
  agents_online: number
  ws_conns: number
  queues: Record<string, number>
  tasks: Record<string, number>
  bots: number
  bots_enabled: number
  models: ModelInfo[]
}

export interface ChatMessage {
  role: 'user' | 'assistant' | 'system'
  content: string
}
