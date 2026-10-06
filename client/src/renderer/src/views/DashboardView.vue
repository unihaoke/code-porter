<script setup lang="ts">
import { computed } from 'vue'

import { useStore } from '../store'
import ToolCard from '../components/ToolCard.vue'
import OnboardingCard from '../components/OnboardingCard.vue'

const emit = defineEmits<{ go: ['settings' | 'logs' | 'bots']; notify: [string, boolean?] }>()

const {
  state,
  startAgent,
  stopAgent,
  startBots,
  stopBots,
  startTools,
  stopTools,
  refreshOverview
} = useStore()

/** 代理（网关任务通道）运行状态。 */
const running = computed(() => !!state.status?.running)
const busy = computed(() => state.busy)

/**
 * IM 机器人渠道元数据（展示层唯一的渠道清单，新增渠道时加一行即可）。
 * credField 指向该渠道配置里的身份字段，用于 status 未到位时判断凭证是否填全。
 */
const BOT_CHANNELS = [
  {
    key: 'feishu',
    name: '飞书',
    credField: 'app_id',
    secretField: 'app_secret',
    credLabel: 'App ID',
    streamTag: '思考过程实时推送到同一张卡片',
    singleNote: '同一 App ID 全机只允许一个实例，重复回复多由多开引起'
  },
  {
    key: 'wecom',
    name: '企业微信',
    credField: 'bot_id',
    secretField: 'secret',
    credLabel: 'Bot ID',
    streamTag: '流式消息实时更新，过程区在正文生成后折叠',
    singleNote: '同一 Bot ID 全平台只允许一条长连接，多开会重复回复'
  }
] as const

/** 某渠道的实时运行态（来自核心 status.bots.<channel>）。 */
function botRuntimeStatus(key: string) {
  return state.status?.bots?.[key]
}
const botRunning = (key: string): boolean => !!state.status?.bots?.[key]?.running
const botEnabled = (key: string): boolean => {
  const b = state.config?.bots?.[key]
  return !!b?.enabled
}
/** 凭证是否配置完整：优先信后端状态，状态未到位时回退看本地配置。 */
function botConfigured(key: string, credField: string, secretField: string): boolean {
  const fromStatus = state.status?.bots?.[key]?.configured
  if (typeof fromStatus === 'boolean') return fromStatus
  const b = state.config?.bots?.[key] as
    | Record<string, string | boolean | undefined>
    | undefined
  return !!b && !!(b[credField] as string) && !!(b[secretField] as string)
}
/** 正在运行的渠道数（标题徽标用）。 */
const runningBotCount = computed(
  () => BOT_CHANNELS.filter((c) => botRunning(c.key)).length
)
/** 至少一个渠道已启用（卡片整体未启用态用）。 */
const anyBotEnabled = computed(() => BOT_CHANNELS.some((c) => botEnabled(c.key)))

/** 可用工具数（健康探测通过）。 */
const availableCount = computed(() => state.health.filter((h) => h.available).length)

/** 已启用的工具。 */
const enabledTools = computed(() => (state.status?.ai_tools ?? []).filter((t) => t.enabled))

/** 本地 AI 工具预热运行时状态（独立于代理 / 机器人，仅手动启停）。 */
const toolsRunning = computed(() => !!state.status?.tools_running)

/** 任务统计。 */
const taskStats = computed(() => {
  const t = state.tasks
  return {
    total: t.length,
    running: t.filter((x) => x.phase === 'start').length,
    failed: t.filter((x) => x.phase === 'failed').length
  }
})

/** 聚合在运行的服务数：代理（1）+ 各机器人渠道 + 本地 AI 工具（1）。 */
const runningServiceCount = computed(
  () =>
    (running.value ? 1 : 0) +
    runningBotCount.value +
    (toolsRunning.value ? 1 : 0)
)

/** 任务行主文案：优先 detail（通常是输入摘要），否则截短 task_id。 */
function taskTitle(t: { detail?: string; task_id: string }): string {
  const d = t.detail?.trim()
  if (d) return d.length > 80 ? d.slice(0, 80) + '…' : d
  return t.task_id.length > 16 ? t.task_id.slice(0, 16) + '…' : t.task_id
}

/** 有效工作目录：配置优先，否则客户端 exe 所在目录。 */
const workDir = computed(() => state.status?.work_dir || '（客户端 exe 所在目录）')

async function onToggle(): Promise<void> {
  if (running.value) await stopAgent()
  else await startAgent()
}

