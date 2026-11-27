<script setup lang="ts">
import { nextTick, onScopeDispose, ref } from 'vue'
import BrandLogo from '../../components/base/BrandLogo.vue'
import ThemeSwitcher from '../../components/base/ThemeSwitcher.vue'
import { redeemAccountCredential } from '../../api/platform-management'

const token = ref('')
const password = ref('')
const confirmPassword = ref('')
const tokenInput = ref<HTMLInputElement>()
const passwordInput = ref<HTMLInputElement>()
const confirmInput = ref<HTMLInputElement>()
const success = ref<HTMLElement>()
const formError = ref('')
const completed = ref(false)
const busy = ref(false)
const lifetime = new AbortController()

onScopeDispose(() => {
  lifetime.abort()
  token.value = ''
  password.value = ''
  confirmPassword.value = ''
})

function fail(message: string, control?: HTMLElement) {
  formError.value = message
  control?.focus()
}

async function submit() {
  if (busy.value || completed.value) return
  formError.value = ''
  if (!token.value.trim()) return fail('请粘贴一次性凭证。', tokenInput.value)
  if (Array.from(password.value).length < 15) return fail('密码至少需要 15 个字符。', passwordInput.value)
  if (new TextEncoder().encode(password.value).length > 4096) return fail('密码长度超出限制。', passwordInput.value)
  if (confirmPassword.value !== password.value) return fail('两次输入的密码不一致。', confirmInput.value)

  busy.value = true
  try {
    await redeemAccountCredential(token.value.trim(), password.value, lifetime.signal)
    if (lifetime.signal.aborted) return
    token.value = ''
    completed.value = true
    await nextTick()
    success.value?.focus()
  } catch (failure) {
    if (!lifetime.signal.aborted) {
      fail(failure instanceof Error ? failure.message : '暂时无法设置密码，请稍后重试。', tokenInput.value)
    }
  } finally {
    password.value = ''
    confirmPassword.value = ''
    if (!lifetime.signal.aborted) busy.value = false
  }
}
</script>

<template>
  <main class="editorial-login relative min-h-screen flex items-center justify-center px-4 py-10">
    <div class="absolute top-4 right-4"><ThemeSwitcher /></div>
    <section class="glass-card w-full max-w-md ui-card-padding space-y-5">
      <header class="space-y-3">
        <p class="editorial-wordmark"><BrandLogo /></p>
        <h1 class="text-2xl font-semibold">设置账号密码</h1>
        <p class="text-muted text-sm">使用超级管理员签发的一次性凭证激活账号或重置本地密码。</p>
      </header>

      <div v-if="completed" ref="success" class="space-y-4" tabindex="-1">
        <p role="status">密码已设置，请使用新密码登录。</p>
        <RouterLink class="liquid-button ui-control-target inline-flex" to="/login">前往登录</RouterLink>
      </div>
      <form v-else class="space-y-4" novalidate :aria-busy="busy" @submit.prevent="submit">
        <label class="block space-y-1 text-sm">
          <span>一次性凭证</span>
          <input ref="tokenInput" v-model.trim="token" class="glass-input ui-control-target w-full"
            type="password" autocomplete="off" maxlength="256" spellcheck="false" required
            :disabled="busy" />
        </label>
        <label class="block space-y-1 text-sm">
          <span>新密码</span>
          <input ref="passwordInput" v-model="password" class="glass-input ui-control-target w-full"
            type="password" autocomplete="new-password" aria-describedby="credential-password-help"
            required :disabled="busy" />
        </label>
        <p id="credential-password-help" class="text-sm text-muted">密码至少 15 个字符。</p>
        <label class="block space-y-1 text-sm">
          <span>确认新密码</span>
          <input ref="confirmInput" v-model="confirmPassword" class="glass-input ui-control-target w-full"
            type="password" autocomplete="new-password" required :disabled="busy" />
        </label>
        <p v-if="formError" class="editorial-field-error" role="alert">{{ formError }}</p>
        <div class="flex flex-wrap items-center gap-3">
          <button type="submit" class="liquid-button ui-control-target" :disabled="busy">
            {{ busy ? '正在确认凭证…' : '设置密码' }}
          </button>
          <RouterLink class="ghost-button ui-control-target" to="/login">返回登录</RouterLink>
        </div>
      </form>
    </section>
  </main>
</template>
