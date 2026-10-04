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

/** 一条界面提示（toast）。 */
export interface Toast {
  id: number
  text: string
  kind: 'info' | 'error'
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
  ready: false,
  toasts: [] as Toast[]
})

let logSeq = 0
let toastSeq = 0
const MAX_LOGS = 3000

/** 弹一条界面提示。错误类停留更久；同屏最多保留 3 条，避免刷屏。 */
function toast(text: string, kind: Toast['kind'] = 'info'): void {
  const id = ++toastSeq
  state.toasts.push({ id, text, kind })
  if (state.toasts.length > 3) state.toasts.splice(0, state.toasts.length - 3)
  window.setTimeout(() => dismissToast(id), kind === 'error' ? 8000 : 3000)
}

/** 关闭一条提示（也供点击 toast 时手动关闭）。 */
function dismissToast(id: number): void {
  const i = state.toasts.findIndex((t) => t.id === id)
  if (i >= 0) state.toasts.splice(i, 1)
}

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
    case 'core-error': {
      // 核心二进制损坏 / 被杀软拦截等致命错误：必须浮到界面上，不能只沉在日志里。
      const msg = (data as { msg?: string } | null)?.msg ?? '本地核心发生错误'
      toast(msg, 'error')
      break
    }
    case 'core-exit': {
      const p = data as { msg?: string } | null
      toast(p?.msg ?? '本地核心进程已退出，请重启客户端', 'error')
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
    if (!state.config) {
      toast('无法连接本地核心进程，操作暂不可用；请重启客户端或查看运行日志', 'error')
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

/**
 * 保存当前配置。
 * @returns 成功返回 null；失败返回后台给出的错误信息（供界面弹提示）。
 */
async function saveConfig(): Promise<string | null> {
  if (!state.config) return '配置尚未加载完成'
  state.busy = true
  try {
    // 必须传纯数据：state.config 是 reactive 代理，直接传会
    // "An object could not be cloned"。
    await api.saveConfig(toPlain(state.config))
    pushLog('info', now(), '配置已保存')
    toast('配置已保存')
    return null
  } catch (err) {
    const msg = errMessage(err)
    pushLog('error', now(), `保存失败：${msg}`)
    toast(`保存失败：${msg}`, 'error')
    return msg
  } finally {
    state.busy = false
  }
}

/**
 * 启动代理。
 * @returns 成功返回 null；失败返回后台给出的错误信息（如秘钥未配置、旧 token 等）。
 */
async function startAgent(): Promise<string | null> {
  state.busy = true
  try {
    state.status = await api.start()
    pushLog('info', now(), '代理已启动')
    toast('代理已启动')
    return null
  } catch (err) {
    const msg = errMessage(err)
    pushLog('error', now(), `启动失败：${msg}`)
    toast(`启动失败：${msg}`, 'error')
    return msg
  } finally {
    state.busy = false
  }
}

/** 停止代理。成功返回 null，失败返回错误信息。 */
async function stopAgent(): Promise<string | null> {
  state.busy = true
  try {
    state.status = await api.stop()
    pushLog('info', now(), '代理已停止')
    toast('代理已停止')
    return null
  } catch (err) {
    const msg = errMessage(err)
    pushLog('error', now(), `停止失败：${msg}`)
    toast(`停止失败：${msg}`, 'error')
    return msg
  } finally {
    state.busy = false
  }
}

/** 统一的错误取信：Electron IPC reject 出来的是 Error 实例。 */
function errMessage(err: unknown): string {
  return err instanceof Error && err.message ? err.message : String(err)
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

/** 在系统文件管理器中打开配置文件所在目录。 */
async function openConfigDir(): Promise<void> {
  const cfgPath = state.status?.config_path
  if (!cfgPath) {
    toast('尚未拿到配置文件路径，请稍候再试', 'error')
    return
  }
  // 去掉最后一段文件名；路径里没有分隔符时 replace 不匹配，原样返回。
  const dir = cfgPath.replace(/[\\/][^\\/]*$/, '')
  const errMsg = await api.openPath(dir)
  if (errMsg) toast(`打开目录失败：${errMsg}`, 'error')
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
    toast,
    dismissToast,
    saveConfig,
    startAgent,
    stopAgent,
    testCli,
    pickWorkDir,
    openConfigDir,
    refreshHealth
  }
}
