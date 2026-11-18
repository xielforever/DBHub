<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { Camera, Images, Plus, Trash2 } from 'lucide-vue-next'

interface Snapshot {
  id: number
  name: string
  sql: string
  columns: string[]
  rows: unknown[][]
  duration_ms: number
  created_at: string
}

const props = defineProps<{
  columns: string[]
  rows: unknown[][]
  sql: string
  duration: number | null
}>()

const emit = defineEmits<{
  (e: 'restore', snap: Snapshot): void
}>()

const STORAGE_SNAP = 'dbhub_result_snapshots'

const snapshots = ref<Snapshot[]>([])
const selectedIds = reactive(new Set<number>())
const diffResult = ref<{ added: unknown[][]; removed: unknown[][]; modified: { old: unknown[]; new: unknown[] }[] } | null>(null)

try {
  const raw = localStorage.getItem(STORAGE_SNAP)
  if (raw) snapshots.value = JSON.parse(raw)
} catch {}

function saveSnapshot() {
  if (!props.columns.length) {
    ElMessage.warning('无结果可保存')
    return
  }
  const snap: Snapshot = {
    id: Date.now(),
    name: `快照 ${new Date().toLocaleString('zh-CN')}`,
    sql: props.sql.slice(0, 500),
    columns: [...props.columns],
    rows: props.rows.map(r => [...(r as any[])]),
    duration_ms: props.duration ?? 0,
    created_at: new Date().toISOString(),
  }
  snapshots.value.unshift(snap)
  persist()
  ElMessage.success('已保存快照')
}

function deleteSnapshot(id: number) {
  snapshots.value = snapshots.value.filter(s => s.id !== id)
  selectedIds.delete(id)
  persist()
}

function persist() {
  try {
    localStorage.setItem(STORAGE_SNAP, JSON.stringify(snapshots.value.slice(0, 20)))
  } catch {}
}

function toggleSelect(id: number) {
  if (selectedIds.has(id)) selectedIds.delete(id)
  else {
    if (selectedIds.size >= 2) {
      // replace oldest
      const first = Array.from(selectedIds)[0]!
      selectedIds.delete(first)
    }
    selectedIds.add(id)
  }
  diffResult.value = null
}

function runDiff() {
  const ids = Array.from(selectedIds)
  if (ids.length !== 2) return
  const a = snapshots.value.find(s => s.id === ids[0]!)!
  const b = snapshots.value.find(s => s.id === ids[1]!)!
  if (!a || !b) return

  const keyA = new Map<string, unknown[]>()
  const keyB = new Map<string, unknown[]>()

  a.rows.forEach(r => keyA.set(JSON.stringify(r), r as any))
  b.rows.forEach(r => keyB.set(JSON.stringify(r), r as any))

  const added: unknown[][] = []
  const removed: unknown[][] = []
  const modified: { old: unknown[]; new: unknown[] }[] = []

  for (const [k, v] of keyB.entries()) {
    if (!keyA.has(k)) added.push(v as any)
  }
  for (const [k, v] of keyA.entries()) {
    if (!keyB.has(k)) removed.push(v as any)
  }

  // simple modified detection: if row count same but first column same, consider modified if other cols differ
  // For simplicity, we treat modified as empty for now, but we can compute by first col equality
  if (a.columns.length && a.rows.length && b.rows.length) {
    const firstColA = new Map<string, unknown[]>()
    a.rows.forEach(r => firstColA.set(String((r as any)[0]), r as any))
    b.rows.forEach(r => {
      const key = String((r as any)[0])
      const old = firstColA.get(key)
      if (old && JSON.stringify(old) !== JSON.stringify(r)) {
        modified.push({ old, new: r as any })
      }
    })
  }

  diffResult.value = { added, removed, modified }
}

const diffPreview = computed(() => {
  if (!diffResult.value) return []
  const out: { type: 'added' | 'removed' | 'modified'; text: string }[] = []
  diffResult.value.added.slice(0, 5).forEach(r => out.push({ type: 'added', text: JSON.stringify(r).slice(0, 120) }))
  diffResult.value.removed.slice(0, 5).forEach(r => out.push({ type: 'removed', text: JSON.stringify(r).slice(0, 120) }))
  diffResult.value.modified.slice(0, 5).forEach(m => out.push({ type: 'modified', text: `${JSON.stringify(m.old).slice(0, 60)} => ${JSON.stringify(m.new).slice(0, 60)}` }))
  return out
})

function restoreSnapshot(snap: Snapshot) {
  emit('restore', snap)
  ElMessage.success(`已恢复快照：${snap.name}`)
}

