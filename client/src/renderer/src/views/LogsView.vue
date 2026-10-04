<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'

import { useStore } from '../store'

const { state } = useStore()
const term = ref<HTMLElement | null>(null)
const filter = ref('')
const levelFilter = ref<'all' | 'error' | 'warn'>('all')

/** 过滤后的日志。 */
const shown = computed(() => {
  let list = state.logs
  if (levelFilter.value !== 'all') list = list.filter((l) => l.level === levelFilter.value)
  const kw = filter.value.trim().toLowerCase()
  if (kw) list = list.filter((l) => l.msg.toLowerCase().includes(kw))
  return list
})

// 新日志到达时自动滚到底部；用户手动上滑时不强制打断。
watch(
  () => state.logs.length,
  () => {
    if (!term.value) return
    void nextTick(() => {
      term.value!.scrollTop = term.value!.scrollHeight
    })
  }
)

function clear(): void {
  state.logs.splice(0, state.logs.length)
}
</script>

<template>
  <div class="page-head">
    <h1>运行日志</h1>
    <p>来自 Go 核心的实时输出，共 {{ state.logs.length }} 条。</p>
  </div>

  <div class="row" style="margin-bottom: 10px">
    <input v-model="filter" class="input" style="max-width: 300px" placeholder="过滤关键字…" />
    <select v-model="levelFilter" class="select" style="width: 120px">
      <option value="all">全部级别</option>
      <option value="error">仅错误</option>
      <option value="warn">仅警告</option>
    </select>
    <span class="spacer" />
    <span style="color: var(--c-text-3); font-size: 12px">显示 {{ shown.length }} 条</span>
    <button class="btn btn--sm" @click="clear">清空</button>
  </div>

  <div ref="term" class="term">
    <div v-if="shown.length === 0" style="color: #626b78">暂无日志。</div>
    <div v-for="l in shown" :key="l.id" class="term__line" :class="'lvl-' + l.level">
      <span class="term__time">{{ l.time }}</span>
      <span class="term__lvl">{{ l.level.toUpperCase() }}</span>
      <span class="term__msg">{{ l.msg }}</span>
    </div>
  </div>
</template>
