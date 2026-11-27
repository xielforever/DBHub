<script setup lang="ts">
import { computed, onScopeDispose, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import type { PlatformDirectoryQuery } from '../../api/platform-management'
import LoadingState from '../../components/base/LoadingState.vue'
import PageHeader from '../../components/base/PageHeader.vue'
import PageState from '../../components/base/PageState.vue'
import { useUserStore } from '../../stores/user'
import ManagedWorkspaceList from './ManagedWorkspaceList.vue'
import PlatformWorkspacesView from './PlatformWorkspacesView.vue'
import WorkspaceAccessPanel from './WorkspaceAccessPanel.vue'

const route = useRoute()
const router = useRouter()
const user = useUserStore()
const requestedPage = typeof route.query.page === 'string' ? Number(route.query.page) : 1
const query = ref<PlatformDirectoryQuery>({
  keyword: typeof route.query.q === 'string' ? Array.from(route.query.q.trim()).slice(0, 100).join('') : '',
  page: Number.isSafeInteger(requestedPage) && requestedPage >= 1 && requestedPage <= 1_000_000 ? requestedPage : 1,
})
const targetWorkspace = computed(() => {
  const value = route.query.manage_workspace
  if (typeof value !== 'string' || !/^[1-9]\d*$/.test(value)) return undefined
  const parsed = Number(value)
  return Number.isSafeInteger(parsed) ? parsed : undefined
})
const directoryRoute = computed(() => ({ name: 'workspace-directory', query: {
  q: query.value.keyword || undefined,
  page: query.value.page > 1 ? String(query.value.page) : undefined,
} }))
const loading = ref(true)
const error = ref('')
let accessRead = 0

async function checkAccess() {
  const read = ++accessRead
  const actor = user.user?.id
  loading.value = true
  error.value = ''
  try {
    await user.loadWorkspaces(true)
  } catch (failure) {
    if (read === accessRead && user.user?.id === actor) {
      error.value = failure instanceof Error ? failure.message : '暂时无法核对工作空间权限。'
    }
  } finally {
    if (read === accessRead && user.user?.id === actor) loading.value = false
  }
}

watch(() => user.user?.id, () => void checkAccess(), { immediate: true, flush: 'sync' })
watch(query, ({ keyword, page }) => {
  if (route.name !== 'workspace-directory') return
  void router.replace({ name: 'workspace-directory', query: {
    ...route.query,
    q: keyword || undefined,
    page: page > 1 ? String(page) : undefined,
  } })
})
onScopeDispose(() => { accessRead += 1 })
</script>

<template>
  <section class="ui-page-stack min-w-0">
    <PageHeader v-if="route.query.manage_workspace !== undefined" title="工作空间授权管理">
      <template #actions>
        <RouterLink :to="directoryRoute" class="ghost-button ui-control-target">返回空间列表</RouterLink>
      </template>
    </PageHeader>
    <PageHeader v-else title="空间管理" description="选择要管理的工作空间。" />
    <LoadingState v-if="loading" label="正在核对工作空间权限…" />
    <PageState v-else-if="error" title="读取工作空间失败" :description="error" tone="danger">
      <template #actions><button type="button" class="ghost-button ui-control-target" @click="checkAccess">重试读取</button></template>
    </PageState>
    <template v-else-if="route.query.manage_workspace !== undefined">
      <WorkspaceAccessPanel v-if="user.platformAdmin && targetWorkspace" :key="`${user.user?.id}:${targetWorkspace}`" :workspace-id="targetWorkspace" />
      <PageState v-else title="此工作空间授权入口不可用" description="请选择目录中的工作空间。" />
    </template>
    <PlatformWorkspacesView v-else-if="user.platformAdmin" v-model="query" @verify-access="checkAccess" />
    <ManagedWorkspaceList v-else v-model="query" @verify-access="checkAccess" />
  </section>
</template>
