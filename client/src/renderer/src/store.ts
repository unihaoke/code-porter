import { reactive } from 'vue'

import type {
  AgentConfig,
  AgentStatus,
  BotTestResult,
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

/** 机器人渠道名 → 中文展示名（未知渠道原样显示）。 */
const BOT_LABELS: Record<string, string> = {
  feishu: '飞书',
  wecom: '企业微信'
}

/** startBots/stopBots 不带渠道时使用的聚合忙状态键。 */
const ALL_BOTS = '__all__'

function botLabel(channel: string): string {
  return BOT_LABELS[channel] ?? channel
}

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
  // 机器人各渠道（feishu/wecom/…）与代理各自独立开关，忙状态按渠道分开：
  // 操作一个渠道时不应禁用代理、其他渠道或本地 AI 工具的按钮。
  botBusy: {} as Record<string, boolean>,
  botTesting: {} as Record<string, boolean>,
  botTestResults: {} as Record<string, BotTestResult>,
  // 本地 AI 工具预热运行时是第三个独立服务，忙状态同样独立。
  toolsBusy: false,
  // 概览手动刷新（重拉运行态 + 免额度安装检测）中。
  refreshing: false,
  // 当前激活的一级页（用于日志未读计数等跨页逻辑）。
  activeTab: 'dashboard' as 'dashboard' | 'bots' | 'settings' | 'logs',
  // 不在日志页时收到的日志条数；切回日志页清零。
  unreadLogs: 0,
  // 外观主题（localStorage 持久化，默认跟随系统浅色）。
  theme: 'light' as 'light' | 'dark',
  ready: false,
  toasts: [] as Toast[]
})

let logSeq = 0
let toastSeq = 0
const MAX_LOGS = 3000

/**
 * 最近一次「已保存」配置的 JSON 快照。配置页用它判断是否有未保存改动
 * （脏检查），保存成功后刷新。配置为纯 JSON 数据，字符串比较最稳。
 */
let savedSnapshot = ''

function snapshotOf(cfg: AgentConfig | null): string {
  return cfg ? JSON.stringify(cfg) : ''
}

/**
 * 指定配置节（顶层键）是否相对已保存快照有改动；
 * 不传 sections 时判断整份配置。
 */
function isDirty(sections?: string[]): boolean {
  if (!state.config) return false
  if (!sections || sections.length === 0) {
    return JSON.stringify(state.config) !== savedSnapshot
  }
  const cur = state.config as Record<string, unknown>
  const saved = (JSON.parse(savedSnapshot || '{}') as Record<string, unknown>)
  return sections.some((k) => JSON.stringify(cur[k]) !== JSON.stringify(saved[k]))
}

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
  // 只有不在日志页时才累计未读，避免 badge 数字无意义地增长。
  if (state.activeTab !== 'logs') state.unreadLogs++
}

/** 切换一级页（App 侧边栏调用）；进入日志页时未读清零。 */
function setActiveTab(tab: typeof state.activeTab): void {
  state.activeTab = tab
  if (tab === 'logs') state.unreadLogs = 0
}

const THEME_KEY = 'codeporter.theme'

/** 应用主题到 <html data-theme>，CSS 变量按该属性切换色板。 */
function applyTheme(theme: 'light' | 'dark'): void {
  document.documentElement.dataset.theme = theme
}

/** 初始化主题：优先本地存储，否则跟随系统外观。 */
function initTheme(): void {
  const saved = localStorage.getItem(THEME_KEY)
  const prefersDark =
    window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches
  state.theme = saved === 'dark' || saved === 'light'
    ? saved
    : prefersDark
      ? 'dark'
      : 'light'
  applyTheme(state.theme)
}

