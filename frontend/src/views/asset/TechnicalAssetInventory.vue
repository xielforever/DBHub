<script setup lang="ts">
import { computed, nextTick, onScopeDispose, ref, shallowRef, useId, watch } from 'vue'
import { useRoute, useRouter, type LocationQueryRaw } from 'vue-router'
import { ChevronDown, ChevronRight, ListFilter, RefreshCw, Search } from 'lucide-vue-next'
import { assetApi, type TechnicalElement, type TechnicalElementKind, type TechnicalElementLevel, type TechnicalElementPage } from '../../api/asset'
import LoadingState from '../../components/base/LoadingState.vue'
import PageState from '../../components/base/PageState.vue'
import { useUserStore } from '../../stores/user'
import { revealTableAction, scrollTableByKeyboard } from '../../utils/table-focus'
import { useReportRun } from '../report/useReportRun'
import TechnicalAssetDetail from './TechnicalAssetDetail.vue'
import { elementAttribute, elementKey, elementKinds, elementLevels, elementPath } from './technicalElements'

const props = defineProps<{ revision: number }>()
const emit = defineEmits<{ 'open-object': [element: TechnicalElement, trigger?: HTMLElement] }>()
const route = useRoute()
const router = useRouter()
const user = useUserStore()
const id = useId()
const pageSize = 8
const kindOptions = Object.keys(elementKinds) as TechnicalElementKind[]
const levelOptions = Object.keys(elementLevels) as TechnicalElementLevel[]
const search = ref('')
const filtersOpen = ref(false)
const tableRegion = ref<HTMLDivElement>()
const history = shallowRef<TechnicalElement[]>([])
const selected = computed(() => history.value[history.value.length - 1])
const detailOpen = ref(false)
const detailHeading = ref<HTMLHeadingElement>()
let opener: HTMLElement | undefined
let openedScrollLeft = 0
let pendingObject: TechnicalElement | undefined
let debounce: ReturnType<typeof setTimeout> | undefined

function queryValues(raw: unknown) {
  return (Array.isArray(raw) ? raw : [raw]).filter((value): value is string => typeof value === 'string')
}
function positive(raw: unknown, maximum = Number.MAX_SAFE_INTEGER) {
  const number = typeof raw === 'string' && /^[1-9]\d*$/.test(raw) ? Number(raw) : 0
  return number <= maximum ? number : 0
}

const selectedSource = computed(() => positive(route.query.connection_id))
const types = computed(() => kindOptions.filter(kind => queryValues(route.query.type).includes(kind)))
const levels = computed(() => levelOptions.filter(level => queryValues(route.query.level ?? route.query.sensitivity).includes(level)))
const tags = computed(() => [...new Set(queryValues(route.query.tag))]
  .filter(tag => tag && [...tag].length <= 20).slice(0, 32))
const page = computed(() => positive(route.query.page, 1000000) || 1)
const keyword = computed(() => [...(queryValues(route.query.q)[0] || '').trim()].slice(0, 200).join(''))
const query = computed(() => ({
  connection_id: selectedSource.value || undefined,
  q: keyword.value || undefined,
  type: types.value,
  level: levels.value,
  tag: tags.value,
  page: page.value,
  page_size: pageSize,
}))
const extraCount = computed(() => levels.value.length + tags.value.length)
const filterCount = computed(() => Number(Boolean(selectedSource.value)) + types.value.length + extraCount.value)
const list = useReportRun<TechnicalElementPage>()
const loading = computed(() => list.state.value.status === 'loading')
const data = computed(() => list.state.value.status === 'success' ? list.state.value.data : undefined)
const pageCount = computed(() => Math.max(1, Math.ceil((data.value?.total ?? 0) / pageSize)))
const sources = shallowRef<TechnicalElementPage['sources']>([])
const availableTags = shallowRef<string[]>([])
const visibleTags = computed(() => [...new Set([...availableTags.value, ...tags.value])])
const sourceName = computed(() => sources.value.find(source => source.id === selectedSource.value)?.name)

