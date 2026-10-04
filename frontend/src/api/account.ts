// 账号 / 会话 / 用户管理 / 秘钥管理接口。
import { api } from './client'

export interface UserView {
  id: string
  username: string
  role: 'admin' | 'member'
  status: 'active' | 'disabled'
  created_at: string
  updated_at: string
}

export interface LoginResult {
  token: string
  expires_at: string
  user: UserView
}

export interface KeyView {
  id: string
  name: string
  scopes: string[]
  prefix: string
  expires_at?: string | null
  last_used_at?: string | null
  expired: boolean
  created_at: string
}

export interface CreatedKey extends KeyView {
  secret: string
}

export const authApi = {
  login(username: string, password: string) {
    return api.post<LoginResult>('/api/auth/login', { username, password })
  },
  logout() {
    return api.post<{ ok: boolean }>('/api/auth/logout')
  },
  me() {
    return api.get<{ user: UserView }>('/api/auth/me')
  },
  changePassword(oldPassword: string, newPassword: string) {
    return api.post<{ ok: boolean }>('/api/me/password', {
      old_password: oldPassword,
      new_password: newPassword,
    })
  },
}

export const usersApi = {
  list() {
    return api.get<{ users: UserView[] }>('/api/users')
  },
  create(username: string, password: string, role: 'admin' | 'member') {
    return api.post<{ user: UserView }>('/api/users', { username, password, role })
  },
  remove(id: string) {
    return api.delete<{ deleted: boolean }>(`/api/users/${id}`)
  },
  resetPassword(id: string, password: string) {
    return api.post<{ ok: boolean }>(`/api/users/${id}/reset-password`, { password })
  },
}

export interface CreateKeyInput {
  name: string
  scopes?: string[]
  expires_at?: string | null
}

export const keysApi = {
  listMine() {
    return api.get<{ keys: KeyView[] }>('/api/keys')
  },
  listUser(userId: string) {
    return api.get<{ keys: KeyView[] }>(`/api/users/${userId}/keys`)
  },
  create(input: CreateKeyInput) {
    return api.post<{ key: CreatedKey }>('/api/keys', input)
  },
  deleteMine(id: string) {
    return api.delete<{ deleted: boolean }>(`/api/keys/${id}`)
  },
  deleteUser(userId: string, id: string) {
    return api.delete<{ deleted: boolean }>(`/api/users/${userId}/keys/${id}`)
  },
}
