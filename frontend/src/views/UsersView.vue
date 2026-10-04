<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { usersApi, type UserView } from '@/api/account'
import { useAuthStore } from '@/stores/auth'

const auth = useAuthStore()
const list = ref<UserView[]>([])
const loadError = ref('')

async function load() {
  loadError.value = ''
  try {
    list.value = (await usersApi.list()).users
  } catch (e) {
    loadError.value = e instanceof Error ? e.message : '加载失败'
  }
}

const showCreate = ref(false)
const username = ref('')
const password = ref('')
const role = ref<'member' | 'admin'>('member')
const error = ref('')
const saving = ref(false)

function openCreate() {
  username.value = ''
  password.value = ''
  role.value = 'member'
  error.value = ''
  showCreate.value = true
}

async function submitCreate() {
  error.value = ''
  saving.value = true
  try {
    await usersApi.create(username.value.trim(), password.value, role.value)
    showCreate.value = false
    await load()
  } catch (e) {
    error.value = e instanceof Error ? e.message : '创建失败'
  } finally {
    saving.value = false
  }
}

async function remove(u: UserView) {
  if (!confirm(`确认删除用户「${u.username}」？\n其秘钥、会话、本地节点与机器人配置会一并删除，且不可恢复。`)) return
  try {
    await usersApi.remove(u.id)
    await load()
  } catch (e) {
    alert(e instanceof Error ? e.message : '删除失败')
  }
}

async function resetPwd(u: UserView) {
  const pw = prompt(`为用户「${u.username}」设置新密码（至少 6 位）：`)
  if (pw === null) return
  if (pw.length < 6) {
    alert('密码至少 6 位')
    return
  }
  try {
    await usersApi.resetPassword(u.id, pw)
    alert('密码已重置，该用户的其他会话已失效')
  } catch (e) {
    alert(e instanceof Error ? e.message : '重置失败')
  }
}

function fmt(s: string): string {
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
        <h2>用户管理</h2>
        <p class="muted">创建登录账号、分配管理员角色、重置密码。删除用户会级联清理其全部资源。</p>
      </div>
      <button class="btn primary" @click="openCreate">+ 创建用户</button>
    </div>

    <div v-if="loadError" class="alert error">{{ loadError }}</div>

    <div class="card">
      <table class="table">
        <thead>
          <tr>
            <th>用户名</th>
            <th>角色</th>
            <th>状态</th>
            <th>创建时间</th>
            <th style="width: 200px"></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="u in list" :key="u.id">
            <td>
              {{ u.username }}
              <span v-if="u.id === auth.user?.id" class="muted" style="font-size: 11px">（当前账号）</span>
            </td>
            <td>
              <span :class="['tag', u.role === 'admin' ? 'tag--admin' : 'tag--member']">
                {{ u.role === 'admin' ? '管理员' : '普通用户' }}
              </span>
            </td>
            <td>{{ u.status === 'active' ? '启用' : '停用' }}</td>
            <td>{{ fmt(u.created_at) }}</td>
            <td>
              <button class="btn btn--ghost btn--sm" @click="resetPwd(u)">重置密码</button>
              <button
                class="btn btn--ghost btn--sm btn--danger"
                :disabled="u.id === auth.user?.id"
                :title="u.id === auth.user?.id ? '不能删除自己' : ''"
                @click="remove(u)"
              >
                删除
              </button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <div v-if="showCreate" class="modal-mask" @click.self="showCreate = false">
      <div class="modal card">
        <h3>创建用户</h3>
        <div class="field">
          <label>用户名（3-64 位小写字母/数字/_-）</label>
          <input v-model="username" placeholder="alice" autocomplete="off" />
        </div>
        <div class="field">
          <label>初始密码（至少 6 位）</label>
          <input v-model="password" type="password" autocomplete="new-password" />
        </div>
        <div class="field">
          <label>角色</label>
          <select v-model="role">
            <option value="member">普通用户（管理自己的秘钥/节点/机器人）</option>
            <option value="admin">管理员（可管理用户、查看全局）</option>
          </select>
        </div>
        <div v-if="error" class="alert error">{{ error }}</div>
        <div class="row" style="justify-content: flex-end">
          <button class="btn btn--ghost" @click="showCreate = false">取消</button>
          <button class="btn primary" :disabled="saving" @click="submitCreate">
            {{ saving ? '创建中…' : '创建' }}
          </button>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.btn--sm {
  padding: 4px 10px;
  font-size: 12px;
  margin-right: 6px;
}

.btn--danger {
  color: var(--danger);
}

.btn--danger:disabled {
  color: var(--muted);
}

.tag--admin {
  color: var(--brand);
}

.tag--member {
  color: var(--muted);
}
</style>
