<script setup lang="ts">
import { computed, nextTick, onScopeDispose, reactive, ref, shallowReactive, watch } from 'vue'
import { Check, ChevronDown, ChevronRight, FileCode2, Folder, MoreHorizontal, Search, Share2, X } from 'lucide-vue-next'
import LoadingState from '../../../components/base/LoadingState.vue'
import { scriptTypes, workbenchScripts, type ScriptEntry, type ScriptFolder } from '../../../api/workbench-scripts'

type RenameNode = { kind: 'folder' | 'script'; id: number; name: string }
const props = defineProps<{
  folders: ScriptFolder[]
  active?: ScriptEntry
  locked?: boolean
  touch?: boolean
  rename: (node: RenameNode, name: string) => Promise<void>
}>()
type MenuTarget = { folder: ScriptFolder | null } | { script: ScriptEntry }
const emit = defineEmits<{ open: [script: ScriptEntry]; menu: [event: MouseEvent | KeyboardEvent, target: MenuTarget] }>()
type Scope = 'mine' | 'shared' | number
type Branch = { items: ScriptEntry[]; page: number; total: number; loading: boolean; error?: string }
type Row = { kind: 'scope'; scope: Scope; folder?: ScriptFolder; depth: number }
  | { kind: 'script'; scope: Scope; script: ScriptEntry; depth: number }
  | { kind: 'status'; scope: Scope; depth: number; hasFolders: boolean }

const expanded = reactive(new Set<Scope>(['mine', 'shared']))
const branches = shallowReactive(new Map<Scope, Branch>())
const requests = new Map<Scope, AbortController>()
const keyword = ref(''), search = ref('')
const pageSize = 20
const list = ref<HTMLUListElement>()
const renaming = ref<{ node: RenameNode; name: string; saving: boolean; error: string }>()
let focusedInput: HTMLInputElement | undefined

const children = computed(() => {
  const groups = new Map<number | null, ScriptFolder[]>()
  for (const folder of props.folders) {
    const list = groups.get(folder.parent_id) ?? []
    list.push(folder)
    groups.set(folder.parent_id, list)
  }
  return groups
})
const rows = computed(() => {
  const output: Row[] = []
  function visit(scope: Scope, depth: number, folder?: ScriptFolder) {
    output.push({ kind: 'scope', scope, folder, depth })
    if (!expanded.has(scope)) return
    const folders = scope === 'shared' ? [] : children.value.get(scope === 'mine' ? null : scope) ?? []
    for (const child of folders) visit(child.id, depth + 1, child)
    for (const script of branches.get(scope)?.items ?? []) output.push({ kind: 'script', scope, script, depth: depth + 1 })
    output.push({ kind: 'status', scope, depth: depth + 1, hasFolders: folders.length > 0 })
  }
  visit('mine', 0)
  visit('shared', 0)
  return output
})
const visibleScopes = computed(() => rows.value.flatMap(row => row.kind === 'status' ? [row.scope] : []))

async function read(scope: Scope, next = false) {
  if (requests.has(scope)) return
  const previous = next ? branches.get(scope) : undefined
  const page = (previous?.page ?? 0) + 1
  const controller = new AbortController()
  requests.set(scope, controller)
  const retained = { items: previous?.items ?? [], page: previous?.page ?? 0, total: previous?.total ?? 0 }
  branches.set(scope, { ...retained, loading: true })
  try {
    const response = await workbenchScripts.list({
      view: scope === 'shared' ? 'shared' : 'mine',
      folder_id: typeof scope === 'number' ? scope : undefined,
      q: search.value, page, page_size: pageSize,
    }, controller.signal)
    if (requests.get(scope) !== controller) return
    const entries = new Map(retained.items.map(item => [item.id, item]))
    for (const item of response.items) entries.set(item.id, item)
    branches.set(scope, { items: [...entries.values()], total: response.total, page, loading: false })
  } catch (error) {
    if (requests.get(scope) !== controller) return
    branches.set(scope, { ...retained, loading: false, error: error instanceof Error ? error.message : '读取脚本失败' })
  } finally {
    if (requests.get(scope) === controller) requests.delete(scope)
  }
}

