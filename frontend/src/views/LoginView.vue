<script setup lang="ts">
import { ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'

const auth = useAuthStore()
const router = useRouter()
const route = useRoute()

const username = ref('')
const password = ref('')
const loading = ref(false)
const error = ref('')

async function submit() {
  if (!username.value || !password.value) {
    error.value = '请输入用户名和密码'
    return
  }
  loading.value = true
  error.value = ''
  try {
    await auth.login(username.value.trim(), password.value)
    const redirect = typeof route.query.redirect === 'string' ? route.query.redirect : '/'
    router.push(redirect)
  } catch (e) {
    error.value = e instanceof Error ? e.message : '登录失败'
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <div class="wrap">
    <div class="card login">
      <div class="head">
        <span class="logo">CP</span>
        <h2>CodePorter 控制台</h2>
        <p class="muted">多租户本地 AI 中继 · 账号登录</p>
      </div>
      <form @submit.prevent="submit">
        <div class="field">
          <label for="username">用户名</label>
          <input id="username" v-model="username" autocomplete="username" placeholder="admin" />
        </div>
        <div class="field">
          <label for="password">密码</label>
          <input
            id="password"
            v-model="password"
            type="password"
            autocomplete="current-password"
            placeholder="首次部署默认 admin123"
            @keyup.enter="submit"
          />
        </div>
        <div v-if="error" class="alert error">{{ error }}</div>
        <button class="btn primary" style="width: 100%; justify-content: center" :disabled="loading">
          {{ loading ? '登录中…' : '登录' }}
        </button>
      </form>
      <p class="muted tip">
        默认账号 admin / admin123，登录后请立即在右上角用户菜单修改密码。
      </p>
    </div>
  </div>
</template>

<style scoped>
.wrap {
  min-height: 100vh;
  display: grid;
  place-items: center;
  padding: 40px 20px;
  background: linear-gradient(135deg, #f6f8fc 0%, #eef2f8 100%);
}

.card.login {
  width: 100%;
  max-width: 380px;
  padding: 28px;
}

.head {
  text-align: center;
  margin-bottom: 20px;
}

.head h2 {
  margin: 10px 0 6px;
  font-size: 18px;
}

.logo {
  display: inline-grid;
  place-items: center;
  width: 40px;
  height: 40px;
  border-radius: 10px;
  background: var(--brand);
  color: #fff;
  font-weight: 800;
}

.tip {
  margin-top: 14px;
  font-size: 12px;
  text-align: center;
  line-height: 1.6;
}
</style>
