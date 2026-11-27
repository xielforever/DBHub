<script setup lang="ts">
import { computed, onMounted, onScopeDispose, ref, useId } from 'vue'
import { workbenchScripts, type ScriptFolder } from '../../../api/workbench-scripts'
import LoadingState from '../../../components/base/LoadingState.vue'
import { useReportRun } from '../../report/useReportRun'

const props = defineProps<{
  initialName: string
  initialFolderId: number | null
  save: (name: string, folderId: number | null) => Promise<void>
}>()
const emit = defineEmits<{ close: [saved: boolean] }>()

const open = ref(true)
const name = ref(props.initialName)
const folderId = ref(props.initialFolderId)
const nameField = ref<HTMLInputElement>()
const title = ref<HTMLElement>()
const formId = useId()
const nameId = useId()
const nameErrorId = useId()
const folderIdLabel = useId()
const folderNoteId = useId()
const nameError = ref('')
const saveError = ref('')
const missingFolder = ref(false)
const saving = ref(false)
const folders = useReportRun<{ items: ScriptFolder[] }>()
let saved = false
let disposed = false

const options = computed(() => {
  const state = folders.state.value
  if (state.status !== 'success') return []
  const byId = new Map(state.data.items.map(folder => [folder.id, folder]))
  return state.data.items.map(folder => {
    const names = [folder.name]
    const visited = new Set([folder.id])
    let parentId = folder.parent_id
    while (parentId !== null && !visited.has(parentId)) {
      const parent = byId.get(parentId)
      if (!parent) break
      visited.add(parentId)
      names.push(parent.name)
      parentId = parent.parent_id
    }
    names.push('我的脚本')
    return { id: folder.id, path: names.reverse().join(' / ') }
  }).sort((left, right) => left.path.localeCompare(right.path))
})
const directoryReady = computed(() => folders.state.value.status === 'success')

const viewportStyle = ref<Record<string, string>>()
function measureViewport() {
  const viewport = window.visualViewport
  viewportStyle.value = viewport ? {
    '--script-save-viewport-height': `${viewport.height}px`,
    '--script-save-viewport-top': `${viewport.offsetTop}px`,
  } : undefined
}

onMounted(() => {
  measureViewport()
  window.visualViewport?.addEventListener('resize', measureViewport)
  window.visualViewport?.addEventListener('scroll', measureViewport)
  void loadFolders()
})
onScopeDispose(() => {
  disposed = true
  window.visualViewport?.removeEventListener('resize', measureViewport)
  window.visualViewport?.removeEventListener('scroll', measureViewport)
})

async function loadFolders() {
  if (saving.value || !open.value) return
  saveError.value = ''
  const loaded = await folders.run('script-save-folders', signal => workbenchScripts.folders(signal))
  if (!loaded || !open.value || folderId.value === null || options.value.some(option => option.id === folderId.value)) return
  folderId.value = null
  missingFolder.value = true
}

function focusOnOpen() {
  const focused = document.activeElement
  if (focused?.closest('.script-save-dialog') && !focused.matches('.script-save-dialog')) return
  if (window.matchMedia('(max-width: 735px), (pointer: coarse)').matches) {
    title.value?.focus({ preventScroll: true })
  } else {
    nameField.value?.focus({ preventScroll: true })
    nameField.value?.select()
  }
}

function cancel(done?: () => void) {
  if (saving.value) return
  folders.reset()
  open.value = false
  done?.()
}

async function submit() {
  if (!open.value || saving.value || !directoryReady.value) return
  const trimmed = name.value.trim()
  if (!trimmed || Array.from(trimmed).length > 200 || trimmed.includes('\0')) {
    nameError.value = '请输入 1 至 200 个字符的有效脚本名称。'
    nameField.value?.focus()
    return
  }

  saveError.value = ''
  saving.value = true
  try {
    await props.save(trimmed, folderId.value)
    if (disposed) return
    saved = true
    open.value = false
  } catch (error) {
    if (!disposed) saveError.value = error instanceof Error ? error.message : '保存脚本失败，请重试。'
  } finally {
    if (!disposed) saving.value = false
  }
}
</script>

