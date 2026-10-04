<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { api } from '@/api/client'
import { streamChat } from '@/api/chat'
import type { Agent, ChatMessage, ModelInfo } from '@/types'

interface Bubble extends ChatMessage {
  pending?: boolean
  error?: string
}

const messages = ref<Bubble[]>([])
const input = ref('')
const streaming = ref(false)
const status = ref('')
const model = ref('')
const mode = ref('pull')
const models = ref<ModelInfo[]>([])
const agents = ref<Agent[]>([])
const selectedAgent = ref('')
const error = ref('')

let controller: AbortController | null = null

const onlineAgents = computed(() => agents.value.filter((a) => a.status !== 'offline'))
const canSend = computed(
  () => input.value.trim().length > 0 && !streaming.value && onlineAgents.value.length > 0,
)

async function loadAgents() {
  try {
    const res = await api.agents()
    agents.value = res.agents ?? []
    // 只有一台在线实例时自动选择；多台时保持用户手动选择
    if (!selectedAgent.value && onlineAgents.value.length === 1) {
      selectedAgent.value = onlineAgents.value[0].id
    }
    if (selectedAgent.value && !agents.value.some((a) => a.id === selectedAgent.value)) {
      selectedAgent.value = ''
    }
  } catch (e) {
    error.value = e instanceof Error ? e.message : '加载客户端列表失败'
  }
}

async function loadModels() {
  try {
    const res = await api.models()
    models.value = res.models ?? []
  } catch (e) {
    error.value = e instanceof Error ? e.message : '加载模型列表失败'
  }
}

function send() {
  if (!canSend.value) return
  const text = input.value.trim()
  input.value = ''

  messages.value.push({ role: 'user', content: text })
  const draft: Bubble = { role: 'assistant', content: '', pending: true }
  messages.value.push(draft)
  const index = messages.value.length - 1

  // 历史只携带已完成的消息，避免把空草稿发给模型。
  const history: ChatMessage[] = messages.value
    .slice(0, index)
    .filter((m) => m.content !== '' && !m.error)
    .map((m) => ({ role: m.role, content: m.content }))

  streaming.value = true
  status.value = '已提交，等待本地 AI 接管…'
  controller = new AbortController()

  streamChat(
    {
      messages: history,
      model: model.value || undefined,
      mode: mode.value,
      agent_id: selectedAgent.value || undefined,
    },
    {
      onMeta: (meta) => {
        status.value = `任务 ${meta.task_id} · ${meta.mode === 'direct' ? '直连' : '队列'}模式`
      },
      onChunk: (delta) => {
        messages.value[index].content += delta
        scrollToBottom()
      },
      onDone: (full) => {
        if (full) messages.value[index].content = full
        messages.value[index].pending = false
        streaming.value = false
        status.value = ''
        scrollToBottom()
      },
      onError: (message) => {
        messages.value[index].error = message
        messages.value[index].pending = false
        messages.value[index].content = messages.value[index].content || message
        streaming.value = false
        status.value = ''
        // 多实例/实例变化后刷新选择器
        loadAgents()
      },
    },
    controller.signal,
  ).catch(() => {
    streaming.value = false
  })
}

function stop() {
  controller?.abort()
  controller = null
  streaming.value = false
  status.value = '已停止'
}

function clearChat() {
  messages.value = []
  status.value = ''
}

function scrollToBottom() {
  requestAnimationFrame(() => {
    const el = document.getElementById('chat-scroll')
    if (el) el.scrollTop = el.scrollHeight
  })
}

function onKeydown(e: KeyboardEvent) {
  if (e.key === 'Enter' && !e.shiftKey) {
    e.preventDefault()
    send()
  }
}

onMounted(() => {
  loadModels()
  loadAgents()
})
</script>

