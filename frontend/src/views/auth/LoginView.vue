<script setup lang="ts">
import BrandLogo from '../../components/base/BrandLogo.vue'
import { computed, nextTick, onMounted, onScopeDispose, reactive, ref } from 'vue'
import ThemeSwitcher from '../../components/base/ThemeSwitcher.vue'
import { isNavigationFailure, useRoute, useRouter } from 'vue-router'
import { CircleAlert, Eye, EyeOff, Info, Lock, User } from 'lucide-vue-next'
import { ApiRequestError } from '../../utils/request'
import { useUserStore } from '../../stores/user'

const router = useRouter()
const route = useRoute()
const userStore = useUserStore()

const form = reactive({ username: '', password: '' })
const loading = ref(false)
const remember = ref(true)
const passwordVisible = ref(false)
const healthOk = ref(false)
const healthText = ref('检测中...')
const challenge = ref(''), factor = ref(''), useRecovery = ref(false)
const challengeFromSSO = ref(false)
const ssoConfigured = ref(false)
const fieldErrors = reactive({ username: '', password: '', factor: '' })
const authError = ref('')
const succeeded = ref(false)
const showResetHelp = ref(false)
const busy = computed(() => loading.value || succeeded.value)
const returnPath = computed(() => {
  const value = route.query.redirect
  if (typeof value !== 'string' || !value.startsWith('/') || value.startsWith('//')) return ''
  const current = new URL(window.location.href)
  try {
    const target = new URL(value, current)
    if (target.origin !== current.origin || target.pathname.startsWith('//')) return ''
    return target.pathname + target.search + target.hash
  } catch {
    return ''
  }
})
const ssoStartURL = computed(() => {
  const start = new URL('/api/v1/auth/sso/start', window.location.href)
  if (returnPath.value) start.searchParams.set('redirect', returnPath.value)
  return start.pathname + start.search
})
let active = true
function clearError(field: keyof typeof fieldErrors) {
  fieldErrors[field] = ''
  authError.value = ''
}
function resetFactor(){challenge.value='';factor.value='';form.password='';useRecovery.value=false;challengeFromSSO.value=false;fieldErrors.factor='';authError.value=''}
onScopeDispose(() => { active = false; resetFactor() })

async function focusInput(id: string) {
  await nextTick()
  if (active) document.getElementById(id)?.focus()
}

async function showFactor(token: string, fromSSO = false) {
  challengeFromSSO.value = fromSSO
  challenge.value = token
  loading.value = false
  await focusInput('mfa-factor')
}

async function returnToPassword() {
  resetFactor()
  await focusInput('username')
}

function clearSSOCallbackMarker() {
  const url = new URL(window.location.href)
  url.searchParams.delete('sso')
  window.history.replaceState(window.history.state, '', url.pathname + url.search + url.hash)
}

function errorMessage(error: unknown, password = false) {
  if (password && error instanceof ApiRequestError && error.status === 401) return '用户名或密码错误，请重新输入'
  return error instanceof Error ? error.message : '登录暂时不可用，请稍后重试'
}

async function enterWorkspace() {
  if (loading.value) return
  loading.value = true
  authError.value = ''
  try {
    await nextTick()
    const failure = await router.replace(returnPath.value || '/')
    if (isNavigationFailure(failure)) throw new Error('页面跳转未完成')
  } catch {
    if (active) authError.value = '已登录，但页面跳转未完成。请点击“进入工作台”重试。'
  } finally { if (active) loading.value = false }
}

async function loggedIn(){
  if (!remember.value) {
    localStorage.removeItem('dbhub_access_token');localStorage.removeItem('dbhub_refresh_token');localStorage.removeItem('dbhub_user')
  }
  succeeded.value = true
  loading.value = false
  await enterWorkspace()
}
async function handleFactor(){
  if(!challenge.value||busy.value)return
  authError.value=''
  fieldErrors.factor = factor.value ? '' : `请输入${useRecovery.value ? '恢复码' : '动态验证码'}`
  if (fieldErrors.factor) { await focusInput('mfa-factor'); return }
  loading.value=true
  try {
    await userStore.verifyMFA(challenge.value,factor.value,useRecovery.value,true)
    if (!active) return
    factor.value=''
    await loggedIn()
  } catch (error) {
    if (!active) return
    factor.value=''
    authError.value=errorMessage(error)
  } finally { if (active) loading.value=false }
  if (authError.value && !succeeded.value) await focusInput('mfa-factor')
}

