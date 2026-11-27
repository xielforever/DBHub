<script setup lang="ts">
import WorkspaceSettings from './WorkspaceSettings.vue'
import IdentitySecuritySettings from './IdentitySecuritySettings.vue'
import SSOSettings from './SSOSettings.vue'
import OperationsSettings from './OperationsSettings.vue'
import { computed, nextTick, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useUserStore } from '../../stores/user'
import { adminApi } from '../../api/admin'
import ConnectorSettings from './ConnectorSettings.vue'
import GroupSettings from './GroupSettings.vue'
import ThemeSwitcher from '../../components/base/ThemeSwitcher.vue'
import PageHeader from '../../components/base/PageHeader.vue'
import PageCategoryTabs from '../../components/base/PageCategoryTabs.vue'
import PageState from '../../components/base/PageState.vue'
import { usePageCategory } from '../../composables/usePageCategory'

const userStore = useUserStore()
const router = useRouter()
const changing = ref(false)
const pwd = reactive({ oldPassword: '', newPassword: '' })
const passwordError = ref(''), currentPassword = ref<HTMLInputElement>(), newPassword = ref<HTMLInputElement>()
const categories = computed(() => [
  { id: 'profile', name: '资料偏好' }, { id: 'security', name: '身份安全' },
  ...(userStore.role === 'admin' ? [{ id: 'workspace', name: '空间设置' }, { id: 'groups', name: '用户组' }] : []),
  { id: 'drivers', name: '数据库驱动' },
  ...(userStore.platformAdmin ? [{ id: 'identity', name: '平台身份' }, { id: 'operations', name: '平台运行' }] : []),
])
const { category, selectCategory } = usePageCategory(categories)
const tab = computed(() => category.value?.id)

const roleLabels: Record<string, string> = {
  admin: '管理员',
  developer: '开发者',
  readonly: '只读用户',
}
const roleLabel = computed(() => roleLabels[userStore.role] ?? userStore.role ?? '-')

async function changePassword() {
  if (changing.value) return
  if (!pwd.oldPassword || pwd.newPassword.length < 8) {
    passwordError.value = '请填写当前密码，且新密码至少 8 个字符。'
    if (!pwd.oldPassword) currentPassword.value?.focus()
    else newPassword.value?.focus()
    return
  }
  if (pwd.newPassword === pwd.oldPassword) {
    passwordError.value = '新密码不能与当前密码相同。'
    newPassword.value?.focus()
    return
  }
  changing.value = true
  passwordError.value = ''
  const identity = userStore.identityEpoch()
  try {
    await adminApi.changePassword(pwd.oldPassword, pwd.newPassword, true)
    if (identity === userStore.identityEpoch()) {
      userStore.clearAuth()
      await router.replace('/login')
    }
  } catch (error) {
    passwordError.value = error instanceof Error ? error.message : '密码更新失败，请重试。'
  } finally {
    pwd.oldPassword = ''
    if (!passwordError.value) pwd.newPassword = ''
    changing.value = false
    if (passwordError.value) { await nextTick(); currentPassword.value?.focus() }
  }
}
function select(id: string) { if (!changing.value) void selectCategory(id) }
</script>

<template>
  <div class="ui-settings-page ui-page-stack">
    <PageHeader title="系统设置（兼容入口）" description="管理账号偏好、安全和当前工作空间设置。" />
    <PageCategoryTabs label="系统设置分类" :categories="categories" :active-id="tab" @select="select">

    <WorkspaceSettings v-if="tab === 'workspace'" />
    <SSOSettings v-else-if="tab === 'identity'" />
    <OperationsSettings v-else-if="tab === 'operations'" />
    <ConnectorSettings v-else-if="tab === 'drivers'" />
    <GroupSettings v-else-if="tab === 'groups'" />

    <div v-else-if="tab === 'profile'" class="space-y-4">
    <RouterLink v-if="userStore.workspaceId && userStore.role !== 'readonly'"
      :to="{ path: '/proxies', query: { workspace_id: userStore.workspaceId } }"
      class="ghost-button inline-flex ui-control-target">连接代理</RouterLink>
    <section class="glass-panel ui-card-padding">
      <h2 class="font-medium mb-4">个人资料</h2>
      <div class="grid grid-cols-1 sm:grid-cols-2 gap-4">
        <div>
          <label for="settings-username" class="block text-xs text-muted mb-1.5">用户名</label>
          <input id="settings-username" class="glass-input" :value="userStore.username" disabled />
        </div>
        <div>
          <label for="settings-role" class="block text-xs text-muted mb-1.5">角色</label>
          <input id="settings-role" class="glass-input" :value="roleLabel" disabled />
        </div>
      </div>
    </section>

    <section class="glass-panel ui-card-padding">
      <h2 class="font-medium mb-4">外观</h2>
      <div class="flex flex-wrap items-center justify-between gap-3">
        <div>
          <p class="text-xs text-muted mt-1">选择浅色、深色或跟随系统，所有页面保持一致。</p>
        </div>
        <ThemeSwitcher />
      </div>
    </section>
    </div>

    <div v-else-if="tab === 'security'" class="space-y-4">
    <IdentitySecuritySettings />
    <section class="glass-panel ui-card-padding">
      <h2 class="font-medium mb-4">修改密码</h2>
      <form class="grid grid-cols-1 sm:grid-cols-3 gap-4" @submit.prevent="changePassword">
        <div>
          <label for="old-pwd" class="block text-xs text-muted mb-1.5">当前密码</label>
          <input id="old-pwd" ref="currentPassword" v-model="pwd.oldPassword" type="password" autocomplete="current-password" class="glass-input" :disabled="changing" :aria-describedby="passwordError ? 'compat-password-error' : undefined" />
        </div>
        <div>
          <label for="new-pwd" class="block text-xs text-muted mb-1.5">新密码</label>
          <input id="new-pwd" ref="newPassword" v-model="pwd.newPassword" type="password" autocomplete="new-password" class="glass-input" placeholder="至少 8 个字符" :disabled="changing" :aria-describedby="passwordError ? 'compat-password-error' : undefined" />
        </div>
        <div class="flex items-end">
          <button type="submit" class="liquid-button w-full sm:w-auto ui-control-target" :disabled="changing">
            {{ changing ? '提交中…' : '更新密码' }}
          </button>
        </div>
      </form>
      <p v-if="passwordError" id="compat-password-error" class="text-sm text-danger mt-3" role="alert">{{ passwordError }}</p>
      <p class="text-xs text-subtle mt-3">修改成功后请使用新密码重新登录；旧会话会失效。</p>
    </section>
    </div>
    <template #unavailable><PageState title="此设置分类不可用" description="请选择当前有权访问的设置分类。" /></template>
    </PageCategoryTabs>
  </div>
</template>
