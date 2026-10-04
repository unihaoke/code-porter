<script setup lang="ts">
import { computed } from 'vue'

import { useStore } from '../store'
import type { ToolConfig } from '@shared/types'

const emit = defineEmits<{ notify: [string, boolean?] }>()

const { state, saveConfig, pickWorkDir, testCli, openConfigDir } = useStore()

const cfg = computed(() => state.config)
/** 配置文件的绝对路径（Go 核心在 status 事件里回报）。 */
const configPath = computed(() => state.status?.config_path ?? '')
const logLevels = ['debug', 'info', 'warn', 'error']

/** 可配置的工具清单。放在脚本里而非模板内联对象——v-for 遍历对象时
 *  第一个参数是「值」、第二个才是「键」，很容易写反而导致渲染报错。 */
const TOOLS = [
  { key: 'claude_code', label: 'Claude Code' },
  { key: 'codex', label: 'Codex' },
  { key: 'trae', label: 'Trae' },
  { key: 'codebuddy', label: 'CodeBuddy' }
] as const

async function onSave(): Promise<void> {
  // 成功/失败的提示（含后台原始错误信息）由 store 统一弹 toast，这里不再重复弹。
  await saveConfig()
}

async function onTestNoProbe(): Promise<void> {
  const r = await testCli(true)
  if (r) emit('notify', `安装检测完成：可用 ${r.ok} · 未安装 ${r.missing}`)
}

/** 工具清单里每一项对应的配置对象（可能因配置缺字段而为空）。 */
function toolCfg(key: string): ToolConfig | undefined {
  return (cfg.value?.mcp as Record<string, ToolConfig | undefined>)?.[key]
}

/** 切换某个工具的启用状态。 */
function toggleTool(key: string): void {
  const t = toolCfg(key)
  if (t) t.enabled = !t.enabled
}
</script>

