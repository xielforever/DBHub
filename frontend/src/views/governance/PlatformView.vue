<script setup lang="ts">
import { computed, onScopeDispose, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import LoadingState from '../../components/base/LoadingState.vue'
import PageCategoryTabs from '../../components/base/PageCategoryTabs.vue'
import PageHeader from '../../components/base/PageHeader.vue'
import PageState from '../../components/base/PageState.vue'
import { usePageCategory } from '../../composables/usePageCategory'
import { useUserStore } from '../../stores/user'
import type { PlatformAccountQuery } from '../../api/platform-management'
import request from '../../utils/request'
import SSOSettings from '../settings/SSOSettings.vue'
import OperationsSettings from '../settings/OperationsSettings.vue'
import PlatformAccountsView from './PlatformAccountsView.vue'
import PlatformRolePermissionsView from './PlatformRolePermissionsView.vue'
import PlatformWorkspaceMenusView from './PlatformWorkspaceMenusView.vue'

const user = useUserStore()
const route = useRoute()
const router = useRouter()
const categories = [
  { id: 'accounts', name: '用户账号' },
  { id: 'identity', name: '登录来源' },
  { id: 'role-permissions', name: '角色权限' },
  { id: 'workspace-menus', name: '工作空间菜单' },
  { id: 'operations', name: '运行配置' },
]
const { category, selectCategory } = usePageCategory(categories)
const accountQuery = ref<PlatformAccountQuery>({ keyword: '', status: '', login_method: '', page: 1 })
const loading = ref(false)
const allowed = ref(false)
const error = ref('')
const title = computed(() => category.value ? '平台管理 · ' + category.value.name : '平台管理')
let currentRead: AbortController | undefined

function chooseCategory(id: string) {
  if (id === category.value?.id) return
  if (id !== 'workspace-menus' && route.query.workspace_id !== undefined) {
    void router.push({ query: { ...route.query, tab: id, workspace_id: undefined } })
    return
  }
  void selectCategory(id)
}

async function checkAccess() {
  currentRead?.abort()
  allowed.value = false
  error.value = ''
  if (!user.user?.id) {
    loading.value = false
    return
  }
  const read = new AbortController()
  currentRead = read
  loading.value = true
  try {
    const result = await request.get<unknown, { is_platform_admin: boolean }>('/platform/status', {
      signal: read.signal, silentError: true,
    })
    if (!read.signal.aborted) allowed.value = result.is_platform_admin
  } catch (failure) {
    if (!read.signal.aborted) error.value = failure instanceof Error ? failure.message : '平台资格暂不可用。'
  } finally {
    if (!read.signal.aborted) loading.value = false
  }
}

watch([() => user.user?.id, () => user.platformAdmin], () => void checkAccess(), { immediate: true, flush: 'sync' })
onScopeDispose(() => currentRead?.abort())
</script>

<template>
  <section class="ui-page-stack">
    <PageHeader :title="title" />
    <LoadingState v-if="loading" variant="detail" label="正在核对平台管理资格…" />
    <PageState v-else-if="error" title="无法核对平台管理资格" :description="error" tone="danger">
      <template #actions><button class="ghost-button ui-control-target" @click="checkAccess">重新核对资格</button></template>
    </PageState>
    <PageState v-else-if="!allowed" title="平台管理权限不足" description="请联系超级管理员核对当前账号的资格。">
      <template #actions><RouterLink to="/profile" class="ghost-button ui-control-target">返回个人设置</RouterLink></template>
    </PageState>
    <PageCategoryTabs v-else label="平台管理分类" :categories="categories" :active-id="category?.id" @select="chooseCategory">
      <PlatformAccountsView v-if="category?.id === 'accounts'" v-model="accountQuery" @verify-access="checkAccess" />
      <SSOSettings v-else-if="category?.id === 'identity'" />
      <PlatformRolePermissionsView v-else-if="category?.id === 'role-permissions'" />
      <PlatformWorkspaceMenusView v-else-if="category?.id === 'workspace-menus'" />
      <OperationsSettings v-else-if="category?.id === 'operations'" />
      <template #unavailable><PageState title="此平台分类不可用" description="请选择上方可用分类。" /></template>
    </PageCategoryTabs>
  </section>
</template>
