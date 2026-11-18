<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { Check, RefreshCw } from 'lucide-vue-next'
import { sourceLabel, type ConnectionItem } from '../../../api/datasource'
import { environmentLabel, targetKind, type QueryTarget } from '../useQueryTarget'
import LoadingState from '../../../components/base/LoadingState.vue'

const props = defineProps<{
  open: boolean; currentTarget: QueryTarget | null; draftTarget: QueryTarget | null
  sources: ConnectionItem[]; databases: string[]; sourceLoading: boolean; databaseLoading: boolean
  sourceError: string; databaseError: string; submitError: string; submitting: boolean; disabledReason: string
  discardRequired: boolean; discardConfirmed: boolean
}>()
const emit = defineEmits<{
  'update:open': [value: boolean]; 'select-source': [id: number]; 'select-database': [name: string]
  'retry-sources': []; 'retry-databases': []; confirm: []; 'update:discardConfirmed': [value: boolean]
}>()
const sourceSearch = ref(''), databaseSearch = ref(''), searchInput = ref<HTMLInputElement>()
const sourceList = ref<HTMLElement>(), databaseList = ref<HTMLElement>()
function revealSelection() {
  for (const list of [sourceList.value, databaseList.value]) {
    const selected = list?.querySelector<HTMLElement>('[aria-selected="true"]')
    if (!list || !selected) continue
    const item = selected.getBoundingClientRect(), bounds = list.getBoundingClientRect()
    if (item.top < bounds.top) list.scrollTop -= bounds.top - item.top
    else if (item.bottom > bounds.bottom) list.scrollTop += item.bottom - bounds.bottom
  }
}
function opened() { searchInput.value?.focus(); revealSelection() }
watch(() => [props.open, props.draftTarget?.source.id, props.draftTarget?.database, props.databaseLoading], revealSelection, { flush: 'post' })
const visibleSources = computed(() => props.sources.filter(item => item.name.toLocaleLowerCase().includes(sourceSearch.value.trim().toLocaleLowerCase())))
const visibleDatabases = computed(() => props.databases.filter(name => name.toLocaleLowerCase().includes(databaseSearch.value.trim().toLocaleLowerCase())))
watch(() => props.open, () => { sourceSearch.value = ''; databaseSearch.value = '' })
watch(() => props.draftTarget?.source.id, () => { databaseSearch.value = '' })
function move(event: KeyboardEvent) {
  if (!['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) return
  const options = [...(event.currentTarget as HTMLElement).querySelectorAll<HTMLButtonElement>('[role=option]:not(:disabled)')]
  if (!options.length) return
  event.preventDefault()
  const index = options.indexOf(document.activeElement as HTMLButtonElement)
  const next = event.key === 'Home' ? 0 : event.key === 'End' ? options.length - 1 : (index + (event.key === 'ArrowDown' ? 1 : -1) + options.length) % options.length
  options[next]?.focus(); options[next]?.click()
}
const label = (target: QueryTarget | null) => target ? `${target.source.name} / ${targetKind(target.source) === 'keys' ? target.database || '配置的逻辑库' : target.database || '未选择数据库'}` : '尚未选择'
function updateOpen(value: boolean) { if (!props.submitting) emit('update:open', value) }
</script>

<template>
  <el-dialog :model-value="open" title="选择查询目标" width="760px" class="query-target-dialog" :close-on-click-modal="false" :close-on-press-escape="!submitting" :show-close="!submitting" @update:model-value="updateOpen" @opened="opened">
    <p class="text-sm text-muted mb-4">先选择数据源，再选择数据库；确认后生效。</p>
    <div class="query-target-columns" :aria-busy="submitting">
      <section class="query-target-column" aria-label="数据源候选">
        <div class="flex items-center justify-between mb-3"><h3>数据源</h3><button class="ghost-button ui-icon-button" aria-label="刷新数据源" :disabled="sourceLoading || submitting" @click="emit('retry-sources')"><RefreshCw :size="16" aria-hidden="true" /></button></div>
        <label class="block text-xs text-muted mb-2" for="target-source-search">搜索数据源</label>
        <input id="target-source-search" ref="searchInput" v-model="sourceSearch" class="glass-input mb-3" placeholder="搜索名称" :disabled="submitting" />
        <LoadingState v-if="sourceLoading" variant="inline" label="正在加载数据源…" />
        <div v-else-if="sourceError" role="alert" class="text-sm text-danger"><p>{{ sourceError }}</p><button class="ghost-button mt-2" :disabled="submitting" @click="emit('retry-sources')">重新加载数据源</button></div>
        <template v-else>
          <div v-if="visibleSources.length" ref="sourceList" role="listbox" aria-label="数据源" class="query-target-list" @keydown="move">
            <button v-for="(source, index) in visibleSources" :key="source.id" type="button" role="option" :aria-selected="draftTarget?.source.id === source.id" :tabindex="draftTarget && visibleSources.some(item => item.id === draftTarget?.source.id) ? (draftTarget.source.id === source.id ? 0 : -1) : (index === 0 ? 0 : -1)" :disabled="submitting" class="query-target-option" @click="emit('select-source', source.id)">
              <span class="min-w-0"><span class="block break-words">{{ source.name }}</span><span class="block text-xs text-muted mt-1">{{ sourceLabel(source) }} · {{ environmentLabel(source) }}</span></span>
              <span v-if="currentTarget?.source.id === source.id" class="text-xs shrink-0">当前</span><Check v-if="draftTarget?.source.id === source.id" :size="16" aria-hidden="true" class="shrink-0" />
            </button>
          </div>
          <p v-else class="text-sm text-muted">{{ sources.length ? '没有匹配的数据源，请调整搜索。' : '当前范围没有可用数据源。' }}</p>
        </template>
      </section>
      <section class="query-target-column" aria-label="数据库候选">
        <h3 class="mb-1">数据库</h3><p class="text-xs text-muted mb-3 break-words">{{ draftTarget?.source.name || '请先选择数据源' }}</p>
        <template v-if="draftTarget && targetKind(draftTarget.source) === 'keys'"><p class="editorial-form-feedback">使用数据源配置的逻辑库{{ draftTarget.database ? `：${draftTarget.database}` : '' }}。</p></template>
        <template v-else>
          <label class="block text-xs text-muted mb-2" for="target-database-search">搜索数据库</label>
          <input id="target-database-search" v-model="databaseSearch" class="glass-input mb-3" placeholder="搜索数据库名称" :disabled="!draftTarget || submitting || databaseLoading" />
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
    <template #footer>
      <div class="query-target-footer">
        <div class="min-w-0 text-left text-sm"><p class="text-muted break-words">当前：{{ label(currentTarget) }}</p><p class="break-words mt-1">待切换：{{ label(draftTarget) }} <span v-if="draftTarget" :class="draftTarget.source.environment === 'prod' ? 'text-danger' : 'text-muted'">· {{ environmentLabel(draftTarget.source) }}</span></p></div>
        <label v-if="discardRequired" class="editorial-form-feedback text-left"><input type="checkbox" :checked="discardConfirmed" :disabled="submitting" @change="emit('update:discardConfirmed', ($event.target as HTMLInputElement).checked)" />切换工作台类型将丢弃当前 SQL 编辑内容，我已自行保存并确认切换。</label>
        <p v-if="submitError" role="alert" class="editorial-form-feedback editorial-form-feedback--error text-left">{{ submitError }}</p>
        <p v-if="disabledReason" role="status" class="text-xs text-muted text-left">{{ disabledReason }}</p>
        <div class="flex justify-end gap-3"><button class="ghost-button ui-control-target" :disabled="submitting" @click="emit('update:open', false)">取消</button><button class="liquid-button ui-control-target" :disabled="!!disabledReason || submitting" @click="emit('confirm')">{{ submitting ? '正在核对…' : '切换到此目标' }}</button></div>
      </div>
    </template>
  </el-dialog>
</template>
