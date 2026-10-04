<script setup lang="ts">
import { computed } from 'vue'

import type { AiToolStatus, HealthEntry } from '@shared/types'

const props = defineProps<{ tool: AiToolStatus; health?: HealthEntry }>()

/** 展示名：优先用可执行文件名（更贴近用户认知），回退到模型 id。 */
const title = computed(() => props.tool.label || props.tool.model)

/** 健康状态 → 展示用的标签样式与文案。 */
const badge = computed(() => {
  if (!props.tool.enabled) return { cls: 'tag--muted', text: '未启用' }
  const h = props.health
  if (!h) return { cls: 'tag--muted', text: '检测中' }
  if (h.available) return { cls: 'tag--ok', text: '可用' }
  const d = h.detail.toLowerCase()
  if (d.includes('not in path') || d.includes('not installed')) {
    return { cls: 'tag--err', text: '未安装' }
  }
  return { cls: 'tag--warn', text: '异常' }
})
</script>

<template>
  <div class="tool">
    <span class="dot" :class="badge.cls === 'tag--ok' ? 'dot--ok' : badge.cls === 'tag--err' ? 'dot--err' : ''" />
    <div style="min-width: 0">
      <div class="tool__name">{{ title }}</div>
      <div class="tool__meta">
        {{ tool.command || '未配置命令' }}
        <span v-if="tool.mode"> · {{ tool.mode }}</span>
      </div>
    </div>
    <div class="tool__right">
      <span class="tag" :class="badge.cls">{{ badge.text }}</span>
    </div>
  </div>
</template>
