<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { api } from '@/api/client'
import type { Overview } from '@/types'

const data = ref<Overview | null>(null)
const error = ref('')

async function load() {
  try {
    data.value = await api.overview()
    error.value = ''
  } catch (e) {
    error.value = e instanceof Error ? e.message : '加载概览失败'
  }
}

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
  if (s === 'running' || s === 'pending') return 'warn'
  if (s === 'failed' || s === 'deadletter' || s === 'timeout') return 'danger'
  return 'muted'
}

onMounted(load)
</script>

<template>
  <div class="page">
    <div class="page-head">
      <div>
        <h2>概览</h2>
        <p>网关与本地节点的实时状态。</p>
      </div>
      <button class="btn" @click="load">刷新</button>
    </div>

    <div v-if="error" class="alert error">{{ error }}</div>

    <div v-if="data" class="grid-stats">
      <div class="card stat">
        <div class="label">本地节点</div>
        <div class="value">{{ data.agents_online }} / {{ data.agents }}</div>
        <div class="muted">在线 / 总数</div>
      </div>
      <div class="card stat">
        <div class="label">长连接</div>
        <div class="value">{{ data.ws_conns }}</div>
        <div class="muted">直连模式可用连接</div>
      </div>
      <div class="card stat">
        <div class="label">任务（全部）</div>
        <div class="value">
          {{ Object.values(data.tasks).reduce((a, b) => a + b, 0) }}
        </div>
        <div class="muted">按状态分布见下</div>
      </div>
    </div>

    <div v-if="data" class="grid-2">
      <div class="card">
        <h3 style="font-size: 14px; margin-bottom: 12px">任务状态</h3>
        <table>
          <thead>
            <tr>
              <th>状态</th>
              <th style="text-align: right">数量</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="(v, k) in data.tasks" :key="k">
              <td><span class="tag" :class="statusTag(k)">{{ statusLabel[k] ?? k }}</span></td>
              <td style="text-align: right">{{ v }}</td>
            </tr>
            <tr v-if="!Object.keys(data.tasks).length">
              <td colspan="2" class="empty">暂无任务</td>
            </tr>
          </tbody>
        </table>
      </div>

      <div class="card">
        <h3 style="font-size: 14px; margin-bottom: 12px">本地 AI 工具</h3>
        <table>
          <thead>
            <tr>
              <th>模型</th>
              <th>状态</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="m in data.models" :key="m.model">
              <td class="mono">{{ m.model }}</td>
              <td>
                <span class="tag" :class="m.available ? 'ok' : 'muted'">
                  {{ m.available ? '可用' : '未就绪' }}
                </span>
              </td>
            </tr>
          </tbody>
        </table>
        <p class="muted" style="font-size: 12px; margin-top: 10px">
          状态来自本地 Agent 的健康上报；显示「未就绪」通常是本机未安装或未登录对应工具。
        </p>
      </div>
    </div>
  </div>
</template>

<style scoped>
.stat .label {
  font-size: 12px;
  color: var(--muted);
}

.stat .value {
  font-size: 24px;
  font-weight: 600;
  margin: 4px 0 2px;
}

.stat .muted {
  font-size: 12px;
}

.grid-2 {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(320px, 1fr));
  gap: 14px;
}
</style>
