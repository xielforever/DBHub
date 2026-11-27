<script setup lang="ts">
import LoadingState from '../../components/base/LoadingState.vue'
import PageHeader from '../../components/base/PageHeader.vue'
import { computed, nextTick, onMounted, onScopeDispose, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useUserStore } from '../../stores/user'
import { ApiRequestError } from '../../utils/request'

const user = useUserStore()
const route = useRoute()
const router = useRouter()
const previousWorkspaceId = user.workspaceId
const previousWorkspace = computed(() => !loadingSpaces.value && !listError.value ? user.workspaces.find(space => space.id === previousWorkspaceId) : undefined)
const invalidOrigin = computed(() => {
  const value = route.query.workspace_id
  return route.query.reason === 'invalid_workspace' ||
    (value !== undefined && (typeof value !== 'string' || !/^\d+$/.test(value) || !Number.isSafeInteger(Number(value)) || Number(value) <= 0)) || originConflict.value
})
const desired = computed(() => invalidOrigin.value ? 0 : Number(route.query.workspace_id) || 0)
const available = computed(() => {
  if (invalidOrigin.value) return []
  return desired.value ? user.workspaces.filter(space => space.id === desired.value) : user.workspaces
})
const selected = ref<number>()
const busy = ref(false)
const loadingSpaces = ref(false)
const listError = ref('')
const confirmError = ref('')
const confirmButton = ref<HTMLButtonElement>()
const retrySpacesButton = ref<HTMLButtonElement>()
const missingOriginMessage = ref<HTMLParagraphElement>()
let active = true
let verification: AbortController | undefined
const target = computed(() => {
  const value = route.query.from
  if (typeof value !== 'string' || !value.startsWith('/') || value.startsWith('//') || value.includes('\\')) return undefined
  const resolved = router.resolve(value)
  return resolved.path === '/workspace-link' ? undefined : resolved
})
const originConflict = computed(() => {
  const value = target.value?.query.workspace_id
  return value !== undefined && (typeof value !== 'string' || !/^\d+$/.test(value) || !Number.isSafeInteger(Number(value)) || Number(value) <= 0 || Number(value) !== Number(route.query.workspace_id))
})
const missingOrigin = computed(() => !invalidOrigin.value && !!desired.value && available.value.length === 0)
const canConfirm = computed(() => !loadingSpaces.value && !listError.value && !busy.value &&
  !!target.value && available.value.some(space => space.id === selected.value))
const chosenWorkspace = computed(() => !loadingSpaces.value && !listError.value ? available.value.find(space => space.id === selected.value) : undefined)
const roleLabels: Record<string, string> = { admin: '管理员', developer: '开发者', readonly: '只读成员' }
const comparisons = computed(() => [
  { label: '链接原工作空间', space: chosenWorkspace.value, missing: missingOrigin.value ? '无有效成员资格' : '待选择或核对' },
  { label: '进入前的工作空间', space: previousWorkspace.value, missing: previousWorkspaceId ? '成员资格未确认' : '进入前未选择空间' },
])

watch(() => route.fullPath, () => {
  confirmError.value = ''
  verification?.abort()
}, { flush: 'sync' })
watch([() => route.fullPath, available], ([link], previous) => {
  if (link !== previous?.[0] || !available.value.some(space => space.id === selected.value)) {
    selected.value = available.value.find(space => space.id === desired.value)?.id
  }
}, { immediate: true })

async function loadSpaces() {
  if (loadingSpaces.value) return false
  loadingSpaces.value = true
  listError.value = ''
  try {
    await user.loadWorkspaces(true)
    return true
  } catch {
    listError.value = '暂时无法核对工作空间，请稍后重试。'
    return false
  } finally {
    loadingSpaces.value = false
  }
}

async function focusUnavailableOrigin(linkPath: string) {
  const trigger = confirmButton.value
  if (!trigger || document.activeElement !== trigger) return
  await nextTick()
  if (!active || route.fullPath !== linkPath || (document.activeElement !== trigger && document.activeElement !== document.body)) return
  const feedback = listError.value ? retrySpacesButton.value : missingOriginMessage.value
  feedback?.focus()
}

async function confirm() {
  const linkPath = route.fullPath
  const chosen = selected.value
  const destination = target.value
  if (!canConfirm.value || chosen === undefined || !destination) return
  const attempt = new AbortController()
  verification = attempt
  busy.value = true
  confirmError.value = ''
  try {
    if (!await loadSpaces() || attempt.signal.aborted || !available.value.some(space => space.id === chosen)) return
    if (!await user.confirmWorkspace(chosen, attempt.signal) || attempt.signal.aborted) return
    await router.replace({ path: destination.path, query: { ...destination.query, workspace_id: String(chosen) }, hash: destination.hash })
  } catch (error) {
    if (attempt.signal.aborted) return
    if (error instanceof ApiRequestError && (error.status === 403 || error.status === 404)) {
      await loadSpaces()
      if (listError.value || missingOrigin.value) return
    }
    if (!attempt.signal.aborted) confirmError.value = error instanceof Error ? error.message : '原空间权限不可用'
  } finally {
    verification = undefined
    busy.value = false
    if (!attempt.signal.aborted && (listError.value || missingOrigin.value)) await focusUnavailableOrigin(linkPath)
  }
}

