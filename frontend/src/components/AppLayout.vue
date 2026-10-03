<script setup lang="ts">
import { RouterLink, RouterView, useRoute, useRouter } from 'vue-router'
import { computed } from 'vue'
import { useAuthStore } from '@/stores/auth'

const auth = useAuthStore()
const router = useRouter()
const route = useRoute()

const navs = [
  { to: '/', label: '概览', icon: '◧' },
  { to: '/chat', label: '对话', icon: '◑' },
  { to: '/bots', label: '机器人', icon: '◍' },
  { to: '/agents', label: '本地节点', icon: '◈' },
  { to: '/tasks', label: '任务', icon: '◇' },
]

const current = computed(() => route.path)

function logout() {
  auth.logout()
  router.push({ name: 'login' })
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
        <button class="btn" style="width: 100%" @click="logout">退出登录</button>
      </div>
    </aside>

    <main class="main">
      <RouterView />
    </main>
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

.main {
  flex: 1;
  min-width: 0;
}
</style>