function updateQuery(changes: LocationQueryRaw, replace = false) {
  const next: LocationQueryRaw = { ...route.query }
  // 地址中的旧对象定位继续指向同一个对象，独立于本次清单筛选。
  if (next.table && !next.asset_connection && next.connection_id) {
    next.asset_connection = next.connection_id
    next.asset_database = next.database
    next.asset_schema = next.schema
  }
  delete next.database
  delete next.schema
  delete next.no_owner
  delete next.starred
  const location = { query: { ...next, ...changes }, hash: route.hash }
  void (replace ? router.replace(location) : router.push(location))
}
function applyFilters(changes: LocationQueryRaw) {
  clearTimeout(debounce)
  updateQuery({ page: undefined, q: search.value.trim() || undefined, ...changes })
}
function toggle(key: 'type' | 'level' | 'tag', current: string[], value: string) {
  const next = current.includes(value) ? current.filter(item => item !== value) : [...current, value]
  applyFilters({ [key]: next.length ? next : undefined, ...(key === 'level' ? { sensitivity: undefined } : {}) })
}
function clearFilters() {
  search.value = ''
  applyFilters({ connection_id: undefined, type: undefined, level: undefined, tag: undefined, sensitivity: undefined })
}
function changePage(value: number) {
  if (!data.value || value < 1 || value > pageCount.value || value === page.value) return
  updateQuery({ page: value > 1 ? String(value) : undefined })
}
async function read() {
  pendingObject = undefined
  detailOpen.value = false
  const params = query.value
  const loaded = await list.run(JSON.stringify([user.workspaceId, params, props.revision]),
    signal => assetApi.elements(params, signal))
  if (!loaded || !data.value) return
  sources.value = data.value.sources
  availableTags.value = data.value.available_tags
  if (page.value > pageCount.value) updateQuery({ page: pageCount.value > 1 ? String(pageCount.value) : undefined }, true)
}

function open(element: TechnicalElement, event: MouseEvent) {
  const trigger = event.currentTarget as HTMLElement
  opener = trigger instanceof HTMLTableRowElement ? trigger.querySelector('button') ?? undefined : trigger
  openedScrollLeft = tableRegion.value?.scrollLeft ?? 0
  history.value = [element]
  detailOpen.value = true
}
function drill(element: TechnicalElement) {
  history.value = [...history.value, element]
}
function openObject(element: TechnicalElement) {
  pendingObject = element
  detailOpen.value = false
}
function finishClose() {
  const target = opener?.isConnected ? opener : tableRegion.value
  const object = pendingObject
  history.value = []
  opener = undefined
  pendingObject = undefined
  if (object) {
    emit('open-object', object, target)
  } else {
    target?.focus({ preventScroll: true })
    if (tableRegion.value) tableRegion.value.scrollLeft = openedScrollLeft
  }
}
function back() {
  history.value = history.value.slice(0, -1)
}

watch(selected, async (element, previous) => {
  if (!element || !previous) return
  await nextTick()
  if (detailOpen.value && selected.value === element) detailHeading.value?.focus()
})
watch(keyword, value => { search.value = value }, { immediate: true })
watch(search, value => {
  clearTimeout(debounce)
  if (value.trim() !== keyword.value) {
    debounce = setTimeout(() => updateQuery({ q: value.trim() || undefined, page: undefined }, true), 180)
  }
})
watch(() => user.workspaceId, () => {
  sources.value = []
  availableTags.value = []
})
watch(() => JSON.stringify([user.workspaceId, props.revision, query.value]), () => {
  void read()
}, { immediate: true })
onScopeDispose(() => clearTimeout(debounce))
</script>

