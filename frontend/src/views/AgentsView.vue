<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { api } from '@/api/client'
import type { Agent, AgentHealth } from '@/types'

const agents = ref<Agent[]>([])
const error = ref('')
const loading = ref(false)

async function load() {
  loading.value = true
  try {
    const res = await api.agents()
    agents.value = res.agents ?? []
    error.value = ''
  } catch (e) {
    error.value = e instanceof Error ? e.message : '加载节点失败'
  } finally {
    loading.value = false
  }
}

function statusTag(s: string): string {
  if (s === 'online') return 'ok'
  if (s === 'busy') return 'warn'
  return 'muted'
}

const statusLabel: Record<string, string> = { online: '在线', offline: '离线', busy: '忙碌' }

function ago(ts: number): string {
  if (!ts) return '—'
  const diff = Math.floor(Date.now() / 1000 - ts)
  if (diff < 60) return `${diff} 秒前`
  if (diff < 3600) return `${Math.floor(diff / 60)} 分钟前`
  return `${Math.floor(diff / 3600)} 小时前`
}

const emptyHealth: AgentHealth = {
  cpu_percent: 0,
  mem_percent: 0,
  inflight: 0,
  queued: 0,
  max_concurrency: 0,
  hostname: '',
  os: '',
  updated_at: '',
}

function healthOf(a: Agent): AgentHealth {
  return a.health ?? emptyHealth
}

const onlineCount = computed(() => agents.value.filter((a) => a.status !== 'offline').length)

onMounted(load)
</script>

<template>
  <div class="page">
    <div class="page-head">
      <div>
        <h2>本地节点</h2>
        <p>运行在你开发机上的 CodePorter Agent，负责调用本机 AI 编码工具。</p>
      </div>
      <button class="btn" :disabled="loading" @click="load">刷新</button>
    </div>

    <div v-if="error" class="alert error">{{ error }}</div>

    <div class="card">
      <table>
        <thead>
          <tr>
            <th>节点</th>
            <th>状态</th>
            <th>心跳</th>
            <th>队列</th>
            <th>负载</th>
            <th>环境</th>
            <th>已接入工具</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="a in agents" :key="a.id">
            <td>
              <strong>{{ a.id }}</strong>
              <div class="muted" style="font-size: 12px">{{ a.name }}</div>
            </td>
            <td><span class="tag" :class="statusTag(a.status)">{{ statusLabel[a.status] ?? a.status }}</span></td>
            <td class="muted">{{ ago(a.last_heartbeat) }}</td>
            <td>{{ a.queued }}</td>
            <td>
              <div class="muted" style="font-size: 12px">
                CPU {{ healthOf(a).cpu_percent.toFixed(0) }}% · 内存 {{ healthOf(a).mem_percent.toFixed(0) }}%
              </div>
              <div class="muted" style="font-size: 12px">
                在途 {{ healthOf(a).inflight }} / {{ healthOf(a).max_concurrency || '—' }}
              </div>
            </td>
            <td class="muted" style="font-size: 12px">
              {{ healthOf(a).hostname || '—' }}<br />{{ healthOf(a).os || '' }}
            </td>
            <td>
              <span v-if="!healthOf(a).mcps?.length" class="muted">—</span>
              <span
                v-for="m in healthOf(a).mcps ?? []"
                :key="m.model"
                class="tag"
                :class="m.available ? 'ok' : 'muted'"
                style="margin-right: 4px"
              >
                {{ m.model }}
              </span>
            </td>
          </tr>
          <tr v-if="!agents.length">
            <td colspan="7" class="empty">
              暂无节点。在开发机上运行 codeporter-agent 后会自动出现。
            </td>
          </tr>
        </tbody>
      </table>
      <p class="muted" style="font-size: 12px; margin-top: 12px">
        共 {{ agents.length }} 个节点，其中 {{ onlineCount }} 个在线。
      </p>
    </div>
  </div>
</template>
