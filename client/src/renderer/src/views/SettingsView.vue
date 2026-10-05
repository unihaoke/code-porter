<script setup lang="ts">
import { computed } from 'vue'

import { useStore } from '../store'
import type { ToolConfig } from '@shared/types'

const emit = defineEmits<{ notify: [string, boolean?] }>()

const { state, saveConfig, saveAndRestartLocalAI, pickWorkDir, testCli, testBot, openConfigDir } = useStore()

const cfg = computed(() => state.config)
/** 配置文件的绝对路径（Go 核心在 status 事件里回报）。 */
const configPath = computed(() => state.status?.config_path ?? '')
/** 飞书机器人的实时运行态（独立于代理）。 */
const botRuntime = computed(() => state.status?.bots?.feishu)
/** 企业微信机器人的实时运行态。 */
const wecomRuntime = computed(() => state.status?.bots?.wecom)
/** 任意渠道机器人正在启停 / 重启时，禁用「保存并重启」避免交叉操作。 */
const anyBotBusy = computed(() => Object.values(state.botBusy).some(Boolean))
const logLevels = ['debug', 'info', 'warn', 'error']
const botModels = [
  { value: '', label: '自动（claude-code）' },
  { value: 'claude-code', label: 'claude-code' },
  { value: 'trae', label: 'trae' },
  { value: 'codebuddy', label: 'codebuddy' },
  { value: 'codex', label: 'codex' }
]

/** 可配置的工具清单。放在脚本里而非模板内联对象——v-for 遍历对象时
 *  第一个参数是「值」、第二个才是「键」，很容易写反而导致渲染报错。 */
const TOOLS = [
  { key: 'claude_code', label: 'Claude Code' },
  { key: 'codex', label: 'Codex' },
  { key: 'trae', label: 'Trae' },
  { key: 'codebuddy', label: 'CodeBuddy' }
] as const

/** 飞书机器人配置（store 已保证 bots.feishu 存在）。 */
const feishu = computed(() => cfg.value!.bots!.feishu)
/** 企业微信机器人配置（store 已保证 bots.wecom 存在）。 */
const wecom = computed(() => cfg.value!.bots!.wecom)

async function onSave(): Promise<void> {
  // 成功/失败的提示（含后台原始错误信息）由 store 统一弹 toast，这里不再重复弹。
  await saveConfig()
}

async function onTestNoProbe(): Promise<void> {
  const r = await testCli(true)
  if (r) emit('notify', `安装检测完成：可用 ${r.ok} · 未安装 ${r.missing}`)
}

/** 保存配置并重启正在运行的代理 / 机器人，让工作目录等本地 AI 改动立即生效。 */
async function onSaveAndRestart(): Promise<void> {
  const err = await saveAndRestartLocalAI()
  if (err) emit('notify', `保存并重启失败：${err}`, true)
}

/** 是否有正在运行、重启后会受影响的服务（仅用于按钮旁的提示）。 */
const anyServiceRunning = computed(
  () =>
    !!state.status?.running ||
    Object.values(state.status?.bots ?? {}).some((b) => b.running)
)

