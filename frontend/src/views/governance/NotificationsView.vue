<script setup lang="ts">
import LoadingState from '../../components/base/LoadingState.vue'
import PageHeader from '../../components/base/PageHeader.vue'
import PageState from '../../components/base/PageState.vue'
import { computed, onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import InboxView from '../inbox/InboxView.vue'
import { useUserStore } from '../../stores/user'

const user = useUserStore()
const route = useRoute()
const loading = ref(true)
const origin = computed(() => {
  const value = route.query.workspace_id
  if (value === undefined) return undefined
  if (typeof value !== 'string' || !Number.isSafeInteger(Number(value)) || Number(value) < 1) return null
  return Number(value)
})
const confirmOrigin = computed(() => origin.value !== undefined && origin.value !== user.workspaceId)
const originLink = computed(() => ({
  path: '/workspace-link',
  query: origin.value === null
    ? { from: route.fullPath, reason: 'invalid_workspace' }
    : { from: route.fullPath, workspace_id: String(origin.value) },
}))

onMounted(async () => {
  try {
    await user.loadWorkspaces()
    if (!confirmOrigin.value) await user.loadPermissions()
  } catch {
    // The page below keeps all records hidden until permission is confirmed.
  } finally {
    loading.value = false
  }
})
</script>

<template>
  <section class="ui-page-stack">
    <PageHeader title="通知" description="通知按其原工作空间打开。切换空间后会重新确认权限。" />
    <LoadingState v-if="loading" variant="detail" label="正在确认通知访问资格…" />
    <PageState v-else-if="confirmOrigin" title="请先确认通知所属的工作空间" description="确认原空间及当前成员资格后读取通知。">
      <template #actions><RouterLink :to="originLink" class="ghost-button ui-control-target">确认原工作空间</RouterLink></template>
    </PageState>
    <InboxView v-else-if="user.workspaceId&&user.permissionStatus==='ready'" :key="user.permissionKey" embedded notifications-only />
    <PageState v-else title="选择原工作空间后查看通知" description="无成员资格或权限服务不可用时，不显示通知标题、数量或数据摘要。">
      <template #actions><RouterLink to="/profile" class="ghost-button">个人设置与安全</RouterLink></template>
    </PageState>
  </section>
</template>