<template>
  <div class="technical-inventory">
    <div class="technical-filter-rail min-w-0">
      <button type="button" class="technical-mobile-filter ghost-button ui-control-target w-full items-center gap-2"
        :aria-controls="id + '-filters'" :aria-expanded="filtersOpen" @click="filtersOpen = !filtersOpen">
        <ListFilter class="size-4" aria-hidden="true" />筛选<span v-if="filterCount">（{{ filterCount }}）</span>
        <ChevronDown class="ml-auto size-4" aria-hidden="true" />
      </button>
      <aside :id="id + '-filters'" class="technical-filters" :class="{ 'filters-open': filtersOpen }" aria-label="技术元素筛选">
        <div class="flex items-center justify-between gap-2 border-b border-divider pb-2">
          <h2 class="text-sm font-semibold">筛选</h2>
          <button type="button" class="editorial-text-action ui-control-target"
            :disabled="!filterCount && !search" @click="clearFilters">清空</button>
        </div>

        <fieldset>
          <legend class="mb-2 text-sm font-semibold">数据源</legend>
          <div class="filter-sources">
            <label class="filter-option">
              <input type="radio" :name="id + '-source'" :checked="!selectedSource" @change="applyFilters({ connection_id: undefined })" />
              <span>全部数据源</span>
            </label>
            <label v-for="source in sources" :key="source.id" class="filter-option">
              <input type="radio" :name="id + '-source'" :checked="source.id === selectedSource"
                @change="applyFilters({ connection_id: String(source.id) })" />
              <span>{{ source.name }}</span>
            </label>
          </div>
          <p v-if="data && selectedSource && !sourceName" class="mt-2 text-xs text-muted">所选数据源不可用</p>
        </fieldset>

        <fieldset>
          <legend class="mb-2 text-sm font-semibold">元素类型</legend>
          <div class="filter-options-grid">
            <label v-for="kind in kindOptions" :key="kind" class="filter-option">
              <input type="checkbox" :checked="types.includes(kind)" @change="toggle('type', types, kind)" />
              <span>{{ elementKinds[kind].label }}</span>
            </label>
          </div>
        </fieldset>

        <details class="border-t border-divider pt-2">
          <summary class="ui-control-target cursor-pointer py-1 text-sm font-medium">分级与标签<span v-if="extraCount">（{{ extraCount }}）</span></summary>
          <fieldset class="mt-2">
            <legend class="text-xs text-muted">分级</legend>
            <div class="filter-options-grid">
              <label v-for="level in levelOptions" :key="level" class="filter-option">
                <input type="checkbox" :checked="levels.includes(level)" @change="toggle('level', levels, level)" />
                <span>{{ elementLevels[level] }}</span>
              </label>
            </div>
          </fieldset>
          <fieldset class="mt-2">
            <legend class="text-xs text-muted">标签</legend>
            <div v-if="visibleTags.length" class="filter-options-grid filter-sources">
              <label v-for="tag in visibleTags" :key="tag" class="filter-option">
                <input type="checkbox" :checked="tags.includes(tag)" :disabled="tags.length >= 32 && !tags.includes(tag)" @change="toggle('tag', tags, tag)" />
                <span>{{ tag }}</span>
              </label>
            </div>
            <p v-else class="py-1 text-xs text-muted">暂无可选标签</p>
          </fieldset>
        </details>
      </aside>
    </div>

    <section class="min-w-0 space-y-3" aria-label="技术元素清单">
      <div class="flex min-w-0 flex-wrap items-center gap-2">
        <div class="relative min-w-0 flex-1 basis-56">
          <Search class="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted" aria-hidden="true" />
          <label class="sr-only" :for="id + '-search'">搜索技术资产</label>
          <input :id="id + '-search'" v-model="search" type="search" maxlength="200"
            class="glass-input w-full !pl-9" placeholder="搜索数据库、表名、字段名等" />
        </div>
        <button type="button" class="ghost-button ui-icon-button ui-control-target" title="刷新目录" aria-label="刷新目录"
          :aria-disabled="loading || undefined" @click="read">
          <RefreshCw class="size-4" aria-hidden="true" />
        </button>
        <slot name="actions" />
      </div>

      <div class="flex min-h-6 flex-wrap items-center justify-between gap-2 text-sm text-muted">
        <span role="status">{{ data ? '共 ' + data.total + ' 个元素' : loading ? '总数正在确认' : '总数暂不可用' }}</span>
        <span v-if="sourceName" class="[overflow-wrap:anywhere]">{{ sourceName }}</span>
      </div>

      <div class="min-w-0 space-y-3">
        <div ref="tableRegion" class="editorial-table-wrap relative" role="region" tabindex="0" aria-label="技术资产表格"
          @focusin="revealTableAction" @keydown.self="scrollTableByKeyboard">
          <table class="editorial-table technical-table" :aria-busy="loading">
            <thead><tr>
              <th scope="col">元素名称</th><th scope="col">类型</th>
              <th scope="col">所在位置</th><th scope="col">属性</th><th scope="col"><span class="sr-only">详情</span></th>
            </tr></thead>
            <tbody>
              <tr v-for="element in data?.items ?? []" :key="elementKey(element)" class="cursor-pointer"
                :class="{ 'bg-selected': detailOpen && history[0] && elementKey(history[0]) === elementKey(element) }" @click="open(element, $event)">
                <th scope="row">
                  <button type="button" class="editorial-text-action flex max-w-full items-center gap-2 !px-0 text-left"
                    :aria-label="'查看' + elementKinds[element.kind].label + ' ' + element.name"
                    aria-haspopup="dialog"
                    @click.stop="open(element, $event)">
                    <component :is="elementKinds[element.kind].icon" class="size-4 shrink-0 text-muted" aria-hidden="true" />
                    <span class="font-mono font-medium [overflow-wrap:anywhere]">{{ element.name }}</span>
                  </button>
                  <p v-if="element.comment" class="mt-1 truncate pl-6 text-xs text-muted" :title="element.comment">{{ element.comment }}</p>
                </th>
                <td class="whitespace-nowrap text-muted">{{ elementKinds[element.kind].label }}</td>
                <td>
                  <span class="text-xs text-muted [overflow-wrap:anywhere]">{{ elementPath(element) }}</span>
                </td>
                <td class="text-sm [overflow-wrap:anywhere]">
                  <span>{{ elementAttribute(element) }}</span>
                  <span v-if="element.level === 'sensitive' || element.level === 'confidential'" class="mt-1 block w-fit whitespace-nowrap"
                    :class="element.level === 'sensitive' ? 'text-warning' : 'text-danger'">{{ elementLevels[element.level] }}</span>
                </td>
                <td><ChevronRight class="size-4 text-muted" aria-hidden="true" /></td>
              </tr>
            </tbody>
          </table>
        </div>
        <LoadingState v-if="loading" variant="inline" label="正在读取技术元素…" />
        <PageState v-else-if="list.state.value.status === 'error'" heading="h3" title="无法读取技术资产" tone="danger"
          :description="list.state.value.message || '请重试当前筛选条件。'">
          <template #actions><button type="button" class="ghost-button ui-control-target" @click="read">重试</button></template>
        </PageState>
        <PageState v-else-if="data && !data.items.length" heading="h3" title="没有符合条件的技术元素"
          :description="filterCount || keyword ? '试试其他关键词或清除筛选。' : '采集数据源的结构后，可在这里查阅技术元素。'" />
        <nav class="flex flex-wrap items-center justify-end gap-2 text-sm text-muted" aria-label="技术资产分页">
          <button type="button" class="ghost-button ui-control-target aria-disabled:opacity-50" :aria-disabled="(!data || page <= 1) || undefined" @click="changePage(page - 1)">上一页</button>
          <span>{{ data ? page + ' / ' + pageCount : '第 ' + page + ' 页' }}</span>
          <button type="button" class="ghost-button ui-control-target aria-disabled:opacity-50" :aria-disabled="(!data || page >= pageCount) || undefined" @click="changePage(page + 1)">下一页</button>
        </nav>
      </div>
      <el-dialog v-model="detailOpen" :title="selected?.name || '技术资产详情'" class="catalog-detail-dialog"
        modal-class="catalog-detail-overlay" align-center append-to-body destroy-on-close :lock-scroll="false"
        @closed="finishClose">
        <template #header="{ titleId, titleClass }">
          <h2 :id="titleId" ref="detailHeading" :class="titleClass" tabindex="-1" class="flex items-center gap-2">
            <component v-if="selected" :is="elementKinds[selected.kind].icon" class="size-4 shrink-0 text-accent" aria-hidden="true" />
            <span class="min-w-0 font-mono [overflow-wrap:anywhere]">{{ selected?.name || '技术资产详情' }}</span>
          </h2>
        </template>
        <TechnicalAssetDetail v-if="selected" :element="selected" :can-go-back="history.length > 1"
          @back="back" @open-related="drill" @open-object="openObject" />
      </el-dialog>
    </section>
  </div>
