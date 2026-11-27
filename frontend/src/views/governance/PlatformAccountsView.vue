<script setup lang="ts">
import { computed, nextTick, onMounted, onScopeDispose, ref, shallowRef } from 'vue'
import LoadingState from '../../components/base/LoadingState.vue'
import PageState from '../../components/base/PageState.vue'
import { useUserStore } from '../../stores/user'
import { ApiRequestError } from '../../utils/request'
import {
  platformManagementApi,
  type AccountCredential,
  type PlatformAccount,
  type PlatformAccountQuery,
  type PlatformAccountStatus,
} from '../../api/platform-management'
import { usePlatformDirectory } from './usePlatformDirectory'

const query = defineModel<PlatformAccountQuery>({ required: true })
const emit = defineEmits<{ verifyAccess: [] }>()
const user = useUserStore()
const search = ref(query.value.keyword)
const status = ref(query.value.status)
const loginMethod = ref(query.value.login_method)
const { items, total, lastPage, loading, error, load, changeQuery, goTo } = usePlatformDirectory(query, platformManagementApi.accounts)

const selected = shallowRef<PlatformAccount>()
const detailTitle = ref<HTMLElement>()
const directoryRoot = ref<HTMLElement>()
const directorySearch = ref<HTMLInputElement>()
const notice = ref('')
const busy = ref(false)
const formError = ref('')
const command = ref<AccountCommand>()
const username = ref('')
const email = ref('')
const reason = ref('')
const receipt = shallowRef<AccountCredential>()
const disclosed = ref(false)
const copyStatus = ref('')
const credentialInput = ref<HTMLInputElement>()
const lifetime = new AbortController()
let receiptTimer: ReturnType<typeof setTimeout> | undefined

type AccountCommand = 'create' | 'activate' | 'reset' | 'disable' | 'enable' | 'revoke'
const commandLabels: Record<AccountCommand, string> = {
  create: '新建本地账号',
  activate: '重新签发激活凭证',
  reset: '签发密码重置凭证',
  disable: '停用账号',
  enable: '恢复账号',
  revoke: '撤销登录会话',
}
const commandEffects: Record<Exclude<AccountCommand, 'create'>, string> = {
  activate: '重新签发会使先前的激活凭证失效。',
  reset: '本人须使用新凭证设置密码。完成重置时会撤销现有登录会话。',
  disable: '停用后不能登录知数，已有会话失效。账号和审计记录保留。',
  enable: '恢复后须重新登录。工作空间访问按当前成员资格校验。',
  revoke: '所有当前知数登录会话将失效。账号本身仍可重新登录。',
}
const statusLabels: Record<PlatformAccountStatus, string> = {
  pending: '待激活',
  active: '已启用',
  inactive: '已停用',
}
const canManageSelected = computed(() => Boolean(selected.value && selected.value.id !== user.user?.id))
const receiptPurpose = computed(() => receipt.value?.purpose === 'activate' ? '激活' : '密码重置')
const commandTitle = computed(() => command.value ? commandLabels[command.value] : '')
const formatDate = (value: string | null) => value ? new Date(value).toLocaleString('zh-CN') : '—'

function loginMethodLabel(account: PlatformAccount) {
  if (account.local_login && account.sso_login) return '本地密码、企业单点登录'
  if (account.sso_login) return '企业单点登录'
  if (account.local_login || account.status === 'pending') return '本地密码'
  return '—'
}

onMounted(() => void load())
onScopeDispose(() => {
  lifetime.abort()
  clearTimeout(receiptTimer)
  receipt.value = undefined
})

function applyFilters() {
  void changeQuery({ keyword: search.value.trim(), status: status.value, login_method: loginMethod.value, page: 1 })
}

function clearFilters() {
  search.value = ''
  status.value = ''
  loginMethod.value = ''
  applyFilters()
}

async function openDetails(account: PlatformAccount) {
  selected.value = account
  notice.value = ''
  await focusDetails()
}

async function focusDetails() {
  await nextTick()
  if (!lifetime.signal.aborted) detailTitle.value?.focus()
}

async function closeDetails() {
  const target = selected.value?.id
  selected.value = undefined
  notice.value = ''
  await load()
  await nextTick()
  if (lifetime.signal.aborted || selected.value) return
  const rowButton = target === undefined ? null
    : directoryRoot.value?.querySelector<HTMLButtonElement>(`[data-account-id="${target}"]`)
  ;(rowButton ?? directorySearch.value)?.focus()
}

