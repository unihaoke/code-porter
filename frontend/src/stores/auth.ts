import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { api, clearToken, getToken, setToken } from '@/api/client'

// 管理端令牌：登录后写入 localStorage，请求时由 api/client.ts 统一带上。
export const useAuthStore = defineStore('auth', () => {
  const token = ref(getToken())
  const loading = ref(false)
  const error = ref('')

  const authed = computed(() => token.value !== '')

  async function login(input: string): Promise<boolean> {
    const value = input.trim()
    if (!value) {
      error.value = '请输入管理端令牌'
      return false
    }
    loading.value = true
    error.value = ''
    try {
      await api.login(value)
      setToken(value)
      token.value = value
      return true
    } catch (e) {
      error.value = e instanceof Error ? e.message : '登录失败'
      clearToken()
      token.value = ''
      return false
    } finally {
      loading.value = false
    }
  }

  function logout(): void {
    clearToken()
    token.value = ''
  }

  return { token, authed, loading, error, login, logout }
})
