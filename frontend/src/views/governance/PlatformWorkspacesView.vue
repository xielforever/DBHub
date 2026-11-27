<script setup lang="ts">
import { nextTick, onMounted, onScopeDispose, ref, shallowRef } from 'vue'
import { useRouter } from 'vue-router'
import LoadingState from '../../components/base/LoadingState.vue'
import PageState from '../../components/base/PageState.vue'
import {
  platformDirectoryPageSize,
  platformManagementApi,
  type PlatformDirectoryQuery,
  type PlatformWorkspace,
} from '../../api/platform-management'
import { useUserStore } from '../../stores/user'
import { newRequestId } from '../../utils/request-id'
import { ApiRequestError } from '../../utils/request'
import { usePlatformDirectory } from './usePlatformDirectory'

const query = defineModel<PlatformDirectoryQuery>({ required: true })
const emit = defineEmits<{ verifyAccess: [] }>()
const user = useUserStore()
const router = useRouter()
const search = ref(query.value.keyword)
const { items, total, lastPage, loading, error, load, changeQuery, goTo } = usePlatformDirectory(query, platformManagementApi.workspaces)
const dialogOpen = ref(false)
const name = ref('')
const saving = ref(false)
const commandError = ref('')
const receipt = shallowRef<{ id: number; name: string; initialAdmin?: string }>()
const receiptError = ref('')
const receiptBusy = ref(false)
const receiptTitle = ref<HTMLElement>()
const enteringSpace = ref<number>()
const enterError = ref('')
const enterNotice = ref<HTMLElement>()
const requestIds = new Map<string, string>()
const lifetime = new AbortController()

onMounted(() => void load())
onScopeDispose(() => lifetime.abort())

function applySearch() {
  void changeQuery({ keyword: search.value.trim(), page: 1 })
}

function openCreate() {
  name.value = ''
  commandError.value = ''
  dialogOpen.value = true
}

function closeCreate() {
  if (!saving.value) dialogOpen.value = false
}

function mayOpen(space: PlatformWorkspace) {
  return space.enabled && user.workspaces.some(workspace => workspace.id === space.id)
}

function openAuthorization(workspaceId: number) {
  void router.push({ name: 'workspace-directory', query: {
    manage_workspace: String(workspaceId),
    q: query.value.keyword || undefined,
    page: query.value.page > 1 ? String(query.value.page) : undefined,
  } })
}

async function enterManagement(id: number, event: MouseEvent, tab?: 'members') {
  if (enteringSpace.value !== undefined) return
  const signal = lifetime.signal
  const actor = user.user?.id
  const trigger = event.currentTarget
  enteringSpace.value = id
  enterError.value = ''
  try {
    await user.loadWorkspaces(true)
    if (signal.aborted || user.user?.id !== actor) return
    const confirmed = await user.confirmWorkspace(id, signal, 'space.page')
    if (signal.aborted || user.user?.id !== actor) return
    if (!confirmed) {
      openAuthorization(id)
      return
    }
    await router.push({ name: 'space-management', query: {
      workspace_id: String(id), tab,
      spaces_q: query.value.keyword || undefined,
      spaces_page: query.value.page > 1 ? String(query.value.page) : undefined,
    } })
  } catch (failure) {
    if (signal.aborted || user.user?.id !== actor) return
    enterError.value = failure instanceof Error ? failure.message : '暂时无法进入空间管理，请重试。'
    if (document.activeElement === trigger) {
      await nextTick()
      enterNotice.value?.focus()
    }
  } finally {
    if (!signal.aborted) enteringSpace.value = undefined
  }
}

