<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
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
// 用户手动停止后，忽略管道里残余帧（如缓冲的 done），避免状态被覆盖。
let manualStop = false

// 打字机（仅直连模式）：SSE 片段往往成批突发到达，直接 append 会「跳变」。
// 这里把收到的片段按码位排队，用固定节拍逐字吐出，节奏与飞书卡片的流式打字感一致；
// 积压过大时自动加速，保证长回复不会滞后太久。
const TYPE_TICK_MS = 24 // 约 42fps
const TYPE_STEP = 2 // 每拍基础吐出码位数
const TYPE_CATCH_UP = 40 // 积压超过一拍量后，按 1/N 追赶
let typeTimer: number | null = null
let typeQueue: string[] = []
let typePos = 0
let typeIndex = -1
let typeFinishing = false
let typeFinal = ''

function startTyper(index: number) {
  stopTyper()
  typeQueue = []
  typePos = 0
  typeIndex = index
  typeFinishing = false
  typeFinal = ''
  typeTimer = window.setInterval(typeTick, TYPE_TICK_MS)
}

function stopTyper() {
  if (typeTimer !== null) {
    window.clearInterval(typeTimer)
    typeTimer = null
  }
  typeIndex = -1
  typeQueue = []
  typePos = 0
  typeFinishing = false
  typeFinal = ''
}

// flushTyper 把尚未吐出的文字一次性落盘（停止/出错/清屏时用）。
function flushTyper() {
  if (typeIndex < 0) return
  const rest = typeQueue.slice(typePos).join('')
  if (rest && messages.value[typeIndex]) {
    messages.value[typeIndex].content += rest
  }
  stopTyper()
}

function typeTick() {
  const m = messages.value[typeIndex]
  if (!m) {
    stopTyper()
    return
  }
  const backlog = typeQueue.length - typePos
  if (backlog > 0) {
    // 积压越大吐出越快：常态匀速打字，突发大批片段时快速追赶。
    const step = backlog > TYPE_STEP ? Math.max(TYPE_STEP, Math.ceil(backlog / TYPE_CATCH_UP)) : TYPE_STEP
    const take = Math.min(step, backlog)
    m.content += typeQueue.slice(typePos, typePos + take).join('')
    typePos += take
    scrollToBottom()
  }
  if (typeQueue.length - typePos === 0 && typeFinishing) {
    // 流已结束且全部文字吐完：用 done 的完整结果收口（防御片段拼接偏差）。
    if (typeFinal) m.content = typeFinal
    m.pending = false
    streaming.value = false
    status.value = ''
    stopTyper()
    scrollToBottom(true)
  }
}

const onlineAgents = computed(() => agents.value.filter((a) => a.status !== 'offline'))
const canSend = computed(
  () => input.value.trim().length > 0 && !streaming.value && onlineAgents.value.length > 0,
)

