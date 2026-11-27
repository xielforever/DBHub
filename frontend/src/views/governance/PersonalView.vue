<script setup lang="ts">
import { computed, nextTick, onMounted, onScopeDispose, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { adminApi } from '../../api/admin'
import { iamApi } from '../../api/iam'
import LoadingState from '../../components/base/LoadingState.vue'
import PageHeader from '../../components/base/PageHeader.vue'
import PageState from '../../components/base/PageState.vue'
import ThemeSwitcher from '../../components/base/ThemeSwitcher.vue'
import { useUserStore } from '../../stores/user'
import { actionLabel } from '../../utils/iam-labels'
import { featureAllowed } from '../../utils/permission'
import request from '../../utils/request'
import IdentitySecuritySettings from '../settings/IdentitySecuritySettings.vue'

interface RestrictionReceipt {
  workspace_id: number
  request_id: string
  kind: string
  execution_fence: string
  created_at: string
}

const restrictionLabels: Record<string, string> = {
  'member.freeze': '成员冻结',
  'action.revoke': '撤销功能许可',
  'assignment.revoke': '撤销角色来源',
  'delegation.revoke': '撤销治理委派',
  'role.disable': '停用角色',
}
const user = useUserStore()
const router = useRouter()
const permission = computed(() => user.permissionStatus === 'ready' ? user.effective : null)
const canManageProxy = computed(() => !!user.workspaceId && !!permission.value
  && featureAllowed(user, 'connection.proxy.manage', user.role !== 'readonly'))
const permissionSources = computed(() => permission.value?.snapshot.sources ?? [])
const home = ref('')
const storedHome = computed(() => user.visibleMenus.find(menu => menu.id === permission.value?.personal_home)?.id ?? '')
const homeSaving = ref(false)
const homeError = ref('')
const homeMessage = ref('')
const verifyHome = ref('')
const homeErrorNotice = ref<HTMLElement>()
const homeStatusNotice = ref<HTMLElement>()
const homeRetryButton = ref<HTMLButtonElement>()
const oldPassword = ref('')
const newPassword = ref('')
const currentPasswordField = ref<HTMLInputElement>()
const newPasswordField = ref<HTMLInputElement>()
const passwordError = ref('')
const passwordSaving = ref(false)
const loggingOut = ref(false)
const permissionLoading = ref(false)
const permissionError = ref('')
const receipts = ref<RestrictionReceipt[]>([])
const receiptLoading = ref(false)
const receiptError = ref('')
let receiptRead: AbortController | undefined
let receiptTimer: ReturnType<typeof setInterval> | undefined
let alive = true

watch(() => user.workspaceId, () => {
  home.value = ''
  verifyHome.value = ''
  homeMessage.value = ''
  homeError.value = ''
  homeSaving.value = false
}, { flush: 'sync' })
watch([storedHome, () => user.visibleMenus], ([saved, menus], previous) => {
  if (!permission.value) return
  if (!home.value || home.value === previous?.[0] || !menus.some(menu => menu.id === home.value)) home.value = saved
}, { immediate: true })

async function checkPermissions(force = false) {
  if (permissionLoading.value) return
  permissionLoading.value = true
  permissionError.value = ''
  try {
    await user.loadWorkspaces(force)
    if (alive) await user.loadPermissions(force)
  } catch {
    if (alive) permissionError.value = '暂时无法核对工作空间或权限。'
  } finally {
    if (alive) permissionLoading.value = false
  }
}

async function confirmHome(current: () => boolean) {
  await user.loadPermissions(true)
  if (!current()) return
  if (!permission.value || permission.value.personal_home !== verifyHome.value) {
    homeError.value = '暂未确认个人首页已更新，请重新核对。'
    return
  }
  home.value = verifyHome.value
  verifyHome.value = ''
  homeMessage.value = '已保存当前空间的个人首页。'
}

function homeActionFocus(event: Event) {
  const active = document.activeElement
  return event.currentTarget instanceof HTMLElement && active instanceof HTMLElement && event.currentTarget.contains(active)
    ? active
    : undefined
}

async function restoreHomeFocus(trigger: HTMLElement | undefined, current: () => boolean) {
  if (!trigger) return
  await nextTick()
  const active = document.activeElement
  if (!current() || (active !== trigger && active !== document.body)) return
  const target = homeRetryButton.value ?? homeErrorNotice.value ?? homeStatusNotice.value
  target?.focus()
}

async function saveHome(event: SubmitEvent) {
  const selected = home.value
  const space = user.workspaceId
  const identity = user.identityEpoch()
  const current = () => alive && space === user.workspaceId && identity === user.identityEpoch()
  if (homeSaving.value || verifyHome.value || !permission.value || !user.visibleMenus.some(menu => menu.id === selected) || selected === storedHome.value) return
  const trigger = homeActionFocus(event)
  homeSaving.value = true
  homeError.value = ''
  homeMessage.value = ''
  verifyHome.value = ''
  try {
    await iamApi.home(selected)
    if (!current()) return
    verifyHome.value = selected
    await confirmHome(current)
  } catch (error) {
    if (current()) homeError.value = verifyHome.value
      ? '保存请求已完成，暂时无法复核个人首页。'
      : error instanceof Error ? error.message : '保存个人首页失败。'
  } finally {
    if (current()) {
      homeSaving.value = false
      await restoreHomeFocus(trigger, current)
    }
  }
}

async function recheckHome(event: MouseEvent) {
  const space = user.workspaceId
  const identity = user.identityEpoch()
  const current = () => alive && space === user.workspaceId && identity === user.identityEpoch()
  if (homeSaving.value || !verifyHome.value) return
  const trigger = homeActionFocus(event)
  homeSaving.value = true
  homeError.value = ''
  try {
    await confirmHome(current)
  } catch {
    if (current()) homeError.value = '暂时无法复核个人首页，请重试。'
  } finally {
    if (current()) {
      homeSaving.value = false
      await restoreHomeFocus(trigger, current)
    }
  }
}

async function loadReceipts() {
  if (receiptRead || !user.user?.id) return
  const read = new AbortController()
  const identity = user.identityEpoch()
  receiptRead = read
  receiptLoading.value = true
  receiptError.value = ''
  try {
    const data = await request.get<unknown, RestrictionReceipt[]>('/iam-command-status', { silentError: true, signal: read.signal })
    if (!read.signal.aborted && identity === user.identityEpoch()) receipts.value = data
  } catch (error) {
    if (read.signal.aborted || identity !== user.identityEpoch()) return
    receipts.value = []
    receiptError.value = error instanceof Error ? error.message : '回执暂不可用'
  } finally {
    if (receiptRead === read) {
      receiptRead = undefined
      receiptLoading.value = false
    }
  }
}

async function changePassword() {
  if (passwordSaving.value || loggingOut.value) return
  passwordError.value = ''
  if (oldPassword.value === newPassword.value) {
    passwordError.value = '新密码不能与当前密码相同。'
    newPasswordField.value?.focus()
    return
  }
  const identity = user.identityEpoch()
  passwordSaving.value = true
  let failed = false
  try {
    await adminApi.changePassword(oldPassword.value, newPassword.value, true)
    if (user.identityEpoch() === identity) {
      oldPassword.value = ''
      newPassword.value = ''
      user.clearAuth()
      await router.replace('/login')
    }
  } catch (error) {
    if (alive && user.identityEpoch() === identity) {
      oldPassword.value = ''
      passwordError.value = error instanceof Error ? error.message : '密码修改失败。'
      failed = true
    }
  } finally {
    if (alive) {
      passwordSaving.value = false
      if (failed) {
        await nextTick()
        currentPasswordField.value?.focus()
      }
    }
  }
}

async function logout() {
  if (loggingOut.value || passwordSaving.value) return
  loggingOut.value = true
  try {
    await user.logout()
  } catch {
    // The store clears the local session even when the server cannot be reached.
  } finally {
    if (!user.isLoggedIn) await router.replace('/login')
    if (alive) loggingOut.value = false
  }
}

function time(value: string) {
  return new Date(value).toLocaleString('zh-CN')
}

onMounted(() => {
  if (user.permissionStatus !== 'ready') void checkPermissions()
  void loadReceipts()
  receiptTimer = setInterval(() => {
    if (receipts.value.some(item => item.execution_fence === 'pending') && !document.hidden) void loadReceipts()
  }, 5000)
})
watch(() => user.user, () => {
  receiptRead?.abort()
  receiptRead = undefined
  receipts.value = []
  receiptError.value = ''
  receiptLoading.value = false
  void loadReceipts()
})
onScopeDispose(() => {
  alive = false
  oldPassword.value = ''
  newPassword.value = ''
  receiptRead?.abort()
  clearInterval(receiptTimer)
})
</script>

<template>
  <section class="ui-settings-page ui-page-stack">
    <PageHeader title="个人设置与安全" description="管理个人偏好、账号安全和当前空间权限。" />
    <div class="grid gap-4 xl:grid-cols-[minmax(0,3fr)_minmax(0,2fr)]">
      <div class="ui-settings-column">
        <section class="glass-panel ui-card-padding space-y-3" aria-labelledby="profile-identity-title">
          <h2 id="profile-identity-title" class="text-lg font-semibold">个人资料</h2>
          <dl class="grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 border-t border-divider pt-3 text-sm">
            <dt class="text-muted">用户名</dt><dd class="break-words">{{ user.username }}</dd>
          </dl>
          <RouterLink v-if="canManageProxy" :to="{ path: '/proxies', query: { workspace_id: user.workspaceId } }"
            class="ghost-button inline-flex ui-control-target">连接代理</RouterLink>
        </section>

        <section class="glass-panel ui-card-padding space-y-4" aria-labelledby="profile-preferences-title">
          <h2 id="profile-preferences-title" class="text-lg font-semibold">首页与主题偏好</h2>
          <form v-if="permission && user.visibleMenus.length" class="flex flex-wrap items-end gap-3" @submit.prevent="saveHome">
            <div class="w-full sm:w-72">
              <label for="profile-home" class="block text-sm">当前空间个人首页</label>
              <select id="profile-home" v-model="home" class="glass-input block w-full mt-1" :disabled="homeSaving || !!verifyHome">
                <option value="" disabled>使用工作空间默认首页</option>
                <option v-for="menu in user.visibleMenus" :key="menu.id" :value="menu.id">{{ menu.label }}</option>
              </select>
            </div>
            <button class="ghost-button ui-control-target" :disabled="homeSaving || !home || home === storedHome || !!verifyHome">
              {{ homeSaving ? '正在核对…' : '保存个人首页' }}
            </button>
          </form>
          <p v-else class="text-sm text-muted">核验工作空间且有可用菜单后，可设置个人首页。</p>
          <div v-if="homeError" class="flex flex-wrap items-center gap-2">
            <p ref="homeErrorNotice" role="alert" class="ui-focus-target text-sm text-danger" tabindex="-1">{{ homeError }}</p>
            <button v-if="verifyHome" ref="homeRetryButton" class="ghost-button ui-control-target" :disabled="homeSaving" @click="recheckHome">重新核对首页</button>
          </div>
          <p v-if="homeMessage" ref="homeStatusNotice" role="status" class="ui-focus-target text-sm text-success" tabindex="-1">{{ homeMessage }}</p>
          <div class="flex flex-wrap items-center gap-3 border-t border-divider pt-3">
            <p class="text-sm text-muted">界面主题</p><ThemeSwitcher />
          </div>
        </section>

        <section class="glass-panel ui-card-padding space-y-4" aria-labelledby="profile-security-title">
          <div class="flex flex-wrap items-start justify-between gap-3">
            <h2 id="profile-security-title" class="text-lg font-semibold">安全与会话</h2>
            <button type="button" class="ghost-button ui-control-target" :disabled="loggingOut || passwordSaving" @click="logout">
              {{ loggingOut ? '正在退出…' : '退出当前会话' }}
            </button>
          </div>
          <IdentitySecuritySettings heading="h3" />
          <section class="space-y-3 border-t border-divider pt-3" aria-labelledby="profile-password-title">
            <h3 id="profile-password-title" class="font-semibold">修改密码</h3>
            <p class="text-sm text-muted">修改成功后请使用新密码重新登录。</p>
            <form class="grid gap-3" @submit.prevent="changePassword" @input="passwordError = ''">
              <label class="block text-sm">当前密码
                <input ref="currentPasswordField" v-model="oldPassword" type="password" autocomplete="current-password"
                  class="glass-input block w-full mt-1" required :disabled="passwordSaving || loggingOut"
                  :aria-describedby="passwordError ? 'profile-password-error' : undefined" />
              </label>
              <label class="block text-sm">新密码
                <input ref="newPasswordField" v-model="newPassword" type="password" autocomplete="new-password"
                  minlength="8" maxlength="128" class="glass-input block w-full mt-1" required :disabled="passwordSaving || loggingOut"
                  :aria-describedby="passwordError ? 'profile-password-error' : undefined" />
              </label>
              <p v-if="passwordError" id="profile-password-error" role="alert" class="text-sm text-danger">{{ passwordError }}</p>
              <button class="liquid-button justify-self-start ui-control-target" :disabled="passwordSaving || loggingOut">
                {{ passwordSaving ? '正在更新…' : '更新密码' }}
              </button>
            </form>
          </section>
        </section>
      </div>

      <div class="ui-settings-column">
        <section class="glass-panel ui-card-padding space-y-3" aria-labelledby="profile-permission-title">
          <h2 id="profile-permission-title" class="text-lg font-semibold">我的当前权限</h2>
          <LoadingState v-if="permissionLoading || user.permissionStatus === 'loading'" variant="inline" label="正在核对当前空间权限…" />
          <div v-else-if="permissionError || user.permissionStatus === 'error'" class="space-y-2">
            <p role="alert" class="text-sm text-danger">{{ permissionError || '暂时无法核对当前空间权限。' }}</p>
            <button class="ghost-button ui-control-target" @click="checkPermissions(true)">重新核对权限</button>
          </div>
          <template v-else-if="permission && user.workspace">
            <dl class="grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-2 border-t border-divider pt-3 text-sm">
              <dt class="text-muted">工作空间</dt><dd class="break-words">{{ user.workspace.name }}</dd>
              <dt class="text-muted">权限模式</dt><dd>{{ permission.snapshot.target ? '按角色许可执行' : user.actionControlled ? '兼容权限，保留当前限制' : '旧权限兼容' }}</dd>
              <template v-if="permission.snapshot.expires_at">
                <dt class="text-muted">有效期</dt><dd>{{ time(permission.snapshot.expires_at) }}</dd>
              </template>
            </dl>
            <ul v-if="permissionSources.length" class="text-sm space-y-2 border-t border-divider pt-3">
              <li v-for="source in permissionSources" :key="source.id" class="break-words">
                {{ source.role_name || '有限期治理恢复' }} · {{ source.kind === 'group' ? '用户组来源' : '直接来源' }}
                <span v-if="source.expires_at"> · 到期 {{ time(source.expires_at) }}</span>
              </li>
            </ul>
            <details class="text-sm">
              <summary class="cursor-pointer ui-control-target flex items-center">查看获准功能与数据身份</summary>
              <p class="text-muted leading-7 break-words">{{ permission.actions.map(actionLabel).join('、') || '暂无空间功能许可' }}</p>
              <p class="mt-3 text-xs text-muted">原数据身份：{{ permission.snapshot.legacy_data_role }}。个人查询与正式内容继续按原数据权限和发布受众校验。</p>
            </details>
          </template>
          <p v-else class="text-sm text-muted">可在页头选择已有成员资格的工作空间后查看权限。</p>
        </section>

        <section class="glass-panel ui-card-padding space-y-3" aria-labelledby="profile-receipts-title">
          <div class="flex flex-wrap justify-between items-start gap-2">
            <h2 id="profile-receipts-title" class="text-lg font-semibold">我的治理操作回执</h2>
            <button class="ghost-button ui-control-target" :disabled="receiptLoading" @click="loadReceipts">刷新回执</button>
          </div>
          <p class="text-sm text-muted">退出或冻结原空间后，仍可查看本人已提交的访问限制及执行边界。</p>
          <LoadingState v-if="receiptLoading && !receipts.length" variant="inline" label="正在读取治理回执…" />
          <p v-if="receiptLoading && receipts.length" role="status" class="text-sm text-muted">正在更新，当前显示上次回执。</p>
          <PageState v-if="receiptError" heading="h3" title="读取回执失败" :description="receiptError" tone="danger">
            <template #actions><button class="ghost-button ui-control-target" @click="loadReceipts">重试读取回执</button></template>
          </PageState>
          <div v-else-if="receipts.length" class="editorial-table-wrap" role="region" tabindex="0" aria-label="治理回执表格">
            <table class="editorial-table profile-receipts-table min-w-[340px]" aria-label="本人治理操作回执">
              <thead><tr><th scope="col">时间</th><th scope="col">原空间</th><th scope="col">操作</th><th scope="col">执行边界</th></tr></thead>
              <tbody>
                <tr v-for="item in receipts" :key="item.workspace_id + ':' + item.request_id">
                  <td><time :datetime="item.created_at">{{ time(item.created_at) }}</time></td>
                  <td>#{{ item.workspace_id }}</td>
                  <td>
                    <details>
                      <summary class="cursor-pointer ui-control-target flex items-center">{{ restrictionLabels[item.kind] || '治理收紧' }}</summary>
                      <p class="text-xs text-muted break-all">请求编号 {{ item.request_id }}</p>
                    </details>
                  </td>
                  <td :class="item.execution_fence === 'confirmed' ? 'text-success' : 'text-warning'">
                    {{ item.execution_fence === 'confirmed' ? '已确认' : '仍待核实' }}
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
          <p v-else-if="!receiptLoading" class="text-sm text-muted">暂无本人提交的收紧回执。</p>
        </section>
      </div>
    </div>
  </section>
</template>

<style scoped>
.profile-receipts-table :is(th, td) { padding-inline: 8px; }
</style>
