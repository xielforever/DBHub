<template>
  <div class="flex flex-col h-full min-h-0">
    <!-- 工具条 -->
    <div class="flex flex-wrap items-center gap-2 px-3 py-2 border-b border-white/10 bg-white/[0.02] shrink-0">
      <div class="flex items-center gap-1.5 text-[11px] text-white/45">
        <span v-if="columns.length" class="px-2 py-0.5 rounded-full bg-white/10 text-white/60">{{ sortedRows.length }} 行 · {{ columns.length }} 列</span>
        <span v-if="truncated" class="px-2 py-0.5 rounded-full bg-amber-400/15 text-amber-300">截断 1000</span>
        <span v-if="hasNoLimit" class="px-2 py-0.5 rounded-full bg-amber-400/15 text-amber-300 flex items-center gap-1">
          <AlertTriangle class="w-3 h-3" /> SELECT 无 LIMIT
        </span>
        <span v-if="isExplain" class="px-2 py-0.5 rounded-full bg-indigo-400/15 text-indigo-300 flex items-center gap-1">
          <FileSearch class="w-3 h-3" /> EXPLAIN
        </span>
      </div>
      <div class="flex-1" />
      <div class="flex items-center gap-1">
        <button class="ghost-button !py-1 !px-2 text-[11px] flex items-center gap-1" :disabled="!columns.length" @click="copyAsCSV">
          <Copy class="w-3 h-3" /> CSV
        </button>
        <button class="ghost-button !py-1 !px-2 text-[11px] flex items-center gap-1" :disabled="!columns.length" @click="copyAsJSON">
          <Braces class="w-3 h-3" /> JSON
        </button>
        <button class="ghost-button !py-1 !px-2 text-[11px] flex items-center gap-1" :disabled="!columns.length" @click="exportCSV">
          <Download class="w-3 h-3" /> 导出
        </button>
        <button class="ghost-button !py-1 !px-2 text-[11px] flex items-center gap-1" @click="emit('toggle-fullscreen')">
          <Maximize2 class="w-3 h-3" /> {{ fullscreen ? '退出全屏' : '全屏' }}
        </button>
      </div>
    </div>

    <!-- 空态 -->
    <div v-if="!columns.length" class="flex-1 flex flex-col items-center justify-center gap-3 text-sm text-white/35 py-12">
      <div class="w-16 h-16 rounded-2xl bg-white/5 border border-white/10 flex items-center justify-center">
        <Table2 class="w-8 h-8 text-white/20" />
      </div>
      <p class="text-white/50">暂无结果</p>
      <p class="text-xs text-white/30 max-w-[320px] text-center leading-5">选中左侧表可直接浏览数据，或在上方编辑器编写 SQL 后点击「运行」<br/>支持 EXPLAIN 查看执行计划</p>
      <p class="text-[11px] text-white/25 mt-2 flex items-center gap-2"><Keyboard class="w-3 h-3" /> Ctrl+Enter 运行 · Ctrl+Shift+F 格式化 · ? 快捷键</p>
    </div>

    <!-- PG EXPLAIN 可视化 -->
    <div v-else-if="isPgExplain" class="flex-1 overflow-auto p-3 space-y-1.5 bg-[#0e0e1a]/50">
      <div v-for="(row, i) in sortedRows" :key="i" class="font-mono text-xs leading-6 px-3 py-1 rounded-lg hover:bg-white/5 flex items-center gap-2" :style="{ paddingLeft: (indentLevel(row[0] as string) * 16 + 12) + 'px' }">
        <span class="w-1.5 h-1.5 rounded-full shrink-0" :class="planDot(row[0] as string)" />
        <span class="text-white/70 truncate">{{ (row[0] as string).trim() }}</span>
        <span v-if="extractCost(row[0] as string)" class="ml-auto text-[10px] px-1.5 py-0.5 rounded bg-amber-400/10 text-amber-300 font-mono">{{ extractCost(row[0] as string) }}</span>
      </div>
      <div class="mt-3 p-2.5 rounded-xl bg-indigo-500/10 border border-indigo-400/20 text-[11px] text-indigo-200/60">
        💡 Seq Scan 表示全表扫描，若 rows 较大建议添加索引；关注 cost 与实际 Execution Time
      </div>
    </div>

    <!-- 虚拟滚动网格 -->
    <div v-else class="flex-1 min-h-0 flex flex-col overflow-hidden">
      <!-- 表头 -->
      <div class="shrink-0 overflow-hidden border-b border-white/10 bg-[#141428]/90 backdrop-blur">
        <table class="w-full text-sm border-collapse table-fixed">
          <colgroup>
            <col style="width: 48px" />
            <col v-for="(col, idx) in columns" :key="col" :style="{ width: (colWidths[idx] || 160) + 'px' }" />
          </colgroup>
          <thead class="text-left text-white/55 text-xs">
            <tr>
              <th class="px-2 py-2.5 font-medium text-right text-white/30 sticky left-0 bg-[#141428]/90 backdrop-blur border-r border-white/5">#</th>
              <th
                v-for="(col, idx) in columns"
                :key="col"
                class="px-3 py-2.5 font-medium whitespace-nowrap relative group/col select-none cursor-pointer hover:text-white hover:bg-white/5"
                @click="toggleSort(idx)"
              >
                <div class="flex items-center gap-1.5">
                  <span class="truncate">{{ col }}</span>
                  <span v-if="isNumericColumn(idx)" class="text-[10px] px-1 rounded bg-indigo-400/20 text-indigo-300">#</span>
                  <span v-if="sortColumn === idx" class="ml-1">
                    <ChevronUp v-if="sortDirection === 'asc'" class="w-3 h-3" />
                    <ChevronDown v-else class="w-3 h-3" />
                  </span>
                </div>
                <span
                  class="absolute top-0 right-0 h-full w-1 cursor-col-resize bg-white/0 hover:bg-indigo-400/50 transition-colors"
                  @mousedown.prevent="startResize($event, idx)"
                />
              </th>
            </tr>
          </thead>
        </table>
      </div>

      <!-- 虚拟滚动体 -->
      <div ref="scrollContainer" class="flex-1 overflow-auto outline-none relative" tabindex="0" @scroll="onScroll">
        <div :style="{ height: totalHeight + 'px', position: 'relative' }">
          <div :style="{ transform: `translateY(${offsetY}px)`, position: 'absolute', top: '0', left: '0', right: '0' }">
            <table class="w-full text-sm border-collapse table-fixed">
              <colgroup>
                <col style="width: 48px" />
                <col v-for="(col, idx) in columns" :key="col" :style="{ width: (colWidths[idx] || 160) + 'px' }" />
              </colgroup>
              <tbody>
                <tr
                  v-for="(row, vi) in visibleRows"
                  :key="startIndex + vi"
                  class="border-b border-white/5 hover:bg-white/[0.06] transition-colors"
                  :class="{ 'bg-indigo-500/10': selectedRow === startIndex + vi }"
                  @click="selectedRow = startIndex + vi"
                >
                  <td class="px-2 py-2 text-right text-white/25 text-xs sticky left-0 bg-[#141428]/80 backdrop-blur border-r border-white/5">{{ baseIndex + startIndex + vi + 1 }}</td>
                  <td
                    v-for="(cell, ci) in row"
                    :key="ci"
                    class="px-3 py-2 whitespace-nowrap max-w-[360px] truncate group/cell relative"
                    :class="cellClass(cell, ci)"
                    :title="cellTitle(cell)"
                    @click.stop="copyCell(cell)"
                  >
                    <span v-if="cell === null || cell === undefined" class="text-white/25 italic select-none">NULL</span>
                    <span v-else-if="isMysqlExplain && columns[ci]?.toLowerCase() === 'type'" class="px-1.5 py-0.5 rounded-full text-[10px]" :class="mysqlTypeBadge(cell as string)">{{ cell }}</span>
                    <span v-else class="flex items-center gap-1">
                      <span class="truncate">{{ formatCell(cell) }}</span>
                      <Copy class="w-3 h-3 opacity-0 group-hover/cell:opacity-60 text-white/40 ml-1 shrink-0" />
                    </span>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>
      </div>

      <div class="flex items-center justify-between px-3 py-1.5 border-t border-white/10 bg-white/[0.02] text-[11px] text-white/35 shrink-0">
        <span>显示 {{ startIndex + 1 }}-{{ Math.min(startIndex + visibleRows.length, sortedRows.length) }} / {{ sortedRows.length }} 行</span>
        <span v-if="truncated" class="text-amber-300/80">结果超过 1000 行，仅显示前 1000 行，建议添加 LIMIT</span>
        <span v-else-if="sortedRows.length > 100" class="text-white/25">虚拟滚动已启用 · 滚动流畅</span>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch, onMounted, onBeforeUnmount } from 'vue'
