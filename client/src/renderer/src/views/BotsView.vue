<script setup lang="ts">
import { computed } from 'vue'

import { useStore } from '../store'
import SaveBar from '../components/SaveBar.vue'

const emit = defineEmits<{ notify: [string, boolean?] }>()

const { state, saveConfig, saveAndRestartRunning, testBot, isDirty } = useStore()

const cfg = computed(() => state.config)
/** 飞书机器人的实时运行态（独立于代理）。 */
const botRuntime = computed(() => state.status?.bots?.feishu)
/** 企业微信机器人的实时运行态。 */
const wecomRuntime = computed(() => state.status?.bots?.wecom)
/** 任意渠道机器人正在启停 / 重启时，禁用保存按钮避免交叉操作。 */
const anyBotBusy = computed(() => Object.values(state.botBusy).some(Boolean))
/** 是否有正在运行、重启后会受影响的服务。 */
const anyServiceRunning = computed(
  () =>
    !!state.status?.running ||
    !!state.status?.tools_running ||
    Object.values(state.status?.bots ?? {}).some((b) => b.running)
)

const botsDirty = computed(() => isDirty(['bots']))
const saveBusy = computed(() => state.busy || anyBotBusy.value || state.toolsBusy)

/** 飞书机器人配置（store 已保证 bots.feishu 存在）。 */
const feishu = computed(() => cfg.value!.bots!.feishu)
/** 企业微信机器人配置（store 已保证 bots.wecom 存在）。 */
const wecom = computed(() => cfg.value!.bots!.wecom)

/**
 * 机器人「处理模型」下拉：始终保留自动与全部工具（避免已选值无法回显），
 * 但对当前未启用的工具标注「（未启用）」，提示用户先去配置页开启。
 */
const botModels = computed(() => {
  const mcp = cfg.value?.mcp as Record<string, { enabled?: boolean } | undefined>
  const enabled = (key: string): boolean => !!mcp[key]?.enabled
  return [
    { value: '', label: '自动（claude-code）' },
    { value: 'claude-code', label: `claude-code${enabled('claude_code') ? '' : '（未启用）'}` },
    { value: 'trae', label: `trae${enabled('trae') ? '' : '（未启用）'}` },
    { value: 'codebuddy', label: `codebuddy${enabled('codebuddy') ? '' : '（未启用）'}` },
    { value: 'codex', label: `codex${enabled('codex') ? '' : '（未启用）'}` }
  ]
})

async function onSave(): Promise<void> {
  await saveConfig()
}

/** 保存配置并重启正在运行的服务（代理 / 机器人 / 工具），让改动立即生效。 */
async function onSaveAndRestart(): Promise<void> {
  const err = await saveAndRestartRunning()
  if (err) emit('notify', `保存并重启失败：${err}`, true)
}

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
</script>

<template>
  <div v-if="cfg" class="page-head page-head--col">
    <h1>IM 机器人</h1>
    <p>
      机器人在本机直连平台长连接，消息本地处理、流式回复，不经过网关；各渠道与代理之间相互独立、可分别启停。
    </p>
  </div>
  <div v-else class="empty">正在加载配置…</div>

  <div v-if="cfg">
    <!-- 飞书机器人（客户端长连接，无需公网域名） -->
    <div class="card" :class="{ 'card--dim': !feishu.enabled }">
      <div class="card__head">
        <div class="card__titlewrap">
          <span class="card__title">飞书机器人</span>
          <span v-if="botsDirty" class="card__dirty">未保存</span>
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
          <span v-if="botsDirty" class="card__dirty">未保存</span>
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

    <SaveBar
      :dirty="botsDirty"
      :busy="saveBusy"
      :any-running="anyServiceRunning"
      @save="onSave"
      @restart="onSaveAndRestart"
    />
  </div>
</template>

<style scoped>
/* 配置页头部改为纵向排列，与设置页保持一致。 */
.page-head--col {
  display: flex;
  flex-direction: column;
  gap: 4px;
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
