import type { Agent, Bot, BotInput, ModelInfo, Overview, TaskItem } from '@/types'

// 管理端令牌存本地，避免刷新页面反复登录。
const TOKEN_KEY = 'codeporter_admin_token'

export function getToken(): string {
  return localStorage.getItem(TOKEN_KEY) ?? ''
}

export function setToken(token: string): void {
  localStorage.setItem(TOKEN_KEY, token)
}

export function clearToken(): void {
  localStorage.removeItem(TOKEN_KEY)
}

// ApiError 携带后端返回的结构化错误码。
export class ApiError extends Error {
  code: string
  status: number

  constructor(message: string, code: string, status: number) {
    super(message)
    this.code = code
    this.status = status
  }
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers)
  if (!headers.has('Content-Type') && init.body) {
    headers.set('Content-Type', 'application/json')
  }
  const token = getToken()
  if (token) {
    headers.set('X-Admin-Token', token)
  }
  const resp = await fetch(path, { ...init, headers })
  if (!resp.ok) {
    let message = `请求失败（HTTP ${resp.status}）`
    let code = 'http_error'
    try {
      const body = await resp.json()
      if (body?.error?.message) {
        message = body.error.message
        code = body.error.code ?? code
      }
    } catch {
      // 非 JSON 响应（如 nginx 错误页）保留默认提示
    }
    if (resp.status === 401) {
      clearToken()
    }
    throw new ApiError(message, code, resp.status)
  }
  if (resp.status === 204) {
    return undefined as T
  }
  return (await resp.json()) as T
}

export const api = {
  login(token: string) {
    return request<{ ok: boolean; auth_required: boolean }>('/api/auth/login', {
      method: 'POST',
      headers: { 'X-Admin-Token': token },
    })
  },

  overview: () => request<Overview>('/api/overview'),

  agents: () => request<{ agents: Agent[] }>('/api/agents'),

  tasks: (limit = 50) => request<{ tasks: TaskItem[]; total: number }>(`/api/tasks?limit=${limit}`),

  models: () => request<{ models: ModelInfo[] }>('/api/models'),

  bots: () => request<{ bots: Bot[] }>('/api/bots'),
  bot: (id: string) => request<{ bot: Bot }>(`/api/bots/${id}`),
  createBot: (input: BotInput) =>
    request<{ bot: Bot }>('/api/bots', { method: 'POST', body: JSON.stringify(input) }),
  updateBot: (id: string, input: Partial<BotInput>) =>
    request<{ bot: Bot }>(`/api/bots/${id}`, { method: 'PUT', body: JSON.stringify(input) }),
  deleteBot: (id: string) => request<{ deleted: boolean }>(`/api/bots/${id}`, { method: 'DELETE' }),
  toggleBot: (id: string, enabled: boolean) =>
    request<{ bot: Bot }>(`/api/bots/${id}/toggle`, {
      method: 'POST',
      body: JSON.stringify({ enabled }),
    }),
}