import { ElMessage } from 'element-plus'
import { AlertTriangle, Braces, ChevronDown, ChevronUp, Copy, Download, FileSearch, Keyboard, Maximize2, Table2 } from 'lucide-vue-next'

const props = withDefaults(
  defineProps<{
    columns: string[]
    rows: unknown[][]
    truncated?: boolean
    baseIndex?: number
    sql?: string
    fullscreen?: boolean
  }>(),
  {
    truncated: false,
    baseIndex: 0,
    sql: '',
    fullscreen: false,
  },
)

const emit = defineEmits<{
  (e: 'toggle-fullscreen'): void
}>()

const ROW_HEIGHT = 36
const BUFFER = 10

const scrollContainer = ref<HTMLDivElement | null>(null)
const scrollTop = ref(0)
const containerHeight = ref(400)
const selectedRow = ref<number | null>(null)
const colWidths = ref<number[]>([])
const sortColumn = ref<number | null>(null)
const sortDirection = ref<'asc' | 'desc'>('asc')

const hasNoLimit = computed(() => {
  const s = (props.sql || '').toUpperCase()
  if (!s.includes('SELECT')) return false
  if (s.includes('EXPLAIN')) return false
  return !s.includes('LIMIT')
})

const isPgExplain = computed(() => props.columns.length === 1 && props.columns[0] === 'QUERY PLAN')
const isMysqlExplain = computed(() => {
  const cols = props.columns.map((c) => c.toLowerCase())
  return cols.includes('select_type') && cols.includes('type')
})
const isExplain = computed(() => isPgExplain.value || isMysqlExplain.value)