const SUGGESTIONS = [
  '帮我审查最近的代码改动，指出潜在问题',
  '给这个项目写一段简洁的 README 介绍',
  '定位并修复一个 bug，说明根因',
  '为现有函数补充单元测试',
]

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
  autoGrow()

  messages.value.push({ role: 'user', content: text })
  const draft: Bubble = { role: 'assistant', content: '', pending: true }
  messages.value.push(draft)
  const index = messages.value.length - 1
  // 直连模式走打字机；队列模式维持片段到达即渲染。
  const useTyper = mode.value === 'direct'
  if (useTyper) startTyper(index)
  jumpToBottom()

  // 历史只携带已完成的消息，避免把空草稿发给模型。
  const history: ChatMessage[] = messages.value
    .slice(0, index)
    .filter((m) => m.content !== '' && !m.error)
    .map((m) => ({ role: m.role, content: m.content }))

  streaming.value = true
  status.value = '已提交，等待本地 AI 接管…'
  controller = new AbortController()
  manualStop = false

  streamChat(
    {
      messages: history,
      model: model.value || undefined,
      mode: mode.value,
      agent_id: selectedAgent.value || undefined,
    },
    {
      onMeta: (meta) => {
        if (manualStop) return
        status.value = `任务 ${meta.task_id} · ${meta.mode === 'direct' ? '直连' : '队列'}模式`
      },
      onChunk: (delta) => {
        if (manualStop) return
        if (!delta) return
        if (useTyper && typeIndex === index) {
          // 按码位入队，避免切开 emoji 等代理对。
          typeQueue.push(...Array.from(delta))
        } else {
          messages.value[index].content += delta
          scrollToBottom()
        }
      },
      onDone: (full) => {
        if (manualStop) return
        if (useTyper && typeIndex === index) {
          // 等打字机把队列吐完再收口，避免最终全文瞬间覆盖掉打字动画。
          typeFinishing = true
          typeFinal = full
          typeTick()
        } else {
          if (full) messages.value[index].content = full
          messages.value[index].pending = false
          streaming.value = false
          status.value = ''
          scrollToBottom(true)
        }
      },
      onError: (message) => {
        if (manualStop) return
        if (useTyper && typeIndex === index) {
          // 出错时把已到达但未展示的文字立即落盘，再挂错误提示。
          flushTyper()
        }
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
  manualStop = true
  controller?.abort()
  controller = null
  // 已收到的片段全部展示出来，不再保留动画。
  flushTyper()
  streaming.value = false
  status.value = '已停止'
  const last = [...messages.value].reverse().find((m) => m.role === 'assistant')
  if (last) last.pending = false
}

function clearChat() {
  manualStop = true
  flushTyper()
  controller?.abort()
  controller = null
  streaming.value = false
  messages.value = []
  status.value = ''
}

// ---------- 滚动跟随：用户上翻时不打断，发送/点按钮才强制吸底 ----------
const scrollEl = ref<HTMLElement | null>(null)
const taEl = ref<HTMLTextAreaElement | null>(null)
const stick = ref(true)

function onScroll() {
  const el = scrollEl.value
  if (!el) return
  stick.value = el.scrollHeight - el.scrollTop - el.clientHeight < 64
}

function scrollToBottom(force = false) {
  if (!force && !stick.value) return
  requestAnimationFrame(() => {
    const el = scrollEl.value
    if (el) el.scrollTop = el.scrollHeight
  })
}

function jumpToBottom() {
  stick.value = true
  scrollToBottom(true)
}

// ---------- textarea 自适应高度 ----------
function autoGrow() {
  const el = taEl.value
  if (!el) return
  el.style.height = 'auto'
  el.style.height = `${Math.min(el.scrollHeight, 220)}px`
}

watch(input, () => autoGrow())

function onKeydown(e: KeyboardEvent) {
  if (e.key === 'Enter' && !e.shiftKey) {
    e.preventDefault()
    send()
  }
}

// ---------- 复制 ----------
const copiedId = ref(-1)

// 网关可能经 http://局域网 IP 访问（非安全上下文，navigator.clipboard 不存在），
// 此时退回 execCommand 方案。
async function copyText(text: string): Promise<boolean> {
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(text)
      return true
    }
    throw new Error('no clipboard api')
  } catch {
    try {
      const ta = document.createElement('textarea')
      ta.value = text
      ta.style.position = 'fixed'
      ta.style.opacity = '0'
      document.body.appendChild(ta)
      ta.select()
      const ok = document.execCommand('copy')
      ta.remove()
      return ok
    } catch {
      return false
    }
  }
}

async function copyMessage(i: number) {
  copiedId.value = i // 乐观反馈，失败再撤回
  const ok = await copyText(messages.value[i].content)
  if (!ok) copiedId.value = -1
  else {
    window.setTimeout(() => {
      if (copiedId.value === i) copiedId.value = -1
    }, 1500)
  }
}

// 代码块的复制按钮通过事件委托处理（内容是 v-html，无法直接绑定）。
async function onTranscriptClick(e: MouseEvent) {
  const btn = (e.target as HTMLElement).closest('button.md-code__copy') as HTMLButtonElement | null
  if (!btn) return
  const code = btn.closest('.md-code')?.querySelector('code')
  if (!code) return
  const old = btn.textContent
  btn.textContent = '已复制'
  const ok = await copyText(code.textContent ?? '')
  if (!ok) btn.textContent = old
  else {
    window.setTimeout(() => {
      btn.textContent = old
    }, 1500)
  }
}

