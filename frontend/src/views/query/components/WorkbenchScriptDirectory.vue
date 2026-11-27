<script setup lang="ts">
import { computed, nextTick, onScopeDispose, ref, useId, watch } from 'vue'
import { ElMessageBox } from 'element-plus'
import { ChevronRight, FileCode2, Folder, MoreHorizontal, Share2 } from 'lucide-vue-next'
import LoadingState from '../../../components/base/LoadingState.vue'
import { workbenchScripts, scriptTypes, type ScriptEntry, type ScriptFolder, type ScriptType, type WorkbenchScript } from '../../../api/workbench-scripts'
import { ApiRequestError } from '../../../utils/request'
import { useReportRun } from '../../report/useReportRun'
import WorkbenchScriptTree from './WorkbenchScriptTree.vue'

const props = defineProps<{
  active?: WorkbenchScript
  locked?: boolean
  desktop?: boolean
  touch?: boolean
  visible?: boolean
  prepare: () => Promise<boolean>
}>()
const emit = defineEmits<{ activate: [item: WorkbenchScript, keepResources?: boolean]; updated: [item: WorkbenchScript] }>()
type MenuTarget = { folder: ScriptFolder | null } | { script: ScriptEntry }
const folders = useReportRun<{ items: ScriptFolder[] }>()
const scripts = useReportRun<{ items: ScriptEntry[]; total: number }>()
const people = useReportRun<Awaited<ReturnType<typeof loadPeople>>>()
const view = ref<'mine' | 'shared'>('mine')
const folder = ref<ScriptFolder | null>(null)
const page = ref(1)
const keyword = ref('')
const query = ref('')
const busy = ref(false)
const error = ref('')
const menu = ref<{ target: MenuTarget; left: number; top: number; type: boolean }>()
const menuElement = ref<HTMLElement>()
const menuTypeTrigger = ref<HTMLButtonElement>()
const submenuElement = ref<HTMLElement>()
const submenuPosition = ref({ left: 0, top: 0 })
const submenuId = useId()
let menuTrigger: HTMLElement | undefined
const share = ref<{ script: ScriptEntry; revokeOnly: boolean }>()
const moving = ref<ScriptEntry>()
const destination = ref<number | null>(null)
const pageSize = 20
const tree = ref<InstanceType<typeof WorkbenchScriptTree>>()
const folderEntries = computed(() => folders.state.value.status === 'success' ? folders.state.value.data.items : [])
const sideMenu = computed(() => props.desktop && !props.touch)
let activeScope = true
onScopeDispose(() => { activeScope = false })

const directory = computed(() => {
  const byParent = new Map<number | null, ScriptFolder[]>()
  for (const entry of folderEntries.value) {
    const list = byParent.get(entry.parent_id) ?? []
    list.push(entry)
    byParent.set(entry.parent_id, list)
  }
  const ordered: { entry: ScriptFolder; depth: number }[] = []
  const pending = [...(byParent.get(null) ?? [])].reverse().map(entry => ({ entry, depth: 0 }))
  while (pending.length) {
    const item = pending.pop()!
    ordered.push(item)
    const children = byParent.get(item.entry.id) ?? []
    for (const child of [...children].reverse()) pending.push({ entry: child, depth: item.depth + 1 })
  }
  return ordered
})

function loadFolders() { return folders.run('script-folders', signal => workbenchScripts.folders(signal)) }
function loadScripts() {
  const params = { view: view.value, folder_id: view.value === 'mine' ? folder.value?.id : undefined, q: query.value, page: page.value, page_size: pageSize }
  return scripts.run(JSON.stringify(params), signal => workbenchScripts.list(params, signal))
}
watch(() => [view.value, folder.value?.id, page.value, query.value, props.active?.version, props.desktop], () => {
  if (!props.desktop) void loadScripts()
}, { immediate: true })
watch(() => props.active?.id, async (id, previous) => {
  if (!props.desktop || id === undefined || id === previous || props.active?.read_only) return
  await nextTick()
  if (tree.value?.hasScript(id)) return
  tree.value?.revealFolder(props.active?.folder_id ?? null)
  void tree.value?.reload()
})
void loadFolders()
function refreshScripts() { return props.desktop ? tree.value?.reload() : loadScripts() }
function showFolder(next: ScriptFolder | null) {
  if (view.value === 'mine' && folder.value?.id === next?.id && page.value === 1) void loadScripts()
  page.value = 1
  view.value = 'mine'
  folder.value = next
}
function showShared() {
  if (view.value === 'shared' && page.value === 1) void loadScripts()
  view.value = 'shared'
  page.value = 1
}