async function handleLogin(event?: SubmitEvent) {
  if (busy.value) return
  authError.value = ''
  fieldErrors.username = form.username ? '' : '请输入用户名'
  fieldErrors.password = form.password ? '' : '请输入密码'
  if (fieldErrors.username || fieldErrors.password) {
    await focusInput(fieldErrors.username ? 'username' : 'password')
    return
  }
  const activeControl = document.activeElement as HTMLElement | null
  const submittedForm = event?.currentTarget as HTMLFormElement | null
  const focusOrigin = activeControl && submittedForm?.contains(activeControl) ? activeControl : null
  loading.value = true
  try {
    const result=await userStore.login(form.username, form.password, true)
    if (!active) return
    form.password=''
    if ('mfa_required' in result) { await showFactor(result.challenge_token); return }
    await loggedIn()
  } catch (error) {
    if (active) authError.value = errorMessage(error, true)
  } finally {
    if (active) loading.value = false
  }
  if (focusOrigin && authError.value && !succeeded.value) {
    await nextTick()
    if (active && document.activeElement === document.body) focusOrigin.focus()
  }
}

onMounted(async () => {
  if (route.query.sso === 'failed') {
    authError.value = '单点登录未完成。请检查身份关联，或联系平台管理员。'
    clearSSOCallbackMarker()
  }
  if(route.query.sso==='complete'){
    loading.value=true
    try{
      const result = await userStore.completeSSO().finally(() => {
        if (active) clearSSOCallbackMarker()
      })
      if (!active) return
      if ('mfa_required' in result) await showFactor(result.challenge_token, true)
      else await loggedIn()
    } catch (error) {
      if (active) authError.value = error instanceof Error ? error.message : '单点登录交接失败，请重新开始'
    } finally { if (active) loading.value = false }
  }
  try{const response=await fetch('/api/v1/auth/sso'),packet=await response.json();ssoConfigured.value=response.ok&&packet.data?.configured===true}catch{/* Password login remains available when discovery status cannot be read. */}
  try {
    const resp = await fetch('/api/health')
    const body = await resp.json()
    healthOk.value = resp.ok && body.code === 0
    healthText.value = healthOk.value ? `正常 · ${body.data.version}` : '异常'
  } catch {
    healthOk.value = false
    healthText.value = '离线'
  }
})
</script>