/** 切换明 / 暗主题并持久化。 */
function toggleTheme(): void {
  state.theme = state.theme === 'dark' ? 'light' : 'dark'
  localStorage.setItem(THEME_KEY, state.theme)
  applyTheme(state.theme)
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

/**
 * 补齐配置中可选但界面直接 v-model 的节，避免旧版 agent.yaml 缺字段时
 * 「Cannot read properties of undefined」。Go 核心的默认配置通常已含这些值，
 * 这里只做防御性兜底。
 */
function ensureConfigShape(cfg: AgentConfig): AgentConfig {
  if (!cfg.bots) {
    cfg.bots = { feishu: defaultFeishuBot(), wecom: defaultWeComBot() }
  } else {
    if (!cfg.bots.feishu) cfg.bots.feishu = defaultFeishuBot()
    if (!cfg.bots.wecom) cfg.bots.wecom = defaultWeComBot()
  }
  return cfg
}

/** 飞书机器人节的默认值（旧版 agent.yaml 缺 bots.feishu 时兜底）。 */
function defaultFeishuBot() {
  return {
    enabled: false,
    app_id: '',
    app_secret: '',
    model: '',
    mention_only: true,
    system_prompt: ''
  }
}

/** 企业微信机器人节的默认值（旧版 agent.yaml 缺 bots.wecom 时兜底）。 */
function defaultWeComBot() {
  return {
    enabled: false,
    bot_id: '',
    secret: '',
    model: '',
    mention_only: true,
    system_prompt: ''
  }
}

/** 初始化：拉配置 + 订阅事件 + 拉一次状态。 */
async function init(): Promise<void> {
  initTheme()
  api.onEvent(handleEvent)
  try {
    state.config = ensureConfigShape(await api.getConfig())
    savedSnapshot = snapshotOf(state.config)
    state.status = await api.status()
  } catch (err) {
    pushLog('error', now(), `初始化失败：${(err as Error).message}`)
    // 核心可能仍在启动，重试几次拿配置。
    for (let i = 0; i < 5 && !state.config; i++) {
      await sleep(700)
      try {
        state.config = ensureConfigShape(await api.getConfig())
        savedSnapshot = snapshotOf(state.config)
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

/**
 * 手动刷新概览：重拉运行态（运行中/启用态/机器人配置态）并重跑一次免额度
 * 安装检测（「本地 AI 可用」数）。
 *
 * 刻意不重拉 config：配置页直接 v-model 编辑 state.config，拉盘会冲掉未保存
 * 的修改；保存后的配置态已由 config.save 的 status 事件同步。
 */
async function refreshOverview(): Promise<void> {
  if (state.refreshing) return
  state.refreshing = true
  try {
    const st = await api.status()
    if (st) state.status = st
    await primeHealth()
    pushLog('info', now(), '概览已刷新')
  } catch (err) {
    const msg = errMessage(err)
    pushLog('error', now(), `刷新概览失败：${msg}`)
    toast(`刷新失败：${msg}`, 'error')
  } finally {
    state.refreshing = false
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
    savedSnapshot = snapshotOf(state.config)
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

/**
 * 启动 IM 机器人（与代理独立：IM 长连接，消息本地闭环）。
 * @param channel 指定渠道（feishu/wecom）；留空启动所有已启用渠道。
 */
async function startBots(channel?: string): Promise<string | null> {
  const key = channel ?? ALL_BOTS
  state.botBusy[key] = true
  try {
    state.status = await api.startBots(channel)
    pushLog('info', now(), channel ? `${botLabel(channel)}机器人已启动` : '所有已启用的机器人渠道已启动')
    toast(channel ? `${botLabel(channel)}机器人已启动` : '机器人服务已启动')
    return null
  } catch (err) {
    const msg = errMessage(err)
    pushLog('error', now(), `机器人启动失败：${msg}`)
    toast(`机器人启动失败：${msg}`, 'error')
    return msg
  } finally {
    state.botBusy[key] = false
  }
}

/** 停止 IM 机器人。channel 留空时停止全部渠道。成功返回 null，失败返回错误信息。 */
async function stopBots(channel?: string): Promise<string | null> {
  const key = channel ?? ALL_BOTS
  state.botBusy[key] = true
  try {
    state.status = await api.stopBots(channel)
    pushLog('info', now(), channel ? `${botLabel(channel)}机器人已停止` : '所有机器人渠道已停止')
    toast(channel ? `${botLabel(channel)}机器人已停止` : '机器人服务已停止')
    return null
  } catch (err) {
    const msg = errMessage(err)
    pushLog('error', now(), `机器人停止失败：${msg}`)
    toast(`机器人停止失败：${msg}`, 'error')
    return msg
  } finally {
    state.botBusy[key] = false
  }
}

/**
 * 启动（预热）本地 AI 工具：只拉起配置中已启用的工具，与代理 / 机器人独立。
 * 开机不会自动启动，仅由界面按钮触发。
 * @returns 成功（含部分工具失败）返回 null；整体启动失败返回错误信息。
 */
async function startTools(): Promise<string | null> {
  state.toolsBusy = true
  try {
    const r = await api.startTools()
    state.status = r.status
    // 预热结果是最新的可用性信息，按工具合并进健康状态。
    const healthByModel = new Map(state.health.map((h) => [h.model, h]))
    for (const it of r.results) {
      healthByModel.set(it.model, { model: it.model, available: it.ok, detail: it.detail })
    }
    state.health = [...healthByModel.values()]
    const okCount = r.results.filter((x) => x.ok).length
    const failCount = r.results.length - okCount
    for (const it of r.results) {
      pushLog(it.ok ? 'info' : 'warn', now(), `工具 ${it.label}（${it.mode}）：${it.detail}`)
    }
    if (failCount > 0) {
      const msg = `本地 AI 工具已启动：${okCount} 个就绪，${failCount} 个失败（详见日志）`
      pushLog('warn', now(), msg)
      toast(msg)
    } else {
      pushLog('info', now(), `本地 AI 工具已启动（${okCount} 个就绪）`)
      toast(`本地 AI 工具已启动（${okCount} 个就绪）`)
    }
    return null
  } catch (err) {
    const msg = errMessage(err)
    pushLog('error', now(), `本地 AI 工具启动失败：${msg}`)
    toast(`本地 AI 工具启动失败：${msg}`, 'error')
    return msg
  } finally {
    state.toolsBusy = false
  }
}

/** 停止本地 AI 工具预热运行时。成功返回 null，失败返回错误信息。 */
async function stopTools(): Promise<string | null> {
  state.toolsBusy = true
  try {
    state.status = await api.stopTools()
    pushLog('info', now(), '本地 AI 工具已停止')
    toast('本地 AI 工具已停止')
    return null
  } catch (err) {
    const msg = errMessage(err)
    pushLog('error', now(), `本地 AI 工具停止失败：${msg}`)
    toast(`本地 AI 工具停止失败：${msg}`, 'error')
    return msg
  } finally {
    state.toolsBusy = false
  }
}

/**
 * 保存配置并重启正在运行的本地服务（代理 / 机器人 / 本地 AI 工具）。
 *
 * 为什么必须重启：本地 AI 配置（工作目录、CLI 命令、工具开关、密钥等）在服务
 * Start 时就被固化进各自的 MCP 注册表，config.save 只更新核心内存配置，
 * 不会重建已运行实例的子进程环境——所以光保存不改运行态，必须 stop+start。
 *
 * @returns 成功返回 null；失败返回错误信息。
 */
async function saveAndRestartRunning(): Promise<string | null> {
  if (!state.config) return '配置尚未加载完成'
  const agentWasRunning = !!state.status?.running
  // 机器人按渠道独立运行：记录当前在跑的渠道集合，稍后只重启这些渠道，
  // 未运行的渠道（含刚在界面上勾选启用但没启动的）不擅自拉起。
  const runningBotChannels = Object.entries(state.status?.bots ?? {})
    .filter(([, st]) => st.running)
    .map(([ch]) => ch)
  const botWasRunning = runningBotChannels.length > 0
  const toolsWereRunning = !!state.status?.tools_running

  state.busy = true
  for (const ch of runningBotChannels) state.botBusy[ch] = true
  state.toolsBusy = true
  try {
    // 1) 先落盘（必须传纯数据，原因同 saveConfig）。
    await api.saveConfig(toPlain(state.config))
    savedSnapshot = snapshotOf(state.config)
    pushLog('info', now(), '配置已保存')

    // 2) 没有运行中的服务：下次启动自然用新配置，无需重启。
    if (!agentWasRunning && !botWasRunning && !toolsWereRunning) {
      pushLog('info', now(), '当前没有运行中的服务，新配置将在下次启动时生效')
      toast('配置已保存；当前无运行中的服务，下次启动即生效')
      try {
        state.status = await api.status()
      } catch {
        /* 状态刷新失败可忽略 */
      }
      return null
    }

    // 3) 逐个重启原本就在运行的服务；未运行的不擅自启动。
    const restarted: string[] = []
    if (agentWasRunning) {
      pushLog('info', now(), '正在重启代理以应用本地 AI 配置…')
      await api.stop()
      state.status = await api.start()
      restarted.push('代理')
    }
    for (const ch of runningBotChannels) {
      pushLog('info', now(), `正在重启${botLabel(ch)}机器人以应用本地 AI 配置…`)
      await api.stopBots(ch)
      state.status = await api.startBots(ch)
      restarted.push(`${botLabel(ch)}机器人`)
    }
    if (toolsWereRunning) {
      pushLog('info', now(), '正在重启本地 AI 工具以应用配置…')
      await api.stopTools()
      state.status = (await api.startTools()).status
      restarted.push('本地 AI 工具')
    }
    const msg = `配置已保存，${restarted.join('、')}已按新配置重启`
    pushLog('info', now(), msg)
    toast(msg)
    return null
  } catch (err) {
    const msg = errMessage(err)
    pushLog('error', now(), `保存并重启失败：${msg}`)
    toast(`保存并重启失败：${msg}`, 'error')
    // 失败后主动拉一次状态，让界面如实反映服务停在了哪一步。
    try {
      state.status = await api.status()
    } catch {
      /* 忽略 */
    }
    return msg
  } finally {
    state.busy = false
    for (const ch of runningBotChannels) state.botBusy[ch] = false
    state.toolsBusy = false
  }
}

/**
 * 测试机器人凭证（不建立长连接）。
 * 注意：校验的是核心内存中「上次保存」的配置，界面改完未保存不会被带上。
 */
async function testBot(channel = 'feishu'): Promise<BotTestResult | null> {
  state.botTesting[channel] = true
  delete state.botTestResults[channel]
  pushLog('info', now(), `开始测试${botLabel(channel)}机器人凭证…`)
  try {
    const r = await api.testBot(channel)
    state.botTestResults[channel] = r
    pushLog(r.ok ? 'info' : 'warn', now(), `${botLabel(channel)}机器人连接测试：${r.detail}`)
    if (r.ok && r.tenant_key) {
      pushLog('info', now(), `凭证有效：租户 ${r.tenant_key}，token 有效期 ${r.expire_seconds ?? 0} 秒`)
    }
    return r
  } catch (err) {
    // 核心在凭证无效时以 IPC 错误返回 detail，这里保留一份失败结果供界面就地展示。
    const msg = errMessage(err)
    state.botTestResults[channel] = { channel, ok: false, detail: msg }
    pushLog('error', now(), `${botLabel(channel)}机器人连接测试失败：${msg}`)
    return null
  } finally {
    state.botTesting[channel] = false
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
    saveAndRestartRunning,
    isDirty,
    setActiveTab,
    toggleTheme,
    startAgent,
    stopAgent,
    startBots,
    stopBots,
    startTools,
    stopTools,
    testBot,
    testCli,
    pickWorkDir,
    openConfigDir,
    refreshHealth,
    refreshOverview
  }
}
