<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { api } from '@/api/client'
import type { Bot, BotChannel, BotInput } from '@/types'

const bots = ref<Bot[]>([])
const error = ref('')
const notice = ref('')
const loading = ref(false)

// 编辑弹窗
const dialog = ref(false)
const editingId = ref<string | null>(null)
const form = ref<BotInput>(blankForm())

function blankForm(): BotInput {
  return {
    name: '',
    channel: 'feishu',
    model: '',
    mode: 'pull',
    agent_id: '',
    webhook_url: '',
    secret: '',
    token: '',
    aes_key: '',
    system_prompt: '',
    mention_only: true,
    enabled: true,
  }
}

const isWecom = computed(() => form.value.channel === 'wecom')
const dialogTitle = computed(() => (editingId.value ? '编辑机器人' : '新建机器人'))

async function load() {
  loading.value = true
  try {
    const res = await api.bots()
    bots.value = res.bots ?? []
    error.value = ''
  } catch (e) {
    error.value = e instanceof Error ? e.message : '加载机器人失败'
  } finally {
    loading.value = false
  }
}

function openCreate() {
  editingId.value = null
  form.value = blankForm()
  dialog.value = true
}

function openEdit(b: Bot) {
  editingId.value = b.id
  form.value = {
    name: b.name,
    channel: b.channel,
    model: b.model,
    mode: b.mode,
    agent_id: b.agent_id,
    // 后端不回传密钥明文，留空表示不修改
    webhook_url: '',
    secret: '',
    token: '',
    aes_key: b.has_aes_key ? '__unchanged__' : '',
    system_prompt: b.system_prompt,
    mention_only: b.mention_only,
    enabled: b.enabled,
  }
  dialog.value = true
}

async function submit() {
  const payload: Partial<BotInput> = { ...form.value }
  // 占位值表示「不修改」，不能提交给后端
  if (payload.aes_key === '__unchanged__') delete payload.aes_key
  for (const key of Object.keys(payload) as (keyof BotInput)[]) {
    if (payload[key] === '') delete payload[key]
  }
  try {
    if (editingId.value) {
      await api.updateBot(editingId.value, payload)
    } else {
      await api.createBot(payload as BotInput)
    }
    dialog.value = false
    notice.value = editingId.value ? '已保存' : '已创建，请到 IM 平台配置回调地址'
    await load()
  } catch (e) {
    error.value = e instanceof Error ? e.message : '保存失败'
  }
}

async function toggle(b: Bot) {
  try {
    await api.toggleBot(b.id, !b.enabled)
    await load()
  } catch (e) {
    error.value = e instanceof Error ? e.message : '操作失败'
  }
}

async function remove(b: Bot) {
  if (!confirm(`确定删除机器人「${b.name}」？删除后对应回调地址立即失效。`)) return
  try {
    await api.deleteBot(b.id)
    await load()
  } catch (e) {
    error.value = e instanceof Error ? e.message : '删除失败'
  }
}

async function copy(text: string) {
  try {
    await navigator.clipboard.writeText(text)
    notice.value = '已复制到剪贴板'
  } catch {
    notice.value = '复制失败，请手动选择文本'
  }
}

function channelHint(channel: BotChannel): string {
  return channel === 'feishu' ? '飞书' : '企业微信'
}

onMounted(load)
</script>

