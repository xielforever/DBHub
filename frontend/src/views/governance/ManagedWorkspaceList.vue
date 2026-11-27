<script setup lang="ts">
import { computed, nextTick, onMounted, onScopeDispose, ref, shallowRef, watch } from 'vue'
import { useRouter } from 'vue-router'
import { iamApi } from '../../api/iam'
import { platformDirectoryPageSize, type PlatformDirectoryQuery } from '../../api/platform-management'
import LoadingState from '../../components/base/LoadingState.vue'
import PageState from '../../components/base/PageState.vue'
import { useUserStore, type Workspace } from '../../stores/user'

const query = defineModel<PlatformDirectoryQuery>({ required: true })
const emit = defineEmits<{ verifyAccess: [] }>()
const user = useUserStore()
const router = useRouter()
const search = ref(query.value.keyword)
const loading = ref(true)
const failed = ref(false)
const spaces = shallowRef<Workspace[]>([])
const checking = ref<number>()
const enterError = ref('')
const notice = ref<HTMLElement>()
const lifetime = new AbortController()

const roleLabels: Record<string, string> = { admin: '管理员', developer: '开发者', readonly: '只读成员' }
const filteredSpaces = computed(() => spaces.value.filter(space =>
  space.name.toLowerCase().includes(query.value.keyword.toLowerCase()),
))
const lastPage = computed(() => Math.max(1, Math.ceil(filteredSpaces.value.length / platformDirectoryPageSize)))
const pageSpaces = computed(() => {
  const first = (query.value.page - 1) * platformDirectoryPageSize
  return filteredSpaces.value.slice(first, first + platformDirectoryPageSize)
})

function applySearch() {
  query.value = { keyword: search.value.trim(), page: 1 }
}

async function load() {
  const { signal } = lifetime
  const actor = user.user?.id
  const candidates = [...user.workspaces]
  const allowed: Workspace[] = []
  for (let first = 0; first < candidates.length && !signal.aborted; first += 4) {
    const checked = await Promise.all(candidates.slice(first, first + 4).map(async space => {
      try {
        const access = await iamApi.effective({ id: space.id, signal })
        if (access.snapshot.subject === actor && access.snapshot.workspace_id === space.id) {
          if (!access.actions.includes('space.page')) return undefined
          return access.is_workspace_admin ? { ...space, role: 'admin' } : space
        }
      } catch { /* Requests with an unconfirmed result are excluded. */ }
      return null
    }))
    if (signal.aborted || user.user?.id !== actor) return
    for (const result of checked) {
      if (result === null) failed.value = true
      else if (result) allowed.push(result)
    }
  }
  if (signal.aborted || user.user?.id !== actor) return
  spaces.value = allowed
  loading.value = false
}

async function enter(spaceId: number, event: MouseEvent) {
  if (checking.value !== undefined) return
  const trigger = event.currentTarget
  const { signal } = lifetime
  checking.value = spaceId
  enterError.value = ''
  try {
    const confirmed = await user.confirmWorkspace(spaceId, signal, 'space.page')
    if (signal.aborted) return
    if (!confirmed) throw new Error('工作空间资格已变化，请刷新列表后重试。')
    await router.push({ name: 'space-management', query: {
      workspace_id: String(spaceId),
      spaces_q: query.value.keyword || undefined,
      spaces_page: query.value.page > 1 ? String(query.value.page) : undefined,
    } })
  } catch (failure) {
    if (signal.aborted) return
    enterError.value = failure instanceof Error ? failure.message : '暂时无法确认该工作空间的管理权限。'
    if (document.activeElement === trigger) {
      await nextTick()
      notice.value?.focus()
    }
  } finally {
    if (!signal.aborted) checking.value = undefined
  }
}

watch([loading, lastPage], () => {
  if (!loading.value && query.value.page > lastPage.value) {
    query.value = { ...query.value, page: lastPage.value }
  }
})
onMounted(() => void load())
onScopeDispose(() => lifetime.abort())
</script>

<template>
  <section class="glass-panel ui-card-padding min-w-0 space-y-4" aria-label="可管理工作空间目录">
    <form class="flex flex-wrap items-end gap-3" @submit.prevent="applySearch">
      <label class="flex min-w-0 flex-auto flex-col gap-1 text-sm">
        <span>搜索工作空间</span>
        <input v-model="search" type="search" class="glass-input ui-control-target w-full" placeholder="工作空间名称" maxlength="100" />
      </label>
      <button type="submit" class="ghost-button ui-control-target" :disabled="loading">搜索</button>
      <button type="button" class="ghost-button ui-control-target" :aria-disabled="loading || checking !== undefined"
        @click="!loading && checking === undefined && emit('verifyAccess')">刷新</button>
    </form>
    <p v-if="enterError" ref="notice" role="alert" tabindex="-1" class="text-sm text-danger">{{ enterError }}</p>
    <LoadingState v-if="loading" label="正在核对空间管理权限…" />
    <template v-else>
      <p v-if="failed" role="status" class="text-sm text-danger">部分空间的管理权限暂未核实。可刷新重试。</p>
      <template v-if="pageSpaces.length">
        <div class="editorial-table-wrap" role="region" tabindex="0" aria-label="工作空间表格">
          <table class="editorial-table min-w-[29rem]">
            <thead><tr><th scope="col">工作空间名称</th><th scope="col">我的空间角色</th><th scope="col">操作</th></tr></thead>
            <tbody>
              <tr v-for="space in pageSpaces" :key="space.id">
                <th scope="row" class="[overflow-wrap:anywhere]">
                  {{ space.name }}
                  <span v-if="space.id === user.workspaceId" class="editorial-table__meta block">当前工作空间</span>
                </th>
                <td>{{ roleLabels[space.role] ?? space.role }}</td>
                <td>
                  <button type="button" class="editorial-text-action ui-control-target whitespace-nowrap"
                    :aria-label="`进入 ${space.name} 管理`" :aria-disabled="checking !== undefined"
                    @click="enter(space.id, $event)">{{ checking === space.id ? '正在核对许可…' : '进入管理' }}</button>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
        <div class="flex flex-wrap items-center justify-between gap-2 text-sm">
          <span>共 {{ filteredSpaces.length }} 个工作空间 · 第 {{ query.page }} / {{ lastPage }} 页</span>
          <div class="flex gap-2">
            <button type="button" class="ghost-button ui-control-target" :disabled="query.page <= 1"
              @click="query = { ...query, page: query.page - 1 }">上一页</button>
            <button type="button" class="ghost-button ui-control-target" :disabled="query.page >= lastPage"
              @click="query = { ...query, page: query.page + 1 }">下一页</button>
          </div>
        </div>
      </template>
      <PageState v-else :title="query.keyword ? '没有符合条件的工作空间' : '暂无已确认可管理的工作空间'"
        :description="query.keyword ? '可调整搜索条件。' : '请联系对应空间的管理员核对成员资格。'" heading="h3" />
    </template>
  </section>
</template>