<template>
  <main
    class="editorial-login relative min-h-screen flex items-center justify-center px-4 py-10"
  >
    <div class="absolute top-4 right-4 z-20"><ThemeSwitcher /></div>

    <section
      class="glass-card w-full max-w-md sm:p-10 relative z-10 app-page-enter ui-card-padding"
      aria-label="登录表单"
    >
      <!-- 品牌区 -->
      <header class="flex flex-col items-center text-center mb-8">
        <p class="editorial-wordmark mb-6"><BrandLogo /> <span class="text-xs font-sans font-normal text-muted ml-3">数据编辑室</span></p>
        <h1 class="text-2xl font-bold tracking-wide">登录</h1>
        <p class="text-sm text-muted mt-1.5">登录以管理您的数据库资产</p>
      </header>

      <p class="sr-only" role="status">{{ succeeded ? '登录成功，正在进入…' : loading ? '正在验证身份…' : '' }}</p>
      <form v-if="!challenge" class="space-y-5" novalidate :aria-busy="loading" @submit.prevent="handleLogin">
        <div>
          <label for="username" class="block text-xs font-medium text-muted mb-1.5">用户名</label>
          <div class="relative">
            <User class="w-4 h-4 text-subtle absolute left-3.5 top-1/2 -translate-y-1/2" />
            <input
              id="username"
              v-model.trim="form.username"
              type="text"
              autocomplete="username"
              class="glass-input pl-10"
              placeholder="请输入用户名"
              :disabled="busy"
              :aria-invalid="!!fieldErrors.username"
              :aria-describedby="fieldErrors.username ? 'username-error' : undefined"
              @input="clearError('username')"
            />
          </div>
          <p v-if="fieldErrors.username" id="username-error" class="editorial-field-error">{{ fieldErrors.username }}</p>
        </div>

        <div>
          <label for="password" class="block text-xs font-medium text-muted mb-1.5">密码</label>
          <div class="relative">
            <Lock class="w-4 h-4 text-subtle absolute left-3.5 top-1/2 -translate-y-1/2" />
            <input
              id="password"
              v-model="form.password"
              :type="passwordVisible ? 'text' : 'password'"
              autocomplete="current-password"
              class="glass-input pl-10 pr-10"
              placeholder="请输入密码"
              :disabled="busy"
              :aria-invalid="!!fieldErrors.password"
              :aria-describedby="fieldErrors.password ? 'password-error' : undefined"
              @input="clearError('password')"
            />
            <button
              type="button"
              class="absolute right-3 top-1/2 -translate-y-1/2 text-subtle hover:text-content transition-colors ui-control-target"
              :aria-label="passwordVisible ? '隐藏密码' : '显示密码'"
              @click="passwordVisible = !passwordVisible"
            >
              <Eye v-if="!passwordVisible" class="w-4 h-4" />
              <EyeOff v-else class="w-4 h-4" />
            </button>
          </div>
          <p v-if="fieldErrors.password" id="password-error" class="editorial-field-error">{{ fieldErrors.password }}</p>
        </div>

        <div class="flex items-center justify-between text-sm">
          <label class="flex items-center gap-2 text-muted cursor-pointer select-none">
            <input v-model="remember" type="checkbox" class="accent-accent w-3.5 h-3.5" :disabled="busy" />
            记住我
          </label>
          <button type="button" class="text-muted hover:text-content transition-colors ui-control-target" :aria-expanded="showResetHelp" aria-controls="reset-help" @click="showResetHelp = !showResetHelp">
            忘记密码？
          </button>
        </div>

        <p v-if="showResetHelp" id="reset-help" class="editorial-form-feedback" role="status"><Info aria-hidden="true" />请联系管理员重置密码。</p>
        <p v-if="authError" role="alert" class="editorial-form-feedback editorial-form-feedback--error"><CircleAlert aria-hidden="true" />{{ authError }}</p>
        <button v-if="succeeded && authError" type="button" class="liquid-button w-full ui-control-target" :disabled="loading" @click="enterWorkspace">进入工作台</button>
        <button v-else type="submit" class="liquid-button w-full text-base ui-control-target" :disabled="busy">
          <span v-if="succeeded">登录成功，正在进入…</span>
          <span v-else-if="!loading">登 录</span>
          <span v-else class="inline-flex items-center gap-2">
            <span class="w-4 h-4 border-2 border-stroke border-t-content rounded-full animate-spin" />
            登录中...
          </span>
        </button>
      </form>
      <a v-if="ssoConfigured&&!challenge&&!busy" :href="ssoStartURL" class="ghost-button w-full flex justify-center mt-4">企业单点登录</a>

      <form v-if="challenge" class="space-y-5" novalidate aria-label="两步验证" :aria-busy="loading" @submit.prevent="handleFactor">
        <p class="text-sm text-muted">{{ challengeFromSSO ? '单点登录已验证，请完成两步验证。' : '密码已验证，请完成两步验证。' }}</p>
        <label class="flex gap-2 text-sm"><input v-model="useRecovery" type="checkbox" :disabled="busy" @change="factor=''; clearError('factor')" />使用一次性恢复码</label>
        <div>
          <label for="mfa-factor" class="block text-xs mb-2">{{ useRecovery ? '恢复码' : '动态验证码' }}</label>
          <input id="mfa-factor" v-model.trim="factor" :type="useRecovery?'password':'text'" :inputmode="useRecovery?'text':'numeric'" autocomplete="one-time-code" :maxlength="useRecovery?32:6" class="glass-input" :disabled="busy" :aria-invalid="!!fieldErrors.factor" :aria-describedby="fieldErrors.factor ? 'factor-error' : authError && !succeeded ? 'mfa-auth-error' : undefined" @input="clearError('factor')" />
          <p v-if="fieldErrors.factor" id="factor-error" class="editorial-field-error">{{ fieldErrors.factor }}</p>
        </div>
        <p v-if="authError" id="mfa-auth-error" role="alert" class="editorial-form-feedback editorial-form-feedback--error"><CircleAlert aria-hidden="true" />{{ authError }}</p>
        <button v-if="succeeded && authError" type="button" class="liquid-button w-full ui-control-target" :disabled="loading" @click="enterWorkspace">进入工作台</button>
        <button v-else type="submit" class="liquid-button w-full ui-control-target" :disabled="busy">{{ succeeded ? '登录成功，正在进入…' : loading ? '验证中…' : '验证并登录' }}</button>
        <button type="button" class="text-sm text-muted ui-control-target" :disabled="busy" @click="returnToPassword">返回密码登录</button>
      </form>

      <!-- 开发期默认账号提示 -->
      <p class="mt-6 text-center text-xs text-muted leading-relaxed">
        管理员账号由部署配置管理，请联系环境维护者获取登录信息
      </p>

      <!-- 后端状态 -->
      <footer class="mt-6 pt-5 border-t border-stroke flex items-center justify-center gap-2 text-xs">
        <span
          class="w-2 h-2 rounded-full"
          :class="healthOk ? 'bg-success' : 'bg-warning'"
        />
        <span class="text-subtle">后端状态：</span>
        <span :class="healthOk ? 'text-success' : 'text-warning'">{{ healthText }}</span>
      </footer>
    </section>
  </main>
</template>