async function readReceipt() {
  const current = receipt.value
  if (!current || receiptBusy.value) return
  const signal = lifetime.signal
  receiptBusy.value = true
  receiptError.value = ''
  try {
    let page = 1
    let out = await platformManagementApi.workspaces({ keyword: current.name, page }, signal)
    let match = out.items.find(item => item.id === current.id)
    while (!match && page * platformDirectoryPageSize < out.total && !signal.aborted) {
      page += 1
      out = await platformManagementApi.workspaces({ keyword: current.name, page }, signal)
      match = out.items.find(item => item.id === current.id)
    }
    if (signal.aborted) return
    if (!match?.founding_admin) {
      receiptError.value = '工作空间已创建，暂未读回首任管理员。'
      return
    }
    receipt.value = { ...current, initialAdmin: match.founding_admin.username }
  } catch (failure) {
    if (!signal.aborted) receiptError.value = failure instanceof Error ? failure.message : '暂未读回首任管理员。'
  } finally {
    if (!signal.aborted) receiptBusy.value = false
  }
}

async function createWorkspace() {
  if (saving.value) return
  const workspaceName = name.value.trim()
  if (!workspaceName) {
    commandError.value = '请填写工作空间名称。'
    return
  }

  let requestId = requestIds.get(workspaceName)
  if (!requestId) {
    requestId = newRequestId()
    requestIds.set(workspaceName, requestId)
  }
  saving.value = true
  commandError.value = ''
  try {
    const created = await platformManagementApi.createWorkspace(workspaceName, requestId, lifetime.signal)
    if (lifetime.signal.aborted) return
    requestIds.delete(workspaceName)
    receipt.value = { id: created.id, name: created.name }
    dialogOpen.value = false
    await nextTick()
    if (lifetime.signal.aborted) return
    receiptTitle.value?.focus()
    await Promise.allSettled([load(), user.loadWorkspaces(true), readReceipt()])
  } catch (failure) {
    if (!lifetime.signal.aborted) {
      commandError.value = failure instanceof Error ? failure.message : '创建结果暂未确认，请使用原名称重试。'
      if (failure instanceof ApiRequestError && failure.status === 403) emit('verifyAccess')
    }
  } finally {
    if (!lifetime.signal.aborted) saving.value = false
  }
}
</script>

