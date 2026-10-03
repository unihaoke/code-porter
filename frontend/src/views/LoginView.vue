<script setup lang="ts">
import { ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'

const auth = useAuthStore()
const router = useRouter()
const route = useRoute()

const token = ref('')
const rememberHint = ref(false)

async function submit() {
  rememberHint.value = true
  const ok = await auth.login(token.value)
  if (ok) {
    const redirect = typeof route.query.redirect === 'string' ? route.query.redirect : '/'
    router.push(redirect)
  }
}
</script>

<template>
  <div class="wrap">
    <div class="card login">
      <div class="head">
        <span class="logo">CP</span>
        <h2>CodePorter 控制台</h2>
      </div>
      <p class="muted">
        输入网关配置中的 <code class="mono">security.admin_token</code> 登录。
        该令牌同时是网页对话、机器人管理的调用凭据。
      </p>

      <div v-if="auth.error" class="alert error">{{ auth.error }}</div>

      <form @submit.prevent="submit">
        <div class="field">
          <label for="token">管理端令牌</label>
          <input
            id="token"
            v-model="token"
            type="password"
            placeholder="change-me-admin-token"
            autocomplete="current-password"
          />
        </div>
        <button class="btn primary" style="width: 100%; justify-content: center" :disabled="auth.loading">
          {{ auth.loading ? '登录中…' : '登录' }}
        </button>
      </form>

      <p v-if="rememberHint && !auth.authed" class="muted" style="margin-top: 14px; font-size: 12px">
        令牌会保存在浏览器本地，退出登录即清除。
      </p>
    </div>
  </div>
</template>

<style scoped>
.wrap {
  min-height: 100%;
  display: grid;
  place-items: center;
  padding: 40px 20px;
}

.login {
  width: 100%;
  max-width: 380px;
}

.head {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 10px;
}

.head h2 {
  font-size: 18px;
}

.logo {
  width: 34px;
  height: 34px;
  border-radius: 9px;
  background: var(--brand);
  color: #fff;
  display: grid;
  place-items: center;
  font-weight: 700;
  font-size: 14px;
}

p.muted {
  font-size: 13px;
  margin: 0 0 18px;
}
</style>
