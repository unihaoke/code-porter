<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { keysApi, type CreatedKey, type KeyView } from '@/api/account'

const list = ref<KeyView[]>([])
const loading = ref(false)
const loadError = ref('')

async function load() {
  loading.value = true
  loadError.value = ''
  try {
    list.value = (await keysApi.listMine()).keys
  } catch (e) {
    loadError.value = e instanceof Error ? e.message : '加载失败'
  } finally {
    loading.value = false
  }
}

// 创建表单
const showCreate = ref(false)
const name = ref('')
const scopeAgent = ref(true)
const scopeAPI = ref(true)
const expiryPreset = ref('never')
const error = ref('')
const creating = ref(false)

// 一次性明文展示
const created = ref<CreatedKey | null>(null)
const copied = ref(false)

function openCreate() {
  name.value = ''
  scopeAgent.value = true
  scopeAPI.value = true
  expiryPreset.value = 'never'
  error.value = ''
  showCreate.value = true
}

function expiresAt(): string | null {
  switch (expiryPreset.value) {
    case '7d':
      return new Date(Date.now() + 7 * 864e5).toISOString()
    case '30d':
      return new Date(Date.now() + 30 * 864e5).toISOString()
    case '90d':
      return new Date(Date.now() + 90 * 864e5).toISOString()
    default:
      return null
  }
}

async function submitCreate() {
  error.value = ''
  if (!name.value.trim()) {
    error.value = '请填写名称'
    return
  }
  const scopes: string[] = []
  if (scopeAgent.value) scopes.push('agent')
  if (scopeAPI.value) scopes.push('api')
  if (scopes.length === 0) {
    error.value = '至少勾选一个权限范围'
    return
  }
  creating.value = true
  try {
    const res = await keysApi.create({ name: name.value.trim(), scopes, expires_at: expiresAt() })
    created.value = res.key
    copied.value = false
    showCreate.value = false
    await load()
  } catch (e) {
    error.value = e instanceof Error ? e.message : '创建失败'
  } finally {
    creating.value = false
  }
}

async function copySecret() {
  if (!created.value) return
  try {
    await navigator.clipboard.writeText(created.value.secret)
    copied.value = true
  } catch {
    /* 剪贴板不可用时用户可手动复制 */
  }
}

async function remove(k: KeyView) {
  if (!confirm(`确认删除秘钥「${k.name}」？\n删除后使用该秘钥的客户端/API 调用会立即被拒绝。`)) return
  await keysApi.deleteMine(k.id)
  await load()
}

function scopeText(k: KeyView): string {
  return k.scopes
    .map((s) => (s === 'agent' ? '客户端接入' : 'OpenAI API'))
    .join('、')
}

function fmtTime(s?: string | null): string {
  if (!s) return '—'
  try {
    return new Date(s).toLocaleString()
  } catch {
    return s
  }
}

onMounted(load)
</script>

<template>
  <div class="page">
    <div class="page-head">
      <div>
        <h2>连接秘钥</h2>
        <p class="muted">
          秘钥用于本地客户端接入（agent）和 OpenAI 兼容调用（api）。明文仅在创建时展示一次，请立即复制保存。
        </p>
      </div>
      <button class="btn primary" @click="openCreate">+ 创建秘钥</button>
    </div>

    <div v-if="loadError" class="alert error">{{ loadError }}</div>
    <div v-if="loading" class="muted">加载中…</div>

    <div class="card" v-if="!loading">
      <table class="table">
        <thead>
          <tr>
            <th>名称</th>
            <th>前缀</th>
            <th>权限</th>
            <th>有效期至</th>
            <th>最近使用</th>
            <th>状态</th>
            <th style="width: 80px"></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="k in list" :key="k.id">
            <td>{{ k.name }}</td>
            <td class="mono">{{ k.prefix }}…</td>
            <td>{{ scopeText(k) }}</td>
            <td>{{ fmtTime(k.expires_at) }}</td>
            <td>{{ fmtTime(k.last_used_at) }}</td>
            <td>
              <span :class="['tag', k.expired ? 'tag--off' : 'tag--on']">
                {{ k.expired ? '已过期' : '有效' }}
              </span>
            </td>
            <td>
              <button class="btn btn--ghost btn--sm" @click="remove(k)">删除</button>
            </td>
          </tr>
          <tr v-if="list.length === 0">
            <td colspan="7" class="empty">还没有秘钥，点击右上角创建</td>
          </tr>
        </tbody>
      </table>
    </div>

    <!-- 创建对话框 -->
    <div v-if="showCreate" class="modal-mask" @click.self="showCreate = false">
      <div class="modal card">
        <h3>创建秘钥</h3>
        <div class="field">
          <label>名称（如：工作笔记本、CI 调用）</label>
          <input v-model="name" placeholder="my-laptop" maxlength="64" />
        </div>
        <div class="field">
          <label>权限范围</label>
          <label class="check-row"><input v-model="scopeAgent" type="checkbox" /> 客户端接入（agent：本地客户端连接网关）</label>
          <label class="check-row"><input v-model="scopeAPI" type="checkbox" /> OpenAI API（api：/v1/chat/completions 调用）</label>
        </div>
        <div class="field">
          <label>有效期</label>
          <select v-model="expiryPreset">
            <option value="never">永久</option>
            <option value="7d">7 天</option>
            <option value="30d">30 天</option>
            <option value="90d">90 天</option>
          </select>
        </div>
        <div v-if="error" class="alert error">{{ error }}</div>
        <div class="row" style="justify-content: flex-end">
          <button class="btn btn--ghost" @click="showCreate = false">取消</button>
          <button class="btn primary" :disabled="creating" @click="submitCreate">
            {{ creating ? '创建中…' : '创建' }}
          </button>
        </div>
      </div>
    </div>

    <!-- 明文一次性展示 -->
    <div v-if="created" class="modal-mask" @click.self="created = null">
      <div class="modal card">
        <h3>秘钥已生成</h3>
        <div class="alert error">
          这是查看秘钥明文的<strong>唯一机会</strong>，关闭后无法再次查看，请立即复制并妥善保存。
        </div>
        <div class="secret-box mono">{{ created.secret }}</div>
        <div class="row" style="justify-content: flex-end; margin-top: 12px">
          <button class="btn btn--ghost" @click="copySecret">{{ copied ? '已复制 ✓' : '复制秘钥' }}</button>
          <button class="btn primary" @click="created = null">我已保存，关闭</button>
        </div>
        <p class="muted" style="font-size: 12px; margin-top: 10px">
          客户端把它填到 agent.key 或环境变量 AGENT_KEY；OpenAI 调用放入 Authorization: Bearer。
        </p>
      </div>
    </div>
  </div>
</template>

<style scoped>
.secret-box {
  background: #f7f8fa;
  border: 1px dashed var(--border);
  border-radius: 8px;
  padding: 12px;
  word-break: break-all;
  font-size: 13px;
  user-select: all;
}

.check-row {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 13px;
  margin: 4px 0;
}

.tag--on {
  color: #047857;
}

.tag--off {
  color: var(--danger);
}

.btn--sm {
  padding: 4px 10px;
  font-size: 12px;
}
</style>
