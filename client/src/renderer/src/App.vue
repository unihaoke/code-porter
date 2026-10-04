<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'

import { useStore } from './store'
import DashboardView from './views/DashboardView.vue'
import SettingsView from './views/SettingsView.vue'
import LogsView from './views/LogsView.vue'

type Tab = 'dashboard' | 'settings' | 'logs'

const { state, init, toast, dismissToast } = useStore()

// 标签页与 URL hash 同步：既支持 #settings / #logs 深链，
// 也让自动化截图能直接打开指定视图。
const TABS: Tab[] = ['dashboard', 'settings', 'logs']

function tabFromHash(): Tab {
  const h = location.hash.replace('#', '') as Tab
  return TABS.includes(h) ? h : 'dashboard'
}

const tab = ref<Tab>(tabFromHash())

function selectTab(t: Tab): void {
  tab.value = t
  location.hash = t === 'dashboard' ? '' : t
}
/** 子视图的轻提示统一走全局 toast 队列（成功 3s、失败 8s 自动消失，也可点击关闭）。 */
function notify(text: string, err = false): void {
  toast(text, err ? 'error' : 'info')
}

const running = computed(() => !!state.status?.running)
const logCount = computed(() => state.logs.length)

/** 侧边栏底部的运行状态点。 */
const statusDot = computed(() => (running.value ? 'dot--live' : 'dot'))

onMounted(() => {
  init()
  window.addEventListener('hashchange', () => (tab.value = tabFromHash()))
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
          <span class="dot" :class="statusDot" />
          概览
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
          <span class="nav__badge">{{ logCount }}</span>
        </button>
      </nav>

      <div class="sidebar__foot">
        <div class="row" style="justify-content: space-between">
          <span style="color: var(--c-text-3); font-size: 11.5px">
            {{ running ? '代理运行中' : '代理已停止' }}
          </span>
          <span class="tag" :class="running ? 'tag--ok' : 'tag--muted'">
            {{ running ? '在线' : '离线' }}
          </span>
        </div>
      </div>
    </aside>

    <main class="main">
      <DashboardView v-if="tab === 'dashboard'" @go="selectTab" @notify="notify" />
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
