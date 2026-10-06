<script setup lang="ts">
/**
 * 配置页 / 机器人页统一的底部保存条（sticky 贴底）。
 * 两个动作语义不同：
 * - 保存配置：仅落盘，已在运行的服务不受影响；
 * - 保存并重启：落盘后自动 stop+start 当前在运行的服务，让改动立即生效。
 */
defineProps<{
  /** 是否有未保存改动（控制「未保存」标记）。 */
  dirty: boolean
  /** 保存 / 重启进行中（禁用按钮）。 */
  busy: boolean
  /** 是否存在运行中、保存后需要重启的服务（用于提示文案）。 */
  anyRunning: boolean
}>()

const emit = defineEmits<{
  save: []
  restart: []
}>()
</script>

<template>
  <div class="savebar">
    <span v-if="dirty" class="savebar__dirty">有未保存的改动</span>
    <button class="btn btn--primary" :disabled="busy" @click="emit('save')">
      {{ busy ? '保存中…' : '保存配置' }}
    </button>
    <button class="btn" :disabled="busy" @click="emit('restart')">
      {{ busy ? '处理中…' : '保存并重启运行中的服务' }}
    </button>
    <span class="savebar__hint">
      <template v-if="anyRunning">
        代理 / 机器人 / 工具在启动时固化配置，点「保存并重启」可让改动立即生效
      </template>
      <template v-else>当前无运行中的服务，保存后下次启动即生效</template>
    </span>
  </div>
</template>