function openMenu(event: MouseEvent | KeyboardEvent, target: MenuTarget) {
  if (busy.value || props.locked || !(event.currentTarget instanceof HTMLElement)) return
  event.preventDefault()
  menuTrigger = event.currentTarget
  const anchor = menuTrigger.getBoundingClientRect()
  const pointer = event instanceof MouseEvent && event.type === 'contextmenu'
  menu.value = {
    target, type: false,
    left: Math.max(8, Math.min(pointer ? event.clientX : anchor.left, window.innerWidth - 198)),
    top: Math.max(8, Math.min(pointer ? event.clientY : anchor.bottom, window.innerHeight - 216)),
  }
  void nextTick(() => menuElement.value?.querySelector<HTMLButtonElement>('button')?.focus())
}
function closeMenu(restore = true) {
  menu.value = undefined
  if (restore) menuTrigger?.focus({ preventScroll: true })
}
watch([() => props.visible, () => props.touch], () => {
  if (menu.value) closeMenu(false)
})
function submenu(value: boolean, focus = true) {
  if (!menu.value || !('folder' in menu.value.target)) return
  if (value) {
    const menuBox = menuElement.value?.getBoundingClientRect()
    const anchor = menuTypeTrigger.value?.getBoundingClientRect()
    const width = 176
    const openRight = !!menuBox && menuBox.right + width + 8 <= window.innerWidth
    submenuPosition.value = sideMenu.value && menuBox && anchor
      ? { left: openRight ? menuBox.right - 1 : Math.max(8, menuBox.left - width + 1), top: Math.max(8, Math.min(anchor.top, window.innerHeight - 156)) }
      : { left: menu.value.left, top: menu.value.top }
  }
  menu.value.type = value
  if (focus) void nextTick(() => {
    const item = value ? submenuElement.value?.querySelector<HTMLButtonElement>('button') : menuTypeTrigger.value
    item?.focus()
  })
}
function menuKeys(event: KeyboardEvent) {
  if (!(event.currentTarget instanceof HTMLElement)) return
  const items = [...event.currentTarget.querySelectorAll<HTMLButtonElement>('button')]
  if (!items.length) return
  const index = items.findIndex(item => item === document.activeElement)
  if (event.key === 'ArrowRight' && (document.activeElement?.hasAttribute('aria-haspopup'))) submenu(true)
  else if (event.key === 'ArrowLeft' && menu.value?.type) submenu(false)
  else if (event.key === 'Escape' && menu.value?.type) submenu(false)
  else if (event.key === 'Escape' || event.key === 'Tab') closeMenu()
  else if (event.key === 'ArrowDown') items[(index + 1) % items.length]?.focus()
  else if (event.key === 'ArrowUp') items[(index + items.length - 1) % items.length]?.focus()
  else return
  if (event.key !== 'Tab') event.preventDefault()
}
function keyboardMenu(event: KeyboardEvent, target: MenuTarget) {
  if (event.key === 'ContextMenu' || (event.shiftKey && event.key === 'F10')) openMenu(event, target)
}

async function perform(action: () => Promise<void>) {
  if (!activeScope || busy.value || props.locked) return
  busy.value = true
  error.value = ''
  try { await action() }
  catch (cause) { error.value = cause instanceof Error ? cause.message : '脚本操作失败，请重试。' }
  finally { busy.value = false }
}
async function askName(title: string, initial = '') {
  try {
    const result = await ElMessageBox.prompt('名称', title, {
      inputValue: initial, inputValidator: value => !!value.trim() && Array.from(value.trim()).length <= 200 || '请输入 1 至 200 个字符',
      confirmButtonText: '确定', cancelButtonText: '取消', closeOnClickModal: false,
    })
    return result.value.trim()
  } catch { return undefined }
}

function nextName(base: string, names: string[]) {
  const used = new Set(names.map(name => name.toLowerCase()))
  let number = 1
  let candidate = base
  while (used.has(candidate.toLowerCase())) candidate = `${base} (${++number})`
  return candidate
}

