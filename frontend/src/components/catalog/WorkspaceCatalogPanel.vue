<script setup lang="ts">
import { computed, nextTick, onMounted, ref, useId, watch } from 'vue'
import { ChevronLeft, ChevronRight, PanelsTopLeft, Pin, Table2, X } from 'lucide-vue-next'
import LoadingState from '../base/LoadingState.vue'
import { catalogApi } from '../../api/catalog'
import type { ColumnInfo } from '../../api/workbench'
import { useReportRun } from '../../views/report/useReportRun'
import CatalogCopyButton from './CatalogCopyButton.vue'
import CatalogText from './CatalogText.vue'
import { readWorkspaceCatalog, type WorkspaceCatalogObject } from './readWorkspaceCatalog'

const props = defineProps<{ id: string; inline?: boolean }>()
const pinned = defineModel<boolean>('pinned', { required: true })
const emit = defineEmits<{ close: [] }>()
const root = ref<HTMLElement>()
defineExpose({ contains: (node: Node) => root.value?.contains(node) ?? false })

const titleId = useId()
const title = ref<HTMLElement>()
const detailTitle = ref<HTMLElement>()
const listScroll = ref<HTMLElement>()
const resultsList = ref<HTMLElement>()
const detailScroll = ref<HTMLElement>()
const search = ref('')
const selected = ref<WorkspaceCatalogObject>()
const pageSize = 50
const visibleCount = ref(pageSize)
const directory = useReportRun<Awaited<ReturnType<typeof readWorkspaceCatalog>>>()
const columns = useReportRun<ColumnInfo[]>()
const matches = computed(() => {
  if (directory.state.value.status !== 'success') return []
  const needle = search.value.trim().toLocaleLowerCase()
  return directory.state.value.data.items.filter(item => item.name.toLocaleLowerCase().includes(needle))
})
const visible = computed(() => matches.value.slice(0, visibleCount.value))
let listOffset = 0
let objectTrigger: HTMLElement | undefined

function resetListPosition() {
  visibleCount.value = pageSize
  if (listScroll.value) listScroll.value.scrollTop = 0
}
function loadDirectory() {
  resetListPosition()
  void directory.run('catalog', readWorkspaceCatalog)
}
onMounted(() => {
  if (!props.inline) title.value?.focus({ preventScroll: true })
  loadDirectory()
})
watch(search, resetListPosition)

async function showMore() {
  const nextIndex = visibleCount.value
  visibleCount.value += pageSize
  await nextTick()
  resultsList.value?.children.item(nextIndex)?.querySelector<HTMLButtonElement>('.catalog-object-open')?.focus()
}

function loadColumns() {
  const item = selected.value
  if (!item) return
  void columns.run(item.key, async signal => (await catalogApi.columns(item.source.id, item, signal)).items ?? [])
}
async function drill(object: WorkspaceCatalogObject, event: MouseEvent) {
  listOffset = listScroll.value?.scrollTop ?? 0
  objectTrigger = event.currentTarget as HTMLElement
  selected.value = object
  loadColumns()
  await nextTick()
  if (detailScroll.value) detailScroll.value.scrollTop = 0
  detailTitle.value?.focus({ preventScroll: true })
}
async function returnToList() {
  selected.value = undefined
  columns.reset()
  await nextTick()
  if (listScroll.value) listScroll.value.scrollTop = listOffset
  const focus = objectTrigger?.isConnected ? objectTrigger : title.value
  focus?.focus({ preventScroll: true })
  objectTrigger = undefined
}
</script>

