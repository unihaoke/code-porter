<script setup lang="ts">
import { computed, ref } from 'vue'
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
  await auth.logout()
  router.push({ name: 'login' })
}

// 修改密码弹窗
const showPwd = ref(false)
const oldPwd = ref('')
const newPwd = ref('')
const pwdMsg = ref('')
const pwdError = ref('')
const savingPwd = ref(false)

function openPwd() {
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
        <div class="user-box">
          <div class="user-name">{{ auth.user?.username ?? '未登录' }}</div>
          <div class="user-role" :class="{ admin: auth.isAdmin }">
            {{ auth.isAdmin ? '管理员' : '普通用户' }}
          </div>
        </div>
        <button class="btn btn--ghost" style="width: 100%" @click="openPwd">修改密码</button>
        <button class="btn" style="width: 100%; margin-top: 6px" @click="logout">退出登录</button>
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

.user-box {
  padding: 8px 10px;
  margin-bottom: 10px;
  border-radius: 8px;
  background: #f7f8fa;
}

.user-name {
  font-size: 13px;
  font-weight: 600;
}

.user-role {
  font-size: 11px;
  color: var(--muted);
  margin-top: 2px;
}

.user-role.admin {
  color: var(--brand);
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