function clear(scope: Scope) {
  requests.get(scope)?.abort()
  requests.delete(scope)
  branches.delete(scope)
}
function reload() {
  const scopes = [...branches.keys()]
  scopes.forEach(clear)
  return Promise.all(visibleScopes.value.map(scope => read(scope)))
}
watch(visibleScopes, scopes => {
  for (const scope of branches.keys()) if (!scopes.includes(scope)) clear(scope)
  for (const scope of scopes) if (!branches.has(scope)) void read(scope)
}, { immediate: true })
watch(search, () => { void reload() })
watch(keyword, value => { if (!value) search.value = '' })
onScopeDispose(() => { for (const scope of branches.keys()) clear(scope) })

function toggle(scope: Scope) {
  if (renaming.value?.saving) return
  renaming.value = undefined
  if (expanded.has(scope)) expanded.delete(scope)
  else expanded.add(scope)
}
function revealFolder(id: number | null) {
  expanded.add('mine')
  const byId = new Map(props.folders.map(folder => [folder.id, folder]))
  while (id !== null && byId.has(id)) {
    expanded.add(id)
    id = byId.get(id)!.parent_id
  }
}
function menu(event: MouseEvent | KeyboardEvent, row: Row) {
  if (event instanceof KeyboardEvent && event.key !== 'ContextMenu' && !(event.shiftKey && event.key === 'F10')) return
  if (renaming.value?.saving) return
  renaming.value = undefined
  if (row.kind === 'script') emit('menu', event, { script: row.script })
  else if (row.kind === 'scope' && row.scope !== 'shared') emit('menu', event, { folder: row.folder ?? null })
}
function menuName(row: Row) {
  if (row.kind === 'script') return row.script.name
  if (row.kind === 'scope' && row.scope !== 'shared') return row.folder?.name ?? '我的脚本'
}
function retry(scope: Scope) { void read(scope, (branches.get(scope)?.page ?? 0) > 0) }
function hasMore(scope: Scope) {
  const branch = branches.get(scope)
  return !!branch && !branch.loading && !branch.error && branch.page * pageSize < branch.total
}
function hasScript(id: number) {
  return [...branches.values()].some(branch => branch.items.some(item => item.id === id))
}
function rowKey(row: Row) {
  return row.kind === 'script' ? `${row.scope}-script-${row.script.id}` : `${row.scope}-${row.kind}`
}
function nodeKey(row: Row) {
  if (row.kind === 'script') return 'script-' + row.script.id
  if (row.kind === 'scope' && row.folder) return 'folder-' + row.folder.id
}
function beginRename(node: RenameNode, revealNew = false) {
  if (revealNew) { keyword.value = ''; search.value = '' }
  renaming.value = { node, name: node.name, saving: false, error: '' }
}
function captureRenameInput(value: unknown) {
  if (!(value instanceof HTMLInputElement) || value === focusedInput) return
  focusedInput = value
  void nextTick(() => {
    if (!value.isConnected) return
    value.focus()
    value.select()
  })
}
async function endRename() {
  const node = renaming.value?.node
  if (!node) return
  renaming.value = undefined
  focusedInput = undefined
  await nextTick()
  list.value?.querySelector<HTMLButtonElement>(`[data-script-node="${node.kind}-${node.id}"] > button`)?.focus({ preventScroll: true })
}
async function saveRename() {
  const entry = renaming.value
  if (!entry || entry.saving || props.locked) return
  const name = entry.name.trim()
  if (!name || Array.from(name).length > 200) { entry.error = '请输入 1 至 200 个字符'; return }
  if (name === entry.node.name) { await endRename(); return }
  entry.saving = true
  entry.error = ''
  try {
    await props.rename(entry.node, name)
    if (renaming.value === entry) await endRename()
  } catch (cause) {
    entry.error = cause instanceof Error ? cause.message : '重命名失败，请重试。'
  } finally { entry.saving = false }
}
function cancelRenameFromKey(event: KeyboardEvent) {
  if (event.isComposing || renaming.value?.saving) return
  event.preventDefault()
  void endRename()
}
function updateScript(item: ScriptEntry) {
  const scope = item.folder_id ?? 'mine'
  const branch = branches.get(scope)
  if (branch) branches.set(scope, { ...branch, items: branch.items.map(script => script.id === item.id ? item : script) })
}
function applySearch() {
  if (renaming.value?.saving) return
  renaming.value = undefined
  search.value = keyword.value.trim()
}
defineExpose({ reload, revealFolder, hasScript, beginRename, updateScript })
</script>

