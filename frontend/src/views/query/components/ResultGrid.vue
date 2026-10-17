<template>
  <div
    ref="container"
    class="min-h-0 flex-1 overflow-auto outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-indigo-400"
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
          <td :colspan="columns.length + 1" class="!h-24 text-center !text-white/55">查询成功，未返回数据</td>
        </tr>
        <template v-else>
          <tr v-if="startIndex" aria-hidden="true">
            <td :colspan="columns.length + 1" class="spacer" :style="{ height: (startIndex * ROW_HEIGHT) + 'px' }" />
          </tr>
          <tr v-for="(row, index) in visibleRows" :key="startIndex + index" class="data-row">
            <th scope="row" class="row-number">{{ startIndex + index + 1 }}</th>
            <td v-for="(cell, columnIndex) in row" :key="columnIndex" :title="cell === null ? 'NULL' : formatCell(cell)">
              <span v-if="cell === null || cell === undefined" class="italic text-white/40">NULL</span>
              <template v-else>{{ formatCell(cell) }}</template>
            </td>
          </tr>
          <tr v-if="bottomSpace" aria-hidden="true">
            <td :colspan="columns.length + 1" class="spacer" :style="{ height: bottomSpace + 'px' }" />
          </tr>
        </template>
      </tbody>
    </table>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'

const props = defineProps<{ columns: string[]; rows: unknown[][] }>()
const ROW_HEIGHT = 36
const HEADER_HEIGHT = 38
const BUFFER = 6
const container = ref<HTMLDivElement | null>(null)
const scrollTop = ref(0)
const viewportHeight = ref(300)
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

watch(() => props.rows, () => {
  scrollTop.value = 0
  if (container.value) container.value.scrollTop = 0
})

onMounted(() => {
  if (!container.value) return
  observer = new ResizeObserver(() => { viewportHeight.value = container.value?.clientHeight ?? 300 })
  observer.observe(container.value)
  viewportHeight.value = container.value.clientHeight
})
onBeforeUnmount(() => observer?.disconnect())
</script>

<style scoped>
.result-table { width: 100%; table-layout: fixed; border-collapse: separate; border-spacing: 0; font-size: 12px; }
.result-table th, .result-table td {
  height: 36px; box-sizing: border-box; padding: 0 12px;
  overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
  border-bottom: 1px solid rgb(255 255 255 / 6%); text-align: left;
  color: rgb(255 255 255 / 80%); font-weight: normal;
}
.result-table thead th {
  position: sticky; top: 0; z-index: 1; height: 38px;
  background: #20243a; color: rgb(255 255 255 / 65%); font-weight: 500;
}
.result-table .row-number { width: 56px; text-align: right; color: rgb(255 255 255 / 40%); }
.result-table .spacer { padding: 0; border: 0; }
.data-row:hover { background: rgb(255 255 255 / 3%); }
</style>