<template>
  <aside :id="id" ref="root" class="workspace-catalog-panel" :class="{ 'workspace-catalog-panel--inline': inline }"
    :aria-labelledby="titleId" @keydown.esc.stop.prevent="emit('close')">
    <header class="flex shrink-0 items-center gap-1 border-b border-stroke p-3" :class="{ 'sr-only': inline && !selected }">
      <h2 :id="titleId" ref="title" tabindex="-1" class="ui-focus-target flex-1 text-sm text-content" :class="{ 'sr-only': selected }">数据目录</h2>
      <button v-if="selected" type="button" class="catalog-list-return ghost-button flex flex-1 items-center gap-1 text-sm" @click="returnToList">
        <ChevronLeft class="size-4" aria-hidden="true" />返回列表
      </button>
      <button v-if="!inline" type="button" class="catalog-pin ghost-button ui-icon-button" aria-label="固定数据目录" :aria-pressed="pinned"
        :title="pinned ? '取消固定数据目录' : '固定数据目录'" @click="pinned = !pinned">
        <Pin class="size-4" aria-hidden="true" />
      </button>
      <button v-if="!inline" type="button" class="ghost-button ui-icon-button" aria-label="关闭数据目录" title="关闭数据目录" @click="emit('close')">
        <X class="size-4" aria-hidden="true" />
      </button>
    </header>

    <template v-if="!selected">
      <div class="shrink-0 p-3 pb-1">
        <input v-model="search" type="search" aria-label="搜索表名或视图名" placeholder="搜索表名或视图名" class="glass-input w-full text-sm" />
      </div>
    </template>
    <div v-show="!selected" ref="listScroll" class="min-h-0 flex-1 overflow-auto p-3" aria-label="表和视图列表">
      <LoadingState v-if="directory.state.value.status === 'loading'" variant="inline" label="正在读取当前空间的表和视图…" />
      <div v-else-if="directory.state.value.status === 'error'" class="space-y-2 text-sm" role="alert">
        <p>读取数据目录失败。</p><button type="button" class="ghost-button ui-control-target" @click="loadDirectory">重试</button>
      </div>
      <template v-else-if="directory.state.value.status === 'success'">
        <div v-if="directory.state.value.data.unavailable.length" class="flex flex-wrap items-center gap-2 pb-2 text-xs" role="status">
          <p :title="directory.state.value.data.unavailable.join('、')">部分来源未读完，列表和搜索仅包含已读取的对象。</p>
          <button type="button" class="ghost-button ui-control-target" @click="loadDirectory">重试读取</button>
        </div>
        <ul v-if="visible.length" ref="resultsList" class="divide-y divide-divider">
          <li v-for="object in visible" :key="object.key" class="catalog-object">
            <button type="button" class="catalog-object-open ui-focus-target" :title="object.name"
              :aria-label="`查看${object.type === 'view' ? '视图' : '表'} ${object.name}${object.sameName ? ' · ' + object.path : ''}`"
              @click="drill(object, $event)">
              <component :is="object.type === 'view' ? PanelsTopLeft : Table2" class="size-4 text-muted" aria-hidden="true" />
              <CatalogText :text="object.name" class="font-mono" />
              <CatalogText v-if="object.sameName" :text="object.path" class="catalog-object-source text-xs text-muted" />
              <ChevronRight class="catalog-object-arrow size-4 text-muted" aria-hidden="true" />
            </button>
            <CatalogCopyButton class="catalog-object-copy" :value="object.name"
              :label="`复制${object.type === 'view' ? '视图' : '表'}名 ${object.name}`" />
          </li>
        </ul>
        <p v-else class="text-sm text-muted" role="status">{{ search.trim() ? '没有匹配的表或视图。' : '暂无可显示的表和视图。' }}</p>
        <button v-if="visibleCount < matches.length" type="button" class="ghost-button ui-control-target mt-3 w-full text-sm" @click="showMore">
          显示更多（{{ visibleCount }} / {{ matches.length }}）
        </button>
      </template>
    </div>

    <div v-if="selected" ref="detailScroll" class="min-h-0 flex-1 overflow-auto p-3">
      <div class="flex min-w-0 items-center gap-2">
        <component :is="selected.type === 'view' ? PanelsTopLeft : Table2" class="size-4 shrink-0 text-muted" aria-hidden="true" />
        <h3 ref="detailTitle" tabindex="-1" class="min-w-0 flex-1 font-mono text-sm ui-focus-target"><CatalogText :text="selected.name" /></h3>
        <CatalogCopyButton :value="selected.name" :label="`复制${selected.type === 'view' ? '视图' : '表'}名 ${selected.name}`" />
      </div>
      <CatalogText :text="selected.path" class="catalog-detail-text mt-1 text-xs text-muted" />
      <CatalogText v-if="selected.comment" :text="selected.comment" class="catalog-detail-text mt-1 text-xs text-muted" />
      <LoadingState v-if="columns.state.value.status === 'loading'" class="mt-3" variant="inline" label="正在读取字段…" />
      <div v-else-if="columns.state.value.status === 'error'" class="mt-3 space-y-2 text-sm" role="alert">
        <p>读取字段失败。</p><button type="button" class="ghost-button ui-control-target" @click="loadColumns">重试</button>
      </div>
      <template v-else-if="columns.state.value.status === 'success'">
        <table v-if="columns.state.value.data.length" class="editorial-table mt-3 w-full table-fixed" aria-label="对象字段">
          <colgroup><col style="width: 56%" /><col /><col :style="{ width: inline ? '48px' : '40px' }" /></colgroup>
          <thead><tr><th scope="col">字段名</th><th scope="col">类型</th><th scope="col"><span class="sr-only">操作</span></th></tr></thead>
          <tbody>
            <tr v-for="field in columns.state.value.data" :key="field.name">
              <td class="font-mono"><CatalogText :text="field.name" /></td>
              <td class="font-mono"><CatalogText :text="field.data_type" /></td>
              <td class="catalog-field-copy"><CatalogCopyButton :value="field.name" :label="`复制字段名 ${field.name}`" /></td>
            </tr>
          </tbody>
        </table>
        <p v-else class="mt-3 text-sm text-muted" role="status">没有可显示的字段。</p>
      </template>
    </div>
  </aside>