async function createDefaultFolder(parentId: number | null) {
  for (let attempt = 0; ; attempt++) {
    const { items } = await workbenchScripts.folders()
    if (!activeScope) return
    const name = nextName('新建目录', items.filter(item => item.parent_id === parentId).map(item => item.name))
    try { return await workbenchScripts.createFolder(name, parentId) }
    catch (error) {
      if (attempt === 2 || !(error instanceof ApiRequestError && error.status === 409)) throw error
    }
  }
}

async function defaultScriptName(type: ScriptType, parentId: number | null) {
  const base = `新建 ${scriptTypes[type]} 脚本`
  const names: string[] = []
  const size = 100
  for (let page = 1; activeScope; page++) {
    const result = await workbenchScripts.list({ view: 'mine', folder_id: parentId ?? undefined, q: base, page, page_size: size })
    names.push(...result.items.map(item => item.name))
    if (page * size >= result.total || result.items.length === 0) break
  }
  return nextName(base, names)
}

async function renameNode(node: { kind: 'folder' | 'script'; id: number }, name: string) {
  if (!activeScope || props.locked || busy.value) throw new Error('当前无法修改名称，请稍后重试。')
  busy.value = true
  try {
    if (node.kind === 'folder') {
      await workbenchScripts.renameFolder(node.id, name)
      if (activeScope) await loadFolders()
    } else {
      const current = await workbenchScripts.get(node.id)
      if (!activeScope) return
      if (current.read_only || current.version === undefined) throw new Error('当前脚本不可重命名。')
      const updated = await workbenchScripts.save(node.id, current.version, { name })
      if (!activeScope) return
      tree.value?.updateScript(updated)
      emit('updated', updated)
    }
  } finally { busy.value = false }
}
function openScript(item: ScriptEntry) {
  return perform(async () => {
    if (!await props.prepare() || !activeScope) return
    const script = await workbenchScripts.get(item.id)
    if (activeScope) emit('activate', script)
  })
}
function createFolder(rename = false) {
  const target = menu.value?.target
  if (!target || !('folder' in target)) return
  const parent = target.folder
  closeMenu()
  if (props.desktop && rename && parent) {
    tree.value?.beginRename({ kind: 'folder', id: parent.id, name: parent.name })
    return
  }
  void perform(async () => {
    if (props.desktop) {
      const created = await createDefaultFolder(parent?.id ?? null)
      if (!created || !activeScope) return
      await loadFolders()
      await nextTick()
      tree.value?.revealFolder(created.parent_id)
      tree.value?.beginRename({ kind: 'folder', id: created.id, name: created.name }, true)
      return
    }
    const name = await askName(rename ? '重命名目录' : '新建目录', rename ? parent?.name : '')
    if (!name || !activeScope) return
    const updated = rename && parent ? await workbenchScripts.renameFolder(parent.id, name)
      : await workbenchScripts.createFolder(name, parent?.id ?? null)
    showFolder(updated)
    await loadFolders()
  })
}
async function createScriptInFolder(type: ScriptType, parent: ScriptFolder | null) {
  const folderId = parent?.id ?? null
  const chosenName = props.desktop ? undefined : await askName(`新建 ${scriptTypes[type]} 脚本`)
  if ((!props.desktop && !chosenName) || !await props.prepare() || !activeScope) return
  const name = chosenName ?? await defaultScriptName(type, folderId)
  if (!activeScope || props.locked) return
  const created = await workbenchScripts.create({ name, folder_id: folderId, script_type: type, content: '' })
  if (!activeScope) return
  if (props.desktop && tree.value) {
    tree.value.revealFolder(folderId)
    tree.value.beginRename({ kind: 'script', id: created.id, name: created.name }, true)
    await nextTick()
    await tree.value?.reload()
  }
  if (!props.desktop) showFolder(parent)
  if (!activeScope) return
  emit('activate', created, props.desktop && props.touch ? true : undefined)
  if (!props.desktop) await loadScripts()
}

function createScript(type: ScriptType) {
  const target = menu.value?.target
  if (!target || !('folder' in target)) return
  closeMenu()
  void perform(() => createScriptInFolder(type, target.folder))
}

defineExpose({ busy })

