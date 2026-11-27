<script setup lang="ts">
import { computed, nextTick, onMounted, onScopeDispose, ref, useId, watch } from 'vue'
import { ArrowLeft, Check, RefreshCw } from 'lucide-vue-next'
import { sourceLabel, type ConnectionItem } from '../../../api/datasource'
import { environmentLabel, targetKind, type QueryTarget } from '../useQueryTarget'
import LoadingState from '../../../components/base/LoadingState.vue'

const props = defineProps<{
  open: boolean; currentTarget: QueryTarget | null; draftTarget: QueryTarget | null
  sources: ConnectionItem[]; databases: string[]; sourceLoading: boolean; databaseLoading: boolean
  sourceError: string; databaseError: string; submitError: string; submitting: boolean; disabledReason: string
  title?: string; directSelection?: boolean
}>()
const emit = defineEmits<{
  'update:open': [value: boolean]; 'select-source': [id: number]; 'select-database': [name: string]
  'retry-sources': []; 'retry-databases': []; confirm: []
}>()
const sourceSearch = ref(''), databaseSearch = ref(''), searchInput = ref<HTMLInputElement>()
const sourceList = ref<HTMLElement>(), databaseList = ref<HTMLElement>()
const databaseHeading = ref<HTMLElement>(), sourceHeading = ref<HTMLElement>()
const sourceSearchId = useId(), databaseSearchId = useId()
const compact = ref(false), choosingDatabase = ref(false)
const desktopQuery = typeof window !== 'undefined' ? window.matchMedia('(min-width: 1024px)') : undefined
const desktop = ref(desktopQuery?.matches ?? false)
const viewport = ref<{ height: number; top: number }>()
const viewportStyle = computed(() => compact.value && viewport.value
  ? { '--target-viewport-height': viewport.value.height + 'px', '--target-viewport-top': viewport.value.top + 'px' }
  : undefined)
function measureViewport() {
  compact.value = window.matchMedia('(max-width: 735px)').matches
  desktop.value = desktopQuery?.matches ?? false
  const current = window.visualViewport
  viewport.value = current ? { height: current.height, top: current.offsetTop } : undefined
}
onMounted(() => {
  measureViewport()
  window.addEventListener('resize', measureViewport)
  window.visualViewport?.addEventListener('resize', measureViewport)
  window.visualViewport?.addEventListener('scroll', measureViewport)
})
onScopeDispose(() => {
  window.removeEventListener('resize', measureViewport)
  window.visualViewport?.removeEventListener('resize', measureViewport)
  window.visualViewport?.removeEventListener('scroll', measureViewport)
})