function useSuggestion(s: string) {
  input.value = s
  autoGrow()
  taEl.value?.focus()
}

// ---------- 轻量 Markdown（先转义再渲染，避免 XSS；不引第三方依赖） ----------
function escapeHtml(s: string): string {
  return s
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
}

function inlineMd(s: string): string {
  // 先抽出行内代码，避免其内部字符被后续规则改写。
  const codes: string[] = []
  let t = s.replace(/`([^`\n]+)`/g, (_m, c: string) => {
    codes.push(escapeHtml(c))
    return `\u0000${codes.length - 1}\u0000`
  })
  t = escapeHtml(t)
  t = t.replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>')
  t = t.replace(/(^|[^\\])\*([^*]+)\*/g, '$1<em>$2</em>')
  t = t.replace(
    /\[([^\]]+)\]\(((?:https?:\/\/|mailto:)[^)\s]+)\)/g,
    '<a href="$2" target="_blank" rel="noopener noreferrer">$1</a>',
  )
  t = t.replace(/\u0000(\d+)\u0000/g, (_m, i) => `<code class="md-icode">${codes[Number(i)]}</code>`)
  return t
}

function renderProse(text: string): string {
  const lines = text.split('\n')
  const html: string[] = []
  let i = 0
  const flushList = (ordered: boolean, items: string[]) => {
    const tag = ordered ? 'ol' : 'ul'
    html.push(`<${tag}>` + items.map((it) => `<li>${inlineMd(it)}</li>`).join('') + `</${tag}>`)
  }
  while (i < lines.length) {
    const line = lines[i]
    const trimmed = line.trim()
    if (!trimmed) {
      i++
      continue
    }
    const heading = /^(#{1,4})\s+(.*)$/.exec(trimmed)
    if (heading) {
      const level = Math.min(heading[1].length + 2, 6) // ### -> h5
      html.push(`<h${level}>${inlineMd(heading[2])}</h${level}>`)
      i++
      continue
    }
    if (/^[-*](?:\s+|$)/.test(trimmed)) {
      const items: string[] = []
      while (i < lines.length && /^\s*[-*](?:\s+|$)/.test(lines[i])) {
        items.push(lines[i].replace(/^\s*[-*]\s?/, ''))
        i++
      }
      flushList(false, items)
      continue
    }
    if (/^\d+[.)](?:\s+|$)/.test(trimmed)) {
      const items: string[] = []
      while (i < lines.length && /^\s*\d+[.)](?:\s+|$)/.test(lines[i])) {
        items.push(lines[i].replace(/^\s*\d+[.)]\s?/, ''))
        i++
      }
      flushList(true, items)
      continue
    }
    if (/^>\s?/.test(trimmed)) {
      const quote: string[] = []
      while (i < lines.length && /^\s*>/.test(lines[i])) {
        quote.push(lines[i].replace(/^\s*>\s?/, ''))
        i++
      }
      html.push(`<blockquote>${quote.map(inlineMd).join('<br>')}</blockquote>`)
      continue
    }
    // 连续普通行合并为一段（模型通常用空行分段，段内换行保留为 <br>）。
    const para: string[] = []
    while (
      i < lines.length &&
      lines[i].trim() &&
      !/^(#{1,4})\s+/.test(lines[i].trim()) &&
      !/^\s*[-*](?:\s+|$)/.test(lines[i]) &&
      !/^\s*\d+[.)](?:\s+|$)/.test(lines[i]) &&
      !/^\s*>/.test(lines[i])
    ) {
      para.push(lines[i].trim())
      i++
    }
    html.push(`<p>${para.map(inlineMd).join('<br>')}</p>`)
  }
  return html.join('')
}

function renderMarkdown(src: string): string {
  // 围栏代码块优先整体抽出；未闭合的围栏（流式中途）也按代码块渲染。
  const fence = /```([^\s`]*)\n?([\s\S]*?)(?:```|$)/g
  const parts: string[] = []
  let last = 0
  let m: RegExpExecArray | null
  while ((m = fence.exec(src))) {
    if (m.index > last) parts.push(renderProse(src.slice(last, m.index)))
    const lang = m[1] || ''
    const code = m[2].replace(/\n$/, '')
    parts.push(
      `<div class="md-code"><div class="md-code__bar"><span class="md-code__lang">${
        lang ? escapeHtml(lang) : 'code'
      }</span><button type="button" class="md-code__copy">复制</button></div><pre><code>${escapeHtml(
        code,
      )}</code></pre></div>`,
    )
    last = fence.lastIndex
  }
  if (last < src.length) parts.push(renderProse(src.slice(last)))
  return parts.join('')
}