function openCommand(value: AccountCommand) {
  if (value !== 'create' && !canManageSelected.value) return
  command.value = value
  username.value = ''
  email.value = ''
  reason.value = ''
  formError.value = ''
  notice.value = ''
}

function closeCommand() {
  if (!busy.value) command.value = undefined
}

function hideReceipt(restoreFocus = true) {
  clearTimeout(receiptTimer)
  receipt.value = undefined
  disclosed.value = false
  copyStatus.value = ''
  if (restoreFocus) void focusDetails()
}

function showReceipt(value: AccountCredential) {
  hideReceipt(false)
  const duration = new Date(value.expires_at).getTime() - Date.now()
  if (!(duration > 0)) {
    notice.value = '凭证已过期，请重新签发。'
    void focusDetails()
    return
  }
  receipt.value = value
  receiptTimer = setTimeout(() => {
    notice.value = '凭证已过期，请重新签发。'
    hideReceipt()
  }, duration)
}

async function revealCredential() {
  disclosed.value = true
  await nextTick()
  credentialInput.value?.focus()
}

async function copyCredential() {
  if (!receipt.value || !disclosed.value) return
  try {
    await navigator.clipboard.writeText(receipt.value.token)
    copyStatus.value = '已复制。'
  } catch {
    credentialInput.value?.select()
    copyStatus.value = '浏览器未允许复制。凭证已选中，可手动复制。'
  }
}

async function submitCommand() {
  const action = command.value
  const target = selected.value
  const explanation = reason.value.trim()
  if (busy.value || !action) return
  if (!explanation) {
    formError.value = '请填写操作原因。'
    return
  }
  if (action !== 'create' && (!target || !canManageSelected.value)) return
  busy.value = true
  formError.value = ''
  try {
    const signal = lifetime.signal
    const payload = { reason: explanation, expected_version: target?.version ?? 0 }
    let account: PlatformAccount
    let credential: AccountCredential | undefined
    if (action === 'create') {
      ({ account, credential } = await platformManagementApi.createAccount({
        username: username.value.trim(), email: email.value.trim(), reason: explanation,
      }, signal))
    } else if (target && (action === 'activate' || action === 'reset')) {
      ({ account, credential } = await platformManagementApi.issueCredential(target.id, { ...payload, purpose: action }, signal))
    } else if (target && action === 'revoke') {
      account = await platformManagementApi.revokeSessions(target.id, payload, signal)
    } else if (target) {
      account = await platformManagementApi.changeStatus(target.id, { ...payload, active: action === 'enable' }, signal)
    } else {
      return
    }
    if (signal.aborted) return
    command.value = undefined
    selected.value = account
    notice.value = `${account.username} · ${commandLabels[action]}成功`
    if (credential) showReceipt(credential)
    else await focusDetails()
  } catch (failure) {
    if (!lifetime.signal.aborted) {
      formError.value = failure instanceof Error ? failure.message : '暂时无法确认账号操作，请刷新后重试。'
      if (failure instanceof ApiRequestError && failure.status === 403) emit('verifyAccess')
    }
  } finally {
    if (!lifetime.signal.aborted) busy.value = false
  }
}
</script>