function exportSnapshot(snap: Snapshot) {
  const blob = new Blob([JSON.stringify(snap, null, 2)], { type: 'application/json' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = `snapshot_${snap.id}.json`
  a.click()
  URL.revokeObjectURL(url)
}

function formatTime(s: string): string {
  return new Date(s).toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' })
}
</script>

<template>
  <div class="flex flex-col h-full min-h-0">
    <div class="px-3 py-2 border-b border-stroke flex items-center gap-2 shrink-0 bg-surface-muted">
      <div class="w-6 h-6 rounded-lg bg-accent-soft border border-accent/30 flex items-center justify-center">
        <Camera class="w-3.5 h-3.5 text-accent" />
      </div>
      <div class="flex-1 min-w-0">
        <p class="text-xs font-medium text-content">结果快照</p>
        <p class="text-xs text-subtle">保存结果集 · 对比差异 · 导出</p>
      </div>
      <span class="px-2 py-0.5 rounded-full bg-surface-muted text-muted text-xs">{{ snapshots.length }} 个</span>
      <button class="ghost-button !py-1 !px-2.5 text-xs flex items-center gap-1 ui-control-target" :disabled="!columns.length" @click="saveSnapshot">
        <Plus class="w-3 h-3" /> 保存当前
      </button>
    </div>

    <div v-if="!snapshots.length" class="flex-1 flex flex-col items-center justify-center gap-3 p-6 text-center">
      <div class="w-12 h-12 rounded-2xl bg-surface-muted border border-stroke flex items-center justify-center">
        <Images class="w-6 h-6 text-subtle" />
      </div>
      <p class="text-xs text-muted">暂无快照</p>
      <p class="text-xs text-subtle leading-5">执行查询后点击「保存当前」可保存结果快照<br/>用于对比不同时间或不同参数的查询结果</p>
    </div>

    <div v-else class="flex-1 flex flex-col min-h-0 overflow-hidden">
      <div class="flex-1 overflow-auto p-3 space-y-2">
        <div
          v-for="snap in snapshots"
          :key="snap.id"
          class="ui-inset-card group rounded-xl border p-3"
          :class="selectedIds.has(snap.id) ? 'ui-inset-card--accent bg-accent-soft border-accent/30' : 'bg-surface-muted border-stroke'"
        >
          <div class="flex items-start justify-between gap-2">
            <div class="flex items-center gap-2 flex-1 min-w-0">
              <input type="checkbox" :checked="selectedIds.has(snap.id)" @change="toggleSelect(snap.id)" />
              <div class="flex-1 min-w-0">
                <p class="text-xs font-medium text-content truncate flex items-center gap-1.5">
                  {{ snap.name }}
                  <span class="px-1 py-0.5 rounded text-xs bg-surface-muted text-subtle">{{ snap.columns.length }} 列 · {{ snap.rows.length }} 行</span>
                </p>
                <p class="text-xs font-mono text-subtle truncate mt-0.5">{{ snap.sql.slice(0, 80) }}</p>
                <p class="text-xs text-subtle mt-1">{{ formatTime(snap.created_at) }} · 耗时 {{ snap.duration_ms }}ms</p>
              </div>
            </div>
            <div class="flex gap-1 opacity-0 group-hover:opacity-100">
              <button class="ghost-button !py-1 !px-1.5 text-xs ui-control-target" @click="restoreSnapshot(snap)">恢复</button>
              <button class="ghost-button !py-1 !px-1.5 text-xs ui-control-target" @click="exportSnapshot(snap)">导出</button>
              <button class="ghost-button !py-1 !px-1.5 text-xs text-danger ui-control-target" @click="deleteSnapshot(snap.id)"><Trash2 class="w-3 h-3" /></button>
            </div>
          </div>
        </div>
      </div>

      <div v-if="selectedIds.size === 2" class="p-3 border-t border-stroke bg-accent-soft shrink-0">
        <div class="flex items-center gap-2 mb-2">
          <span class="text-xs text-accent">已选 2 个快照，可对比差异</span>
          <div class="flex-1" />
          <button class="liquid-button !py-1 !px-3 text-xs ui-control-target" @click="runDiff">对比差异</button>
          <button class="ghost-button !py-1 !px-2 text-xs ui-control-target" @click="selectedIds.clear()">清空选择</button>
        </div>
        <div v-if="diffResult" class="space-y-2">
          <div class="grid grid-cols-3 gap-2 text-xs">
            <div class="ui-inset-card ui-inset-card--success rounded-lg bg-success-soft border border-success/20 p-2 text-center">
              <p class="text-success font-medium">+ 新增 {{ diffResult.added.length }}</p>
            </div>
            <div class="ui-inset-card ui-inset-card--danger rounded-lg bg-danger-soft border border-danger/20 p-2 text-center">
              <p class="text-danger font-medium">- 删除 {{ diffResult.removed.length }}</p>
            </div>
            <div class="ui-inset-card ui-inset-card--warning rounded-lg bg-warning-soft border border-warning/20 p-2 text-center">
              <p class="text-warning font-medium">~ 修改 {{ diffResult.modified.length }}</p>
            </div>
          </div>
          <div class="max-h-40 overflow-auto rounded-lg bg-surface-inset border border-stroke p-2 text-xs font-mono">
            <div v-for="(r, i) in diffPreview" :key="i" class="py-0.5" :class="r.type === 'added' ? 'text-success' : r.type === 'removed' ? 'text-danger' : 'text-warning'">
              <span class="inline-block w-4">{{ r.type === 'added' ? '+' : r.type === 'removed' ? '-' : '~' }}</span>{{ r.text }}
            </div>
          </div>
        </div>
      </div>

      <div v-else-if="selectedIds.size > 2" class="p-3 border-t border-stroke bg-warning-soft shrink-0 text-xs text-warning">
        请选择 2 个快照进行对比，当前已选 {{ selectedIds.size }} 个
        <button class="ghost-button !py-1 !px-2 text-xs ml-2 ui-control-target" @click="selectedIds.clear()">清空</button>
      </div>
    </div>
  </div>
</template>