</template>

<style scoped>
.technical-inventory {
  display: grid;
  grid-template-columns: 12rem minmax(0, 1fr);
  gap: var(--db-section-gap);
}
.technical-filter-rail {
  border-inline-end: 1px solid var(--db-divider);
  padding-inline-end: 0.875rem;
}
.technical-filters { display: grid; gap: 1rem; }
.technical-filters fieldset { min-inline-size: 0; }
.technical-mobile-filter { display: none; }
.filter-options-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 0.125rem 0.375rem; }
.filter-option {
  display: flex;
  align-items: center;
  gap: 0.375rem;
  min-height: 1.875rem;
  font-size: 0.875rem;
  cursor: pointer;
  overflow-wrap: anywhere;
}
.filter-option input { flex: none; accent-color: var(--db-action-primary); }
.filter-sources { max-height: 11rem; overflow-y: auto; }
.technical-table { table-layout: fixed; min-width: 32rem; }
.technical-table :is(th, td) { padding: 0.625rem 0.75rem; }
.technical-table th:nth-child(1) { width: 32%; white-space: normal; }
.technical-table th:nth-child(2) { width: 4.5rem; }
.technical-table th:nth-child(4) { width: 20%; }
.technical-table th:nth-child(5) { width: 2rem; }
.technical-table th[scope="row"] { font-size: 0.875rem; }
.technical-table td:last-child { padding-inline: 0.375rem; }
@media (max-width: 47.999rem) {
  .technical-inventory { grid-template-columns: minmax(0, 1fr); gap: 0.75rem; }
  .technical-filter-rail { border: 0; padding: 0; }
  .technical-mobile-filter { display: flex; }
  .technical-filters { display: none; }
  .technical-filters.filters-open {
    display: grid;
    margin-top: 0.5rem;
    padding: 0.75rem;
    border: 1px solid var(--db-border);
    background: var(--db-surface-data);
  }
  .filter-option { min-height: var(--db-control-height-sm); }
}
</style>
