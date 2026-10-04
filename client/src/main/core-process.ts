import { spawn, type ChildProcessWithoutNullStreams } from 'node:child_process'
import { EventEmitter } from 'node:events'
import fs from 'node:fs'
import path from 'node:path'
import { app } from 'electron'

import type { CoreEventName, IpcAction } from '../shared/types'

/** 核心侧的一条协议消息。 */
type CoreMessage =
  | { id: string; ok: boolean; result?: unknown; error?: string }
  | { event: CoreEventName; data?: unknown }

/** 待完成请求的回调。 */
type Pending = {
  resolve: (v: unknown) => void
  reject: (e: Error) => void
  timer: NodeJS.Timeout
}

/**
 * CoreProcess 管理 Go 核心子进程，并通过 stdin/stdout 的 JSON 行协议通信。
 *
 * 关键约束：核心的 stdout **只承载协议消息**（它的运行日志也走事件通道），
 * 所以这里绝不能把 stdout 当普通日志流读取或打印，否则会干扰解析。
 */
export class CoreProcess extends EventEmitter {
  private proc: ChildProcessWithoutNullStreams | null = null
  private pending = new Map<string, Pending>()
  private seq = 0
  private ready = false
  /** stdout 残留缓冲，用于处理跨行的半包。 */
  private buf = ''

  constructor(private readonly configPath: string) {
    super()
  }

  /** 核心是否已就绪（收到过 ready 事件）。 */
  isReady(): boolean {
    return this.ready
  }

  /** 子进程是否还活着。 */
  isRunning(): boolean {
    return this.proc !== null
  }

  /** 定位 Go 核心可执行文件的候选路径。 */
  static candidates(): string[] {
    const exeName = process.platform === 'win32' ? 'codeporter-core.exe' : 'codeporter-core'
    const roots: string[] = []

    // 1) 开发态：仓库内 backend 编译产物
    //    __dirname = <repo>/client/dist-main/src/main，需上溯 4 层到仓库根。
    const dev = path.resolve(__dirname, '../../../..')
    roots.push(path.join(dev, 'backend', 'bin'))
    roots.push(path.join(dev, 'backend'))

    // 2) 打包态：resources 目录（electron-builder 的 extraResources）
    if (process.resourcesPath) roots.push(path.join(process.resourcesPath, 'core'))
    if (app.isPackaged && process.resourcesPath) roots.push(path.join(process.resourcesPath))

    // 3) 用户数据目录（首次使用时可自行放置）
    roots.push(path.join(app.getPath('userData'), 'core'))

    return roots.map((r) => path.join(r, exeName))
  }

  /** 找到第一个存在的核心可执行文件。 */
  static resolveBinary(): string | null {
    for (const p of CoreProcess.candidates()) {
      try {
        if (fs.existsSync(p)) return p
      } catch {
        /* 忽略探测异常，继续下一个候选 */
      }
    }
    return null
  }

