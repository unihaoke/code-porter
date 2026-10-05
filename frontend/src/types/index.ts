// 与后端 console.go 等响应结构对应的类型定义。

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
  models: ModelInfo[]
}

export interface ChatMessage {
  role: 'user' | 'assistant' | 'system'
  content: string
}
