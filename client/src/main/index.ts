import { app, BrowserWindow, dialog, ipcMain, shell } from 'electron'
import path from 'node:path'
import fs from 'node:fs'

import { CoreProcess } from './core-process'
import type { AgentConfig, CliTestResult, AgentStatus } from '../shared/types'

const isDev = !app.isPackaged

/** 主窗口引用，核心事件需要转发给它。 */
let win: BrowserWindow | null = null
/** Go 核心进程封装。 */
let core: CoreProcess | null = null

/**
 * 解析 agent.yaml 的绝对路径。
 * 开发态放在 backend/configs/agent.yaml；打包态放在用户数据目录，
 * 并在首次运行时从 templates 复制一份初始配置。
 */
function resolveConfigPath(): string {
  if (isDev) {
    return path.resolve(__dirname, '../../../../backend/configs/agent.yaml')
  }
  const userCfg = path.join(app.getPath('userData'), 'configs', 'agent.yaml')
  console.log('[config] userData =', app.getPath('userData'))
  if (fs.existsSync(userCfg)) return userCfg

  const tpl = path.join(process.resourcesPath, 'core', 'configs', 'agent.yaml')
  try {
    fs.mkdirSync(path.dirname(userCfg), { recursive: true })
    if (fs.existsSync(tpl)) {
      fs.copyFileSync(tpl, userCfg)
    } else {
      // 模板缺失时的兜底内容：必须与 backend/configs/agent.yaml 保持同一代格式。
      // 绝不能再写旧版 agent.token——多租户版本只认控制台生成的 agent.key，
      // 历史上这里写过 token 占位值，导致用户粘贴新秘钥后仍被旧字段卡住启动。
      fs.writeFileSync(
        userCfg,
        [
          '# CodePorter LocalAgent 配置（首次运行自动生成）',
          '# 连接秘钥在网关控制台「秘钥」页创建（勾选 agent 权限）后粘贴到 agent.key',
          'agent:',
          '  id: ""',
          '  name: ""',
          '  key: ""',
          'gateway:',
          '  addr: "http://127.0.0.1:9022"',
          '  timeout: 30s',
          '  insecure_tls: false',
          'pull:',
          '  interval_min: 500ms',
          '  interval_max: 3s',
          '  backoff_factor: 1.6',
          'worker_pool:',
          '  max_concurrency: 2',
          '  queue_size: 4',
          'direct:',
          '  enabled: true',
          '  heartbeat_interval: 20s',
          '  pong_timeout: 60s',
          '  reconnect_min: 1s',
          '  reconnect_max: 30s',
          'health:',
          '  interval: 15s',
          'mcp:',
          '  work_dir: ""',
          '  trae:',
          '    enabled: true',
          '    command: "trae-mcp"',
          '  claude_code:',
          '    enabled: true',
          '    command: "claude"',
          '  codebuddy:',
          '    enabled: true',
          '    command: "codebuddy-mcp"',
          '  codex:',
          '    enabled: false',
          '    command: "codex"',
          'secrets: {}',
          'log:',
          '  level: info',
          ''
        ].join('\n'),
        'utf8'
      )
    }
  } catch (err) {
    // 不静默：写不了用户目录时必须让人知道，否则界面会一直加载失败。
    console.error('[config] 初始化用户配置失败:', err)
  }
  console.log('[config] 使用配置文件:', userCfg)
  return userCfg
}

/** 创建主窗口。 */
function createWindow(): void {
  win = new BrowserWindow({
    width: 1180,
    height: 780,
    minWidth: 940,
    minHeight: 620,
    show: false,
    backgroundColor: '#f5f6f8',
    title: 'CodePorter 本地代理',
    autoHideMenuBar: true,
    webPreferences: {
      preload: path.join(__dirname, '../preload/index.js'),
      sandbox: false,
      contextIsolation: true,
      nodeIntegration: false
    }
  })

  // 把渲染进程的 console 转发到主进程输出，排查界面问题用。
  win.webContents.on('console-message', (_e, level, message, line, source) => {
    if (level >= 2) console.log(`[renderer] ${message} (${source}:${line})`)
  })

  // 兜底：首帧迟迟不来（页面加载失败、资源缺失等）时也要把窗口露出来，
  // 否则用户双击 exe 后什么都看不到，也没法判断出了什么事。
  const showTimer = setTimeout(() => win?.show(), 8000)
  win.webContents.on('did-fail-load', (_e, code, desc, url) => {
    console.error('[renderer] 页面加载失败:', code, desc, url)
    clearTimeout(showTimer)
    win?.show()
  })

  win.on('ready-to-show', () => {
    clearTimeout(showTimer)
    win?.show()
    void maybeCaptureShot()
  })

  if (isDev && process.env.VITE_DEV_SERVER_URL) {
    const hash = process.env.CODEPORTER_TAB ? `#${process.env.CODEPORTER_TAB}` : ''
    void win.loadURL(process.env.VITE_DEV_SERVER_URL + hash)
  } else {
    const hash = process.env.CODEPORTER_TAB ? `#${process.env.CODEPORTER_TAB}` : ''
    void win.loadURL(
      'file://' +
        path.join(__dirname, '../../../dist/electron/index.html').replace(/\\/g, '/') +
        hash
    )
  }
  win.on('closed', () => {
    win = null
  })
}

