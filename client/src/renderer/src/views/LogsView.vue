<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'

import { useStore } from '../store'
import type { DisplayLog } from '../store'

const { state, toast } = useStore()
const term = ref<HTMLElement | null>(null)
const filter = ref('')
const levelFilter = ref<'all' | 'error' | 'warn'>('all')
/** 新日志到达时是否自动滚到底部（用户想回看上文时可关掉）。 */
const autoScroll = ref(true)

/** 过滤后的日志。 */
const shown = computed(() => {
  let list = state.logs
  if (levelFilter.value !== 'all') list = list.filter((l) => l.level === levelFilter.value)
  const kw = filter.value.trim().toLowerCase()
  if (kw) list = list.filter((l) => l.msg.toLowerCase().includes(kw))
  return list
})

const errorCount = computed(() => state.logs.filter((l) => l.level === 'error').length)
const warnCount = computed(() => state.logs.filter((l) => l.level === 'warn').length)

// 新日志到达时自动滚到底部；用户关闭自动滚动后不强制打断。
watch(
  () => state.logs.length,
  () => {
    if (!autoScroll.value || !term.value) return
    void nextTick(() => {
      term.value!.scrollTop = term.value!.scrollHeight
    })
  }
)

function clear(): void {
  state.logs.splice(0, state.logs.length)
}

function lineText(l: DisplayLog): string {
  return `${l.time} [${l.level.toUpperCase()}] ${l.msg}`
}

/** 复制当前过滤结果到剪贴板。 */
async function copyAll(): Promise<void> {
  try {
    await navigator.clipboard.writeText(shown.value.map(lineText).join('\n'))
    toast(`已复制 ${shown.value.length} 条日志`)
  } catch {
    toast('复制失败：浏览器未授权剪贴板', 'error')
  }
}

/** 导出当前过滤结果为 .log 文件。 */
function exportLogs(): void {
  const blob = new Blob([shown.value.map(lineText).join('\n')], {
    type: 'text/plain;charset=utf-8'
  })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  const stamp = new Date().toISOString().replace(/[:.]/g, '-').slice(0, 19)
  a.href = url
  a.download = `codeporter-${stamp}.log`
  a.click()
  URL.revokeObjectURL(url)
}
</script>

<template>
  <div class="logs-page">
    <div class="page-head">
      <h1>运行日志</h1>
      <p>来自 Go 核心的实时输出，共 {{ state.logs.length }} 条（错误 {{ errorCount }} · 警告 {{ warnCount }}）。</p>
    </div>

    <div class="logs-toolbar row row--wrap">
      <input v-model="filter" class="input" style="max-width: 260px" placeholder="过滤关键字…" />
      <select v-model="levelFilter" class="select" style="width: 110px">
        <option value="all">全部级别</option>
        <option value="error">仅错误</option>
        <option value="warn">仅警告</option>
      </select>
      <label class="switch" title="新日志到达时自动滚到底部">
        <input v-model="autoScroll" type="checkbox" />
        <span class="switch__track" />
        <span style="font-size: 12px; color: var(--c-text-2)">自动滚动</span>
      </label>
      <span class="spacer" />
      <span style="color: var(--c-text-3); font-size: 12px">显示 {{ shown.length }} 条</span>
      <button class="btn btn--sm" :disabled="shown.length === 0" @click="copyAll">复制</button>
      <button class="btn btn--sm" :disabled="shown.length === 0" @click="exportLogs">导出</button>
      <button class="btn btn--sm" :disabled="state.logs.length === 0" @click="clear">清空</button>
    </div>

    <div ref="term" class="term logs-term">
      <div v-if="shown.length === 0" style="color: var(--c-text-3)">暂无日志。</div>
      <div v-for="l in shown" :key="l.id" class="term__line" :class="'lvl-' + l.level">
        <span class="term__time">{{ l.time }}</span>
        <span class="term__lvl">{{ l.level.toUpperCase() }}</span>
        <span class="term__msg">{{ l.msg }}</span>
      </div>
    </div>
  </div>
</template>

<style scoped>
/* 日志是排障主界面：占满主区域剩余高度，而不是固定 340px 小窗。 */
.logs-page {
  display: flex;
  flex-direction: column;
  height: calc(100vh - 50px); /* 抵消 .main 上下 padding（22+28） */
}

.logs-toolbar {
  gap: 8px;
  margin-bottom: 10px;
  flex: none;
}

.logs-term {
  flex: 1;
  min-height: 200px;
  height: auto;
}
</style>