onMounted(() => {
  loadModels()
  loadAgents()
  void nextTick(autoGrow)
})

onUnmounted(() => {
  stopTyper()
  controller?.abort()
})
</script>

<template>
  <div class="chat-page">
    <!-- 顶部工具条：目标客户端 / 模型 / 模式 -->
    <header class="chat-top">
      <div class="chat-top__title">
        <span class="chat-top__dot" :class="{ on: onlineAgents.length > 0 }" />
        <span class="chat-top__name">网页对话</span>
      </div>
      <div class="chat-top__tools">
        <select
          v-model="selectedAgent"
          class="ctl"
          :disabled="onlineAgents.length === 0"
          :title="onlineAgents.length === 0 ? '没有在线客户端：请在本机用秘钥启动 CodePorter 客户端' : '选择目标客户端'"
        >
          <option value="" disabled>
            {{ onlineAgents.length === 0 ? '无在线客户端' : '选择客户端（自动）' }}
          </option>
          <option v-for="a in agents" :key="a.id" :value="a.id">
            {{ a.name || a.id }} · {{ a.status === 'online' ? '在线' : a.status === 'busy' ? '忙碌' : '离线' }}
          </option>
        </select>
        <select v-model="model" class="ctl" title="选择模型">
          <option value="">默认模型</option>
          <option v-for="m in models" :key="m.model" :value="m.model">
            {{ m.model }}{{ m.available ? '' : '（不可用）' }}
          </option>
        </select>
        <select v-model="mode" class="ctl" title="执行模式">
          <option value="pull">队列模式</option>
          <option value="direct">直连模式</option>
        </select>
        <button class="icon-btn" type="button" title="刷新客户端列表" @click="loadAgents">
          <svg viewBox="0 0 16 16" width="15" height="15" aria-hidden="true">
            <path
              d="M13.5 8a5.5 5.5 0 1 1-1.61-3.89M13.5 2.5v3h-3"
              fill="none"
              stroke="currentColor"
              stroke-width="1.4"
              stroke-linecap="round"
              stroke-linejoin="round"
            />
          </svg>
        </button>
        <button class="icon-btn" type="button" title="清空对话" :disabled="messages.length === 0" @click="clearChat">
          <svg viewBox="0 0 16 16" width="15" height="15" aria-hidden="true">
            <path
              d="M3 4.5h10M6.5 4.5V3.2a.7.7 0 0 1 .7-.7h1.6a.7.7 0 0 1 .7.7v1.3M4.5 4.5l.6 8.2a1 1 0 0 0 1 .9h3.8a1 1 0 0 0 1-.9l.6-8.2"
              fill="none"
              stroke="currentColor"
              stroke-width="1.4"
              stroke-linecap="round"
              stroke-linejoin="round"
            />
          </svg>
        </button>
      </div>
    </header>

    <!-- 消息区 -->
    <div ref="scrollEl" class="chat-scroll" @scroll="onScroll" @click="onTranscriptClick">
      <div class="chat-col">
        <!-- 欢迎屏 -->
        <div v-if="!messages.length" class="welcome">
          <div class="welcome__logo">CP</div>
          <h2 class="welcome__title">你好，我是 CodePorter</h2>
          <p class="welcome__sub">请求会下发到你本机的 AI 编码工具执行，结果实时回流到此处。</p>
          <div class="welcome__tips">
            <button
              v-for="s in SUGGESTIONS"
              :key="s"
              type="button"
              class="tip-chip"
              @click="useSuggestion(s)"
            >
              {{ s }}
            </button>
          </div>
        </div>

        <!-- 消息流 -->
        <template v-else>
          <div
            v-for="(m, i) in messages"
            :key="i"
            class="msg"
            :class="m.role === 'user' ? 'msg--user' : 'msg--ai'"
          >
            <template v-if="m.role === 'user'">
              <div class="msg__user">
                <div class="bubble-user">{{ m.content }}</div>
                <div class="msg__actions">
                  <button type="button" class="act" @click="copyMessage(i)">
                    {{ copiedId === i ? '已复制' : '复制' }}
                  </button>
                </div>
              </div>
            </template>
            <template v-else>
              <div class="msg__avatar" title="本地 AI">CP</div>
              <div class="msg__body">
                <div class="msg__name">
                  本地 AI
                  <span v-if="m.pending && !m.content" class="msg__state">正在响应</span>
                  <span v-else-if="m.error" class="msg__state msg__state--err">出错了</span>
                </div>

                <div v-if="m.pending && !m.content" class="dots" aria-label="等待本机响应">
                  <span /><span /><span />
                </div>

                <div v-if="m.content" class="md" :class="{ 'md--err': m.error }">
                  <div class="md__prose" v-html="renderMarkdown(m.content)" />
                </div>

                <div v-if="m.error" class="err-box">
                  <span class="err-box__title">请求失败</span>
                  <span class="err-box__msg">{{ m.error }}</span>
                </div>

                <div v-if="!m.pending && m.content" class="msg__actions">
                  <button type="button" class="act" @click="copyMessage(i)">
                    {{ copiedId === i ? '已复制' : '复制' }}
                  </button>
                </div>
              </div>
            </template>
          </div>
        </template>
      </div>
    </div>

    <!-- 回到底部 -->
    <transition name="fade">
      <button v-if="!stick && messages.length" type="button" class="to-bottom" title="回到底部" @click="jumpToBottom">
        <svg viewBox="0 0 16 16" width="16" height="16" aria-hidden="true">
          <path d="M3.5 6 8 10.5 12.5 6" fill="none" stroke="currentColor" stroke-width="1.6"
            stroke-linecap="round" stroke-linejoin="round" />
        </svg>
      </button>
    </transition>

    <!-- 底部输入坞 -->
    <footer class="chat-dock">
      <div class="chat-col">
        <div v-if="onlineAgents.length === 0" class="banner banner--warn">
          没有在线客户端：请在本机用「秘钥」页生成的秘钥启动 CodePorter 客户端（agent.key / AGENT_KEY）。
        </div>
        <div v-else-if="onlineAgents.length > 1 && !selectedAgent" class="banner">
          检测到 {{ onlineAgents.length }} 台在线客户端，请在上方选择目标机器。
        </div>
        <div v-if="error" class="banner banner--err">{{ error }}</div>

        <div class="composer" :class="{ 'composer--disabled': streaming }">
          <textarea
            ref="taEl"
            v-model="input"
            class="composer__input"
            placeholder="给你本机的 AI 编码工具下达任务…"
            :disabled="streaming"
            rows="1"
            @keydown="onKeydown"
          ></textarea>
          <div class="composer__foot">
            <span class="composer__hint">
              <template v-if="status">
                <span class="composer__spinner" />{{ status }}
              </template>
              <template v-else>Enter 发送 · Shift+Enter 换行</template>
            </span>
            <button v-if="streaming" type="button" class="send send--stop" title="停止" @click="stop">
              <svg viewBox="0 0 16 16" width="15" height="15" aria-hidden="true">
                <rect x="4.5" y="4.5" width="7" height="7" rx="1.5" fill="currentColor" />
              </svg>
            </button>
            <button v-else type="button" class="send" :disabled="!canSend" title="发送" @click="send">
              <svg viewBox="0 0 16 16" width="16" height="16" aria-hidden="true">
                <path
                  d="M8 12.5v-9m0 0L4 7.5m4-4 4 4"
                  fill="none"
                  stroke="currentColor"
                  stroke-width="1.7"
                  stroke-linecap="round"
                  stroke-linejoin="round"
                />
              </svg>
            </button>
          </div>
        </div>
        <div class="dock-note">内容在你本机执行，网关只负责中继，不会留存对话内容。</div>
      </div>
    </footer>
  </div>