function scriptCommand(command: 'share' | 'revoke' | 'move' | 'copy' | 'rename') {
  const target = menu.value?.target
  if (!target || !('script' in target)) return
  const item = target.script
  closeMenu()
  if (command === 'rename') {
    if (!item.read_only) tree.value?.beginRename({ kind: 'script', id: item.id, name: item.name })
  } else if (command === 'copy') {
    void perform(async () => { await navigator.clipboard.writeText((await workbenchScripts.get(item.id)).content) })
  } else if (command === 'move') {
    destination.value = item.folder_id ?? null
    moving.value = item
  } else {
    share.value = { script: item, revokeOnly: command === 'revoke' }
    void reloadPeople()
  }
}

function move() {
  const item = moving.value
  if (!item) return
  void perform(async () => {
    const current = await workbenchScripts.get(item.id)
    if (!activeScope || current.version === undefined || current.read_only) return
    const updated = await workbenchScripts.save(item.id, current.version, { folder_id: destination.value })
    emit('updated', updated)
    moving.value = undefined
    tree.value?.revealFolder(updated.folder_id ?? null)
    await refreshScripts()
  })
}

async function loadPeople(id: number, signal: AbortSignal) {
  const [list, readers] = await Promise.all([workbenchScripts.readers(id, true, signal), workbenchScripts.readers(id, false, signal)])
  return { candidates: list.items, readers: readers.items }
}
function reloadPeople() {
  const id = share.value?.script.id
  if (id) return people.run(String(id), signal => loadPeople(id, signal))
}
function changeReader(id: number, grant: boolean) {
  const script = share.value?.script
  if (!script) return
  void perform(async () => {
    await workbenchScripts.grant(script.id, id, grant)
    await Promise.all([reloadPeople(), refreshScripts()])
  })
}
</script>

