<script setup lang="ts">
import { onMounted, onScopeDispose, ref } from 'vue'
import { ElMessageBox } from 'element-plus'
import LoadingState from '../../components/base/LoadingState.vue'
import PageState from '../../components/base/PageState.vue'
import request from '../../utils/request'

type Binding = { subject: string; username: string; version: number; enabled: boolean; updated_at: string }
type BindingList = { issuer: string; items: Binding[] }
type BindingChange = { username: string; subject: string; expected_version: number; enabled: boolean }
const configured = ref<boolean>()
const data = ref<BindingList>()
const username = ref('')
const subject = ref('')
const loading = ref(true)
const saving = ref(false)
const readError = ref('')
const actionError = ref('')
const message = ref('')
const pending = ref<BindingChange>()
const controller = new AbortController()
const options = { silentError: true, signal: controller.signal }

async function load() {
  loading.value = true
  readError.value = ''
  try {
    const info = await request.get<unknown, { configured: boolean }>('/auth/sso', options)
    if (controller.signal.aborted) return
    const result = info.configured
      ? await request.get<unknown, BindingList>('/platform/sso-bindings', options)
      : undefined
    if (controller.signal.aborted) return
    configured.value = info.configured
    data.value = result
    if (pending.value) {
      const command = pending.value
      const verified = result?.items.some(item => item.subject === command.subject && item.username === command.username
        && item.enabled === command.enabled && item.version > command.expected_version)
      message.value = verified ? '已读回身份关联变更。' : '保存请求已完成，当前目录未显示该版本，请核对。'
      pending.value = undefined
    }
  } catch (failure) {
    if (controller.signal.aborted) return
    data.value = undefined
    configured.value = undefined
    readError.value = failure instanceof Error ? failure.message : '读取单点登录配置失败。'
  } finally {
    if (!controller.signal.aborted) loading.value = false
  }
}

async function save(command: BindingChange, newBinding: boolean) {
  if (loading.value || saving.value || pending.value) return
  saving.value = true
  actionError.value = ''
  message.value = ''
  try {
    const label = newBinding ? '关联' : command.enabled ? '恢复' : '撤销'
    await ElMessageBox.confirm(
      '确认' + label + '「' + command.username + '」的单点登录身份「' + command.subject + '」？该用户的旧会话会失效。',
      '确认身份关联变更',
      { type: 'warning', confirmButtonText: '确认' + label, cancelButtonText: '取消' },
    )
    if (controller.signal.aborted) return
    await request.post('/platform/sso-bindings', command, options)
    if (controller.signal.aborted) return
    if (newBinding) {
      username.value = ''
      subject.value = ''
    }
    pending.value = command
    await load()
  } catch (failure) {
    if (!controller.signal.aborted && failure !== 'cancel' && failure !== 'close') {
      actionError.value = failure instanceof Error ? failure.message : '身份关联操作失败。'
    }
  } finally {
    if (!controller.signal.aborted) saving.value = false
  }
}

function create() {
  if (!username.value || !subject.value) return
  void save({ username: username.value, subject: subject.value, expected_version: 0, enabled: true }, true)
}

function toggle(item: Binding) {
  void save({ username: item.username, subject: item.subject, expected_version: item.version, enabled: !item.enabled }, false)
}

onMounted(load)
onScopeDispose(() => controller.abort())
</script>

<template>
  <section class="glass-panel space-y-4 ui-card-padding" aria-labelledby="platform-sso-heading" :aria-busy="loading || saving">
    <div class="flex flex-wrap items-start justify-between gap-3">
      <div class="min-w-0">
        <h2 id="platform-sso-heading" class="text-lg font-semibold">企业单点登录身份关联</h2>
        <p class="mt-2 text-sm text-muted">将外部身份与现有知数用户关联。工作空间权限由对应空间管理。</p>
      </div>
      <button type="button" class="ghost-button ui-control-target" :disabled="loading || saving" @click="load">刷新单点登录配置</button>
    </div>
    <p v-if="actionError" role="alert" class="text-sm text-danger">{{ actionError }}</p>
    <p v-if="message" role="status" class="text-sm text-content">{{ message }}</p>
    <LoadingState v-if="loading" label="正在读取单点登录配置…" variant="detail" />
    <template v-else-if="readError">
      <p v-if="pending" role="status" class="text-sm text-muted">保存请求已完成，身份关联仍待读回确认。</p>
      <PageState heading="h3" title="读取单点登录配置失败" :description="readError" tone="danger">
        <template #actions><button type="button" class="ghost-button ui-control-target" @click="load">重试读取配置</button></template>
      </PageState>
    </template>
    <PageState v-else-if="configured === false" heading="h3" title="此环境尚未配置 OIDC" description="发行者、客户端与回调地址由平台维护者在部署配置中设置。" />
    <template v-else-if="data">
      <dl class="grid grid-cols-[auto_minmax(0,1fr)] gap-3 text-sm border-y border-divider py-3">
        <dt class="text-muted">提供方类型</dt><dd>OpenID Connect（OIDC）</dd>
        <dt class="text-muted">发行者</dt><dd class="break-all">{{ data.issuer }}</dd>
      </dl>
      <form class="grid sm:grid-cols-2 gap-3" aria-label="关联现有用户" @submit.prevent="create">
        <label class="block text-sm">现有知数用户名
          <input v-model.trim="username" class="glass-input block w-full mt-1" maxlength="256" required :disabled="saving" />
        </label>
        <label class="block text-sm">身份提供方 subject
          <input v-model.trim="subject" class="glass-input block w-full mt-1" maxlength="255" required :disabled="saving" />
        </label>
        <button class="liquid-button sm:col-span-2 justify-self-start ui-control-target" :disabled="saving || !username || !subject">关联现有用户</button>
      </form>
      <div v-if="data.items.length" class="editorial-table-wrap" role="region" tabindex="0" aria-label="身份关联表格">
        <table class="editorial-table min-w-[620px]" aria-label="当前身份关联">
          <thead><tr><th scope="col">用户名</th><th scope="col">外部身份</th><th scope="col">版本</th><th scope="col">状态</th><th scope="col">操作</th></tr></thead>
          <tbody>
            <tr v-for="item in data.items" :key="item.subject">
              <td class="break-all">{{ item.username }}</td>
              <td class="break-all">{{ item.subject }}</td>
              <td>{{ item.version }}</td>
              <td class="whitespace-nowrap">{{ item.enabled ? '有效' : '已撤销' }}</td>
              <td><button type="button" class="ghost-button whitespace-nowrap ui-control-target" :disabled="saving"
                :aria-label="(item.enabled ? '撤销关联' : '恢复关联') + ' · ' + item.username" @click="toggle(item)">{{ item.enabled ? '撤销关联' : '恢复关联' }}</button></td>
            </tr>
          </tbody>
        </table>
      </div>
      <PageState v-else heading="h3" title="暂无身份关联" description="可为现有用户关联该发行者的外部身份。" />
    </template>
  </section>
</template>