  /** 启动核心进程。 */
  start(): void {
    if (this.proc) return
    const bin = CoreProcess.resolveBinary()
    if (!bin) {
      this.emit('core-error', new Error(
        '未找到 Go 核心可执行文件（已查找：' + CoreProcess.candidates().join('、') + '）。' +
        '开发态请先执行 `npm run dev:core` 编译；打包态请确认 resources/core 目录存在，' +
        '且 codeporter-core.exe 是完整的（几字节大小说明它被覆盖损坏，或被安全软件隔离）。'
      ))
      return
    }

    // cwd 设为配置文件所在目录：AI CLI 的默认工作目录以此为基准。
    const cwd = path.dirname(this.configPath)
    try {
      this.proc = spawn(bin, ['-ipc', '-config', this.configPath], {
        cwd,
        windowsHide: true,
        stdio: ['pipe', 'pipe', 'pipe']
      }) as ChildProcessWithoutNullStreams
    } catch (err) {
      // spawn 在 Windows 上可能同步抛错（二进制损坏、cwd 不存在、被安全软件拦截）。
      // 必须转成事件而不是抛出，否则上层整个初始化会中断，界面都出不来。
      this.proc = null
      const reason = err instanceof Error ? err : new Error(String(err))
      this.emit('core-error', new Error(
        `无法启动核心进程（${bin}）：${reason.message}。` +
        '常见原因是文件被覆盖损坏（大小只有几字节）或被安全软件拦截，' +
        '请重新执行 build-client.bat 并把程序目录加入扫描白名单后重试。'
      ))
      return
    }

    this.proc.stdout.setEncoding('utf8')
    this.proc.stdout.on('data', (chunk: string) => this.onStdout(chunk))

    // 核心的 stderr 只用于诊断（正常日志已走事件通道）。
    this.proc.stderr.setEncoding('utf8')
    this.proc.stderr.on('data', (chunk: string) => {
      for (const line of chunk.split('\n')) {
        if (line.trim()) this.emit('core-stderr', line.trim())
      }
    })

    this.proc.on('error', (err) => this.emit('core-error', err))
    this.proc.on('exit', (code) => {
      this.ready = false
      this.proc = null
      // 让所有在途请求快速失败，而不是一直挂着。
      for (const [, p] of this.pending) {
        clearTimeout(p.timer)
        p.reject(new Error(`核心进程已退出（code=${code}）`))
      }
      this.pending.clear()
      this.emit('core-exit', code)
    })
  }

  /** 处理 stdout 增量：按行切分并解析协议消息。 */
  private onStdout(chunk: string): void {
    this.buf += chunk
    let idx: number
    while ((idx = this.buf.indexOf('\n')) >= 0) {
      const line = this.buf.slice(0, idx).trim()
      this.buf = this.buf.slice(idx + 1)
      if (!line) continue
      let msg: CoreMessage
      try {
        msg = JSON.parse(line) as CoreMessage
      } catch {
        // 不是协议消息：多半是意外写到了 stdout，转成日志事件以免静默丢失。
        this.emit('core-stderr', `[非协议输出] ${line.slice(0, 500)}`)
        continue
      }
      this.route(msg)
    }
  }

  /** 分发一条消息：要么是响应，要么是事件。 */
  private route(msg: CoreMessage): void {
    if ('event' in msg) {
      if (msg.event === 'ready') this.ready = true
      this.emit('event', msg.event, msg.data)
      return
    }
    const p = this.pending.get(msg.id)
    if (!p) return
    clearTimeout(p.timer)
    this.pending.delete(msg.id)
    if (msg.ok) p.resolve(msg.result)
    else p.reject(new Error(msg.error || '核心返回失败'))
  }

  /** 向核心发送一个请求并等待响应。 */
  call<T = unknown>(action: IpcAction, params?: unknown, timeoutMs = 120_000): Promise<T> {
    if (!this.proc) return Promise.reject(new Error('核心进程未启动'))
    const id = String(++this.seq)
    const payload = JSON.stringify({ id, action, params }) + '\n'
    return new Promise<T>((resolve, reject) => {
      const timer = setTimeout(() => {
        this.pending.delete(id)
        reject(new Error(`请求 ${action} 超时（${timeoutMs}ms）`))
      }, timeoutMs)
      this.pending.set(id, { resolve: resolve as (v: unknown) => void, reject, timer })
      this.proc!.stdin.write(payload, 'utf8')
    })
  }

  /** 通知核心退出（不等待响应）。 */
  dispose(): void {
    if (!this.proc) return
    try {
      this.proc.stdin.write(JSON.stringify({ id: 'bye', action: 'app.quit' }) + '\n')
      this.proc.stdin.end()
    } catch {
      /* 进程可能已退出，忽略 */
    }
    const p = this.proc
    setTimeout(() => {
      if (!p.killed) p.kill()
    }, 1500)
    this.proc = null
    this.ready = false
  }
}
