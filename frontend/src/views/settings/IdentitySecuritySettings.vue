<script setup lang="ts">
import { nextTick, onMounted, onScopeDispose, ref } from 'vue'
import LoadingState from '../../components/base/LoadingState.vue'
import { useUserStore } from '../../stores/user'
import type { LoginResult } from '../../types/api'
import request from '../../utils/request'

type Status = { enabled: boolean; recovery_codes_remaining: number }
type Setup = { setup_id: string; qr_image: string; expires_at: string }

const props = withDefaults(defineProps<{ heading?: 'h2' | 'h3' }>(), { heading: 'h2' })
const store = useUserStore()
const status = ref<Status>()
const setup = ref<Setup>()
const password = ref('')
const code = ref('')
const recovery = ref(false)
const busy = ref(false)
const loading = ref(false)
const readError = ref('')
const actionError = ref('')
const feedback = ref('')
const statusHeading = ref<HTMLElement>()
const passwordField = ref<HTMLInputElement>()
const codeField = ref<HTMLInputElement>()
const controller = new AbortController()
const requestOptions = { signal: controller.signal, silentError: true }
let alive = true

function clear() {
  password.value = ''
  code.value = ''
  setup.value = undefined
  recovery.value = false
}

function clearActionFeedback() {
  actionError.value = ''
  feedback.value = ''
}

async function focus(field: typeof passwordField) {
  await nextTick()
  if (alive) field.value?.focus()
}

async function load() {
  if (loading.value) return
  loading.value = true
  readError.value = ''
  try {
    const data = await request.get<unknown, Status>('/auth/mfa', requestOptions)
    if (alive) status.value = data
  } catch (error) {
    if (alive) {
      status.value = undefined
      readError.value = error instanceof Error ? error.message : '身份安全状态暂不可用。'
      clear()
    }
  } finally {
    if (alive) loading.value = false
  }
}

function retryStatus(event: MouseEvent) {
  if (loading.value || busy.value) return
  if (event.currentTarget === document.activeElement) statusHeading.value?.focus()
  void load()
}

async function work(action: () => Promise<void>, failureField: typeof passwordField) {
  if (busy.value || loading.value) return
  busy.value = true
  clearActionFeedback()
  let failed = false
  try {
    await action()
  } catch (error) {
    if (alive) {
      actionError.value = error instanceof Error ? error.message : '安全设置操作失败。'
      password.value = ''
      code.value = ''
      failed = true
    }
  } finally {
    if (alive) {
      busy.value = false
      if (failed) await focus(failureField)
    }
  }
}

async function start() {
  if (!password.value) return
  await work(async () => {
    const data = await request.post<unknown, Setup>('/auth/mfa/setup', { password: password.value }, requestOptions)
    if (!alive) return
    setup.value = data
    password.value = ''
  }, passwordField)
  if (setup.value) await focus(codeField)
}

async function cancel() {
  clear()
  clearActionFeedback()
  await focus(passwordField)
}

