<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { api } from '@/api/client'
import type { TaskItem } from '@/types'

const tasks = ref<TaskItem[]>([])
const error = ref('')
const loading = ref(false)
const limit = ref(50)

const statusLabel: Record<string, string> = {
  pending: '排队中',
  running: '执行中',
  success: '成功',
  failed: '失败',
  timeout: '超时',
  deadletter: '死信',
}

function statusTag(s: string): string {
  if (s === 'success') return 'ok'
  if (s === 'running') return 'warn'
  if (s === 'pending') return ''
  return 'danger'
}

function time(ts: number): string {
  if (!ts) return '—'
  return new Date(ts * 1000).toLocaleString('zh-CN', { hour12: false })
}

async function load() {
  loading.value = true
  try {
    const res = await api.tasks(limit.value)
    tasks.value = res.tasks ?? []
    error.value = ''
  } catch (e) {
    error.value = e instanceof Error ? e.message : '加载任务失败'
  } finally {
    loading.value = false
  }
}

onMounted(load)
</script>

<template>
  <div class="page">
    <div class="page-head">
      <div>
        <h2>任务</h2>
        <p>最近下发给本地 AI 的任务记录（网关内存保存，重启后清空）。</p>
      </div>
      <div class="row">
        <select v-model.number="limit" style="width: 110px" @change="load">
          <option :value="20">20 条</option>
          <option :value="50">50 条</option>
          <option :value="200">200 条</option>
        </select>
        <button class="btn" :disabled="loading" @click="load">刷新</button>
      </div>
    </div>

    <div v-if="error" class="alert error">{{ error }}</div>

    <div class="card">
      <table>
        <thead>
          <tr>
            <th>任务 ID</th>
            <th>来源</th>
            <th>模型</th>
            <th>通路</th>
            <th>状态</th>
            <th>重试</th>
            <th>创建时间</th>
            <th>请求预览</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="t in tasks" :key="t.id">
            <td class="mono">{{ t.id }}</td>
            <td class="muted" style="font-size: 12px">{{ t.source || '—' }}</td>
            <td class="mono">{{ t.model }}</td>
            <td>{{ t.mode === 'direct' ? '直连' : '队列' }}</td>
            <td>
              <span class="tag" :class="statusTag(t.status)">{{ statusLabel[t.status] ?? t.status }}</span>
              <div v-if="t.error" class="muted" style="font-size: 12px">{{ t.error }}</div>
            </td>
            <td>{{ t.attempts }}</td>
            <td class="muted" style="font-size: 12px">{{ time(t.created_at) }}</td>
            <td class="muted" style="font-size: 12px; max-width: 280px">{{ t.prompt }}</td>
          </tr>
          <tr v-if="!tasks.length">
            <td colspan="8" class="empty">暂无任务</td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>
