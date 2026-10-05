// 管理端 fetch 封装：所有请求自动带会话 Bearer 令牌，401 时清会话并回登录页。

import type { Agent, ModelInfo, Overview, TaskItem } from '@/types'

const TOKEN_KEY = 'codeporter_session'
const USER_KEY = 'codeporter_user'

export function getToken(): string {
  return localStorage.getItem(TOKEN_KEY) ?? ''
}

export function setSession(token: string, user: unknown): void {
  localStorage.setItem(TOKEN_KEY, token)
  localStorage.setItem(USER_KEY, JSON.stringify(user))
}

export function clearSession(): void {
  localStorage.removeItem(TOKEN_KEY)
  localStorage.removeItem(USER_KEY)
}

export class ApiError extends Error {
  code: string
  status: number
  agents?: { id: string; name: string; status: string }[]

  constructor(message: string, code: string, status: number, agents?: unknown) {
    super(message)
    this.code = code
    this.status = status
    if (Array.isArray(agents)) {
      this.agents = agents as { id: string; name: string; status: string }[]
    }
  }
}

async function request<T = unknown>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers)
  if (!headers.has('Content-Type') && init.body) {
    headers.set('Content-Type', 'application/json')
  }
  const token = getToken()
  if (token) {
    headers.set('Authorization', `Bearer ${token}`)
  }
  const resp = await fetch(path, { ...init, headers })
  if (!resp.ok) {
    let body: Record<string, unknown> = {}
    try {
      body = await resp.json()
    } catch {
      /* 非 JSON 响应 */
    }
    const err = body?.error as { message?: string; code?: string } | undefined
    if (resp.status === 401) {
      clearSession()
      // 登录接口自身的 401 不跳转（页面要展示错误）
      if (path !== '/api/auth/login' && !window.location.pathname.startsWith('/login')) {
        window.location.assign('/login')
      }
    }
    const agents = (body as { agents?: unknown }).agents
    throw new ApiError(
      err?.message ?? `请求失败（HTTP ${resp.status}）`,
      err?.code ?? 'http_error',
      resp.status,
      agents,
    )
  }
  if (resp.status === 204) {
    return undefined as T
  }
  return (await resp.json()) as T
}

// 控制台/业务接口的便捷方法（返回结构与后端 handler 对齐）。
export const api = {
  get: <T>(path: string) => request<T>(path),
  post: <T>(path: string, body?: unknown) =>
    request<T>(path, { method: 'POST', body: body === undefined ? undefined : JSON.stringify(body) }),
  put: <T>(path: string, body?: unknown) =>
    request<T>(path, { method: 'PUT', body: JSON.stringify(body) }),
  delete: <T>(path: string) => request<T>(path, { method: 'DELETE' }),

  // ---- 控制台数据 ----
  overview: () => request<Overview>('/api/overview'),
  agents: () => request<{ agents: Agent[] }>('/api/agents'),
  tasks: (query = '') => request<{ tasks: TaskItem[]; total: number }>(`/api/tasks${query ? `?${query}` : ''}`),
  models: () => request<{ models: ModelInfo[] }>('/api/models'),
}
