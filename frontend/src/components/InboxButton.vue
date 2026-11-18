<script setup lang="ts">
import { computed, onMounted, onScopeDispose, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { automationApi } from '../api/automation'
import { useUserStore } from '../stores/user'
import { iamUiEnabled } from '../utils/iam-mode'
import { createNotificationSummaryLoader } from '../utils/notification-state'
import type { NotificationSummaryState } from '../utils/notification-state'
import NotificationIndicator from './base/NotificationIndicator.vue'

const router = useRouter()
const user = useUserStore()
const state = ref<NotificationSummaryState>({ unread: 0, known: false, loading: false, failed: false })
const context = computed(() => user.user?.id && user.workspaceId && (!iamUiEnabled || user.permissionStatus === 'ready')
  ? `${user.user.id}:${user.permissionKey}` : null)
const loader = createNotificationSummaryLoader(
  signal => automationApi.inbox({ page_size: 1, state: 'unread' }, signal),
  value => { state.value = value },
  () => navigator.onLine && !document.hidden,
)
let timer: ReturnType<typeof setInterval> | undefined
const refresh = () => { void loader.refresh() }
watch(context, value => { void loader.setContext(value) }, { immediate: true, flush: 'sync' })

onMounted(() => {
  timer = setInterval(refresh, 30000)
  window.addEventListener('dbhub-inbox-updated', refresh)
  window.addEventListener('online', refresh)
  document.addEventListener('visibilitychange', refresh)
})
onScopeDispose(() => {
  loader.dispose()
  clearInterval(timer)
  window.removeEventListener('dbhub-inbox-updated', refresh)
  window.removeEventListener('online', refresh)
  document.removeEventListener('visibilitychange', refresh)
})
function open() {
  const query = context.value ? { workspace_id: String(user.workspaceId) } : undefined
  void router.push({ path: '/notifications', query })
}
</script>

<template>
  <NotificationIndicator :unread="state.unread" :known="state.known" :loading="state.loading" :failed="state.failed"
    :qualified="!!context" @click="open" />
</template>