</template>

<style scoped>
/* ===== 页面框架：全屏三段式（工具条 / 消息列 / 输入坞） ===== */
.chat-page {
  position: relative;
  display: flex;
  flex-direction: column;
  height: 100vh;
  background: var(--panel);
}

.chat-top {
  flex: none;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 10px 20px;
  border-bottom: 1px solid var(--border);
}

.chat-top__title {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 13.5px;
  font-weight: 600;
}

.chat-top__dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: #c2c8d2;
}

.chat-top__dot.on {
  background: var(--ok);
  box-shadow: 0 0 0 3px var(--ok-soft);
}

.chat-top__tools {
  display: flex;
  align-items: center;
  gap: 8px;
}

/* 紧凑型选择器（覆盖全局 input/select 样式） */
.ctl {
  width: auto;
  padding: 6px 28px 6px 10px;
  font-size: 12.5px;
  border-radius: 8px;
  background-color: #fff;
}

.icon-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 32px;
  height: 32px;
  border: 1px solid var(--border);
  border-radius: 8px;
  background: #fff;
  color: var(--muted);
  cursor: pointer;
  transition: all 0.15s ease;
}

.icon-btn:hover:not(:disabled) {
  color: var(--brand);
  border-color: var(--brand);
}

.icon-btn:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}

