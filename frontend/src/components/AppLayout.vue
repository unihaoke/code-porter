<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from 'vue'
import { RouterLink, RouterView, useRoute, useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { authApi } from '@/api/account'

const auth = useAuthStore()
const router = useRouter()
const route = useRoute()

const navs = computed(() => {
  const items = [
    { to: '/', label: '概览', icon: '◧', admin: false },
    { to: '/chat', label: '对话', icon: '◑', admin: false },
    { to: '/keys', label: '秘钥', icon: '⚿', admin: false },
    { to: '/bots', label: '机器人', icon: '◍', admin: false },
    { to: '/agents', label: '本地节点', icon: '◈', admin: false },
    { to: '/tasks', label: '任务', icon: '◇', admin: false },
    { to: '/users', label: '用户管理', icon: '♛', admin: true },
  ]
  return items.filter((n) => !n.admin || auth.isAdmin)
})

const current = computed(() => route.path)

async function logout() {
  menuOpen.value = false
  await auth.logout()
  router.push({ name: 'login' })
}

// ---------- 账号下拉菜单（参考 GitHub / GitLab 等常见后台的账号区交互） ----------
const menuOpen = ref(false)
const menuRef = ref<HTMLElement | null>(null)

const username = computed(() => auth.user?.username ?? '未登录')
const roleLabel = computed(() => (auth.isAdmin ? '管理员' : '普通用户'))
/** 头像首字母（用户名可能是邮箱，取 @ 前的首字符）。 */
const avatarText = computed(() => {
  const name = auth.user?.username?.trim()
  if (!name) return '?'
  return name.replace(/^@/, '').charAt(0).toUpperCase()
})

function toggleMenu(): void {
  menuOpen.value = !menuOpen.value
}

function onDocClick(e: MouseEvent): void {
  if (menuOpen.value && menuRef.value && !menuRef.value.contains(e.target as Node)) {
    menuOpen.value = false
  }
}

function onKeydown(e: KeyboardEvent): void {
  if (e.key === 'Escape') menuOpen.value = false
}

document.addEventListener('mousedown', onDocClick)
document.addEventListener('keydown', onKeydown)
onBeforeUnmount(() => {
  document.removeEventListener('mousedown', onDocClick)
  document.removeEventListener('keydown', onKeydown)
})

// ---------- 修改密码弹窗 ----------
const showPwd = ref(false)
const oldPwd = ref('')
const newPwd = ref('')
const pwdMsg = ref('')
const pwdError = ref('')
const savingPwd = ref(false)

function openPwd() {
  menuOpen.value = false
  oldPwd.value = ''
  newPwd.value = ''
  pwdMsg.value = ''
  pwdError.value = ''
  showPwd.value = true
}

async function submitPwd() {
  pwdError.value = ''
  pwdMsg.value = ''
  if (newPwd.value.length < 6) {
    pwdError.value = '新密码至少 6 位'
    return
  }
  savingPwd.value = true
  try {
    await authApi.changePassword(oldPwd.value, newPwd.value)
    pwdMsg.value = '密码已更新（其他登录会话已失效）'
    oldPwd.value = ''
    newPwd.value = ''
  } catch (e) {
    pwdError.value = e instanceof Error ? e.message : '修改失败'
  } finally {
    savingPwd.value = false
  }
}
</script>

<template>
  <div class="shell">
    <aside class="side">
      <div class="brand">
        <span class="logo">CP</span>
        <div>
          <strong>CodePorter</strong>
          <div class="muted" style="font-size: 11px">本地 AI 中继网关</div>
        </div>
      </div>
      <nav>
        <RouterLink
          v-for="n in navs"
          :key="n.to"
          :to="n.to"
          class="nav-item"
          :class="{ active: current === n.to || (n.to !== '/' && current.startsWith(n.to)) }"
        >
          <span class="icon">{{ n.icon }}</span>{{ n.label }}
        </RouterLink>
      </nav>
      <div class="side-foot">
        <div ref="menuRef" class="user-menu">
          <button
            class="user-chip"
            :class="{ 'user-chip--open': menuOpen }"
            type="button"
            :aria-expanded="menuOpen"
            aria-haspopup="menu"
            @click="toggleMenu"
          >
            <span class="avatar">{{ avatarText }}</span>
            <span class="user-meta">
              <span class="user-name" :title="username">{{ username }}</span>
              <span class="user-role-tag" :class="{ admin: auth.isAdmin }">{{ roleLabel }}</span>
            </span>
            <svg class="chev" viewBox="0 0 12 12" width="12" height="12" aria-hidden="true">
              <path d="M2.5 4.5 6 8l3.5-3.5" fill="none" stroke="currentColor" stroke-width="1.5"
                stroke-linecap="round" stroke-linejoin="round" />
            </svg>
          </button>

          <div v-if="menuOpen" class="dropdown" role="menu">
            <div class="dropdown__head">
              <div class="dropdown__name" :title="username">{{ username }}</div>
              <div class="dropdown__role">
                <span class="dot" :class="auth.isAdmin ? 'dot--admin' : 'dot--member'" />
                {{ roleLabel }}
              </div>
            </div>
            <div class="dropdown__sep" />
            <button class="dropdown__item" type="button" role="menuitem" @click="openPwd">
              <svg class="dropdown__icon" viewBox="0 0 16 16" width="15" height="15" aria-hidden="true">
                <g fill="none" stroke="currentColor" stroke-width="1.4"
                  stroke-linecap="round" stroke-linejoin="round">
                  <circle cx="6" cy="6" r="3" />
                  <path d="M8.2 8.2 13 13M11 10.5l1.2-1.2M9 12.5l1.2-1.2" />
                </g>
              </svg>
              修改密码
            </button>
            <button class="dropdown__item dropdown__item--danger" type="button" role="menuitem" @click="logout">
              <svg class="dropdown__icon" viewBox="0 0 16 16" width="15" height="15" aria-hidden="true">
                <g fill="none" stroke="currentColor" stroke-width="1.4"
                  stroke-linecap="round" stroke-linejoin="round">
                  <path d="M6 2.5H4a1.5 1.5 0 0 0-1.5 1.5v8A1.5 1.5 0 0 0 4 13.5h2" />
                  <path d="M8.5 5 11.5 8l-3 3M11.5 8H5.5" />
                </g>
              </svg>
              退出登录
            </button>
          </div>
        </div>
      </div>
    </aside>

    <main class="main">
      <RouterView />
    </main>

    <!-- 修改密码弹窗 -->
    <div v-if="showPwd" class="modal-mask" @click.self="showPwd = false">
      <div class="modal card">
        <h3 style="margin-bottom: 12px">修改密码</h3>
        <div class="field">
          <label>当前密码</label>
          <input v-model="oldPwd" type="password" autocomplete="current-password" />
        </div>
        <div class="field">
          <label>新密码（至少 6 位）</label>
          <input v-model="newPwd" type="password" autocomplete="new-password" />
        </div>
        <div v-if="pwdError" class="alert error">{{ pwdError }}</div>
        <div v-if="pwdMsg" class="alert success">{{ pwdMsg }}</div>
        <div style="display: flex; gap: 8px; justify-content: flex-end; margin-top: 12px">
          <button class="btn btn--ghost" @click="showPwd = false">关闭</button>
          <button class="btn primary" :disabled="savingPwd" @click="submitPwd">
            {{ savingPwd ? '提交中…' : '保存' }}
          </button>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.shell {
  display: flex;
  min-height: 100%;
}

.side {
  width: 208px;
  flex: 0 0 208px;
  background: #fff;
  border-right: 1px solid var(--border);
  display: flex;
  flex-direction: column;
  padding: 18px 14px;
  position: sticky;
  top: 0;
  height: 100vh;
}

.brand {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 0 6px 18px;
}

.logo {
  width: 32px;
  height: 32px;
  border-radius: 8px;
  background: var(--brand);
  color: #fff;
  display: grid;
  place-items: center;
  font-weight: 700;
  font-size: 13px;
}

nav {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.nav-item {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 9px 12px;
  border-radius: 8px;
  color: var(--text);
  font-size: 13px;
}

.nav-item:hover {
  background: #f4f6f9;
}

.nav-item.active {
  background: var(--brand-soft);
  color: var(--brand);
  font-weight: 500;
}

.icon {
  opacity: 0.7;
}

.side-foot {
  margin-top: auto;
  padding-top: 14px;
}

/* ---------- 账号区：chip + 下拉菜单 ---------- */
.user-menu {
  position: relative;
}

.user-chip {
  width: 100%;
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 7px 8px;
  border: 1px solid transparent;
  border-radius: 10px;
  background: transparent;
  cursor: pointer;
  text-align: left;
  transition: background 0.15s ease, border-color 0.15s ease;
}

.user-chip:hover {
  background: #f4f6f9;
}

.user-chip--open,
.user-chip--open:hover {
  background: #f4f6f9;
  border-color: var(--border);
}

.avatar {
  flex: 0 0 auto;
  width: 32px;
  height: 32px;
  border-radius: 50%;
  display: grid;
  place-items: center;
  background: var(--brand);
  color: #fff;
  font-size: 13px;
  font-weight: 600;
  user-select: none;
}

.user-meta {
  flex: 1 1 auto;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 1px;
}

.user-name {
  font-size: 13px;
  font-weight: 600;
  color: var(--text);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.user-role-tag {
  font-size: 11px;
  color: var(--muted);
  line-height: 1.4;
}

.user-role-tag.admin {
  color: var(--brand);
}

.chev {
  flex: 0 0 auto;
  color: var(--muted);
  transition: transform 0.15s ease;
}

.user-chip--open .chev {
  transform: rotate(180deg);
}

.dropdown {
  position: absolute;
  left: 0;
  right: 0;
  bottom: calc(100% + 8px);
  background: #fff;
  border: 1px solid var(--border);
  border-radius: 10px;
  box-shadow: 0 8px 24px rgba(16, 24, 40, 0.12);
  padding: 6px;
  z-index: 40;
  animation: menu-in 0.14s ease-out;
}

@keyframes menu-in {
  from {
    opacity: 0;
    transform: translateY(6px);
  }
  to {
    opacity: 1;
    transform: none;
  }
}

.dropdown__head {
  padding: 8px 10px 7px;
  min-width: 0;
}

.dropdown__name {
  font-size: 13px;
  font-weight: 600;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.dropdown__role {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-top: 2px;
  font-size: 11.5px;
  color: var(--muted);
}

.dropdown__role .dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  display: inline-block;
}

.dot--admin {
  background: var(--brand);
}

.dot--member {
  background: #9aa4b2;
}

.dropdown__sep {
  height: 1px;
  background: var(--border);
  margin: 4px 2px 6px;
}

.dropdown__item {
  width: 100%;
  display: flex;
  align-items: center;
  gap: 9px;
  padding: 8px 10px;
  border: none;
  border-radius: 7px;
  background: transparent;
  color: var(--text);
  font-size: 13px;
  cursor: pointer;
  text-align: left;
}

.dropdown__item:hover {
  background: var(--brand-soft);
  color: var(--brand);
}

.dropdown__item--danger:hover {
  background: var(--danger-soft);
  color: var(--danger);
}

.dropdown__icon {
  flex: 0 0 auto;
  opacity: 0.75;
}

.modal-mask {
  position: fixed;
  inset: 0;
  background: rgba(15, 23, 42, 0.45);
  display: grid;
  place-items: center;
  z-index: 50;
}

.modal {
  width: 420px;
  max-width: calc(100vw - 40px);
  padding: 22px;
}

.main {
  flex: 1;
  min-width: 0;
}
</style>