<template>
  <div class="script-tree flex min-h-0 flex-1 flex-col" :class="{ 'script-tree--touch': touch }">
    <form class="script-tree-search" role="search" @submit.prevent="applySearch">
      <button type="submit" class="ghost-button ui-control-target inline-grid place-items-center" aria-label="搜索脚本"><Search :size="16" aria-hidden="true" /></button>
      <input v-model="keyword" type="search" class="min-w-0 flex-1 bg-transparent px-1 text-sm" placeholder="搜索脚本"
        aria-label="搜索已展开的分组和目录" />
    </form>
    <p v-if="search" class="px-3 pb-1 text-xs text-muted" role="status">正在筛选已展开的分组和目录</p>
    <ul ref="list" class="script-tree-list min-h-0 flex-1 overflow-auto" aria-label="脚本目录树">
      <li v-for="row in rows" :key="rowKey(row)" :data-script-node="nodeKey(row)" :style="{ '--tree-padding': Math.min(row.depth, 6) * 14 + 7 + 'px' }">
        <form v-if="renaming && nodeKey(row) === renaming.node.kind + '-' + renaming.node.id" class="script-tree-rename"
          @submit.prevent="saveRename" @keydown.esc.stop="cancelRenameFromKey">
          <div class="flex min-w-0 items-center gap-1">
            <input :ref="captureRenameInput" v-model="renaming.name" class="glass-input min-w-0 flex-1 !px-1.5 !py-1"
              :aria-label="renaming.node.kind === 'folder' ? '重命名目录' : '重命名脚本'"
              :aria-invalid="!!renaming.error" :readonly="renaming.saving" @input="renaming.error = ''" />
            <button type="submit" class="ghost-button ui-icon-button" aria-label="保存名称" title="保存名称"
              :disabled="locked || renaming.saving"><Check :size="14" aria-hidden="true" /></button>
            <button type="button" class="ghost-button ui-icon-button" aria-label="取消重命名" title="取消重命名"
              :disabled="renaming.saving" @click="endRename"><X :size="14" aria-hidden="true" /></button>
          </div>
          <p v-if="renaming.error" class="pt-1 text-xs text-danger" role="alert">{{ renaming.error }}</p>
        </form>
        <button v-else-if="row.kind === 'scope'" type="button" class="script-tree-row" :class="{ 'script-tree-root': !row.folder }"
          :title="row.folder?.name" :aria-expanded="expanded.has(row.scope)" @click="toggle(row.scope)"
          @contextmenu="menu($event, row)" @keydown="menu($event, row)">
          <ChevronDown v-if="expanded.has(row.scope)" :size="14" class="shrink-0" aria-hidden="true" />
          <ChevronRight v-else :size="14" class="shrink-0" aria-hidden="true" />
          <Folder v-if="row.folder" :size="15" class="shrink-0 text-muted" aria-hidden="true" />
          <span class="min-w-0 truncate">{{ row.folder?.name ?? (row.scope === 'mine' ? '我的脚本' : '分享给我') }}</span>
        </button>
        <button v-else-if="row.kind === 'script'" type="button" class="script-tree-row script-tree-file" :aria-current="active?.id === row.script.id || undefined"
          :aria-disabled="locked || undefined" :title="row.script.owner_name ? `${row.script.name} · ${row.script.owner_name}` : row.script.name"
          @click="emit('open', row.script)" @contextmenu="menu($event, row)" @keydown="menu($event, row)">
          <Share2 v-if="row.script.shared || row.script.read_only" :size="15" class="shrink-0 text-accent" aria-label="已分享" />
          <FileCode2 v-else :size="15" class="shrink-0 text-muted" aria-hidden="true" />
          <span class="min-w-0 truncate">{{ row.script.name }}</span>
          <span class="ml-auto shrink-0 text-xs text-muted">{{ row.script.read_only ? '只读' : scriptTypes[row.script.script_type] }}</span>
        </button>
        <div v-else class="script-tree-status text-xs text-muted">
          <LoadingState v-if="branches.get(row.scope)?.loading" variant="inline" label="正在读取脚本…" />
          <template v-else-if="branches.get(row.scope)?.error">
            <p class="break-words text-danger" role="alert">{{ branches.get(row.scope)?.error }}</p>
            <button type="button" class="ghost-button ui-control-target" @click="retry(row.scope)">重试读取脚本</button>
          </template>
          <button v-else-if="hasMore(row.scope)" type="button" class="ghost-button ui-control-target" @click="read(row.scope, true)">加载更多</button>
          <p v-else-if="!branches.get(row.scope)?.items.length && (search || !row.hasFolders)">{{ search ? '没有匹配的脚本' : '暂无脚本' }}</p>
        </div>
        <button v-if="touch && menuName(row) && (!renaming || nodeKey(row) !== renaming.node.kind + '-' + renaming.node.id)"
          type="button" class="script-tree-menu-trigger ghost-button ui-icon-button" :aria-label="`${menuName(row)} 操作`"
          aria-haspopup="menu" :disabled="locked || renaming?.saving" @click="menu($event, row)">
          <MoreHorizontal :size="18" aria-hidden="true" />
        </button>
      </li>
    </ul>
  </div>