/** 测试指定渠道凭证（不建立长连接，校验已保存的配置）。 */
async function onTestBot(channel: 'feishu' | 'wecom'): Promise<void> {
  const r = await testBot(channel)
  if (!r) {
    emit('notify', '连接测试失败，详见运行日志', true)
  } else if (r.ok) {
    const mins = Math.max(1, Math.floor((r.expire_seconds ?? 0) / 60))
    const label = channel === 'feishu' ? '飞书' : '企业微信'
    if (r.tenant_key) {
      emit('notify', `连接正常：${r.detail}（租户 ${r.tenant_key}，凭证约 ${mins} 分钟有效）`)
    } else {
      emit('notify', `${label}连接正常：${r.detail}`)
    }
  } else {
    emit('notify', `连接失败：${r.detail}`, true)
  }
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
      修改后点「保存配置」写入下面的配置文件；代理与机器人的改动分别重启对应服务后生效。
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

      <div class="row row--wrap" style="margin-top: 14px; gap: 10px">
        <button class="btn" :disabled="state.testing" @click="onTestNoProbe">
          {{ state.testing ? '检测中…' : '仅检测安装' }}
        </button>
        <button
          class="btn btn--primary"
          :disabled="state.busy || anyBotBusy || state.testing"
          @click="onSaveAndRestart"
        >
          {{ state.busy || anyBotBusy ? '保存并重启中…' : '保存并重启本地 AI' }}
        </button>
        <span class="field__hint">
          工作目录、CLI 命令、工具开关、密钥等改动在服务启动时固化，
          <strong>仅保存不会影响正在运行的代理 / 机器人</strong>；点此按钮保存后会自动重启当前正在运行的服务{{ anyServiceRunning ? '' : '（当前无运行中的服务，将只保存）' }}。
        </span>
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

    <!-- 飞书机器人（客户端长连接，无需公网域名） -->
    <div class="card" :class="{ 'card--dim': !feishu.enabled }">
      <div class="card__head">
        <div class="card__titlewrap">
          <span class="card__title">飞书机器人</span>
          <span v-if="feishu.enabled" class="badge" :class="botRuntime?.running ? 'badge--on' : 'badge--off'">
            {{ botRuntime?.running ? '运行中' : '已启用 · 未运行' }}
          </span>
          <span v-else class="badge badge--off">未启用</span>
        </div>
        <span class="card__hint">长连接直连，无需公网域名 / 回调地址 / 加密配置</span>
      </div>

      <label class="switch">
        <input v-model="feishu.enabled" type="checkbox" />
        <span class="switch__track" />
        <span>启用飞书机器人（消息在本机直接处理并回复，不经过网关队列）</span>
      </label>

      <template v-if="feishu.enabled">
        <div class="divider" />
        <div class="grid grid--2">
          <label class="field">
            <span class="field__label">App ID</span>
            <input
              v-model="feishu.app_id"
              class="input input--mono"
              placeholder="cli_xxxxxxxxxxxxxxxx"
            />
          </label>
          <label class="field">
            <span class="field__label">App Secret</span>
            <input
              v-model="feishu.app_secret"
              class="input input--mono"
              type="password"
              placeholder="应用凭证页的 App Secret"
            />
            <span class="field__hint">建议改用环境变量 FEISHU_APP_SECRET 注入</span>
          </label>
          <label class="field">
            <span class="field__label">处理模型</span>
            <select v-model="feishu.model" class="select">
              <option v-for="m in botModels" :key="m.value" :value="m.value">{{ m.label }}</option>
            </select>
          </label>
        </div>

        <div class="divider" />
        <label class="switch">
          <input v-model="feishu.mention_only" type="checkbox" />
          <span class="switch__track" />
          <span>群聊中仅响应 @机器人 的消息（私聊始终响应）</span>
        </label>

        <label class="field" style="margin-top: 12px">
          <span class="field__label">附加系统提示（可选）</span>
          <textarea
            v-model="feishu.system_prompt"
            class="textarea"
            rows="2"
            placeholder="例如：回答请简洁，并给出可执行的修改建议"
          />
        </label>

        <div class="divider" />

        <!-- 凭证测试：不建立长连接，校验已保存配置 -->
        <div class="row row--wrap bot-test">
          <button class="btn" :disabled="state.botTesting.feishu" @click="onTestBot('feishu')">
            {{ state.botTesting.feishu ? '测试中…' : '测试连接' }}
          </button>
          <span class="field__hint">仅校验 App ID / Secret，不会启动长连接；校验的是<strong>已保存</strong>的配置，修改后请先保存。</span>
        </div>
        <div
          v-if="state.botTestResults.feishu"
          class="bot-test__result"
          :class="state.botTestResults.feishu.ok ? 'bot-test__result--ok' : 'bot-test__result--err'"
        >
          <template v-if="state.botTestResults.feishu.ok">
            ✓ {{ state.botTestResults.feishu.detail }}<template v-if="state.botTestResults.feishu.tenant_key">
              （租户 {{ state.botTestResults.feishu.tenant_key }}，token 有效期约
              {{ Math.max(1, Math.floor((state.botTestResults.feishu.expire_seconds ?? 0) / 60)) }} 分钟）
            </template>
          </template>
          <template v-else>✗ {{ state.botTestResults.feishu.detail }}</template>
        </div>

        <div class="notice">
          <strong>首次使用：</strong>在飞书开放平台的企业自建应用中开启「机器人」能力，
          事件订阅选择「使用长连接接收事件」并添加 <code>im.message.receive_v1</code>，
          开通发消息权限后发布版本。保存配置后到「概览」页单独<strong>启动/重启机器人服务</strong>即可，
          无需重启代理。处理消息时，AI 的思考过程与执行结果会实时更新到群里的同一张卡片。
        </div>
      </template>
    </div>

    <!-- 企业微信机器人（智能机器人 API 模式，WebSocket 长连接，无需公网域名） -->
    <div class="card" :class="{ 'card--dim': !wecom.enabled }">
      <div class="card__head">
        <div class="card__titlewrap">
          <span class="card__title">企业微信机器人</span>
          <span v-if="wecom.enabled" class="badge" :class="wecomRuntime?.running ? 'badge--on' : 'badge--off'">
            {{ wecomRuntime?.running ? '运行中' : '已启用 · 未运行' }}
          </span>
          <span v-else class="badge badge--off">未启用</span>
        </div>
        <span class="card__hint">智能机器人 API 模式 · 长连接直连，无需公网域名 / 回调地址</span>
      </div>

      <label class="switch">
        <input v-model="wecom.enabled" type="checkbox" />
        <span class="switch__track" />
        <span>启用企业微信机器人（消息在本机直接处理并回复，不经过网关队列）</span>
      </label>

      <template v-if="wecom.enabled">
        <div class="divider" />
        <div class="grid grid--2">
          <label class="field">
            <span class="field__label">Bot ID</span>
            <input
              v-model="wecom.bot_id"
              class="input input--mono"
              placeholder="智能机器人的 Bot ID"
            />
          </label>
          <label class="field">
            <span class="field__label">Secret</span>
            <input
              v-model="wecom.secret"
              class="input input--mono"
              type="password"
              placeholder="智能机器人的 Secret"
            />
            <span class="field__hint">建议改用环境变量 WECOM_BOT_SECRET 注入</span>
          </label>
          <label class="field">
            <span class="field__label">处理模型</span>
            <select v-model="wecom.model" class="select">
              <option v-for="m in botModels" :key="m.value" :value="m.value">{{ m.label }}</option>
            </select>
          </label>
        </div>

        <div class="divider" />
        <label class="switch">
          <input v-model="wecom.mention_only" type="checkbox" />
          <span class="switch__track" />
          <span>群聊中仅响应 @机器人 的消息（私聊始终响应）</span>
        </label>

        <label class="field" style="margin-top: 12px">
          <span class="field__label">附加系统提示（可选）</span>
          <textarea
            v-model="wecom.system_prompt"
            class="textarea"
            rows="2"
            placeholder="例如：回答请简洁，并给出可执行的修改建议"
          />
        </label>

        <div class="divider" />

        <!-- 凭证测试：不建立长连接，校验已保存配置 -->
        <div class="row row--wrap bot-test">
          <button class="btn" :disabled="state.botTesting.wecom" @click="onTestBot('wecom')">
            {{ state.botTesting.wecom ? '测试中…' : '测试连接' }}
          </button>
          <span class="field__hint">仅校验 Bot ID / Secret（订阅握手），不会保持长连接；校验的是<strong>已保存</strong>的配置，修改后请先保存。</span>
        </div>
        <div
          v-if="state.botTestResults.wecom"
          class="bot-test__result"
          :class="state.botTestResults.wecom.ok ? 'bot-test__result--ok' : 'bot-test__result--err'"
        >
          <template v-if="state.botTestResults.wecom.ok">
            ✓ {{ state.botTestResults.wecom.detail }}
          </template>
          <template v-else>✗ {{ state.botTestResults.wecom.detail }}</template>
        </div>

        <div class="notice">
          <strong>首次使用：</strong>在企业微信管理后台
          「安全与管理 → 管理工具 → 智能机器人」中创建机器人，启用 <strong>API 模式</strong>
          并选择「长连接」接入，复制 Bot ID 与 Secret 填入上方，并把机器人发布到可见范围。
          保存配置后到「概览」页单独启动该渠道即可，无需重启代理、与飞书渠道互不影响。
          处理消息时 AI 回复以<strong>流式消息</strong>实时更新：思考与工具过程显示在灰色引用区，
          正文生成后过程折叠为摘要；同一 Bot ID 全平台只允许一条长连接，多开会重复回复。
        </div>
      </template>
    </div>

    <div class="row" style="margin-top: 16px">
      <button class="btn btn--primary" :disabled="state.busy" @click="onSave">
        {{ state.busy ? '保存中…' : '保存配置' }}
      </button>
      <button
        class="btn"
        @click="emit('notify', '代理相关改动需在概览页重启代理；飞书 / 企业微信机器人改动只需在概览页重启对应渠道，各渠道与代理互不影响')"
      >
        改动如何生效？
      </button>
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

/* 测试连接一行：按钮与说明并排，窄屏自动换行。 */
.bot-test {
  gap: 10px;
  align-items: center;
}

/* 测试结果就地反馈，避免用户再去日志里翻。 */
.bot-test__result {
  margin-top: 10px;
  padding: 8px 12px;
  border-radius: 8px;
  font-size: 12.5px;
  line-height: 1.6;
  word-break: break-all;
}

.bot-test__result--ok {
  color: var(--c-ok);
  background: var(--c-ok-soft);
}

.bot-test__result--err {
  color: var(--c-err);
  background: var(--c-err-soft);
}
</style>
