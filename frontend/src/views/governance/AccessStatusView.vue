<script setup lang="ts">
import { computed, onMounted, onScopeDispose, ref } from 'vue'
import { useRoute } from 'vue-router'
import PageHeader from '../../components/base/PageHeader.vue'
import PageState from '../../components/base/PageState.vue'
import { useUserStore } from '../../stores/user'

const props = defineProps<{ kind: 'no-menu' | 'denied' | 'unavailable'; entryLabel?: string }>()
const user = useUserStore()
const route = useRoute()
const refreshing = ref(false)
const feedback = ref('')
const refreshFailed = ref(false)
const workspace = computed(() => user.permissionStatus === 'ready' && !refreshFailed.value ? user.workspace : undefined)
const canReturn = computed(() => !!workspace.value && user.safeHome !== '/no-menu' && user.safeHome !== route.path)
const restored = computed(() => props.kind === 'no-menu' && canReturn.value)
const ready = computed(() => !!workspace.value)
const failureNotice = computed(() => refreshFailed.value || user.permissionStatus === 'error')
const roleNames = computed(() => {
  if (!ready.value) return '待确认'
  const names = new Set(user.effective?.snapshot.sources?.map(source => source.role_name).filter(Boolean))
  if (names.size) return [...names].join('、')
  const compatibleRoles: Record<string, string> = { admin: '管理员', developer: '开发者', readonly: '只读成员' }
  return (!user.actionControlled && compatibleRoles[user.role]) || '未显示角色名称'
})
const heading = computed(() => {
  if (restored.value) return '当前空间已有可用业务菜单'
  if (props.kind === 'no-menu') {
    if (!user.workspaceId) return '请选择工作空间'
    return ready.value ? '当前没有可显示的业务菜单' : '暂无法确认可用业务菜单'
  }
  if (props.kind === 'unavailable') return props.entryLabel ? `${props.entryLabel}的功能尚未授权` : '当前入口的功能尚未授权'
  return '当前空间没有此操作权限'
})

let active = true
async function refresh(force = true) {
  if (refreshing.value) return
  refreshing.value = true
  refreshFailed.value = false
  feedback.value = ''
  try {
    await user.loadWorkspaces(force)
    if (!active) return
    await user.loadPermissions(force)
    if (active && force) feedback.value = workspace.value ? '已重新核对当前空间权限。' : '请选择已有成员资格的工作空间。'
  } catch {
    if (!active) return
    refreshFailed.value = true
  } finally {
    if (active) refreshing.value = false
  }
}

onMounted(() => {
  if (user.permissionStatus === 'idle') void refresh(false)
})
onScopeDispose(() => { active = false })
</script>

<template>
  <section class="ui-page-stack">
    <PageHeader :title="heading" />
    <section class="glass-panel ui-card-padding max-w-4xl space-y-3" aria-labelledby="access-context-title">
      <h2 id="access-context-title" class="text-lg font-semibold">当前身份与空间</h2>
      <dl class="grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-2 border-t border-divider pt-3 text-sm">
        <dt class="text-muted">当前用户</dt><dd class="break-words">{{ user.username || '待确认' }}</dd>
        <dt class="text-muted">当前空间</dt><dd class="break-words">{{ workspace?.name || (user.workspaceId ? '待确认' : '未选择') }}</dd>
      </dl>
      <p class="text-sm text-muted">可在页头选择已有成员资格的工作空间。</p>
    </section>

    <section class="glass-panel ui-card-padding max-w-4xl space-y-3" aria-labelledby="access-permission-title">
      <h2 id="access-permission-title" class="text-lg font-semibold">我的权限</h2>
      <dl class="grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-2 border-t border-divider pt-3 text-sm">
        <dt class="text-muted">空间角色</dt><dd class="break-words">{{ roleNames }}</dd>
        <dt class="text-muted">业务菜单</dt><dd>{{ ready ? (user.visibleMenus.length ? '有可用菜单' : '暂无可用菜单') : '待确认' }}</dd>
      </dl>
      <div class="flex flex-wrap gap-3">
        <RouterLink v-if="canReturn" :to="user.safeHome" class="liquid-button ui-control-target">返回可用首页</RouterLink>
        <RouterLink to="/profile" class="ghost-button ui-control-target">查看我的当前权限</RouterLink>
        <button type="button" class="ghost-button ui-control-target"
          :aria-disabled="refreshing || undefined" :aria-busy="refreshing" @click="refresh()">
          {{ refreshing ? '正在核对权限…' : '刷新权限' }}
        </button>
      </div>
      <p v-if="failureNotice" role="alert" class="text-sm text-danger">暂时无法核对工作空间或权限，请重试。</p>
      <p v-else-if="feedback" role="status" class="text-sm text-muted">{{ feedback }}</p>
    </section>

    <PageState v-if="!restored && ready" class="max-w-4xl" :title="kind === 'no-menu' ? '业务菜单暂不可用' : '当前操作受限'" tone="warning"
      :description="kind === 'no-menu' ? '如需使用业务菜单，请联系当前空间的管理员。' : '请联系当前空间的管理员核对相应功能许可。'" />
  </section>
</template>