/* ===== 消息区 ===== */
.chat-scroll {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  display: flex;
  flex-direction: column;
}

.chat-col {
  align-self: center;
  /* 输入坞里的同名列处于非 flex 容器，需要 auto 外边距才能居中；
     在 flex 列里 auto margin 同样居中，两处行为一致。 */
  margin-inline: auto;
  width: 100%;
  max-width: 780px;
  min-height: 100%;
  padding: 22px 20px 8px;
  display: flex;
  flex-direction: column;
}

/* 欢迎屏 */
.welcome {
  margin: auto;
  text-align: center;
  padding: 24px 0 40px;
}

.welcome__logo {
  width: 52px;
  height: 52px;
  margin: 0 auto 16px;
  border-radius: 14px;
  display: grid;
  place-items: center;
  background: var(--brand);
  color: #fff;
  font-weight: 700;
  font-size: 19px;
  box-shadow: 0 8px 20px rgba(47, 111, 235, 0.28);
}

.welcome__title {
  font-size: 22px;
  margin-bottom: 8px;
}

.welcome__sub {
  margin: 0;
  color: var(--muted);
  font-size: 13.5px;
}

.welcome__tips {
  margin-top: 26px;
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 10px;
  text-align: left;
}

.tip-chip {
  padding: 12px 14px;
  border: 1px solid var(--border);
  border-radius: 12px;
  background: #fff;
  color: var(--text);
  font-size: 13px;
  font-family: inherit;
  text-align: left;
  cursor: pointer;
  transition: all 0.15s ease;
}

.tip-chip:hover {
  border-color: var(--brand);
  color: var(--brand);
  background: var(--brand-soft);
  transform: translateY(-1px);
}

/* 消息行 */
.msg {
  display: flex;
  gap: 12px;
  margin-bottom: 18px;
}

.msg--user {
  justify-content: flex-end;
}

.msg__user {
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  gap: 4px;
  min-width: 0;
  max-width: 78%;
}

.bubble-user {
  background: #f2f4f7;
  border-radius: 16px;
  padding: 10px 16px;
  font-size: 14px;
  line-height: 1.65;
  white-space: pre-wrap;
  word-break: break-word;
}

.msg__avatar {
  flex: none;
  width: 28px;
  height: 28px;
  margin-top: 2px;
  border-radius: 8px;
  display: grid;
  place-items: center;
  background: var(--brand);
  color: #fff;
  font-weight: 700;
  font-size: 11px;
  user-select: none;
}

.msg__body {
  min-width: 0;
  flex: 1;
}