</template>

<style scoped>
.script-tree-search { display: flex; align-items: center; margin: 10px 12px 6px; border: 1px solid var(--db-border); border-radius: var(--db-radius-control); background: var(--db-surface-data); }
.script-tree-search button { border: 0; min-width: 38px; }
.script-tree-search input { min-height: 36px; }
.script-tree-list { list-style: none; padding: 0 6px 8px; margin: 0; }
.script-tree-row { display: flex; align-items: center; gap: 7px; width: 100%; min-height: 34px; padding: 3px 7px 3px var(--tree-padding); border: 0; border-left: 2px solid transparent; text-align: left; }
.script-tree-root { font-weight: 650; border-top: 1px solid var(--db-border); }
.script-tree-file { padding-left: calc(var(--tree-padding) + 14px); }
.script-tree-row:hover { background: var(--db-interactive-hover); }
.script-tree-row[aria-current='true'] { border-left-color: var(--db-accent); color: var(--db-accent); background: var(--db-selected); font-weight: 650; }
.script-tree-status { padding-inline-start: calc(var(--tree-padding) + 10px); }
.script-tree-status > p { padding: 5px 0; }
.script-tree-rename { padding: 3px 2px 3px var(--tree-padding); }
.script-tree-rename input { min-height: 30px; }
.script-tree-rename .ui-icon-button { min-width: 28px; min-height: 30px; padding: 3px; }
.script-tree--touch li:has(> .script-tree-menu-trigger) {
  display: grid;
  min-width: 0;
  grid-template-columns: minmax(0, 1fr) 44px;
  align-items: center;
}
.script-tree--touch .script-tree-row { min-width: 0; min-height: 44px; }
.script-tree--touch .script-tree-search > button,
.script-tree--touch .script-tree-rename .ui-icon-button,
.script-tree-menu-trigger { min-width: 44px; min-height: 44px; }
.script-tree--touch :is(.script-tree-search, .script-tree-rename) input { min-height: 44px; }
.script-tree-menu-trigger { background: transparent; border-color: transparent; box-shadow: none; }
</style>