<template>
  <div v-if="cfg" class="page-head page-head--col">
    <h1>配置</h1>
    <p>
      修改后点「保存配置」写入下面的配置文件；已在运行的任务需重启代理才生效。
    </p>
    <div v-if="configPath" class="cfg-path">
      <span class="cfg-path__label">配置文件：</span>
      <code class="cfg-path__value" :title="configPath">{{ configPath }}</code>
      <button class="btn btn--sm" @click="openConfigDir">打开所在文件夹</button>
    </div>
  </div>
  <div v-else class="empty">正在加载配置…</div>

  <div v-if="cfg">
    <!-- 网关连接 -->
    <div class="card">
      <div class="card__head"><span class="card__title">网关连接</span></div>
      <div class="grid grid--2">
        <label class="field">
          <span class="field__label">网关地址</span>
          <input v-model="cfg.gateway.addr" class="input input--mono" placeholder="https://your-gateway" />
          <span class="field__hint">本地开发一般是 http://127.0.0.1:9022</span>
        </label>
        <label class="field">
          <span class="field__label">请求超时</span>
          <input v-model="cfg.gateway.timeout" class="input input--mono" />
        </label>
        <label class="field">
          <span class="field__label">实例 ID（自动生成，无需填写）</span>
          <input v-model="cfg.agent.id" class="input input--mono" placeholder="首次启动自动生成" readonly />
        </label>
        <label class="field">
          <span class="field__label">机器名</span>
          <input v-model="cfg.agent.name" class="input input--mono" placeholder="留空取本机主机名" />
        </label>
        <label class="field">
          <span class="field__label">连接秘钥</span>
          <input v-model="cfg.agent.key" class="input input--mono" type="password" placeholder="cp_..." />
          <span class="field__hint">在网关控制台「秘钥」页创建（含 agent 权限）；建议用环境变量 AGENT_KEY 注入</span>
        </label>
      </div>
      <div class="divider" />
      <label class="switch">
        <input v-model="cfg.gateway.insecure_tls" type="checkbox" />
        <span class="switch__track" />
        <span>跳过 TLS 证书校验（自签证书场景）</span>
      </label>
    </div>

    <!-- 本地 AI -->
    <div class="card">
      <div class="card__head">
        <span class="card__title">本地 AI</span>
        <span class="card__hint">CLI 模式下通常无需填写密钥，走本机登录态</span>
      </div>

      <label class="field" style="margin-bottom: 14px">
        <span class="field__label">工作目录（AI CLI 在此目录下工作）</span>
        <div class="input-row">
          <input
            v-model="cfg.mcp.work_dir"
            class="input input--mono"
            placeholder="留空 = 客户端 exe 所在目录"
          />
          <button class="btn" @click="pickWorkDir">浏览…</button>
          <button class="btn" @click="cfg.mcp.work_dir = ''" title="清空并回退默认">默认</button>
        </div>
        <span class="field__hint">
          Claude Code / Codex 会在此目录读写文件、执行命令，决定它们能看到哪些项目代码。
        </span>
      </label>

      <div class="divider" />

      <div class="grid grid--2" style="gap: 10px">
        <div v-for="t in TOOLS" :key="t.key" class="tool">
          <label class="switch">
            <input
              :checked="cfg.mcp[t.key]?.enabled ?? false"
              type="checkbox"
              @change="toggleTool(t.key)"
            />
            <span class="switch__track" />
          </label>
          <div style="min-width: 0">
            <div class="tool__name">{{ t.label }}</div>
            <div class="tool__meta">{{ cfg.mcp[t.key]?.command || '未配置' }}</div>
          </div>
        </div>
      </div>

      <div class="divider" />

      <div class="grid grid--2">
        <label class="field">
          <span class="field__label">Anthropic API Key（可选）</span>
          <input v-model="cfg.secrets.anthropic_api_key" class="input input--mono" type="password" />
        </label>
        <label class="field">
          <span class="field__label">OpenAI API Key（可选）</span>
          <input v-model="cfg.secrets.openai_api_key" class="input input--mono" type="password" />
        </label>
      </div>
      <p class="field__hint" style="margin-top: 8px">
        留空即可。Claude Code / Codex 默认使用本机登录态（订阅），只有使用
        <code>--bare</code> 或显式指定第三方 Key 时才需要填写。
      </p>

      <div class="row" style="margin-top: 14px">
        <button class="btn" :disabled="state.testing" @click="onTestNoProbe">
          {{ state.testing ? '检测中…' : '仅检测安装' }}
        </button>
      </div>
    </div>

    <!-- 运行参数 -->
    <div class="card">
      <div class="card__head"><span class="card__title">运行参数</span></div>
      <div class="grid grid--3">
        <label class="field">
          <span class="field__label">最大并发任务数</span>
          <input v-model.number="cfg.worker_pool.max_concurrency" class="input" type="number" min="1" max="16" />
        </label>
        <label class="field">
          <span class="field__label">本地队列长度</span>
          <input v-model.number="cfg.worker_pool.queue_size" class="input" type="number" min="1" max="64" />
        </label>
        <label class="field">
          <span class="field__label">日志级别</span>
          <select v-model="cfg.log.level" class="select">
            <option v-for="l in logLevels" :key="l" :value="l">{{ l }}</option>
          </select>
        </label>
      </div>
      <div class="divider" />
      <label class="switch">
        <input v-model="cfg.direct.enabled" type="checkbox" />
        <span class="switch__track" />
        <span>启用 SSE 直连模式（低延迟交互式对话）</span>
      </label>
    </div>

    <div class="row" style="margin-top: 16px">
      <button class="btn btn--primary" :disabled="state.busy" @click="onSave">
        {{ state.busy ? '保存中…' : '保存配置' }}
      </button>
      <button class="btn" @click="emit('notify', '如未生效，请停止代理后重新启动')">需要重启代理</button>
    </div>
  </div>
</template>

<style scoped>
/* 配置页头部改为纵向排列，好放下完整配置路径行。 */
.page-head--col {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.cfg-path {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-top: 6px;
  font-size: 12px;
  min-width: 0;
}

.cfg-path__label {
  color: var(--c-text-3);
  flex: 0 0 auto;
}

.cfg-path__value {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--c-text-2);
}
</style>