/** 注册所有 IPC 处理器。 */
function registerIpc(): void {
  const need = (): CoreProcess => {
    if (!core) throw new Error('核心进程未启动')
    // 进程对象已消失 = 核心没起来或已被杀掉，给出可排查的提示而不是一句"未启动"。
    if (!core.isRunning()) {
      throw new Error(
        '核心进程未启动：Go 核心（codeporter-core.exe）没有在运行。' +
        '通常是该文件被覆盖损坏（只有几字节）或被安全软件拦截，' +
        '请重新执行 build-client.bat 后重启应用。'
      )
    }
    return core
  }

  ipcMain.handle('config:get', () => need().call<AgentConfig>('config.get', undefined, 20_000))
  ipcMain.handle('config:save', (_e, cfg: AgentConfig) =>
    need().call<{ saved: boolean; path: string }>('config.save', cfg, 20_000)
  )
  ipcMain.handle('agent:start', () => need().call<AgentStatus>('agent.start', undefined, 30_000))
  ipcMain.handle('agent:stop', () => need().call<AgentStatus>('agent.stop', undefined, 60_000))
  ipcMain.handle('agent:status', () => need().call<AgentStatus>('agent.status', undefined, 20_000))
  ipcMain.handle('cli:test', (_e, skipProbe: boolean) =>
    need().call<CliTestResult>('cli.test', { skip_probe: skipProbe }, 6 * 60_000)
  )
  ipcMain.handle('dialog:pickDirectory', async (_e, title: string) => {
    const r = await dialog.showOpenDialog(win!, {
      title: title || '选择目录',
      properties: ['openDirectory', 'createDirectory']
    })
    return r.canceled || r.filePaths.length === 0 ? null : r.filePaths[0]
  })
  ipcMain.handle('shell:openExternal', (_e, url: string) => {
    if (/^https?:\/\//i.test(url)) void shell.openExternal(url)
  })
  // 打开本地目录（如配置文件所在文件夹）。openPath 失败时返回错误信息字符串，
  // 成功返回空串——交回渲染层用于提示。
  ipcMain.handle('shell:openPath', (_e, target: string) => {
    if (typeof target !== 'string' || !target) return '路径为空'
    return shell.openPath(target)
  })
  ipcMain.handle('app:quit', () => {
    core?.dispose()
    app.quit()
  })
}

/** 安全地给渲染进程发消息：窗口或 frame 已销毁时忽略，避免退出期报错。 */
function sendToRenderer(channel: string, ...args: unknown[]): void {
  if (!win || win.isDestroyed()) return
  const wc = win.webContents
  if (wc.isDestroyed()) return
  try {
    wc.send(channel, ...args)
  } catch {
    /* frame 可能正在释放，忽略 */
  }
}

/**
 * 开发期截图：设置 CODEPORTER_SHOT=<png路径> 启动时自动截图并退出，
 * 便于在没有人工观察的情况下核对界面效果。
 */
async function maybeCaptureShot(): Promise<void> {
  const target = process.env.CODEPORTER_SHOT
  if (!isDev || !target || !win) return
  await new Promise((r) => setTimeout(r, 2500))
  try {
    const img = await win.webContents.capturePage()
    fs.writeFileSync(target, img.toPNG())
    console.log('[shot] 已保存界面截图:', target)
  } catch (err) {
    console.error('[shot] 截图失败:', err)
  }
  core?.dispose()
  app.quit()
}

/** 把核心事件转发给渲染进程。 */
function bridgeCoreEvents(): void {
  if (!core) return
  core.on('event', (name: string, data: unknown) => {
    sendToRenderer('core:event', name, data)
  })
  core.on('core-stderr', (line: string) => {
    sendToRenderer('core:event', 'log', {
      level: 'info',
      time: new Date().toLocaleTimeString('zh-CN', { hour12: false }),
      msg: line
    })
  })
  core.on('core-error', (err: Error) => {
    const msg = `核心进程错误：${err.message}`
    sendToRenderer('core:event', 'log', {
      level: 'error',
      time: new Date().toLocaleTimeString('zh-CN', { hour12: false }),
      msg
    })
    // 额外发一条结构化事件，让界面能直接弹提示，而不只是沉到日志里。
    sendToRenderer('core:event', 'core-error', { msg })
  })
  core.on('core-exit', (code: number | null) => {
    const msg = `核心进程已退出（code=${code}），启动/检测等操作将不可用，请重启客户端`
    sendToRenderer('core:event', 'log', {
      level: 'warn',
      time: new Date().toLocaleTimeString('zh-CN', { hour12: false }),
      msg
    })
    sendToRenderer('core:event', 'core-exit', { code, msg })
  })
}

void app.whenReady().then(() => {
  const cfgPath = resolveConfigPath()
  core = new CoreProcess(cfgPath)
  // 核心起不来不能连累界面：出错时转成 core-error 事件，界面照常显示并给出日志。
  try {
    core.start()
  } catch (err) {
    console.error('[core] 启动失败:', err)
    core.emit('core-error', err instanceof Error ? err : new Error(String(err)))
  }
  bridgeCoreEvents()
  registerIpc()
  createWindow()

  app.on('activate', () => {
    if (BrowserWindow.getAllWindows().length === 0) createWindow()
  })
})

app.on('window-all-closed', () => {
  core?.dispose()
  if (process.platform !== 'darwin') app.quit()
})

app.on('before-quit', () => core?.dispose())