<template>
  <section class="ui-page-stack min-w-0">
    <p v-if="notice" class="text-sm text-muted" role="status">{{ notice }}</p>

    <section v-if="selected" aria-labelledby="platform-account-detail" class="space-y-4">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <button type="button" class="ghost-button ui-control-target" @click="closeDetails">返回账号列表</button>
        <span class="text-sm text-muted">{{ statusLabels[selected.status] }}</span>
      </div>
      <div class="glass-panel ui-card-padding space-y-4">
        <h2 id="platform-account-detail" ref="detailTitle" class="editorial-object-title break-all" tabindex="-1">
          {{ selected.username }}
        </h2>
        <dl class="grid gap-3 text-sm sm:grid-cols-2 lg:grid-cols-3">
          <div><dt class="text-muted">联系邮箱</dt><dd class="break-all">{{ selected.email || '—' }}</dd></div>
          <div><dt class="text-muted">登录方式</dt><dd>{{ loginMethodLabel(selected) }}</dd></div>
          <div><dt class="text-muted">平台资格</dt><dd>{{ selected.is_platform_admin ? '超级管理员' : '普通账号' }}</dd></div>
          <div><dt class="text-muted">最近登录</dt><dd>{{ formatDate(selected.last_login_at) }}</dd></div>
          <div><dt class="text-muted">创建时间</dt><dd>{{ formatDate(selected.created_at) }}</dd></div>
          <div><dt class="text-muted">多因素认证</dt><dd>{{ selected.mfa_enabled ? '已启用' : '未启用' }}</dd></div>
        </dl>
      </div>
      <div class="glass-panel ui-card-padding space-y-3">
        <h3 class="editorial-object-title">登录与安全</h3>
        <p v-if="selected.sso_login" class="text-sm text-muted">企业单点登录凭据由其身份提供方维护。</p>
        <div v-if="canManageSelected" class="flex flex-wrap gap-2">
          <button v-if="selected.status === 'pending'" type="button" class="ghost-button ui-control-target"
            @click="openCommand('activate')">{{ commandLabels.activate }}</button>
          <button v-if="selected.local_login && selected.status === 'active'" type="button" class="ghost-button ui-control-target"
            @click="openCommand('reset')">{{ commandLabels.reset }}</button>
          <button v-if="selected.status === 'active'" type="button" class="ghost-button ui-control-target"
            @click="openCommand('revoke')">{{ commandLabels.revoke }}</button>
          <button v-if="selected.status === 'active'" type="button" class="ghost-button ui-control-target text-danger"
            @click="openCommand('disable')">{{ commandLabels.disable }}</button>
          <button v-if="selected.status === 'inactive'" type="button" class="ghost-button ui-control-target"
            @click="openCommand('enable')">{{ commandLabels.enable }}</button>
        </div>
        <RouterLink v-else class="ghost-button ui-control-target inline-flex" to="/profile">前往个人安全设置</RouterLink>
      </div>
    </section>

    <section v-else ref="directoryRoot" class="glass-panel ui-card-padding min-w-0 space-y-4" aria-label="用户账号列表">
      <form class="flex flex-wrap items-end gap-3" @submit.prevent="applyFilters">
        <label class="flex min-w-0 flex-auto flex-col gap-1 text-sm">
          <span>搜索账号</span>
          <input ref="directorySearch" v-model="search" type="search" class="glass-input ui-control-target w-full"
            placeholder="用户名或邮箱" maxlength="100" />
        </label>
        <label class="flex flex-col gap-1 text-sm">
          <span id="platform-account-login-filter">登录方式</span>
          <select v-model="loginMethod" class="glass-input ui-control-target" aria-labelledby="platform-account-login-filter">
            <option value="">全部方式</option>
            <option value="local">本地密码</option>
            <option value="sso">企业单点登录</option>
          </select>
        </label>
        <label class="flex flex-col gap-1 text-sm">
          <span id="platform-account-status-filter">账号状态</span>
          <select v-model="status" class="glass-input ui-control-target" aria-labelledby="platform-account-status-filter">
            <option value="">全部状态</option>
            <option v-for="(value, key) in statusLabels" :key="key" :value="key">{{ value }}</option>
          </select>
        </label>
        <button type="submit" class="ghost-button ui-control-target">搜索</button>
        <button type="button" class="ghost-button ui-control-target" @click="clearFilters">清除筛选</button>
        <button type="button" class="liquid-button ui-control-target sm:ml-auto" @click="openCommand('create')">
          {{ commandLabels.create }}
        </button>
      </form>
      <LoadingState v-if="loading" label="正在读取用户账号…" />
      <PageState v-else-if="error" title="读取用户账号失败" :description="error" tone="danger">
        <template #actions><button type="button" class="ghost-button ui-control-target" @click="load">重试读取</button></template>
      </PageState>
      <PageState v-else-if="!items.length" title="没有符合条件的账号" description="可调整搜索条件或新建本地账号。" />
      <template v-else>
        <div class="editorial-table-wrap" role="region" tabindex="0" aria-label="用户账号表格">
          <table class="editorial-table min-w-[42rem]">
            <thead><tr><th scope="col">账号</th><th scope="col">登录方式</th><th scope="col">账号状态</th>
              <th scope="col">最近登录</th><th scope="col">操作</th></tr></thead>
            <tbody>
              <tr v-for="account in items" :key="account.id">
                <th scope="row" class="break-all">{{ account.username }}
                  <span v-if="account.email" class="editorial-table__meta block">{{ account.email }}</span>
                </th>
                <td>{{ loginMethodLabel(account) }}</td>
                <td>{{ statusLabels[account.status] }}</td>
                <td class="whitespace-nowrap">{{ formatDate(account.last_login_at) }}</td>
                <td><button type="button" class="editorial-text-action ui-control-target"
                  :data-account-id="account.id" :aria-label="`查看 ${account.username} 的账号详情`"
                  @click="openDetails(account)">查看详情</button></td>
              </tr>
            </tbody>
          </table>
        </div>
        <div class="flex flex-wrap items-center justify-between gap-2 text-sm">
          <span>共 {{ total }} 个账号 · 第 {{ query.page }} / {{ lastPage }} 页</span>
          <div class="flex gap-2">
            <button type="button" class="ghost-button ui-control-target" :disabled="query.page <= 1"
              @click="goTo(query.page - 1)">上一页</button>
            <button type="button" class="ghost-button ui-control-target" :disabled="query.page >= lastPage"
              @click="goTo(query.page + 1)">下一页</button>
          </div>
        </div>
      </template>
    </section>

    <el-dialog v-if="command" :model-value="true" :title="commandTitle" append-to-body align-center destroy-on-close
      width="min(560px, calc(100vw - 24px))" :close-on-click-modal="false" :close-on-press-escape="!busy"
      :show-close="!busy" class="glass-dialog" @close="closeCommand">
      <form class="space-y-4" :aria-busy="busy" @submit.prevent="submitCommand">
        <template v-if="command === 'create'">
          <label class="block space-y-1 text-sm">
            <span>用户名</span>
            <input v-model.trim="username" class="glass-input ui-control-target block w-full" autocomplete="off" required
              minlength="3" maxlength="64" pattern="[A-Za-z0-9][A-Za-z0-9_.\-]{2,63}" :disabled="busy" />
          </label>
          <label class="block space-y-1 text-sm">
            <span>联系邮箱</span>
            <input v-model.trim="email" class="glass-input ui-control-target block w-full"
              type="email" autocomplete="off" maxlength="254" :disabled="busy" />
          </label>
          <p class="text-sm text-muted">新账号由本人激活后登录。工作空间成员资格单独授予。</p>
        </template>
        <div v-else-if="selected" class="space-y-1">
          <p>操作对象 <strong class="break-all">{{ selected.username }}</strong></p>
          <p class="text-sm text-muted">{{ commandEffects[command] }}</p>
        </div>
        <label class="block space-y-1 text-sm">
          <span>操作原因</span>
          <input v-model="reason" class="glass-input ui-control-target block w-full"
            required maxlength="300" :disabled="busy" />
        </label>
        <p v-if="formError" class="editorial-field-error" role="alert">{{ formError }}</p>
        <div class="flex flex-wrap justify-end gap-2">
          <button type="button" class="ghost-button ui-control-target" :disabled="busy" @click="closeCommand">取消</button>
          <button type="submit" class="liquid-button ui-control-target" :disabled="busy">
            {{ busy ? '正在提交…' : command === 'create' ? '创建账号' : `确认${commandTitle}` }}
          </button>
        </div>
      </form>
    </el-dialog>

    <el-dialog v-if="receipt" :model-value="true" :title="`一次性${receiptPurpose}凭证`"
      append-to-body align-center destroy-on-close width="min(580px, calc(100vw - 24px))"
      :close-on-click-modal="false" class="glass-dialog" @close="hideReceipt()">
      <div class="space-y-4">
        <p class="text-sm text-muted">请在核验身份后把凭证交给 {{ selected?.username }}。本次关闭后可重新签发新凭证。</p>
        <p class="text-sm">有效至 {{ formatDate(receipt.expires_at) }}</p>
        <button v-if="!disclosed" type="button" class="ghost-button ui-control-target" @click="revealCredential">显示凭证</button>
        <div v-else class="space-y-2">
          <label class="block space-y-1 text-sm">
            <span>一次性凭证</span>
            <input ref="credentialInput" :value="receipt.token" class="glass-input ui-control-target block w-full font-mono"
              readonly autocomplete="off" spellcheck="false" @focus="credentialInput?.select()" />
          </label>
          <button type="button" class="ghost-button ui-control-target" @click="copyCredential">复制凭证</button>
          <p v-if="copyStatus" class="text-sm text-muted" role="status">{{ copyStatus }}</p>
        </div>
        <div class="flex flex-wrap justify-between gap-2">
          <RouterLink class="editorial-text-action ui-control-target inline-flex" to="/account/setup">账号设置页</RouterLink>
          <button type="button" class="liquid-button ui-control-target" @click="hideReceipt()">已记录凭证</button>
        </div>
      </div>
    </el-dialog>
  </section>
</template>
