import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { clearSession, getToken, setSession } from '@/api/client'
import { authApi, type UserView } from '@/api/account'

interface StoredUser extends UserView {}

export const useAuthStore = defineStore('auth', () => {
  const token = ref(getToken())
  const user = ref<StoredUser | null>(loadStoredUser())

  const isAdmin = computed(() => user.value?.role === 'admin')
  const isAuthed = computed(() => token.value !== '' && !!user.value)

  function loadStoredUser(): StoredUser | null {
    const raw = localStorage.getItem('codeporter_user')
    if (!raw) return null
    try {
      return JSON.parse(raw) as StoredUser
    } catch {
      return null
    }
  }

  async function login(username: string, password: string): Promise<boolean> {
    const value = await authApi.login(username, password)
    token.value = value.token
    user.value = value.user
    setSession(value.token, value.user)
    return true
  }

  async function fetchMe(): Promise<void> {
    if (!token.value) return
    const { user: me } = await authApi.me()
    user.value = me
    localStorage.setItem('codeporter_user', JSON.stringify(me))
  }

  async function logout(): Promise<void> {
    try {
      await authApi.logout()
    } catch {
      /* 本地清理优先 */
    }
    token.value = ''
    user.value = null
    clearSession()
  }

  return { token, user, isAdmin, isAuthed, login, fetchMe, logout }
})
