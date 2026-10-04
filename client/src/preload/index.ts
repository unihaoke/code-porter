import { contextBridge, ipcRenderer } from 'electron'

import type { AgentConfig, AgentStatus, CliTestResult, CoreEventName, CodeporterApi } from '../shared/types'

/**
 * 暴露给渲染进程的安全 API。
 * 渲染进程不开 nodeIntegration，只能通过这里与主进程通信。
 */
/**
 * 跨进程的数据必须能被结构化克隆：Proxy / 函数 / DOM 节点都会让 IPC 抛
 * "An object could not be cloned"。渲染层已用 toPlain() 处理过，
 * 这里在边界上再兜一次底，避免以后新增调用点踩同一个坑。
 */
function plain<T>(v: unknown): T {
  return JSON.parse(JSON.stringify(v ?? null)) as T
}

const api: CodeporterApi = {
  getConfig: () => ipcRenderer.invoke('config:get') as Promise<AgentConfig>,
  saveConfig: (cfg: AgentConfig) => ipcRenderer.invoke('config:save', plain(cfg)),
  start: () => ipcRenderer.invoke('agent:start') as Promise<AgentStatus>,
  stop: () => ipcRenderer.invoke('agent:stop') as Promise<AgentStatus>,
  status: () => ipcRenderer.invoke('agent:status') as Promise<AgentStatus>,
  testCli: (skipProbe: boolean) => ipcRenderer.invoke('cli:test', skipProbe) as Promise<CliTestResult>,
  pickDirectory: (title: string) => ipcRenderer.invoke('dialog:pickDirectory', title) as Promise<string | null>,
  openExternal: (url: string) => ipcRenderer.invoke('shell:openExternal', url) as Promise<void>,
  quit: () => ipcRenderer.invoke('app:quit') as Promise<void>,
  onEvent: (cb: (name: CoreEventName, data: unknown) => void) => {
    const handler = (_e: unknown, name: CoreEventName, data: unknown): void => cb(name, data)
    ipcRenderer.on('core:event', handler)
    // 返回取消订阅函数，组件卸载时务必调用，避免监听器泄漏。
    return () => ipcRenderer.removeListener('core:event', handler)
  },
  isCoreReady: () => true
}

contextBridge.exposeInMainWorld('codeporter', api)