<template>
  <section class="ui-page-stack min-w-0">
    <p v-if="enterError" ref="enterNotice" role="alert" tabindex="-1" class="text-sm text-danger break-words">
      {{ enterError }}
    </p>
    <section v-if="receipt" class="glass-panel ui-card-padding space-y-3" aria-labelledby="workspace-creation-receipt">
      <h2 id="workspace-creation-receipt" ref="receiptTitle" class="editorial-object-title break-all" tabindex="-1">
        已创建 {{ receipt.name }}
      </h2>
      <dl v-if="receipt.initialAdmin" class="text-sm">
        <dt class="text-muted">首任管理员</dt><dd class="break-all">{{ receipt.initialAdmin }}</dd>
      </dl>
      <LoadingState v-else-if="receiptBusy" label="正在读取首任管理员…" />
      <p v-else-if="receiptError" class="text-sm text-danger" role="alert">{{ receiptError }}</p>
      <div class="flex flex-wrap items-center gap-2">
        <button type="button" class="ghost-button ui-control-target inline-flex" :aria-disabled="enteringSpace !== undefined"
          @click="enterManagement(receipt.id, $event, 'members')">{{ enteringSpace === receipt.id ? '正在核验权限…' : '进入新空间成员管理' }}</button>
        <button v-if="receiptError" type="button" class="ghost-button ui-control-target" @click="readReceipt">重试读取</button>
      </div>
    </section>

    <section class="glass-panel ui-card-padding min-w-0 space-y-4" aria-label="平台工作空间目录">
      <form class="flex flex-wrap items-end gap-3" @submit.prevent="applySearch">
        <label class="flex min-w-0 flex-auto flex-col gap-1 text-sm">
          <span>搜索工作空间</span>
          <input v-model="search" type="search" class="glass-input ui-control-target w-full"
            placeholder="工作空间名称" maxlength="100" />
        </label>
        <button type="submit" class="ghost-button ui-control-target">搜索</button>
        <button type="button" class="liquid-button ui-control-target sm:ml-auto" @click="openCreate">创建工作空间</button>
      </form>

      <LoadingState v-if="loading" label="正在读取平台工作空间…" />
      <PageState v-else-if="error" title="读取平台工作空间失败" :description="error" tone="danger">
        <template #actions><button type="button" class="ghost-button ui-control-target" @click="emit('verifyAccess')">重试读取</button></template>
      </PageState>
      <PageState v-else-if="!items.length" title="没有符合条件的工作空间" description="可调整搜索条件或创建工作空间。" />
      <template v-else>
        <div class="editorial-table-wrap" role="region" tabindex="0" aria-label="平台工作空间表格">
          <table class="editorial-table min-w-[37rem]">
            <thead><tr><th scope="col">工作空间</th><th scope="col">状态</th><th scope="col">首任管理员</th>
              <th scope="col">创建时间</th><th scope="col">操作</th></tr></thead>
            <tbody>
              <tr v-for="space in items" :key="space.id">
                <th scope="row" class="break-all">{{ space.name }}</th>
                <td>{{ space.enabled ? '已启用' : '已停用' }}</td>
                <td>{{ space.founding_admin?.username || '—' }}</td>
                <td class="whitespace-nowrap">{{ new Date(space.created_at).toLocaleString('zh-CN') }}</td>
                <td>
                  <button v-if="mayOpen(space)" type="button" :aria-label="`进入 ${space.name} 管理`"
                    :aria-disabled="enteringSpace !== undefined" class="editorial-text-action ui-control-target inline-flex whitespace-nowrap"
                    @click="enterManagement(space.id, $event)">{{ enteringSpace === space.id ? '正在核验权限…' : '进入管理' }}</button>
                  <button v-else-if="space.enabled" type="button" :aria-label="`进入 ${space.name} 管理`"
                    class="editorial-text-action ui-control-target whitespace-nowrap" @click="openAuthorization(space.id)">进入管理</button>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
        <div class="flex flex-wrap items-center justify-between gap-2 text-sm">
          <span>共 {{ total }} 个工作空间 · 第 {{ query.page }} / {{ lastPage }} 页</span>
          <div class="flex gap-2">
            <button type="button" class="ghost-button ui-control-target" :disabled="query.page <= 1"
              @click="goTo(query.page - 1)">上一页</button>
            <button type="button" class="ghost-button ui-control-target" :disabled="query.page >= lastPage"
              @click="goTo(query.page + 1)">下一页</button>
          </div>
        </div>
      </template>
    </section>

    <el-dialog v-if="dialogOpen" :model-value="true" title="创建工作空间" append-to-body align-center destroy-on-close
      width="min(560px, calc(100vw - 24px))" :close-on-click-modal="false" :close-on-press-escape="!saving"
      :show-close="!saving" class="glass-dialog" @close="closeCreate">
      <form class="space-y-4" :aria-busy="saving" @submit.prevent="createWorkspace">
        <label class="block space-y-1 text-sm">
          <span>工作空间名称</span>
          <input v-model="name" class="glass-input ui-control-target block w-full" autocomplete="off"
            required maxlength="60" :disabled="saving" />
        </label>
        <p class="text-sm text-muted">创建人登记为首任管理员。新空间从空目录开始，成员资格单独授予。</p>
        <p v-if="commandError" class="editorial-field-error" role="alert">{{ commandError }}</p>
        <div class="flex flex-wrap justify-end gap-2">
          <button type="button" class="ghost-button ui-control-target" :disabled="saving" @click="closeCreate">取消</button>
          <button type="submit" class="liquid-button ui-control-target" :disabled="saving">{{ saving ? '正在创建…' : '确认创建' }}</button>
        </div>
      </form>
    </el-dialog>
  </section>
</template>
