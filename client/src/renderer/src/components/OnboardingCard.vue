<script setup lang="ts">
import { computed, ref } from 'vue'

import { useStore } from '../store'

const DISMISS_KEY = 'codeporter.onboardingDismissed'

const { state } = useStore()

/** 用户手动收起后不再出现（localStorage 持久化）。 */
const dismissed = ref(localStorage.getItem(DISMISS_KEY) === '1')

function dismiss(): void {
  dismissed.value = true
  localStorage.setItem(DISMISS_KEY, '1')
}

/** 步骤 1：网关地址已填且已配置秘钥。 */
const gatewayReady = computed(
  () =>
    !!state.status?.has_key ||
    (!!state.config?.agent?.key?.trim() && !!state.config?.gateway?.addr?.trim())
)

/** 步骤 2：至少一个已启用工具在本机可用。 */
const toolReady = computed(() => state.health.some((h) => h.available))

/** 步骤 3：代理或任一机器人渠道正在运行。 */
const serving = computed(
  () =>
    !!state.status?.running ||
    Object.values(state.status?.bots ?? {}).some((b) => b.running)
)

const steps = computed(() => [
  { key: 'gateway', done: gatewayReady.value, text: '配置网关地址与连接秘钥', target: 'settings' },
  { key: 'tool', done: toolReady.value, text: '安装并启用至少一个本地 AI CLI', target: 'settings' },
  { key: 'run', done: serving.value, text: '启动代理（或 IM 机器人）开始接任务', target: 'dashboard' }
])

/** 三步全部完成后自动隐藏（不写 dismissed，重装/异常时还能作为状态回顾出现）。 */
const visible = computed(
  () => !dismissed.value && !!state.config && !steps.value.every((s) => s.done)
)

const emit = defineEmits<{ go: [string] }>()
</script>

<template>
  <div v-if="visible" class="card onboarding">
    <div class="card__head">
      <span class="card__title">快速上手</span>
      <button class="btn btn--sm btn--link" @click="dismiss">收起</button>
    </div>
    <div class="onboarding__steps">
      <button
        v-for="(s, i) in steps"
        :key="s.key"
        class="onboarding__step"
        :class="{
          'onboarding__step--done': s.done,
          'onboarding__step--static': !s.done && s.target === 'dashboard'
        }"
        @click="!s.done && s.target !== 'dashboard' && emit('go', s.target)"
      >
        <span class="onboarding__check" :class="{ 'onboarding__check--done': s.done }">
          {{ s.done ? '✓' : i + 1 }}
        </span>
        <span class="onboarding__text">{{ s.text }}</span>
      </button>
    </div>
  </div>
</template>
