<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'

import { useStore } from './store'
import DashboardView from './views/DashboardView.vue'
import BotsView from './views/BotsView.vue'
import SettingsView from './views/SettingsView.vue'
import LogsView from './views/LogsView.vue'

type Tab = 'dashboard' | 'bots' | 'settings' | 'logs'

const { state, init, toast, dismissToast, setActiveTab, toggleTheme } = useStore()

// 标签页与 URL hash 同步：既支持 #settings / #logs 深链，
// 也让自动化截图能直接打开指定视图。
const TABS: Tab[] = ['dashboard', 'bots', 'settings', 'logs']

function tabFromHash(): Tab {
  const h = location.hash.replace('#', '') as Tab
  return TABS.includes(h) ? h : 'dashboard'
}

const tab = ref<Tab>(tabFromHash())

function selectTab(t: Tab): void {
  tab.value = t
  setActiveTab(t)
  location.hash = t === 'dashboard' ? '' : t
}
/** 子视图的轻提示统一走全局 toast 队列（成功 3s、失败 8s 自动消失，也可点击关闭）。 */
function notify(text: string, err = false): void {
  toast(text, err ? 'error' : 'info')
}

/**
 * 聚合运行态：代理 + 各机器人渠道 + 本地 AI 工具，共三类服务。
 * 侧栏不再只反映代理，避免「只开机器人却显示离线」的误导。
 */
const runningServices = computed(() => {
  const bots = Object.values(state.status?.bots ?? {}).filter((b) => b.running).length
  return (state.status?.running ? 1 : 0) + bots + (state.status?.tools_running ? 1 : 0)
})
const anyRunning = computed(() => runningServices.value > 0)

onMounted(() => {
  init()
  setActiveTab(tab.value)
  window.addEventListener('hashchange', () => {
    const t = tabFromHash()
    tab.value = t
    setActiveTab(t)
  })
})
</script>

<template>
  <div class="shell">
    <aside class="sidebar">
      <div class="brand">
        <div class="brand__logo">CP</div>
        <div>
          <div class="brand__name">CodePorter</div>
          <div class="brand__ver">v{{ state.status?.version ?? '0.2.0' }} 本地代理</div>
        </div>
      </div>

      <nav class="nav">
        <button
          class="nav__item"
          :class="{ 'nav__item--active': tab === 'dashboard' }"
          @click="selectTab('dashboard')"
        >
          <span class="dot" :class="anyRunning ? 'dot--live' : 'dot'" />
          概览
        </button>
        <button
          class="nav__item"
          :class="{ 'nav__item--active': tab === 'bots' }"
          @click="selectTab('bots')"
        >
          机器人
        </button>
        <button
          class="nav__item"
          :class="{ 'nav__item--active': tab === 'settings' }"
          @click="selectTab('settings')"
        >
          配置
        </button>
        <button
          class="nav__item"
          :class="{ 'nav__item--active': tab === 'logs' }"
          @click="selectTab('logs')"
        >
          运行日志
          <span v-if="state.unreadLogs > 0" class="nav__badge nav__badge--unread">
            {{ state.unreadLogs > 99 ? '99+' : state.unreadLogs }}
          </span>
        </button>
      </nav>

      <div class="sidebar__foot">
        <div class="row" style="justify-content: space-between">
          <span style="color: var(--c-text-3); font-size: 11.5px">
            {{ anyRunning ? `${runningServices} 个服务运行中` : '全部服务已停止' }}
          </span>
          <span class="tag" :class="anyRunning ? 'tag--ok' : 'tag--muted'">
            {{ anyRunning ? '运行中' : '已停止' }}
          </span>
        </div>
        <button
          class="btn btn--sm theme-btn"
          :title="state.theme === 'dark' ? '切换到浅色主题' : '切换到深色主题'"
          @click="toggleTheme"
        >
          {{ state.theme === 'dark' ? '☀ 浅色' : '☾ 深色' }}
        </button>
      </div>
    </aside>

    <main class="main">
      <DashboardView v-if="tab === 'dashboard'" @go="selectTab" @notify="notify" />
      <BotsView v-else-if="tab === 'bots'" @notify="notify" />
      <SettingsView v-else-if="tab === 'settings'" @notify="notify" />
      <LogsView v-else />
    </main>

    <!-- 全局提示：右下角堆叠，点击任意一条可立即关闭 -->
    <div v-if="state.toasts.length" class="toast-wrap">
      <div
        v-for="t in state.toasts"
        :key="t.id"
        class="toast"
        :class="{ 'toast--err': t.kind === 'error' }"
        title="点击关闭"
        @click="dismissToast(t.id)"
      >
        {{ t.text }}
      </div>
    </div>
  </div>
</template>

<style scoped>
/* 未读日志用错误色徽章，和普通的计数灰字区分开。 */
.nav__badge--unread {
  color: var(--c-err);
  font-weight: 650;
}

.theme-btn {
  width: 100%;
  margin-top: 2px;
}
</style>
