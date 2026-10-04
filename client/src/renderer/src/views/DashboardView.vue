<script setup lang="ts">
import { computed } from 'vue'

import { useStore } from '../store'
import ToolCard from '../components/ToolCard.vue'

const emit = defineEmits<{ go: ['settings' | 'logs']; notify: [string, boolean?] }>()

const { state, startAgent, stopAgent, testCli } = useStore()

const running = computed(() => !!state.status?.running)
const busy = computed(() => state.busy || state.testing)

/** 可用工具数（健康探测通过）。 */
const availableCount = computed(() => state.health.filter((h) => h.available).length)

/** 已启用的工具。 */
const enabledTools = computed(() => (state.status?.ai_tools ?? []).filter((t) => t.enabled))

/** 任务统计。 */
const taskStats = computed(() => {
  const t = state.tasks
  return {
    total: t.length,
    running: t.filter((x) => x.phase === 'start').length,
    failed: t.filter((x) => x.phase === 'failed').length
  }
})

/** 有效工作目录：配置优先，否则客户端 exe 所在目录。 */
const workDir = computed(() => state.status?.work_dir || '（客户端 exe 所在目录）')

async function onToggle(): Promise<void> {
  if (running.value) await stopAgent()
  else await startAgent()
}

async function onTest(): Promise<void> {
  const r = await testCli(false)
  if (!r) {
    emit('notify', '测试失败，详见运行日志', true)
    return
  }
  if (r.ok > 0 && r.failed === 0 && r.missing === 0 && r.warned === 0) {
    emit('notify', `全部正常：${r.ok} 个工具可用`)
  } else {
    emit('notify', `可用 ${r.ok} · 未安装 ${r.missing} · 需处理 ${r.warned} · 失败 ${r.failed}`)
  }
}
</script>

<template>
  <div class="page-head">
    <h1>概览</h1>
    <p>本地代理的运行状态、本地 AI 工具与任务情况。</p>
  </div>

  <!-- 关键指标 -->
  <div class="grid grid--4">
    <div class="stat">
      <div class="stat__label"><span class="dot" :class="running ? 'dot--live' : 'dot'" />运行状态</div>
      <div class="stat__value" :style="{ color: running ? 'var(--c-ok)' : 'var(--c-text-3)' }">
        {{ running ? '运行中' : '已停止' }}
      </div>
      <div class="stat__sub">{{ running ? '正在连接网关拉取任务' : '点击右侧按钮启动' }}</div>
    </div>

    <div class="stat">
      <div class="stat__label">网关地址</div>
      <div class="stat__value stat__value--sm">{{ state.status?.gateway ?? '—' }}</div>
      <div class="stat__sub">Agent ID：{{ state.status?.agent_id ?? '—' }}</div>
    </div>

    <div class="stat">
      <div class="stat__label">本地 AI</div>
      <div class="stat__value">
        {{ availableCount }}
        <span style="font-size: 13px; color: var(--c-text-3); font-weight: 500">
          / {{ enabledTools.length }} 可用
        </span>
      </div>
      <div class="stat__sub">已启用 {{ enabledTools.length }} 个工具</div>
    </div>

    <div class="stat">
      <div class="stat__label">任务</div>
      <div class="stat__value">{{ taskStats.total }}</div>
      <div class="stat__sub">
        进行中 {{ taskStats.running }} · 失败 {{ taskStats.failed }}
      </div>
    </div>
  </div>

  <!-- 主操作 -->
  <div class="card" style="margin-top: 14px">
    <div class="card__head">
      <span class="card__title">代理控制</span>
      <span class="card__hint">配置文件：{{ state.status?.config_path ?? '—' }}</span>
    </div>
    <div class="row row--wrap">
      <button class="btn" :class="running ? 'btn--danger' : 'btn--primary'" :disabled="busy" @click="onToggle">
        {{ running ? '停止代理' : '启动代理' }}
      </button>
      <button class="btn" :disabled="busy" @click="onTest">
        {{ state.testing ? '测试中…' : '测试 CLI 连接' }}
      </button>
      <button class="btn" @click="emit('go', 'settings')">前往配置</button>
      <button class="btn" @click="emit('go', 'logs')">查看日志</button>
      <span class="spacer" />
      <span class="tag" :class="state.status?.direct_mode ? 'tag--primary' : 'tag--muted'">
        {{ state.status?.direct_mode ? 'SSE 直连模式' : 'Pull 队列模式' }}
      </span>
    </div>
  </div>

  <!-- 本地 AI 工具 -->
  <div class="card">
    <div class="card__head">
      <span class="card__title">本地 AI 工具</span>
      <span class="card__hint">工作目录：{{ workDir }}</span>
    </div>
    <div class="grid grid--2">
      <ToolCard
        v-for="t in state.status?.ai_tools ?? []"
        :key="t.model"
        :tool="t"
        :health="state.health.find((h) => h.model === t.model)"
      />
    </div>
  </div>

  <!-- 最近任务 -->
  <div class="card">
    <div class="card__head">
      <span class="card__title">最近任务</span>
      <span class="card__hint">最新 {{ state.tasks.length }} 条</span>
    </div>
    <div v-if="state.tasks.length === 0" class="empty">还没有任务记录。任务由网关下发，本地执行后回传结果。</div>
    <div v-else class="grid" style="gap: 6px">
      <div v-for="t in state.tasks.slice(0, 8)" :key="t.task_id + t.phase + (t.elapsed_ms ?? 0)" class="row">
        <span
          class="tag"
          :class="t.phase === 'success' ? 'tag--ok' : t.phase === 'failed' ? 'tag--err' : 'tag--primary'"
        >
          {{ t.phase === 'start' ? '进行中' : t.phase === 'success' ? '成功' : '失败' }}
        </span>
        <span class="tool__meta">{{ t.model }}</span>
        <span style="color: var(--c-text-2); min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap">
          {{ t.task_id }}
        </span>
        <span class="spacer" />
        <span v-if="t.elapsed_ms" class="tool__meta">{{ t.elapsed_ms }} ms</span>
      </div>
    </div>
  </div>
</template>