const sortedRows = computed(() => {
  if (sortColumn.value === null || isExplain.value) return props.rows
  const idx = sortColumn.value
  const dir = sortDirection.value === 'asc' ? 1 : -1
  const copy = [...props.rows]
  copy.sort((a: any, b: any) => {
    const av = a[idx]
    const bv = b[idx]
    if (av === null || av === undefined) return 1
    if (bv === null || bv === undefined) return -1
    const an = Number(av)
    const bn = Number(bv)
    if (!isNaN(an) && !isNaN(bn)) return (an - bn) * dir
    return String(av).localeCompare(String(bv)) * dir
  })
  return copy
})

function toggleSort(idx: number) {
  if (isExplain.value) return
  if (sortColumn.value === idx) {
    sortDirection.value = sortDirection.value === 'asc' ? 'desc' : 'asc'
  } else {
    sortColumn.value = idx
    sortDirection.value = 'asc'
  }
}

const totalHeight = computed(() => sortedRows.value.length * ROW_HEIGHT)
const visibleCount = computed(() => Math.ceil(containerHeight.value / ROW_HEIGHT) + BUFFER * 2)
const startIndex = computed(() => Math.max(0, Math.floor(scrollTop.value / ROW_HEIGHT) - BUFFER))
const offsetY = computed(() => startIndex.value * ROW_HEIGHT)
const visibleRows = computed(() => sortedRows.value.slice(startIndex.value, startIndex.value + visibleCount.value))

function onScroll() {
  if (scrollContainer.value) {
    scrollTop.value = scrollContainer.value.scrollTop
  }
}

let resizeObserver: ResizeObserver | null = null
onMounted(() => {
  if (scrollContainer.value) {
    containerHeight.value = scrollContainer.value.clientHeight
    resizeObserver = new ResizeObserver(() => {
      if (scrollContainer.value) containerHeight.value = scrollContainer.value.clientHeight
    })
    resizeObserver.observe(scrollContainer.value)
  }
})
onBeforeUnmount(() => {
  resizeObserver?.disconnect()
})

watch(() => props.rows, () => {
  if (scrollContainer.value) scrollContainer.value.scrollTop = 0
  scrollTop.value = 0
  selectedRow.value = null
})

function isNumericColumn(idx: number) {
  if (!sortedRows.value.length) return false
  const sample = sortedRows.value.slice(0, 20)
  return sample.every((r) => {
    const v = (r as any)[idx]
    return v === null || v === undefined || v === '' || !isNaN(Number(v))
  })
}

function cellClass(cell: unknown, colIdx: number) {
  if (cell === null || cell === undefined) return 'text-white/25'
  if (isNumericColumn(colIdx)) return 'text-indigo-200/90 text-right font-mono'
  if (typeof cell === 'boolean') return 'text-amber-200/80'
  return 'text-white/75'
}

