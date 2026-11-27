<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'

const props = defineProps<{ columns: string[]; rows: unknown[][] }>()
const ROW_HEIGHT = 36
const HEADER_HEIGHT = 38
const BUFFER = 6
const container = ref<HTMLDivElement | null>(null)
const scrollTop = ref(0)
const viewportHeight = ref(300)
const clippedCells = ref<Set<string>>(new Set())
const dialogOpen = ref(false)
const fullValue = ref<{ text: string; column: string; row: number }>()
const copyStatus = ref('')
let cellTrigger: HTMLButtonElement | null = null
let observer: ResizeObserver | null = null

const startIndex = computed(() => Math.max(0, Math.floor(Math.max(0, scrollTop.value - HEADER_HEIGHT) / ROW_HEIGHT) - BUFFER))
const visibleCount = computed(() => Math.ceil(viewportHeight.value / ROW_HEIGHT) + BUFFER * 2)
const visibleRows = computed(() => props.rows.slice(startIndex.value, startIndex.value + visibleCount.value))
const bottomSpace = computed(() => Math.max(0, props.rows.length - startIndex.value - visibleRows.value.length) * ROW_HEIGHT)

function onScroll() {
  scrollTop.value = container.value?.scrollTop ?? 0
}

function formatCell(cell: unknown): string {
  if (cell === null || cell === undefined) return ''
  if (typeof cell === 'object') {
    try { return JSON.stringify(cell) } catch { return String(cell) }
  }
  return String(cell)
}

function measureCells() {
  const clipped = new Set<string>()
  container.value?.querySelectorAll<HTMLElement>('[data-result-cell]').forEach(cell => {
    const key = cell.dataset.resultCell
    if (key && (cell.scrollWidth > cell.clientWidth || /[\r\n]/.test(cell.textContent ?? ''))) clipped.add(key)
  })
  clippedCells.value = clipped
}

function openValue(event: MouseEvent, value: unknown, row: number, columnIndex: number) {
  if (!(event.currentTarget instanceof HTMLButtonElement)) return
  cellTrigger = event.currentTarget
  fullValue.value = { text: formatCell(value), column: props.columns[columnIndex] ?? '', row: row + 1 }
  copyStatus.value = ''
  dialogOpen.value = true
}

async function copyValue() {
  const value = fullValue.value
  if (!value) return
  let status = '已复制完整值。'
  try {
    await navigator.clipboard.writeText(value.text)
  } catch {
    status = '复制失败，请选择并复制原文。'
  }
  if (value === fullValue.value && dialogOpen.value) copyStatus.value = status
}

function closeValue() {
  fullValue.value = undefined
  const focusTarget = cellTrigger?.isConnected ? cellTrigger : container.value
  focusTarget?.focus({ preventScroll: true })
  cellTrigger = null
}

watch(() => props.rows, () => {
  scrollTop.value = 0
  if (container.value) container.value.scrollTop = 0
  if (dialogOpen.value) {
    dialogOpen.value = false
    cellTrigger = null
  }
})
watch([visibleRows, () => props.columns], measureCells, { flush: 'post' })

onMounted(() => {
  if (!container.value) return
  observer = new ResizeObserver(() => {
    viewportHeight.value = container.value?.clientHeight ?? 300
    measureCells()
  })
  observer.observe(container.value)
  viewportHeight.value = container.value.clientHeight
  void nextTick(measureCells)
})
onBeforeUnmount(() => observer?.disconnect())
</script>

