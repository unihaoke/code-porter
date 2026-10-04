import { reactive } from 'vue'

import type {
  AgentConfig,
  AgentStatus,
  CliTestResult,
  HealthEntry,
  LogEntry,
  TaskEvent
} from '@shared/types'

/** 单条日志在界面里的展示形态。 */
export interface DisplayLog extends LogEntry {
  id: number
}

declare global {
  interface Window {
    codeporter: import('@shared/types').CodeporterApi
  }
}

const api = window.codeporter

/**
 * 全局状态。用 reactive + 模块级单例，组件直接共享同一份引用，
 * 避免多视图各自维护副本导致状态不一致。
 */
const state = reactive({
  config: null as AgentConfig | null,
  status: null as AgentStatus | null,
  health: [] as HealthEntry[],
  logs: [] as DisplayLog[],
  tasks: [] as TaskEvent[],
  testing: false,
  testResult: null as CliTestResult | null,
  busy: false,
  ready: false
})

let logSeq = 0
const MAX_LOGS = 3000

/** 追加一条日志并滚动到底部。 */
function pushLog(level: LogEntry['level'], time: string, msg: string): void {
  state.logs.push({ id: ++logSeq, level, time, msg })
  if (state.logs.length > MAX_LOGS) state.logs.splice(0, state.logs.length - MAX_LOGS)
}

/** 处理来自 Go 核心的事件。 */
function handleEvent(name: string, data: unknown): void {
  switch (name) {
    case 'log': {
      const p = data as LogEntry
      const msg = (p.msg ?? String(data)).trim()
      // 丢掉空消息：否则界面会出现「只有级别没有内容」的噪声行。
      if (msg) pushLog(p.level ?? 'info', p.time ?? '', msg)
      break
    }
    case 'status': {
      state.status = data as AgentStatus
      break
    }
    case 'health': {
      state.health = (data as HealthEntry[]) ?? []
      break
    }
    case 'task': {
      const t = data as TaskEvent
      state.tasks.unshift(t)
      if (state.tasks.length > 50) state.tasks.length = 50
      break
    }
    case 'ready': {
      state.ready = true
      break
    }
    default:
      break
  }
}

/** 初始化：拉配置 + 订阅事件 + 拉一次状态。 */
async function init(): Promise<void> {
  api.onEvent(handleEvent)
  try {
    state.config = await api.getConfig()
    state.status = await api.status()
  } catch (err) {
    pushLog('error', now(), `初始化失败：${(err as Error).message}`)
    // 核心可能仍在启动，重试几次拿配置。
    for (let i = 0; i < 5 && !state.config; i++) {
      await sleep(700)
      try {
        state.config = await api.getConfig()
        state.status = await api.status()
      } catch {
        /* 继续重试 */
      }
    }
  }
  await refreshHealth()
  // 代理未运行时不会有 health 事件（它由运行中的健康上报器推送），
  // 所以启动时主动做一次「仅安装检测」：不消耗额度，也能立刻点亮仪表盘。
  void primeHealth()
}

/**
 * 用免额度的安装检测填充工具健康状态。
 *
 * 之所以不能只等 health 事件：health 由运行中的健康上报器按心跳推送，
 * 代理没启动时界面会一直停在「检测中」。
 */
async function primeHealth(): Promise<void> {
  try {
    const r = await api.testCli(true)
    state.health = r.results
      .filter((x) => x.status !== 'skipped')
      .map((x) => ({ model: x.model, available: x.ok, detail: x.detail }))
  } catch {
    /* 核心尚未就绪时忽略 */
  }
}

/** 主动拉一次状态。 */
async function refreshHealth(): Promise<void> {
  try {
    const st = await api.status()
    if (st) state.status = st
  } catch {
    /* 状态不可用时忽略 */
  }
}

function now(): string {
  return new Date().toLocaleTimeString('zh-CN', { hour12: false })
}