function downloadRecoveryCodes(codes: string[]) {
  const blob = new Blob([
    '知数 一次性恢复码 — ' + store.username + '\n每个码只能使用一次，请离线保存。\n\n' + codes.join('\n') + '\n',
  ], { type: 'text/plain;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = 'zhishu-recovery-codes.txt'
  link.click()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
}

async function refreshSessionContext() {
  await Promise.all([
    load(),
    store.loadWorkspaces().then(() => store.loadPermissions()).catch(() => {
      // Workspace permission failure is presented by the store on the profile.
    }),
  ])
}

async function enable() {
  if (!setup.value || !/^\d{6}$/.test(code.value)) return
  await work(async () => {
    const result = await request.post<unknown, LoginResult & { recovery_codes: string[] }>(
      '/auth/mfa/enable', { setup_id: setup.value!.setup_id, code: code.value }, requestOptions,
    )
    if (!alive) return
    store.acceptSession(result)
    try {
      downloadRecoveryCodes(result.recovery_codes)
      feedback.value = '两步验证已启用，已发起恢复码文件下载。'
    } catch {
      feedback.value = '两步验证已启用，恢复码文件下载失败。请联系管理员获取恢复帮助。'
    } finally {
      result.recovery_codes.fill('')
      clear()
    }
    await refreshSessionContext()
  }, codeField)
}

async function disable() {
  if (!password.value || !code.value || (!recovery.value && !/^\d{6}$/.test(code.value))) return
  await work(async () => {
    const result = await request.post<unknown, LoginResult>('/auth/mfa/disable', {
      password: password.value, ...(recovery.value ? { recovery_code: code.value } : { code: code.value }),
    }, requestOptions)
    if (!alive) return
    store.acceptSession(result)
    clear()
    feedback.value = '两步验证已关闭，其他会话已失效。'
    await refreshSessionContext()
  }, passwordField)
}

onMounted(load)
onScopeDispose(() => {
  alive = false
  controller.abort()
  clear()
})
</script>

<template>
  <section class="space-y-3 border-t border-divider pt-3" :class="{ 'glass-panel ui-card-padding': props.heading === 'h2' }"
    aria-label="两步验证设置" :aria-busy="busy || loading">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <component :is="heading" ref="statusHeading" class="ui-focus-target font-semibold" tabindex="-1">两步验证（MFA）</component>
      <span v-if="status && !loading" class="text-sm" :class="status.enabled ? 'text-success' : 'text-muted'">
        {{ status.enabled ? '已启用' : '未启用' }}
      </span>
    </div>
    <p class="text-sm text-muted">使用身份验证器生成动态验证码。启用或关闭后，其他已登录会话会失效。</p>
    <p v-if="feedback" role="status" class="text-sm text-content">{{ feedback }}</p>
    <p v-if="actionError" id="mfa-action-error" role="alert" class="text-sm text-danger">{{ actionError }}</p>
    <LoadingState v-if="loading" variant="inline" label="正在读取身份安全配置…" />
    <div v-else-if="readError" class="flex flex-wrap items-center gap-2">
      <p role="alert" class="text-sm text-danger">{{ readError }}</p>
      <button type="button" class="ghost-button ui-control-target" :disabled="busy" @click="retryStatus">重新读取状态</button>
    </div>
    <template v-else-if="status && !status.enabled">
      <form v-if="!setup" class="space-y-3" @submit.prevent="start" @input="clearActionFeedback">
        <label class="block text-sm">验证当前密码后绑定身份验证器
          <input ref="passwordField" v-model="password" type="password" autocomplete="current-password" class="glass-input block w-full mt-1"
            :disabled="busy" :aria-describedby="actionError ? 'mfa-action-error' : undefined" />
        </label>
        <button class="liquid-button ui-control-target" :disabled="busy || !password">开始绑定</button>
      </form>
      <form v-else class="space-y-3" @submit.prevent="enable" @input="clearActionFeedback">
        <p class="text-sm">请用身份验证器扫描二维码，在 {{ new Date(setup.expires_at).toLocaleTimeString() }} 前输入验证码。</p>
        <img :src="setup.qr_image" alt="身份验证器绑定二维码" width="256" height="256" class="max-w-full" />
        <label class="block text-sm">身份验证器动态验证码
          <input ref="codeField" v-model.trim="code" inputmode="numeric" maxlength="6" autocomplete="one-time-code"
            class="glass-input block w-full mt-1" :disabled="busy" :aria-describedby="actionError ? 'mfa-action-error' : undefined" />
        </label>
        <p class="text-sm text-muted">启用后请离线保存恢复码文件。每个恢复码只能使用一次。</p>
        <div class="flex flex-wrap gap-2">
          <button class="liquid-button ui-control-target" :disabled="busy || !/^\d{6}$/.test(code)">确认启用并下载恢复码</button>
          <button type="button" class="ghost-button ui-control-target" :disabled="busy" @click="cancel">取消绑定</button>
        </div>
      </form>
    </template>
    <form v-else-if="status" class="space-y-3" @submit.prevent="disable" @input="clearActionFeedback">
      <p class="text-sm text-muted">剩余一次性恢复码：{{ status.recovery_codes_remaining }}</p>
      <label class="block text-sm">当前密码
        <input ref="passwordField" v-model="password" type="password" autocomplete="current-password" class="glass-input block w-full mt-1"
          :disabled="busy" :aria-describedby="actionError ? 'mfa-action-error' : undefined" />
      </label>
      <label class="flex items-center gap-2 text-sm">
        <input v-model="recovery" type="checkbox" :disabled="busy" @change="code = ''" />使用一次性恢复码关闭
      </label>
      <label class="block text-sm">{{ recovery ? '恢复码' : '动态验证码' }}
        <input ref="codeField" v-model.trim="code" :type="recovery ? 'password' : 'text'" :inputmode="recovery ? 'text' : 'numeric'"
          :maxlength="recovery ? 32 : 6" autocomplete="one-time-code" class="glass-input block w-full mt-1"
          :disabled="busy" :aria-describedby="actionError ? 'mfa-action-error' : undefined" />
      </label>
      <button class="liquid-button ui-control-target" :disabled="busy || !password || !code || (!recovery && !/^\d{6}$/.test(code))">
        验证并关闭两步验证
      </button>
    </form>
  </section>
</template>