<template>
  <div class="page chat-page">
    <div class="page-head">
      <div>
        <h2>网页对话</h2>
        <p>请求会下发到你本机的 AI 编码工具执行，结果实时回流到此处。</p>
      </div>
      <div class="row">
        <select
          v-model="selectedAgent"
          :disabled="onlineAgents.length === 0"
          style="width: 200px"
          :title="onlineAgents.length === 0 ? '没有在线客户端：请在本机用秘钥启动 CodePorter 客户端' : '选择目标客户端'"
        >
          <option value="" disabled>
            {{ onlineAgents.length === 0 ? '无在线客户端' : '选择客户端（自动）' }}
          </option>
          <option v-for="a in agents" :key="a.id" :value="a.id">
            {{ a.name || a.id }} · {{ a.status === 'online' ? '在线' : a.status === 'busy' ? '忙碌' : '离线' }}
          </option>
        </select>
        <select v-model="model" style="width: 150px">
          <option value="">自动（默认模型）</option>
          <option v-for="m in models" :key="m.model" :value="m.model">
            {{ m.model }}{{ m.available ? '' : '（不可用）' }}
          </option>
        </select>
        <select v-model="mode" style="width: 130px">
          <option value="pull">队列模式</option>
          <option value="direct">直连模式</option>
        </select>
        <button class="btn" @click="loadAgents">刷新客户端</button>
        <button class="btn" @click="clearChat">清空</button>
      </div>
    </div>

    <div v-if="onlineAgents.length === 0" class="alert info">
      没有在线客户端：请在你的电脑上用「秘钥」页生成的秘钥启动 CodePorter 本地客户端（agent.key / AGENT_KEY）。
    </div>
    <div v-else-if="onlineAgents.length > 1 && !selectedAgent" class="alert info">
      检测到 {{ onlineAgents.length }} 台在线客户端，请在上方选择目标机器。
    </div>
    <div v-if="error" class="alert error">{{ error }}</div>

    <div class="card chat">
      <div id="chat-scroll" class="scroll">
        <div v-if="!messages.length" class="empty">
          还没有消息。试试输入「帮我审查这段代码的性能问题」。
        </div>
        <div v-for="(m, i) in messages" :key="i" class="bubble-row" :class="{ user: m.role === 'user' }">
          <div class="bubble" :class="{ error: m.error }">
            <div class="who">{{ m.role === 'user' ? '我' : '本地 AI' }}</div>
            <div v-if="m.role === 'user'" class="text">{{ m.content }}</div>
            <div v-else class="text assistant">
              <span v-if="m.content">{{ m.content }}</span>
              <span v-else-if="m.pending" class="typing">等待本机响应…</span>
              <span v-else-if="m.error" class="muted">（无内容）</span>
              <span v-if="m.pending && m.content" class="caret"></span>
            </div>
          </div>
        </div>
      </div>

      <div class="composer">
        <textarea
          v-model="input"
          rows="3"
          placeholder="Enter 发送，Shift + Enter 换行"
          :disabled="streaming"
          @keydown="onKeydown"
        ></textarea>
        <div class="row" style="margin-top: 10px">
          <span v-if="status" class="muted" style="font-size: 12px">{{ status }}</span>
          <span class="spacer"></span>
          <button v-if="streaming" class="btn danger" @click="stop">停止</button>
          <button class="btn primary" :disabled="!canSend" @click="send">发送</button>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.chat {
  display: flex;
  flex-direction: column;
  height: calc(100vh - 180px);
  min-height: 420px;
  padding: 0;
}

.scroll {
  flex: 1;
  overflow-y: auto;
  padding: 18px 20px;
}

.bubble-row {
  display: flex;
  margin-bottom: 14px;
}

.bubble-row.user {
  justify-content: flex-end;
}

.bubble {
  max-width: 76%;
  background: #f4f6f9;
  border-radius: 12px;
  padding: 10px 14px;
}

.bubble-row.user .bubble {
  background: var(--brand-soft);
}

.bubble.error {
  background: var(--danger-soft);
}

.who {
  font-size: 11px;
  color: var(--muted);
  margin-bottom: 4px;
}

.text {
  white-space: pre-wrap;
  word-break: break-word;
}

.text.assistant {
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 13px;
}

.typing {
  color: var(--muted);
}

.caret {
  display: inline-block;
  width: 6px;
  height: 14px;
  background: var(--brand);
  margin-left: 2px;
  animation: blink 1s steps(2, start) infinite;
  vertical-align: text-bottom;
}

@keyframes blink {
  to {
    visibility: hidden;
  }
}

.composer {
  border-top: 1px solid var(--border);
  padding: 14px 20px;
}
</style>