async function selectSource(id: number) {
  if (!compact.value || props.draftTarget?.source.id !== id) emit('select-source', id)
  if (!compact.value) return
  choosingDatabase.value = true
  await nextTick()
  databaseHeading.value?.focus()
}
async function backToSources() {
  choosingDatabase.value = false
  await nextTick()
  sourceHeading.value?.focus()
}
function revealSelection() {
  if (compact.value) return
  for (const list of [sourceList.value, databaseList.value]) {
    const selected = list?.querySelector<HTMLElement>('[aria-selected="true"]')
    if (!list || !selected) continue
    const item = selected.getBoundingClientRect(), bounds = list.getBoundingClientRect()
    if (item.top < bounds.top) list.scrollTop -= bounds.top - item.top
    else if (item.bottom > bounds.bottom) list.scrollTop += item.bottom - bounds.bottom
  }
}
function opened() {
  if (!compact.value) searchInput.value?.focus({ preventScroll: true })
  revealSelection()
}
watch(() => [props.open, props.draftTarget?.source.id, props.draftTarget?.database, props.databaseLoading], revealSelection, { flush: 'post' })
const visibleSources = computed(() => props.sources.filter(item => item.name.toLocaleLowerCase().includes(sourceSearch.value.trim().toLocaleLowerCase())))
const visibleDatabases = computed(() => props.databases.filter(name => name.toLocaleLowerCase().includes(databaseSearch.value.trim().toLocaleLowerCase())))
watch(() => props.open, () => { sourceSearch.value = ''; databaseSearch.value = ''; choosingDatabase.value = false })
watch(() => props.draftTarget?.source.id, () => { databaseSearch.value = '' })
function move(event: KeyboardEvent) {
  if (!['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) return
  const options = [...(event.currentTarget as HTMLElement).querySelectorAll<HTMLButtonElement>('[role=option]:not(:disabled)')]
  if (!options.length) return
  event.preventDefault()
  const index = options.indexOf(document.activeElement as HTMLButtonElement)
  const next = event.key === 'Home' ? 0 : event.key === 'End' ? options.length - 1 : (index + (event.key === 'ArrowDown' ? 1 : -1) + options.length) % options.length
  options[next]?.focus()
  if (event.currentTarget === databaseList.value ? !props.directSelection : !compact.value) options[next]?.click()
}
const label = (target: QueryTarget | null) => target ? `${target.source.name} / ${targetKind(target.source) === 'keys' ? target.database || '配置的逻辑库' : target.database || '未选择数据库'}` : '尚未选择'
function updateOpen(value: boolean) { if (!props.submitting) emit('update:open', value) }
</script>

<template>
  <el-dialog :key="desktop ? 'desktop' : 'narrow'" :model-value="open" :title="title ?? '选择查询目标'" width="760px" class="query-target-dialog" :style="viewportStyle"
    :lock-scroll="!desktop" :modal-class="desktop ? 'query-target-desktop-overlay' : undefined"
    :close-on-click-modal="false" :close-on-press-escape="!submitting" :show-close="!submitting" @update:model-value="updateOpen" @opened="opened">
    <div v-if="compact" class="query-target-steps" aria-label="选择步骤">
      <span :aria-current="!choosingDatabase ? 'step' : undefined">1. 选择数据源</span>
      <span :aria-current="choosingDatabase ? 'step' : undefined">2. 选择数据库</span>
    </div>
    <p v-else-if="!directSelection" class="text-sm text-muted mb-4">先选择数据源，再选择数据库，确认后生效。</p>
    <div class="query-target-columns" :aria-busy="submitting">
      <section v-show="!compact || !choosingDatabase" class="query-target-column" aria-label="数据源候选">
        <div class="flex items-center justify-between mb-3"><h3 ref="sourceHeading" tabindex="-1">数据源</h3><button class="ghost-button ui-icon-button" aria-label="刷新数据源" :disabled="sourceLoading || submitting" @click="emit('retry-sources')"><RefreshCw :size="16" aria-hidden="true" /></button></div>
        <label class="block text-xs text-muted mb-2" :for="sourceSearchId">搜索数据源</label>
        <input :id="sourceSearchId" ref="searchInput" v-model="sourceSearch" class="glass-input mb-3" placeholder="搜索名称" :disabled="submitting" />
        <LoadingState v-if="sourceLoading" variant="inline" label="正在加载数据源…" />
        <div v-else-if="sourceError" role="alert" class="text-sm text-danger"><p>{{ sourceError }}</p><button class="ghost-button mt-2" :disabled="submitting" @click="emit('retry-sources')">重新加载数据源</button></div>
        <template v-else>
          <div v-if="visibleSources.length" ref="sourceList" role="listbox" aria-label="数据源" class="query-target-list" @keydown="move">
            <button v-for="(source, index) in visibleSources" :key="source.id" type="button" role="option" :aria-selected="draftTarget?.source.id === source.id" :tabindex="draftTarget && visibleSources.some(item => item.id === draftTarget?.source.id) ? (draftTarget.source.id === source.id ? 0 : -1) : (index === 0 ? 0 : -1)" :disabled="submitting" class="query-target-option" @click="selectSource(source.id)">
              <span class="min-w-0"><span class="block break-words">{{ source.name }}</span><span class="block text-xs text-muted mt-1">{{ sourceLabel(source) }} · {{ environmentLabel(source) }}</span></span>
              <span v-if="currentTarget?.source.id === source.id" class="text-xs shrink-0">当前</span><Check v-if="draftTarget?.source.id === source.id" :size="16" aria-hidden="true" class="shrink-0" />
            </button>
          </div>
          <p v-else class="text-sm text-muted">{{ sources.length ? '没有匹配的数据源，请调整搜索。' : '当前范围没有可用数据源。' }}</p>
        </template>
      </section>
      <section v-show="!compact || choosingDatabase" class="query-target-column" aria-label="数据库候选">
        <button v-if="compact" type="button" class="ghost-button ui-control-target inline-flex items-center gap-1 mb-2 text-sm"
          :disabled="submitting" @click="backToSources"><ArrowLeft :size="16" aria-hidden="true" />返回数据源</button>
        <h3 ref="databaseHeading" tabindex="-1" class="mb-1">数据库</h3><p class="text-xs text-muted mb-3 break-words">{{ draftTarget?.source.name || '请先选择数据源' }}</p>
        <template v-if="draftTarget && targetKind(draftTarget.source) === 'keys'">
          <button v-if="directSelection" type="button" class="query-target-option" :disabled="!!disabledReason || submitting"
            @click="emit('select-database', draftTarget.database)">
            <span>逻辑库 {{ draftTarget.database }}<span class="block text-xs text-muted mt-1">选择后关联到脚本</span></span>
          </button>
          <p v-else class="editorial-form-feedback">使用数据源配置的逻辑库{{ draftTarget.database ? `：${draftTarget.database}` : '' }}。</p>
        </template>
        <template v-else>
          <label class="block text-xs text-muted mb-2" :for="databaseSearchId">搜索数据库</label>
          <input :id="databaseSearchId" v-model="databaseSearch" class="glass-input mb-3" placeholder="搜索数据库名称" :disabled="!draftTarget || submitting || databaseLoading" />
          <LoadingState v-if="databaseLoading" variant="inline" label="正在加载数据库…" />
          <div v-else-if="databaseError" role="alert" class="text-sm text-danger"><p>{{ databaseError }}</p><button class="ghost-button mt-2" :disabled="submitting" @click="emit('retry-databases')">重新加载数据库</button></div>
          <div v-else-if="visibleDatabases.length" ref="databaseList" role="listbox" aria-label="数据库" class="query-target-list" @keydown="move">
            <button v-for="(name, index) in visibleDatabases" :key="name" type="button" role="option" :aria-selected="draftTarget?.database === name" :tabindex="draftTarget && visibleDatabases.includes(draftTarget.database) ? (draftTarget.database === name ? 0 : -1) : (index === 0 ? 0 : -1)" :disabled="submitting" class="query-target-option" @click="emit('select-database', name)">
              <span class="break-all">{{ name }}</span><span v-if="currentTarget?.source.id === draftTarget?.source.id && currentTarget?.database === name" class="text-xs shrink-0">当前</span><Check v-if="draftTarget?.database === name" :size="16" aria-hidden="true" class="shrink-0" />
            </button>
          </div>
          <p v-else-if="draftTarget" class="text-sm text-muted">{{ databases.length ? '没有匹配的数据库，请调整搜索。' : '该数据源没有可用数据库。' }}</p>
        </template>
      </section>
    </div>
    <p v-if="directSelection && submitError" role="alert" class="editorial-form-feedback editorial-form-feedback--error mt-3">{{ submitError }}</p>
    <p v-else-if="directSelection && submitting" role="status" class="text-sm text-muted mt-3">正在核对目标…</p>
    <template v-if="!directSelection" #footer>
      <div class="query-target-footer">
        <div v-if="(!compact || choosingDatabase) && (currentTarget || draftTarget)" class="min-w-0 text-left text-sm">
          <p v-if="currentTarget && !compact" class="text-muted break-words">当前：{{ label(currentTarget) }}</p>
          <p v-if="draftTarget && (draftTarget.database || targetKind(draftTarget.source) === 'keys')" class="break-words">待切换：{{ label(draftTarget) }} <span :class="draftTarget.source.environment === 'prod' ? 'text-danger' : 'text-muted'">· {{ environmentLabel(draftTarget.source) }}</span></p>
        </div>
        <p v-if="submitError" role="alert" class="editorial-form-feedback editorial-form-feedback--error text-left">{{ submitError }}</p>
        <p v-if="disabledReason && (!compact || choosingDatabase)" role="status" class="text-xs text-muted text-left">{{ disabledReason }}</p>
        <div class="flex flex-wrap justify-end gap-2 sm:gap-3">
          <button type="button" class="ghost-button ui-control-target" :disabled="submitting" @click="emit('update:open', false)">取消</button>
          <button v-if="!compact || choosingDatabase" type="button" class="liquid-button ui-control-target" :disabled="!!disabledReason || submitting"
            @click="emit('confirm')">{{ submitting ? '正在核对…' : '切换到此目标' }}</button>
        </div>
      </div>
    </template>
  </el-dialog>
</template>