.msg__name {
  font-size: 12.5px;
  font-weight: 600;
  color: var(--muted);
  margin-bottom: 5px;
  display: flex;
  align-items: center;
  gap: 8px;
}

.msg__state {
  font-weight: 400;
  font-size: 11.5px;
  color: var(--brand);
}

.msg__state--err {
  color: var(--danger);
}

.msg__actions {
  display: flex;
  gap: 4px;
  opacity: 0;
  transition: opacity 0.15s ease;
}

.msg:hover .msg__actions,
.msg__actions:focus-within {
  opacity: 1;
}

.act {
  border: none;
  background: transparent;
  color: var(--muted);
  font-size: 12px;
  padding: 3px 8px;
  border-radius: 6px;
  cursor: pointer;
}

.act:hover {
  background: #f2f4f7;
  color: var(--text);
}

/* 等待动画（三点） */
.dots {
  display: inline-flex;
  gap: 5px;
  padding: 6px 0;
}

.dots span {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: #b6bdc9;
  animation: dot-bounce 1.2s infinite ease-in-out;
}

.dots span:nth-child(2) {
  animation-delay: 0.15s;
}

.dots span:nth-child(3) {
  animation-delay: 0.3s;
}

@keyframes dot-bounce {
  0%,
  60%,
  100% {
    transform: translateY(0);
    opacity: 0.5;
  }
  30% {
    transform: translateY(-4px);
    opacity: 1;
  }
}

/* Markdown 正文 */
.md {
  font-size: 14px;
  line-height: 1.75;
  color: var(--text);
  word-break: break-word;
}

.md :deep(p) {
  margin: 0 0 10px;
}

.md :deep(p:last-child) {
  margin-bottom: 0;
}

.md :deep(ul),
.md :deep(ol) {
  margin: 4px 0 10px;
  padding-left: 22px;
}

.md :deep(li) {
  margin: 3px 0;
}

.md :deep(h5),
.md :deep(h6) {
  font-size: 14px;
  font-weight: 600;
  margin: 14px 0 6px;
}

.md :deep(blockquote) {
  margin: 8px 0;
  padding: 4px 12px;
  border-left: 3px solid var(--border);
  color: var(--muted);
}

.md :deep(a) {
  text-decoration: underline;
  text-underline-offset: 2px;
}

.md-icode {
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 12.5px;
  background: #eef0f3;
  border-radius: 5px;
  padding: 1px 5px;
}

/* 代码块 */
.md-code {
  margin: 10px 0;
  border-radius: 10px;
  overflow: hidden;
  background: #1b1f24;
}

.md-code__bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 6px 12px;
  background: rgba(255, 255, 255, 0.06);
}

.md-code__lang {
  font-size: 11.5px;
  color: #9aa6b6;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
}

.md-code__copy {
  border: none;
  background: transparent;
  color: #9aa6b6;
  font-size: 11.5px;
  padding: 2px 8px;
  border-radius: 5px;
  cursor: pointer;
}

.md-code__copy:hover {
  background: rgba(255, 255, 255, 0.1);
  color: #fff;
}

.md-code pre {
  margin: 0;
  padding: 12px 14px;
  overflow-x: auto;
}

.md-code code {
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 12.5px;
  line-height: 1.7;
  color: #e6edf3;
  white-space: pre;
}

/* 错误态 */
.md--err {
  color: var(--danger);
}

.err-box {
  margin-top: 8px;
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 10px 12px;
  border-radius: 10px;
  background: var(--danger-soft);
  font-size: 12.5px;
}

.err-box__title {
  font-weight: 600;
  color: var(--danger);
}

.err-box__msg {
  color: var(--danger);
  opacity: 0.85;
  word-break: break-word;
}

/* 回到底部 */
.to-bottom {
  position: absolute;
  right: max(20px, calc((100% - 780px) / 2 - 12px));
  bottom: 116px;
  width: 36px;
  height: 36px;
  border-radius: 50%;
  border: 1px solid var(--border);
  background: #fff;
  color: var(--muted);
  box-shadow: 0 4px 14px rgba(16, 24, 40, 0.12);
  display: grid;
  place-items: center;
  cursor: pointer;
  z-index: 5;
}