</template>

<style scoped>
.workspace-catalog-panel {
  position: absolute;
  inset: 0;
  display: flex;
  min-height: 0;
  flex-direction: column;
  overflow: hidden;
  pointer-events: auto;
  background: var(--db-surface-solid);
  border: 1px solid var(--db-border);
  box-shadow: -8px 0 20px rgb(0 0 0 / 10%);
}
.catalog-pin[aria-pressed='true'] {
  color: var(--db-accent);
  border-color: var(--db-accent);
  background: var(--db-selected);
}
.catalog-object { position: relative; }
.catalog-object-open {
  display: flex;
  width: 100%;
  min-height: 36px;
  align-items: center;
  gap: 6px;
  padding-inline: 4px 44px;
  color: var(--db-text-primary);
  text-align: start;
  font-size: 13px;
}
.catalog-object-open:hover { background: var(--db-interactive-hover); }
.catalog-object-open :is(svg, .catalog-object-source) { flex-shrink: 0; }
.catalog-object-arrow { margin-inline-start: auto; }
.catalog-object-source { max-width: 37%; }
.catalog-object-arrow, .catalog-object-copy { position: absolute; }
.catalog-object-arrow { inset-inline-end: 2px; }
.catalog-object-copy { inset-block-start: 50%; inset-inline-end: 18px; transform: translateY(-50%); }
.catalog-field-copy { padding: 0 2px; }
.workspace-catalog-panel--inline {
  position: static;
  min-width: 0;
  max-height: min(60dvh, 36rem);
  border: 0;
  box-shadow: none;
}
.workspace-catalog-panel--inline :is(input[type='search'], .catalog-list-return, .catalog-object-open) {
  min-height: 44px;
}
.workspace-catalog-panel--inline :deep(.catalog-copy) {
  width: 44px;
  min-width: 44px;
  min-height: 44px;
}
.workspace-catalog-panel--inline .catalog-object {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 44px;
  align-items: center;
  gap: 4px;
}
.workspace-catalog-panel--inline .catalog-object-open {
  display: grid;
  min-width: 0;
  grid-template-columns: 16px minmax(0, 1fr) 16px;
  column-gap: 6px;
  row-gap: 0;
  padding: 4px;
}
.workspace-catalog-panel--inline .catalog-object-open > :first-child { grid-column: 1; grid-row: 1 / span 2; }
.workspace-catalog-panel--inline .catalog-object-open > .font-mono { grid-column: 2; grid-row: 1; }
.workspace-catalog-panel--inline .catalog-object-source {
  grid-column: 2;
  grid-row: 2;
  max-width: none;
}
.workspace-catalog-panel--inline :is(.catalog-object-source, .catalog-detail-text) {
  white-space: normal;
  overflow-wrap: anywhere;
}
.workspace-catalog-panel--inline .catalog-object-arrow {
  position: static;
  grid-column: 3;
  grid-row: 1 / span 2;
}
.workspace-catalog-panel--inline .catalog-object-copy {
  position: static;
  transform: none;
}
</style>
