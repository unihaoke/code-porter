import { getToken } from './client'
import type { ChatMessage } from '@/types'

// 网页对话走 SSE（POST + text/event-stream）。
// 之所以不用 EventSource：它只支持 GET，而对话需要把完整历史放进请求体。

export interface ChatMeta {
  task_id: string
  model: string
  mode: string
  agent: string
}

export interface ChatHandlers {
  onMeta?: (meta: ChatMeta) => void
  onChunk?: (delta: string) => void
  onDone?: (full: string) => void
  onError?: (message: string) => void
}

export interface ChatPayload {
  messages: ChatMessage[]
  model?: string
  mode?: string
  agent_id?: string
  work_dir?: string
  stream?: boolean
  max_tokens?: number
}

export async function streamChat(
  payload: ChatPayload,
  handlers: ChatHandlers,
  signal?: AbortSignal,
): Promise<void> {
  const resp = await fetch('/api/chat', {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      'X-Admin-Token': getToken(),
    },
    body: JSON.stringify({ ...payload, stream: true }),
    signal,
  })

  if (!resp.ok) {
    let message = `对话请求失败（HTTP ${resp.status}）`
    try {
      const body = await resp.json()
      if (body?.error?.message) message = body.error.message
    } catch {
      /* 忽略解析失败 */
    }
    handlers.onError?.(message)
    return
  }
  if (!resp.body) {
    handlers.onError?.('当前浏览器不支持流式响应')
    return
  }

  const reader = resp.body.getReader()
  const decoder = new TextDecoder()
  let buffer = ''

  try {
    for (;;) {
      if (signal?.aborted) break
      const { done, value } = await reader.read()
      if (done) break
      buffer += decoder.decode(value, { stream: true })
      const frames = buffer.split('\n\n')
      // 最后一段可能是不完整的帧，留在 buffer 里等下一批数据。
      buffer = frames.pop() ?? ''
      for (const frame of frames) {
        handleFrame(frame, handlers)
      }
    }
    if (buffer.trim()) {
      handleFrame(buffer, handlers)
    }
  } finally {
    await reader.cancel().catch(() => undefined)
  }
}

// handleFrame 解析单个 SSE 帧：event 行 + data 行。
function handleFrame(frame: string, handlers: ChatHandlers): void {
  let event = 'message'
  const dataLines: string[] = []
  for (const raw of frame.split('\n')) {
    const line = raw.trim()
    if (!line || line.startsWith(':')) continue
    if (line.startsWith('event:')) {
      event = line.slice(6).trim()
    } else if (line.startsWith('data:')) {
      dataLines.push(line.slice(5).trim())
    }
  }
  if (!dataLines.length) return
  const raw = dataLines.join('\n')
  if (raw === '[DONE]') return

  try {
    const payload = JSON.parse(raw) as Record<string, string>
    switch (event) {
      case 'meta':
        handlers.onMeta?.(payload as unknown as ChatMeta)
        break
      case 'chunk':
        handlers.onChunk?.(payload.content ?? '')
        break
      case 'done':
        handlers.onDone?.(payload.content ?? '')
        break
      case 'error':
        handlers.onError?.(payload.message ?? '未知错误')
        break
    }
  } catch {
    // 非 JSON 负载（如保活注释）忽略
  }
}