function formatCell(cell: unknown): string {
  if (cell === null || cell === undefined) return ''
  if (typeof cell === 'object') {
    try {
      return JSON.stringify(cell)
    } catch {
      return String(cell)
    }
  }
  return String(cell)
}

function cellTitle(cell: unknown): string {
  if (cell === null || cell === undefined) return 'NULL'
  return formatCell(cell)
}

async function copyCell(cell: unknown) {
  const text = cell === null || cell === undefined ? '' : formatCell(cell)
  try {
    await navigator.clipboard.writeText(text)
    ElMessage.success({ message: '已复制', duration: 1200 })
  } catch {
    ElMessage.warning('复制失败')
  }
}

async function copyAsCSV() {
  if (!props.columns.length) return
  const header = props.columns.join(',')
  const lines = sortedRows.value.map((r) =>
    (r as any[])
      .map((c) => {
        const s = formatCell(c)
        if (s.includes(',') || s.includes('"') || s.includes('\n')) {
          return `"${s.replace(/"/g, '""')}"`
        }
        return s
      })
      .join(','),
  )
  const csv = [header, ...lines].join('\n')
  try {
    await navigator.clipboard.writeText(csv)
    ElMessage.success('CSV 已复制到剪贴板')
  } catch {
    ElMessage.warning('复制失败')
  }
}

async function copyAsJSON() {
  if (!props.columns.length) return
  const arr = sortedRows.value.map((r) => {
    const obj: Record<string, unknown> = {}
    props.columns.forEach((c, i) => {
      obj[c] = (r as any)[i]
    })
    return obj
  })
  try {
    await navigator.clipboard.writeText(JSON.stringify(arr, null, 2))
    ElMessage.success('JSON 已复制')
  } catch {
    ElMessage.warning('复制失败')
  }
}

function exportCSV() {
  if (!props.columns.length) return
  const header = props.columns.join(',')
  const lines = sortedRows.value.map((r) =>
    (r as any[])
      .map((c) => {
        const s = formatCell(c)
        if (s.includes(',') || s.includes('"') || s.includes('\n')) {
          return `"${s.replace(/"/g, '""')}"`
        }
        return s
      })
      .join(','),
  )
  const csv = [header, ...lines].join('\n')
  const blob = new Blob([csv], { type: 'text/csv;charset=utf-8;' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = `query_result_${new Date().toISOString().slice(0, 19).replace(/[:T]/g, '-')}.csv`
  a.click()
  URL.revokeObjectURL(url)
}

function startResize(e: MouseEvent, idx: number) {
  const startX = e.clientX
  const startW = colWidths.value[idx] || 160
  const onMove = (ev: MouseEvent) => {
    const delta = ev.clientX - startX
    const newW = Math.max(80, startW + delta)
    const copy = [...colWidths.value]
    while (copy.length < props.columns.length) copy.push(160)
    copy[idx] = newW
    colWidths.value = copy
  }
  const onUp = () => {
    window.removeEventListener('mousemove', onMove)
    window.removeEventListener('mouseup', onUp)
  }
  window.addEventListener('mousemove', onMove)
  window.addEventListener('mouseup', onUp)
}

// PG Explain helpers
function indentLevel(text: string): number {
  const leading = text.match(/^\s*/)?.[0].length || 0
  return Math.floor(leading / 2)
}
function planDot(text: string) {
  if (text.includes('Seq Scan')) return 'bg-rose-400'
  if (text.includes('Index Scan') || text.includes('Index Only Scan')) return 'bg-emerald-400'
  if (text.includes('Bitmap')) return 'bg-amber-400'
  if (text.includes('Sort') || text.includes('Hash')) return 'bg-violet-400'
  return 'bg-white/20'
}
function extractCost(text: string) {
  const m = text.match(/cost=[\d.]+\.\.([\d.]+)/)
  return m ? `cost ${m[1]}` : ''
}
function mysqlTypeBadge(t: string) {
  const map: Record<string, string> = {
    ALL: 'bg-rose-500/20 text-rose-300',
    index: 'bg-amber-500/20 text-amber-300',
    range: 'bg-emerald-500/20 text-emerald-300',
    ref: 'bg-sky-500/20 text-sky-300',
    eq_ref: 'bg-indigo-500/20 text-indigo-300',
    const: 'bg-emerald-500/30 text-emerald-200',
  }
  return map[t] || 'bg-white/10 text-white/50'
}
</script>