<template>
  <div class="page">
    <div class="page-head">
      <div>
        <h2>IM 机器人</h2>
        <p>把飞书 / 企业微信群里的消息转发到本机 AI，并把结果回推到群里。</p>
      </div>
      <div class="row">
        <button class="btn" :disabled="loading" @click="load">刷新</button>
        <button class="btn primary" @click="openCreate">新建机器人</button>
      </div>
    </div>

    <div v-if="error" class="alert error">{{ error }}</div>
    <div v-if="notice" class="alert info">{{ notice }}</div>

    <div class="card">
      <table>
        <thead>
          <tr>
            <th>名称</th>
            <th>渠道</th>
            <th>模型</th>
            <th>状态</th>
            <th>回调地址</th>
            <th style="text-align: right">操作</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="b in bots" :key="b.id">
            <td>
              <strong>{{ b.name }}</strong>
              <div class="muted mono" style="font-size: 11px">{{ b.id }}</div>
            </td>
            <td><span class="tag">{{ b.channel_name }}</span></td>
            <td class="mono">{{ b.model || '默认' }}</td>
            <td>
              <span class="tag" :class="b.enabled ? 'ok' : 'muted'">{{ b.enabled ? '启用' : '停用' }}</span>
              <div v-if="!b.can_reply" class="muted" style="font-size: 11px">未配置 Webhook，无法回推</div>
            </td>
            <td>
              <div class="mono" style="font-size: 11px; word-break: break-all">
                {{ b.callback_url || '（未配置 public_addr）' }}
              </div>
              <button v-if="b.callback_url" class="btn-link" @click="copy(b.callback_url)">复制</button>
            </td>
            <td style="text-align: right; white-space: nowrap">
              <button class="btn" @click="openEdit(b)">编辑</button>
              <button class="btn" @click="toggle(b)">{{ b.enabled ? '停用' : '启用' }}</button>
              <button class="btn danger" @click="remove(b)">删除</button>
            </td>
          </tr>
          <tr v-if="!bots.length">
            <td colspan="6" class="empty">
              还没有机器人。点击「新建机器人」开始接入{{ channelHint('feishu') }}或{{ channelHint('wecom') }}。
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <!-- 新建 / 编辑弹窗 -->
    <div v-if="dialog" class="mask" @click.self="dialog = false">
      <div class="modal">
        <h3 style="font-size: 15px; margin-bottom: 14px">{{ dialogTitle }}</h3>

        <div class="field">
          <label>名称</label>
          <input v-model="form.name" placeholder="例如：研发群助手" />
        </div>

        <div class="field">
          <label>渠道</label>
          <select v-model="form.channel">
            <option value="feishu">飞书</option>
            <option value="wecom">企业微信</option>
          </select>
        </div>

        <div class="grid-2">
          <div class="field">
            <label>默认模型（留空用网关默认）</label>
            <select v-model="form.model">
              <option value="">自动</option>
              <option value="claude-code">claude-code</option>
              <option value="trae">trae</option>
              <option value="codebuddy">codebuddy</option>
              <option value="codex">codex</option>
            </select>
          </div>
          <div class="field">
            <label>投递通路</label>
            <select v-model="form.mode">
              <option value="pull">队列模式（推荐，离线也不丢）</option>
              <option value="direct">直连模式（低延迟，需长连接在线）</option>
            </select>
          </div>
        </div>

        <div class="field">
          <label>出站 Webhook（结果回推到群）</label>
          <input
            v-model="form.webhook_url"
            :placeholder="isWecom
              ? 'https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=xxxx'
              : 'https://open.feishu.cn/open-apis/bot/v2/hook/xxxx'"
          />
          <div class="muted" style="font-size: 11px; margin-top: 4px">
            {{ isWecom ? '企业微信群机器人 Webhook 地址' : '飞书自定义机器人 Webhook 地址' }}
          </div>
        </div>

        <div class="grid-2">
          <div class="field">
            <label>{{ isWecom ? '回调 Token' : 'Verification Token' }}</label>
            <input v-model="form.token" :placeholder="isWecom ? '企业微信回调 Token' : '可留空'" />
          </div>
          <div class="field">
            <label>{{ isWecom ? 'EncodingAESKey' : 'Encrypt Key（事件加密）' }}</label>
            <input v-model="form.aes_key" placeholder="43 位 Base64，留空表示回调不加密" />
          </div>
        </div>

        <div class="field">
          <label>{{ isWecom ? '签名 Secret（群机器人一般留空）' : '签名 Secret（Webhook 验签）' }}</label>
          <input v-model="form.secret" placeholder="可留空" />
        </div>

        <div class="field">
          <label>附加系统提示（可选）</label>
          <textarea v-model="form.system_prompt" rows="2" placeholder="例如：回答请简洁，并给出可执行的修改建议"></textarea>
        </div>

        <div class="field">
          <label>固定节点（留空用默认节点）</label>
          <input v-model="form.agent_id" placeholder="local-pc" />
        </div>

        <label class="check">
          <input v-model="form.mention_only" type="checkbox" style="width: auto" />
          群聊中仅响应 @机器人 的消息
        </label>
        <label class="check">
          <input v-model="form.enabled" type="checkbox" style="width: auto" />
          启用该机器人
        </label>

        <div class="row" style="margin-top: 18px; justify-content: flex-end">
          <button class="btn" @click="dialog = false">取消</button>
          <button class="btn primary" @click="submit">保存</button>
        </div>

        <p class="muted" style="font-size: 12px; margin-top: 14px">
          保存后在 IM 平台把「回调地址」配置为列表中显示的 URL。密钥字段留空表示不修改，不会覆盖已有值。
        </p>
      </div>
    </div>
  </div>
</template>

<style scoped>
.btn-link {
  border: none;
  background: none;
  color: var(--brand);
  cursor: pointer;
  padding: 0;
  font-size: 12px;
  margin-top: 4px;
}

.mask {
  position: fixed;
  inset: 0;
  background: rgba(16, 24, 40, 0.35);
  display: grid;
  place-items: center;
  padding: 24px;
  z-index: 20;
}

.modal {
  background: #fff;
  border-radius: 12px;
  padding: 22px 24px;
  width: 100%;
  max-width: 560px;
  max-height: 88vh;
  overflow-y: auto;
  box-shadow: 0 12px 32px rgba(16, 24, 40, 0.18);
}

.grid-2 {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 12px;
}

.check {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 13px;
  color: var(--text);
  margin-bottom: 8px;
}

.check input {
  width: auto;
}

.btn + .btn {
  margin-left: 8px;
}
</style>