/**
 * 剥掉 Vue 的响应式代理，返回纯数据副本。
 *
 * Electron 的 IPC 走结构化克隆（structured clone），**Proxy 不可克隆**：
 * 直接把 `reactive()` 出来的对象传过去会报
 * "An object could not be cloned"（表现就是保存配置失败）。
 * 配置都是纯 JSON 数据，用 JSON 往返做深拷贝最稳，也顺带把嵌套层的
 * 代理一并去掉（toRaw() 只解最外层，够不到深层）。
 */
function toPlain<T>(v: T): T {
  return JSON.parse(JSON.stringify(v)) as T
}

function sleep(ms: number): Promise<void> {
  return new Promise((r) => setTimeout(r, ms))
}

/** 保存当前配置。 */
async function saveConfig(): Promise<boolean> {
  if (!state.config) return false
  state.busy = true
  try {
    // 必须传纯数据：state.config 是 reactive 代理，直接传会
    // "An object could not be cloned"。
    await api.saveConfig(toPlain(state.config))
    pushLog('info', now(), '配置已保存')
    return true
  } catch (err) {
    pushLog('error', now(), `保存失败：${(err as Error).message}`)
    return false
  } finally {
    state.busy = false
  }
}

/** 启动代理。 */
async function startAgent(): Promise<void> {
  state.busy = true
  try {
    state.status = await api.start()
    pushLog('info', now(), '代理已启动')
  } catch (err) {
    pushLog('error', now(), `启动失败：${(err as Error).message}`)
  } finally {
    state.busy = false
  }
}

/** 停止代理。 */
async function stopAgent(): Promise<void> {
  state.busy = true
  try {
    state.status = await api.stop()
    pushLog('info', now(), '代理已停止')
  } catch (err) {
    pushLog('error', now(), `停止失败：${(err as Error).message}`)
  } finally {
    state.busy = false
  }
}

/** 测试本地 AI 连通性。 */
async function testCli(skipProbe = false): Promise<CliTestResult | null> {
  state.testing = true
  state.testResult = null
  pushLog('info', now(), skipProbe ? '开始检测本地 AI（仅安装检测）…' : '开始测试本地 AI 连通性…')
  try {
    const r = await api.testCli(skipProbe)
    state.testResult = r
    // 测试结果本身就是最新的健康信息，顺手同步到仪表盘。
    state.health = r.results
      .filter((x) => x.status !== 'skipped')
      .map((x) => ({ model: x.model, available: x.ok, detail: x.detail }))
    // 测试结果本身就是最新的健康信息，顺手同步到仪表盘。
    state.health = r.results
      .filter((x) => x.status !== 'skipped')
      .map((x) => ({ model: x.model, available: x.ok, detail: x.detail }))
    // 测试报告整段写入日志，便于事后翻查。
    for (const line of r.report.split('\n')) {
      if (line.trim()) pushLog('info', now(), line.replace(/\r/g, ''))
    }
    return r
  } catch (err) {
    pushLog('error', now(), `测试失败：${(err as Error).message}`)
    return null
  } finally {
    state.testing = false
  }
}

/** 打开目录选择对话框并写回配置。 */
async function pickWorkDir(): Promise<void> {
  if (!state.config) return
  const cur = state.config.mcp?.work_dir || ''
  const dir = await api.pickDirectory('选择 AI CLI 的工作目录')
  if (!dir) return
  state.config.mcp.work_dir = dir === cur ? '' : dir
  pushLog('info', now(), dir === cur ? '已恢复为默认（客户端 exe 所在目录）' : `工作目录：${dir}`)
}

export function useStore() {
  // 注意：这里必须暴露可写 state。若用 readonly() 包装，DeepReadonly 会让
  // 配置页的 v-model、日志页的清空操作全部编译报错。
  // 约定：组件只改自己负责的字段，其余变更一律走下面的 action。
  return {
    state,
    init,
    saveConfig,
    startAgent,
    stopAgent,
    testCli,
    pickWorkDir,
    refreshHealth
  }
}