async function returnToPrevious() {
  if (busy.value || !previousWorkspace.value) return
  const attempt = new AbortController()
  verification = attempt
  busy.value = true
  confirmError.value = ''
  try {
    if (!await loadSpaces() || attempt.signal.aborted || !previousWorkspace.value) return
    if (await user.confirmWorkspace(previousWorkspaceId, attempt.signal) && !attempt.signal.aborted) {
      const path = user.safeHome === '/no-menu' ? '/profile' : user.safeHome
      await router.replace({ path, query: { workspace_id: String(previousWorkspaceId) } })
    }
  } catch { if (!attempt.signal.aborted) confirmError.value = '暂时无法返回进入前的工作空间，可打开个人设置。' }
  finally {
    verification = undefined
    busy.value = false
  }
}

onMounted(() => void loadSpaces())
onScopeDispose(() => { active = false; verification?.abort() })
</script>

<template>
  <section class="ui-page-stack min-w-0 max-w-5xl">
    <PageHeader title="确认链接所属的工作空间" description="核对链接所属空间的成员资格及当前操作权限。" />
    <p v-if="invalidOrigin" role="alert" class="text-sm text-danger">链接中的工作空间标识无效或不一致，请从原工作空间重新打开该对象。</p>
    <p v-else-if="!target" role="alert" class="text-sm text-danger">链接缺少可用的站内目标，请从原工作空间重新打开该对象。</p>
    <LoadingState v-if="loadingSpaces" variant="inline" label="正在核对可用工作空间…" />
    <label v-if="!invalidOrigin && target" class="block text-sm max-w-sm">原工作空间
      <el-select v-model="selected" class="w-full mt-2" aria-label="链接原工作空间" :loading="loadingSpaces"
        :disabled="loadingSpaces || busy || !!listError || !available.length" loading-text="正在核对工作空间…">
        <el-option v-for="space in available" :key="space.id" :value="space.id" :label="space.name" />
      </el-select>
    </label>
    <p v-if="!loadingSpaces && !listError && missingOrigin" ref="missingOriginMessage" tabindex="-1" role="status" class="text-warning text-sm ui-focus-target">
      你没有此链接原工作空间的有效成员资格。不会改用其他空间解释对象编号。
    </p>
    <div v-if="listError" class="flex flex-wrap items-center gap-2">
      <p role="alert" class="text-danger text-sm">{{ listError }}</p>
      <button ref="retrySpacesButton" type="button" class="ghost-button ui-control-target" @click="loadSpaces">重试读取工作空间</button>
    </div>
    <div class="grid gap-3 sm:grid-cols-2" aria-label="工作空间对照">
      <section v-for="item in comparisons" :key="item.label" class="glass-panel min-w-0 p-4 space-y-3">
        <h2 class="editorial-object-title">{{ item.label }}</h2>
        <dl class="space-y-2 text-sm">
          <div class="grid grid-cols-[6rem_minmax(0,1fr)] gap-2"><dt class="text-muted">空间名称</dt><dd class="break-words">{{ item.space?.name || '—' }}</dd></div>
          <div class="grid grid-cols-[6rem_minmax(0,1fr)] gap-2"><dt class="text-muted">成员资格</dt><dd>{{ item.space ? '已有成员资格' : item.missing }}</dd></div>
          <div v-if="item.space && roleLabels[item.space.role]" class="grid grid-cols-[6rem_minmax(0,1fr)] gap-2"><dt class="text-muted">成员角色</dt><dd>{{ roleLabels[item.space.role] }}</dd></div>
        </dl>
      </section>
    </div>
    <p v-if="confirmError" role="alert" class="text-danger text-sm">{{ confirmError }}</p>
    <p class="text-sm text-muted">进入后按原空间已有权限访问对象。</p>
    <div class="flex flex-wrap gap-3">
      <button ref="confirmButton" type="button" class="liquid-button ui-control-target" :disabled="!busy && !canConfirm" :aria-disabled="busy || undefined" @click="confirm">
        {{ busy ? '正在确认空间权限…' : '进入原空间并检查权限' }}
      </button>
      <button v-if="previousWorkspace" type="button" class="ghost-button ui-control-target" :disabled="busy" @click="returnToPrevious">取消并返回进入前的空间</button>
      <RouterLink to="/profile" class="ghost-button">返回个人设置</RouterLink>
    </div>
  </section>
</template>