.to-bottom:hover {
  color: var(--brand);
  border-color: var(--brand);
}

.fade-enter-active,
.fade-leave-active {
  transition: opacity 0.18s ease;
}

.fade-enter-from,
.fade-leave-to {
  opacity: 0;
}

/* ===== 输入坞 ===== */
.chat-dock {
  flex: none;
  border-top: 1px solid var(--border);
  padding: 12px 20px 14px;
  background: var(--panel);
}

.banner {
  max-width: 780px;
  margin: 0 auto 8px;
  border-radius: 8px;
  padding: 8px 12px;
  font-size: 12.5px;
  background: var(--brand-soft);
  color: var(--brand);
}

.banner--warn {
  background: var(--warn-soft);
  color: var(--warn);
}

.banner--err {
  background: var(--danger-soft);
  color: var(--danger);
}

.composer {
  border: 1px solid var(--border);
  border-radius: 18px;
  background: #fff;
  box-shadow: 0 2px 10px rgba(16, 24, 40, 0.06);
  padding: 10px 12px 8px;
  transition: border-color 0.15s ease, box-shadow 0.15s ease;
}

.composer:focus-within {
  border-color: var(--brand);
  box-shadow: 0 0 0 3px var(--brand-soft);
}

.composer--disabled {
  background: #fafbfc;
}

.composer__input {
  display: block;
  width: 100%;
  border: none;
  padding: 4px 6px;
  font-size: 14px;
  line-height: 1.6;
  resize: none;
  background: transparent;
  max-height: 220px;
}

.composer__input:focus {
  outline: none;
  box-shadow: none;
  border: none;
}

.composer__foot {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  margin-top: 6px;
}

.composer__hint {
  font-size: 12px;
  color: var(--muted);
  display: inline-flex;
  align-items: center;
  gap: 7px;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.composer__spinner {
  width: 11px;
  height: 11px;
  flex: none;
  border: 2px solid var(--brand-soft);
  border-top-color: var(--brand);
  border-radius: 50%;
  animation: spin 0.7s linear infinite;
}

@keyframes spin {
  to {
    transform: rotate(360deg);
  }
}

.send {
  flex: none;
  width: 34px;
  height: 34px;
  border-radius: 50%;
  border: none;
  background: var(--brand);
  color: #fff;
  display: grid;
  place-items: center;
  cursor: pointer;
  transition: filter 0.15s ease, background 0.15s ease;
}

.send:hover:not(:disabled) {
  filter: brightness(1.08);
}

.send:disabled {
  background: #d4d9e0;
  cursor: not-allowed;
}

.send--stop {
  background: #374151;
}

.send--stop:hover {
  background: var(--danger);
}

.dock-note {
  text-align: center;
  font-size: 11.5px;
  color: var(--muted);
  opacity: 0.8;
  margin-top: 8px;
}

/* ===== 移动端 ===== */
@media (max-width: 860px) {
  .chat-page {
    height: calc(100dvh - 52px); /* 顶部移动端栏高 52px */
  }

  .chat-top {
    padding: 8px 12px;
    flex-wrap: wrap;
  }

  .chat-top__tools {
    gap: 6px;
    flex-wrap: wrap;
    width: 100%;
  }

  /* 选择器两列流式排布，图标按钮固定，保证 390px 宽不横向溢出 */
  .ctl {
    flex: 1 1 calc(50% - 6px);
    min-width: 0;
    width: auto;
    padding: 6px 24px 6px 8px;
  }

  .chat-col {
    padding: 16px 12px 8px;
  }

  .welcome__tips {
    grid-template-columns: 1fr;
  }

  .msg__user {
    max-width: 88%;
  }

  .chat-dock {
    padding: 10px 12px;
    padding-bottom: max(10px, env(safe-area-inset-bottom));
  }

  .to-bottom {
    right: 16px;
    bottom: 108px;
  }

  /* 16px 起步，避免 iOS Safari 聚焦时自动放大页面 */
  .composer__input {
    font-size: 16px;
  }
}
</style>
