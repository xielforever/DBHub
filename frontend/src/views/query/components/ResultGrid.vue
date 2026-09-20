<template>
  <div class="flex flex-col h-full min-h-0">
    <!-- 工具条 -->
    <div class="flex flex-wrap items-center gap-2 px-3 py-2 border-b border-white/10 bg-white/[0.02] shrink-0">
      <div class="flex items-center gap-1.5 text-[11px] text-white/45">
        <span v-if="columns.length" class="px-2 py-0.5 rounded-full bg-white/10 text-white/60">{{ rows.length }} 行 · {{ columns.length }} 列</span>
        <span v-if="truncated" class="px-2 py-0.5 rounded-full bg-amber-400/15 text-amber-300">截断 1000</span>
        <span v-if="hasNoLimit" class="px-2 py-0.5 rounded-full bg-amber-400/15 text-amber-300 flex items-center gap-1">
          <AlertTriangle class="w-3 h-3" /> SELECT 无 LIMIT
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
    <div v-if="!columns.length" class="flex-1 flex flex-col items-center justify-center gap-2 text-sm text-white/35 py-10">
      <Table2 class="w-8 h-8 text-white/20" />
      <p>选中表可直接浏览数据，或编写 SQL 后点击「运行」</p>
      <p class="text-[11px] text-white/25">快捷键 Ctrl+Enter 运行 · Ctrl+Shift+F 格式化</p>
    </div>

    <!-- 网格 -->
    <div v-else class="flex-1 overflow-auto outline-none relative" tabindex="0">
      <table class="w-full text-sm border-collapse">
        <thead class="sticky top-0 z-10 bg-[#141428]/90 backdrop-blur">
          <tr class="text-left text-white/55 text-xs">
            <th class="px-2 py-2 font-medium border-b border-white/10 w-12 text-right text-white/30 sticky left-0 bg-[#141428]/90 backdrop-blur">#</th>
            <th
              v-for="(col, idx) in columns"
              :key="col"
              class="px-3 py-2.5 font-medium border-b border-white/10 whitespace-nowrap relative group/col select-none"
              :style="{ width: colWidths[idx] ? colWidths[idx] + 'px' : 'auto', minWidth: '80px' }"
            >
              <div class="flex items-center gap-1.5">
                <span class="truncate">{{ col }}</span>
                <span v-if="isNumericColumn(idx)" class="text-[10px] px-1 rounded bg-indigo-400/20 text-indigo-300">#</span>
              </div>
              <!-- 拖拽手柄 -->
              <span
                class="absolute top-0 right-0 h-full w-1 cursor-col-resize bg-white/0 hover:bg-indigo-400/50 transition-colors"
                @mousedown.prevent="startResize($event, idx)"
              />
            </th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="(row, i) in rows"
            :key="i"
            class="border-b border-white/5 hover:bg-white/[0.06] transition-colors"
            :class="{ 'bg-indigo-500/10': selectedRow === i }"
            @click="selectedRow = i"
          >
            <td class="px-2 py-2 text-right text-white/25 text-xs sticky left-0 bg-[#141428]/80 backdrop-blur border-r border-white/5">{{ baseIndex + i + 1 }}</td>
            <td
              v-for="(cell, ci) in row"
              :key="ci"
              class="px-3 py-2 whitespace-nowrap max-w-[360px] truncate group/cell relative"
              :class="cellClass(cell, ci)"
              :title="cellTitle(cell)"
              @click.stop="copyCell(cell)"
            >
              <span v-if="cell === null || cell === undefined" class="text-white/25 italic select-none">NULL</span>
              <span v-else class="flex items-center gap-1">
                <span class="truncate">{{ formatCell(cell) }}</span>
                <Copy class="w-3 h-3 opacity-0 group-hover/cell:opacity-60 text-white/40 ml-1 shrink-0" />
              </span>
            </td>
          </tr>
        </tbody>
      </table>
      <p v-if="truncated" class="px-4 py-2 text-xs text-amber-300/80 sticky bottom-0 bg-amber-950/30 backdrop-blur border-t border-amber-400/20">结果超过 1000 行，仅显示前 1000 行，建议添加 LIMIT</p>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { AlertTriangle, Braces, Copy, Download, Maximize2, Table2 } from 'lucide-vue-next'

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

const selectedRow = ref<number | null>(null)
const colWidths = ref<number[]>([])

const hasNoLimit = computed(() => {
  const s = (props.sql || '').toUpperCase()
  if (!s.includes('SELECT')) return false
  return !s.includes('LIMIT')
})

function isNumericColumn(idx: number) {
  if (!props.rows.length) return false
  const sample = props.rows.slice(0, 20)
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
  const lines = props.rows.map((r) =>
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
  const arr = props.rows.map((r) => {
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
  const lines = props.rows.map((r) =>
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
    // 确保数组长度
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
</script>