/** 独立开关单个机器人渠道：不经过网关，消息由本机长连接直接闭环。 */
async function onToggleBot(key: string): Promise<void> {
  if (botRunning(key)) {
    const err = await stopBots(key)
    if (err) emit('notify', `机器人停止失败：${err}`, true)
    return
  }
  const err = await startBots(key)
  if (err) emit('notify', `机器人启动失败：${err}`, true)
}

/** 手动启停本地 AI 工具：仅预热配置中已启用的工具，开机不会自动启动。 */
async function onToggleTools(): Promise<void> {
  if (toolsRunning.value) {
    const err = await stopTools()
    if (err) emit('notify', `本地 AI 工具停止失败：${err}`, true)
    return
  }
  const err = await startTools()
  if (err) emit('notify', `本地 AI 工具启动失败：${err}`, true)
}

/** 手动刷新概览：重拉运行态并重跑免额度安装检测。 */
async function onRefresh(): Promise<void> {
  await refreshOverview()
}

/** 引导卡跳转：目标就是当前概览页的步骤不导航。 */
function onboardingGo(target: string): void {
  if (target === 'settings' || target === 'logs' || target === 'bots') emit('go', target)
}
</script>

<template>
  <div class="page-head">
    <div class="page-head__row">
      <h1>概览</h1>
      <button class="btn btn--sm" :disabled="state.refreshing" title="重新拉取运行状态并检测本地 AI 安装情况" @click="onRefresh">
        {{ state.refreshing ? '刷新中…' : '刷新' }}
      </button>
    </div>
    <p>代理（网关下发的任务）与 IM 机器人（飞书 / 企业微信长连接消息）相互独立；机器人各渠道之间也可分别启停。</p>
  </div>

  <!-- 关键指标 -->
  <div class="grid grid--3">
    <div class="stat">
      <div class="stat__label"><span class="dot" :class="runningServiceCount > 0 ? 'dot--live' : 'dot'" />服务状态</div>
      <div class="stat__value" :style="{ color: runningServiceCount > 0 ? 'var(--c-ok)' : 'var(--c-text-3)' }">
        {{ runningServiceCount > 0 ? `${runningServiceCount} 个运行中` : '全部已停止' }}
      </div>
      <div class="stat__sub">代理 · 机器人 · 本地 AI 工具合计</div>
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

  <!-- 首次使用引导（全部步骤完成或手动收起后隐藏） -->
  <div style="margin-top: 14px">
    <OnboardingCard @go="onboardingGo" />
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
      <button class="btn" @click="emit('go', 'settings')">前往配置</button>
      <button class="btn" @click="emit('go', 'logs')">查看日志</button>
      <span class="spacer" />
      <span class="tag" :class="state.status?.direct_mode ? 'tag--primary' : 'tag--muted'">
        {{ state.status?.direct_mode ? 'SSE 直连模式' : 'Pull 队列模式' }}
      </span>
    </div>
    <p class="svc-note">接收网关 / OpenAI 兼容接口 / 网页控制台下发的任务，调用本机 AI 执行后回传。</p>
    <div class="row row--wrap svc-meta">
      <span><span class="tool__meta">网关：</span>{{ state.status?.gateway || '—' }}</span>
      <span><span class="tool__meta">Agent ID：</span>{{ state.status?.agent_id || '—' }}</span>
    </div>
  </div>

  <!-- IM 机器人：多渠道长连接，各渠道独立于代理、独立于彼此启停 -->
  <div class="card" :class="{ 'card--dim': !anyBotEnabled }">
    <div class="card__head">
      <div class="card__titlewrap">
        <span class="card__title">IM 机器人</span>
        <span class="badge" :class="runningBotCount > 0 ? 'badge--on' : 'badge--off'">
          {{ !anyBotEnabled ? '未启用' : runningBotCount > 0 ? `${runningBotCount} 个渠道运行中` : '已停止' }}
        </span>
      </div>
      <button class="btn btn--sm" @click="emit('go', 'bots')">配置</button>
    </div>

    <div
      v-for="ch in BOT_CHANNELS"
      :key="ch.key"
      class="bot-channel"
      :class="{ 'bot-channel--off': !botEnabled(ch.key) }"
    >
      <div class="row row--wrap">
        <span class="bot-channel__name">{{ ch.name }}</span>
        <span class="badge" :class="botRunning(ch.key) ? 'badge--on' : 'badge--off'">
          {{ !botEnabled(ch.key) ? '未启用' : botRunning(ch.key) ? '运行中' : '已停止' }}
        </span>
        <span v-if="botEnabled(ch.key)" class="spacer" />
        <button
          v-if="botEnabled(ch.key)"
          class="btn btn--sm"
          :class="botRunning(ch.key) ? 'btn--danger' : 'btn--primary'"
          :disabled="!!state.botBusy[ch.key]"
          @click="onToggleBot(ch.key)"
        >
          {{ state.botBusy[ch.key] ? '处理中…' : botRunning(ch.key) ? '停止' : '启动' }}
        </button>
      </div>

      <div v-if="botEnabled(ch.key)" class="row row--wrap svc-meta">
        <span><span class="tool__meta">{{ ch.credLabel}}：</span>{{ botRuntimeStatus(ch.key)?.credential_id || '未配置' }}</span>
        <span><span class="tool__meta">模型：</span>{{ botRuntimeStatus(ch.key)?.model || 'claude-code' }}</span>
        <span class="tag" :class="botRuntimeStatus(ch.key)?.mention_only ? 'tag--primary' : 'tag--muted'">
          {{ botRuntimeStatus(ch.key)?.mention_only ? '仅响应 @机器人' : '响应所有群消息' }}
        </span>
        <span v-if="!botConfigured(ch.key, ch.credField, ch.secretField)" class="tag tag--err">
          凭证未配置完整
        </span>
      </div>
      <p v-if="botEnabled(ch.key)" class="svc-note">
        <span class="tag tag--muted bot-channel__streamtag">{{ ch.streamTag }}</span>
        {{ ch.singleNote }}。
      </p>
      <p v-else class="svc-note bot-channel__disabled">
        未启用：在「配置」页开启并保存凭证后，可在此独立启停（不影响其他渠道与代理）。
      </p>
    </div>
  </div>

  <!-- 本地 AI 工具：手动预热，独立于代理 / 机器人，开机不自动启动 -->
  <div class="card">
    <div class="card__head">
      <div class="card__titlewrap">
        <span class="card__title">本地 AI 工具</span>
        <span class="badge" :class="toolsRunning ? 'badge--on' : 'badge--off'">
          {{ toolsRunning ? '运行中' : '已停止' }}
        </span>
      </div>
      <span class="card__hint">工作目录：{{ workDir }}</span>
    </div>
    <div class="row row--wrap">
      <button
        class="btn"
        :class="toolsRunning ? 'btn--danger' : 'btn--primary'"
        :disabled="state.toolsBusy || (!toolsRunning && enabledTools.length === 0)"
        @click="onToggleTools"
      >
        {{ state.toolsBusy ? '处理中…' : toolsRunning ? '停止本地 AI 工具' : '启动本地 AI 工具' }}
      </button>
      <span class="spacer" />
      <span class="tag tag--muted">仅启动配置中已启用的工具，开机不会自动启动</span>
    </div>
    <div class="grid grid--2" style="margin-top: 12px">
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
      <div v-for="t in state.tasks.slice(0, 8)" :key="t.task_id + t.phase + (t.elapsed_ms ?? 0)" class="task-row">
        <span
          class="tag"
          :class="t.phase === 'success' ? 'tag--ok' : t.phase === 'failed' ? 'tag--err' : 'tag--primary'"
        >
          {{ t.phase === 'start' ? '进行中' : t.phase === 'success' ? '成功' : '失败' }}
        </span>
        <span class="tag tag--muted">{{ t.model }}</span>
        <span class="task-row__title" :title="t.detail || t.task_id">{{ taskTitle(t) }}</span>
        <span class="spacer" />
        <span v-if="t.elapsed_ms" class="tool__meta">{{ t.elapsed_ms }} ms</span>
      </div>
    </div>
  </div>
</template>

<style scoped>
/* 标题行：标题左、刷新按钮右。 */
.page-head__row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.page-head__row h1 {
  margin: 0;
}

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

/* IM 机器人卡内的渠道分区：渠道之间留分隔感，未启用渠道弱化。 */
.bot-channel {
  padding: 12px 0;
  border-top: 1px dashed var(--c-border, #e5e7eb);
}

.bot-channel:first-of-type {
  border-top: none;
  padding-top: 4px;
}

.bot-channel__name {
  font-weight: 600;
  font-size: 14px;
  color: var(--c-text-1);
}

.bot-channel__streamtag {
  margin-right: 8px;
}

.bot-channel__disabled {
  margin-top: 6px;
}

.bot-channel--off .bot-channel__name {
  color: var(--c-text-3);
}

/* 最近任务行：状态 + 模型 + 摘要单行省略。 */
.task-row {
  display: flex;
  align-items: center;
  gap: 8px;
}

.task-row__title {
  color: var(--c-text-2);
  font-size: 12.5px;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