<template>
  <div v-if="desktop" class="flex h-full min-h-0 flex-col text-sm">
    <header class="flex shrink-0 items-center gap-2 border-b border-stroke px-3 py-1.5">
      <h2 class="mr-auto font-semibold text-content">脚本</h2>
      <slot name="toolbar" />
    </header>
    <p v-if="error" class="px-3 pt-2 text-xs text-danger" role="alert">{{ error }}</p>
    <div v-if="folders.state.value.status === 'error'" class="px-3 pt-2 text-xs">
      <p class="text-danger" role="alert">{{ folders.state.value.message }}</p>
      <button type="button" class="ghost-button ui-control-target" @click="loadFolders">重试读取目录</button>
    </div>
    <WorkbenchScriptTree ref="tree" :folders="folderEntries" :active="active" :locked="busy || locked" :rename="renameNode" :touch="touch"
      @open="openScript" @menu="openMenu" />
  </div>
  <div v-else class="space-y-2 text-sm" aria-label="脚本目录">
    <div class="flex flex-wrap items-center gap-1">
      <button type="button" class="ghost-button ui-control-target" :aria-pressed="view==='mine' && !folder"
        @click="showFolder(null)" @contextmenu="openMenu($event, { folder: null })" @keydown="keyboardMenu($event, { folder: null })">我的脚本</button>
      <button type="button" class="ghost-button ui-control-target px-1" aria-label="我的脚本操作" @click="openMenu($event, { folder: null })"><MoreHorizontal class="size-4" aria-hidden="true" /></button>
      <button type="button" class="ghost-button ui-control-target" :aria-pressed="view==='shared'" @click="showShared">分享给我</button>
    </div>
    <p v-if="folders.state.value.status === 'error'" class="text-xs text-danger" role="alert">{{ folders.state.value.message }}</p>
    <ul v-if="view==='mine'" aria-label="我的目录">
      <li v-for="{ entry, depth } in directory" :key="entry.id" class="flex items-center gap-1" :style="{ paddingInlineStart: Math.min(depth, 6) * 11 + 'px' }">
        <button type="button" class="query-target-option min-w-0 flex-1" :aria-pressed="folder?.id===entry.id"
          @click="showFolder(entry)" @contextmenu="openMenu($event, { folder: entry })" @keydown="keyboardMenu($event, { folder: entry })">
          <Folder class="size-4 shrink-0 text-muted" aria-hidden="true" /><span class="truncate">{{ entry.name }}</span>
        </button>
        <button type="button" class="ghost-button ui-control-target px-1" :aria-label="`${entry.name} 目录操作`" @click="openMenu($event, { folder: entry })"><MoreHorizontal class="size-4" aria-hidden="true" /></button>
      </li>
    </ul>
    <form class="flex flex-wrap items-end gap-1" @submit.prevent="query=keyword.trim(); page=1">
      <label class="min-w-0 flex-1 text-xs">搜索脚本<input v-model="keyword" class="glass-input mt-1 block w-full" /></label>
      <button class="ghost-button ui-control-target text-xs">搜索</button>
    </form>
    <p v-if="error" class="break-words text-xs text-danger" role="alert">{{ error }}</p>
    <LoadingState v-if="scripts.state.value.status==='loading'" variant="inline" label="正在读取脚本…" />
    <template v-else-if="scripts.state.value.status==='error'">
      <p class="text-xs text-danger" role="alert">{{ scripts.state.value.message }}</p>
      <button type="button" class="ghost-button ui-control-target" @click="loadScripts">重试读取脚本</button>
    </template>
    <template v-else-if="scripts.state.value.status==='success'">
      <ul v-if="scripts.state.value.data.items.length" aria-label="脚本列表">
        <li v-for="item in scripts.state.value.data.items" :key="item.id" class="flex items-center gap-1">
          <button type="button" class="query-target-option min-w-0 flex-1" :aria-pressed="active?.id===item.id" :disabled="busy || locked"
            @click="openScript(item)" @contextmenu="openMenu($event, { script: item })" @keydown="keyboardMenu($event, { script: item })">
            <Share2 v-if="item.shared || item.read_only" class="size-4 shrink-0 text-accent" aria-label="已分享" />
            <FileCode2 v-else class="size-4 shrink-0 text-muted" aria-hidden="true" />
            <span class="min-w-0">
              <span class="block truncate">{{ item.name }}</span>
              <span class="block truncate text-xs text-muted">{{ scriptTypes[item.script_type] }}<template v-if="item.read_only"> · {{ item.owner_name }} · 只读</template></span>
            </span>
          </button>
          <button type="button" class="ghost-button ui-control-target px-1" :aria-label="`${item.name} 脚本操作`" @click="openMenu($event, { script: item })"><MoreHorizontal class="size-4" aria-hidden="true" /></button>
        </li>
      </ul>
      <p v-else class="text-xs text-muted">当前目录暂无匹配的脚本。</p>
      <div v-if="scripts.state.value.data.total > pageSize" class="flex items-center gap-1 text-xs">
        <button type="button" class="ghost-button ui-control-target" :disabled="page===1" @click="page--">上一页</button>
        <span>第 {{ page }} 页</span>
        <button type="button" class="ghost-button ui-control-target" :disabled="page*pageSize>=scripts.state.value.data.total" @click="page++">下一页</button>
      </div>
    </template>
  </div>

  <Teleport to="body">
    <div v-if="menu" class="fixed inset-0 z-[2500]" @pointerdown.self="closeMenu()">
      <div v-show="sideMenu || !menu.type" ref="menuElement" role="menu" aria-label="脚本操作" class="glass-panel absolute grid w-44 gap-1 p-1 shadow-lg"
        :class="{ 'desktop-script-menu': sideMenu }"
        :style="{ left: menu.left+'px', top: menu.top+'px' }" @keydown="menuKeys">
        <template v-if="'folder' in menu.target">
          <button type="button" role="menuitem" class="ghost-button ui-control-target text-left" @pointerenter="sideMenu && submenu(false, false)" @click="createFolder()">新建目录</button>
          <button ref="menuTypeTrigger" type="button" role="menuitem" class="ghost-button ui-control-target flex items-center justify-between text-left"
            aria-haspopup="menu" :aria-controls="submenuId" :aria-expanded="menu.type" @pointerenter="sideMenu && submenu(true, false)" @click="submenu(true)">新建脚本 <ChevronRight :size="15" aria-hidden="true" /></button>
          <button v-if="menu.target.folder" type="button" role="menuitem" class="ghost-button ui-control-target text-left"
            @pointerenter="sideMenu && submenu(false, false)" @click="createFolder(true)">重命名目录</button>
        </template>
        <template v-else-if="menu.target.script.read_only">
          <button type="button" role="menuitem" class="ghost-button ui-control-target text-left" @click="scriptCommand('copy')">复制脚本</button>
        </template>
        <template v-else>
          <button v-if="desktop" type="button" role="menuitem" class="ghost-button ui-control-target text-left" @click="scriptCommand('rename')">重命名</button>
          <button type="button" role="menuitem" class="ghost-button ui-control-target text-left" @click="scriptCommand('share')">分享</button>
          <button v-if="menu.target.script.shared" type="button" role="menuitem" class="ghost-button ui-control-target text-left" @click="scriptCommand('revoke')">取消分享</button>
          <button type="button" role="menuitem" class="ghost-button ui-control-target text-left" @click="scriptCommand('move')">移动</button>
        </template>
      </div>
      <div v-if="menu.type" :id="submenuId" ref="submenuElement" role="menu" aria-label="选择脚本类型" class="glass-panel absolute grid w-44 gap-1 p-1 shadow-lg"
        :class="{ 'desktop-script-menu': sideMenu }"
        :style="{ left: submenuPosition.left+'px', top: submenuPosition.top+'px' }" @keydown="menuKeys">
        <button v-for="(label, type) in scriptTypes" :key="type" type="button" role="menuitem" class="ghost-button ui-control-target text-left" @click="createScript(type)">{{ label }} 脚本</button>
      </div>
    </div>
  </Teleport>

  <el-dialog v-if="moving" :model-value="true" title="移动脚本" width="min(90vw, 440px)" :close-on-click-modal="false" @close="moving=undefined">
    <label class="block text-sm">目标目录
      <select v-model="destination" class="glass-input mt-1 block w-full">
        <option :value="null">我的脚本</option>
        <option v-for="{ entry, depth } in directory" :key="entry.id" :value="entry.id">{{ '　'.repeat(depth) }}{{ entry.name }}</option>
      </select>
    </label>
    <p v-if="error" role="alert" class="mt-2 text-sm text-danger">{{ error }}</p>
    <template #footer><button type="button" class="liquid-button ui-control-target" :disabled="busy" @click="move">确认移动</button></template>
  </el-dialog>

  <el-dialog v-if="share" :model-value="true" :title="share.revokeOnly ? '取消脚本分享' : '分享脚本'" width="min(90vw, 490px)"
    :close-on-click-modal="false" @close="share=undefined; people.reset()">
    <LoadingState v-if="people.state.value.status==='loading'" variant="inline" label="正在读取成员…" />
    <template v-else-if="people.state.value.status==='error'">
      <p class="text-sm text-danger" role="alert">{{ people.state.value.message }}</p>
      <button type="button" class="ghost-button ui-control-target" @click="reloadPeople">重试读取成员</button>
    </template>
    <template v-else-if="people.state.value.status==='success'">
      <p class="mb-2 text-xs text-muted">获邀成员可以查看并复制该脚本的已保存文本。</p>
      <ul class="max-h-56 overflow-auto">
        <li v-for="person in share.revokeOnly ? people.state.value.data.readers : people.state.value.data.candidates" :key="person.user_id" class="flex items-center justify-between gap-2 py-1">
          <span class="break-all">{{ person.display_name }}</span>
          <button v-if="people.state.value.data.readers.some(item => item.user_id===person.user_id)" type="button" class="ghost-button ui-control-target" :disabled="busy" @click="changeReader(person.user_id,false)">撤回</button>
          <button v-else type="button" class="ghost-button ui-control-target" :disabled="busy" @click="changeReader(person.user_id,true)">授予只读</button>
        </li>
      </ul>
      <p v-if="!(share.revokeOnly ? people.state.value.data.readers : people.state.value.data.candidates).length" class="text-sm text-muted">没有符合条件的成员。</p>
    </template>
    <p v-if="error" role="alert" class="text-sm text-danger">{{ error }}</p>
  </el-dialog>
</template>

<style scoped>
.desktop-script-menu { gap: 2px; }
.desktop-script-menu > [role='menuitem'] {
  display: flex;
  align-items: center;
  justify-content: flex-start;
  min-height: 32px;
  border-color: transparent;
  background: transparent;
  box-shadow: none;
  text-align: left;
}
.desktop-script-menu > [aria-haspopup='menu'] { justify-content: space-between; }
.desktop-script-menu > [role='menuitem']:hover,
.desktop-script-menu > [role='menuitem']:focus-visible,
.desktop-script-menu > [aria-expanded='true'] { background: var(--db-interactive-hover); }
</style>