<template>
  <el-dialog v-model="open" title="另存为脚本" width="min(480px, calc(100vw - 24px))" class="glass-dialog script-save-dialog"
    :style="viewportStyle" align-center :close-on-click-modal="false" :close-on-press-escape="!saving" :show-close="!saving"
    :before-close="cancel" @opened="focusOnOpen" @closed="emit('close', saved)">
    <template #header="{ titleId, titleClass }">
      <h2 :id="titleId" ref="title" :class="titleClass" tabindex="-1">另存为脚本</h2>
    </template>
    <form :id="formId" class="grid gap-3" :aria-busy="saving" @submit.prevent="submit">
      <div class="grid gap-1">
        <label :for="nameId" class="text-sm">脚本名称</label>
        <input :id="nameId" ref="nameField" v-model="name" class="glass-input w-full min-w-0 ui-control-target"
          :readonly="saving" :aria-invalid="!!nameError" :aria-describedby="nameError ? nameErrorId : undefined"
          @input="nameError = ''" />
        <p v-if="nameError" :id="nameErrorId" class="text-sm text-danger" role="alert">{{ nameError }}</p>
      </div>
      <div class="grid gap-1">
        <label :for="folderIdLabel" class="text-sm">保存位置</label>
        <select :id="folderIdLabel" v-model="folderId" class="glass-input w-full min-w-0 ui-control-target"
          :disabled="!directoryReady || saving" :aria-describedby="missingFolder ? folderNoteId : undefined"
          @change="missingFolder = false">
          <option :value="null">我的脚本（根目录）</option>
          <option v-for="option in options" :key="option.id" :value="option.id">{{ option.path }}</option>
        </select>
        <LoadingState v-if="folders.state.value.status === 'loading'" variant="inline" label="正在读取脚本目录…" />
        <div v-else-if="folders.state.value.status === 'error'" class="grid justify-items-start gap-1">
          <p class="text-sm text-danger" role="alert">读取脚本目录失败。{{ folders.state.value.message }}</p>
          <button type="button" class="ghost-button ui-control-target" @click="loadFolders">重试读取目录</button>
        </div>
        <p v-else-if="missingFolder" :id="folderNoteId" class="text-sm text-warning" role="status">
          原选目录已不可用，当前选中「我的脚本（根目录）」。
        </p>
      </div>
      <div v-if="saveError" class="grid justify-items-start gap-1">
        <p class="text-sm text-danger" role="alert">{{ saveError }}</p>
        <button type="button" class="ghost-button ui-control-target" @click="loadFolders">刷新目录</button>
      </div>
    </form>
    <template #footer>
      <div class="flex flex-wrap justify-end gap-2">
        <button type="button" class="ghost-button ui-control-target" :disabled="saving" @click="cancel()">取消</button>
        <button type="submit" :form="formId" class="liquid-button ui-control-target"
          :disabled="!directoryReady" :aria-disabled="saving || undefined">{{ saving ? '保存中…' : '保存' }}</button>
      </div>
    </template>
  </el-dialog>
</template>

<style scoped>
:global(.el-dialog.script-save-dialog) {
  display: flex;
  flex-direction: column;
  max-height: calc(100dvh - 32px);
}
:global(.script-save-dialog .el-dialog__body) {
  min-height: 0;
  overflow-y: auto;
}
:global(.script-save-dialog .el-dialog__header),
:global(.script-save-dialog .el-dialog__footer) {
  flex: none;
}
@media (max-width: 735px) {
  :global(.el-dialog.script-save-dialog) {
    align-self: flex-start;
    max-height: calc(var(--script-save-viewport-height, 100dvh) - 24px - env(safe-area-inset-bottom, 0px));
    margin-top: calc(var(--script-save-viewport-top, 0px) + 12px);
    margin-bottom: 12px;
  }
  .glass-input {
    font-size: 16px;
  }
}
</style>
