<script setup lang="ts">
import { computed } from 'vue'

import { useStore } from '../store'
import ToolCard from '../components/ToolCard.vue'

const emit = defineEmits<{ go: ['settings' | 'logs']; notify: [string, boolean?] }>()

const { state, startAgent, stopAgent, startBots, stopBots, testCli } = useStore()

/** 代理（网关任务通道）运行状态。 */
const running = computed(() => !!state.status?.running)
const busy = computed(() => state.busy || state.testing)

/** 飞书机器人运行态（与代理独立，来自核心 status.bots.feishu）。 */
const botStatus = computed(() => state.status?.bots?.feishu)
const botRunning = computed(() => !!botStatus.value?.running)
const botEnabled = computed(() => !!state.config?.bots?.feishu?.enabled)
const botConfigured = computed(
  () =>
    !!botStatus.value?.configured ||
    (!!state.config?.bots?.feishu?.app_id && !!state.config?.bots?.feishu?.app_secret)
)

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

/** 独立开关机器人服务：不经过网关，消息由本机长连接直接闭环。 */
async function onToggleBot(): Promise<void> {
  if (botRunning.value) {
    await stopBots()
    return
  }
  const err = await startBots()
  if (err) emit('notify', `机器人启动失败：${err}`, true)
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
    <p>代理（网关下发的任务）与机器人（飞书长连接消息）是两个相互独立、可分别启停的服务。</p>
  </div>

  <!-- 关键指标 -->
  <div class="grid grid--4">
    <div class="stat">
      <div class="stat__label"><span class="dot" :class="running ? 'dot--live' : 'dot'" />代理状态</div>
      <div class="stat__value" :style="{ color: running ? 'var(--c-ok)' : 'var(--c-text-3)' }">
        {{ running ? '运行中' : '已停止' }}
      </div>
      <div class="stat__sub">{{ running ? '正在连接网关拉取任务' : '机器人可脱离代理单独运行' }}</div>
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

  <!-- 代理服务：网关任务通道 -->
  <div class="card" style="margin-top: 14px">
    <div class="card__head">
      <div class="card__titlewrap">
        <span class="card__title">代理服务</span>
        <span class="badge" :class="running ? 'badge--on' : 'badge--off'">
          {{ running ? '运行中' : '已停止' }}
        </span>
      </div>
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
    <p class="svc-note">接收网关 / OpenAI 兼容接口 / 网页控制台下发的任务，调用本机 AI 执行后回传。</p>
  </div>

  <!-- 机器人服务：飞书长连接，独立于代理启停 -->
  <div class="card" :class="{ 'card--dim': !botEnabled }">
    <div class="card__head">
      <div class="card__titlewrap">
        <span class="card__title">机器人服务 · 飞书</span>
        <span class="badge" :class="botRunning ? 'badge--on' : 'badge--off'">
          {{ !botEnabled ? '未启用' : botRunning ? '运行中' : '已停止' }}
        </span>
      </div>
      <button class="btn btn--sm" @click="emit('go', 'settings')">配置</button>
    </div>

    <div v-if="botEnabled" class="row row--wrap">
      <button
        class="btn"
        :class="botRunning ? 'btn--danger' : 'btn--primary'"
        :disabled="state.botBusy"
        @click="onToggleBot"
      >
        {{ state.botBusy ? '处理中…' : botRunning ? '停止机器人' : '启动机器人' }}
      </button>
      <span class="spacer" />
      <span class="tag tag--muted">长连接模式</span>
      <span class="tag tag--primary">思考过程实时推送到同一张卡片</span>
    </div>

    <div v-if="botEnabled" class="row row--wrap svc-meta">
      <span><span class="tool__meta">App ID：</span>{{ botStatus?.app_id || '未配置' }}</span>
      <span><span class="tool__meta">模型：</span>{{ botStatus?.model || 'claude-code' }}</span>
      <span class="tag" :class="botStatus?.mention_only ? 'tag--primary' : 'tag--muted'">
        {{ botStatus?.mention_only ? '仅响应 @机器人' : '响应所有群消息' }}
      </span>
      <span v-if="!botConfigured" class="tag tag--err">凭证未配置完整</span>
    </div>
    <p v-if="botEnabled" class="svc-note">
      机器人独立与飞书保持长连接，消息在本机直接处理，<strong>无需启动代理、无需公网域名</strong>；
      同一 App ID 全机只允许一个实例，重复回复多由多开引起。
    </p>
    <div v-else class="row">
      <button class="btn" @click="emit('go', 'settings')">前往配置并启用</button>
      <span class="tool__meta" style="align-self: center">启用并保存 App ID / Secret 后，可在此独立启停。</span>
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

<style scoped>
/* 服务卡下方的说明文字。 */
.svc-note {
  margin: 10px 0 0;
  font-size: 12px;
  line-height: 1.7;
  color: var(--c-text-3);
}

/* 机器人配置摘要行。 */
.svc-meta {
  gap: 8px 18px;
  font-size: 12.5px;
  margin-top: 10px;
}
</style>