<template>
  <div
    ref="container"
    class="result-grid ui-focus-target min-h-0 flex-1 overflow-auto"
    role="region"
    aria-label="SQL 查询结果表格"
    tabindex="0"
    @scroll="onScroll"
  >
    <table class="result-table" :style="{ minWidth: (columns.length * 180 + 56) + 'px' }">
      <caption class="sr-only">查询结果，共 {{ rows.length }} 行、{{ columns.length }} 列</caption>
      <colgroup>
        <col style="width: 56px" />
        <col v-for="(_, index) in columns" :key="index" />
      </colgroup>
      <thead>
        <tr>
          <th scope="col" class="row-number">#</th>
          <th v-for="(column, index) in columns" :key="index" scope="col" :title="column">{{ column }}</th>
        </tr>
      </thead>
      <tbody>
        <tr v-if="!rows.length">
          <td :colspan="columns.length + 1" class="!h-24 text-center !text-muted">查询成功，未返回数据</td>
        </tr>
        <template v-else>
          <tr v-if="startIndex" aria-hidden="true">
            <td :colspan="columns.length + 1" class="spacer" :style="{ height: (startIndex * ROW_HEIGHT) + 'px' }" />
          </tr>
          <tr v-for="(row, index) in visibleRows" :key="startIndex + index" class="data-row">
            <th scope="row" class="row-number">{{ startIndex + index + 1 }}</th>
            <td v-for="(cell, columnIndex) in row" :key="columnIndex" :title="cell === null ? 'NULL' : formatCell(cell)">
              <span v-if="cell === null || cell === undefined" class="italic text-subtle">NULL</span>
              <component
                :is="clippedCells.has(`${startIndex + index}:${columnIndex}`) ? 'button' : 'span'"
                v-else
                :data-result-cell="`${startIndex + index}:${columnIndex}`"
                :type="clippedCells.has(`${startIndex + index}:${columnIndex}`) ? 'button' : undefined"
                :aria-label="clippedCells.has(`${startIndex + index}:${columnIndex}`) ? `查看第 ${startIndex + index + 1} 行 ${columns[columnIndex]} 的完整值` : undefined"
                class="cell-value"
                @click="openValue($event, cell, startIndex + index, columnIndex)"
              >{{ formatCell(cell) }}</component>
            </td>
          </tr>
          <tr v-if="bottomSpace" aria-hidden="true">
            <td :colspan="columns.length + 1" class="spacer" :style="{ height: bottomSpace + 'px' }" />
          </tr>
        </template>
      </tbody>
    </table>
  </div>
  <el-dialog
    v-model="dialogOpen"
    title="单元格完整值"
    width="min(92vw, 640px)"
    class="glass-dialog"
    :close-on-click-modal="false"
    @close-auto-focus="closeValue"
  >
    <template v-if="fullValue">
      <p class="text-sm text-muted">第 {{ fullValue.row }} 行 · {{ fullValue.column }}</p>
      <pre class="mt-2 max-h-[50vh] overflow-auto whitespace-pre-wrap break-all bg-surface-muted p-3 text-sm" tabindex="0" aria-label="完整值">{{ fullValue.text }}</pre>
    </template>
    <template #footer>
      <span class="mr-2 text-sm text-muted" role="status">{{ copyStatus }}</span>
      <button type="button" class="ghost-button ui-control-target" @click="copyValue">复制完整值</button>
      <button type="button" class="ghost-button ui-control-target" @click="dialogOpen = false">关闭</button>
    </template>
  </el-dialog>
</template>



<style scoped>
.result-grid:focus-visible { outline-offset: -2px; }
@media (forced-colors: active) {
  .result-grid:focus-visible { outline-color: Highlight; }
}
.result-table { width: 100%; table-layout: fixed; border-collapse: separate; border-spacing: 0; font-size: 12px; }
.result-table th, .result-table td {
  height: 36px; box-sizing: border-box; padding: 0 12px;
  overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
  border-bottom: 1px solid var(--db-divider); text-align: left;
  color: var(--db-text-secondary); font-weight: normal;
}
.result-table thead th {
  position: sticky; top: 0; z-index: 1; height: 38px;
  background: var(--db-surface-solid); color: var(--db-text-secondary); font-weight: 500;
}
.result-table .row-number { width: 56px; text-align: right; color: var(--db-text-secondary); }
.result-table .spacer { padding: 0; border: 0; }
.cell-value { display: block; width: 100%; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
button.cell-value { border: 0; background: transparent; padding: 0; text-align: inherit; color: inherit; cursor: pointer; }
button.cell-value:hover { text-decoration: underline; }
button.cell-value:focus-visible { outline: 2px solid var(--db-accent); outline-offset: -2px; }
.data-row:hover { background: var(--db-surface-muted); }
</style>
