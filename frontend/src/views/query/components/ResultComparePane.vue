<template>
  <div class="flex flex-col h-full min-h-0">
    <div class="px-3 py-2 border-b border-white/10 flex items-center gap-2 shrink-0 bg-white/[0.02]">
      <div class="w-6 h-6 rounded-lg bg-sky-500/20 border border-sky-400/30 flex items-center justify-center">
        <Split class="w-3.5 h-3.5 text-sky-300" />
      </div>
      <div class="flex-1 min-w-0">
        <p class="text-xs font-medium text-white/80">结果对比</p>
        <p class="text-[10px] text-white/40">跨标签 · 跨快照对比</p>
      </div>
      <button class="ghost-button !py-1 !px-2.5 text-xs flex items-center gap-1" @click="addCurrent">
        <Plus class="w-3 h-3" /> 添加当前
      </button>
    </div>

    <div v-if="compareList.length < 2" class="flex-1 flex flex-col items-center justify-center gap-3 p-6 text-center">
      <div class="w-12 h-12 rounded-2xl bg-white/5 border border-white/10 flex items-center justify-center">
        <Split class="w-6 h-6 text-white/20" />
      </div>
      <p class="text-xs text-white/50">需要 2 个结果集进行对比</p>
      <p class="text-[11px] text-white/30 leading-5">点击「添加当前」保存当前结果<br/>或从快照中选择，支持跨标签对比</p>
      <div class="mt-2 flex gap-2">
        <button class="liquid-button !py-1.5 !px-3 text-xs" :disabled="!currentColumns.length" @click="addCurrent">添加当前结果</button>
        <button class="ghost-button !py-1.5 !px-3 text-xs" @click="loadFromSnapshots">从快照导入</button>
      </div>
    </div>

    <div v-else class="flex-1 flex flex-col min-h-0 overflow-hidden">
      <div class="p-3 border-b border-white/10 bg-white/[0.02] flex items-center gap-2 shrink-0">
        <div v-for="(item, idx) in compareList" :key="item.id" class="flex-1 rounded-xl border p-2.5" :class="idx === 0 ? 'bg-indigo-500/10 border-indigo-400/20' : 'bg-emerald-500/10 border-emerald-400/20'">
          <div class="flex items-center justify-between">
            <p class="text-xs font-medium truncate" :class="idx === 0 ? 'text-indigo-300' : 'text-emerald-300'">{{ item.name }}</p>
            <button class="ghost-button !py-0.5 !px-1 text-[10px] text-rose-300/60" @click="removeItem(item.id)"><Trash2 class="w-3 h-3" /></button>
          </div>
          <p class="text-[11px] text-white/40 mt-1">{{ item.columns.length }} 列 · {{ item.rows.length }} 行</p>
          <p class="text-[10px] font-mono text-white/30 truncate mt-0.5">{{ item.sql.slice(0, 60) }}</p>
        </div>
      </div>

      <div class="flex-1 overflow-auto p-3 space-y-3">
        <div class="grid grid-cols-3 gap-2 text-[11px]">
          <div class="rounded-xl bg-emerald-500/10 border border-emerald-400/20 p-2.5 text-center">
            <p class="text-emerald-300 font-medium">+ 新增 {{ diff.added.length }}</p>
            <p class="text-[10px] text-white/30 mt-1">仅在 B 中</p>
          </div>
          <div class="rounded-xl bg-rose-500/10 border border-rose-400/20 p-2.5 text-center">
            <p class="text-rose-300 font-medium">- 删除 {{ diff.removed.length }}</p>
            <p class="text-[10px] text-white/30 mt-1">仅在 A 中</p>
          </div>
          <div class="rounded-xl bg-amber-500/10 border border-amber-400/20 p-2.5 text-center">
            <p class="text-amber-300 font-medium">~ 修改 {{ diff.modified.length }}</p>
            <p class="text-[10px] text-white/30 mt-1">同 Key 不同值</p>
          </div>
        </div>

        <div class="rounded-xl bg-white/5 border border-white/10 overflow-hidden">
          <div class="px-3 py-2 border-b border-white/10 bg-white/5 flex items-center gap-2 text-[11px] text-white/40">
            <span>差异详情 · 最多显示 50 行</span>
            <div class="flex-1" />
            <el-select v-model="diffFilter" size="small" class="w-24" popper-class="glass-popper">
              <el-option label="全部" value="all" />
              <el-option label="新增" value="added" />
              <el-option label="删除" value="removed" />
              <el-option label="修改" value="modified" />
            </el-select>
          </div>
          <div class="max-h-[40vh] overflow-auto">
            <table class="w-full text-xs">
              <thead class="sticky top-0 bg-[#141428]/90 backdrop-blur text-white/45">
                <tr>
                  <th class="text-left px-3 py-2 w-12">类型</th>
                  <th v-for="c in displayColumns" :key="c" class="text-left px-3 py-2 font-medium">{{ c }}</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="(row, i) in filteredDiffRows" :key="i" class="border-b border-white/5" :class="row._type === 'added' ? 'bg-emerald-500/5' : row._type === 'removed' ? 'bg-rose-500/5' : 'bg-amber-500/5'">
                  <td class="px-3 py-2">
                    <span class="px-1.5 py-0.5 rounded text-[10px]" :class="row._type === 'added' ? 'bg-emerald-500/20 text-emerald-300' : row._type === 'removed' ? 'bg-rose-500/20 text-rose-300' : 'bg-amber-500/20 text-amber-300'">{{ row._type === 'added' ? '+' : row._type === 'removed' ? '-' : '~' }}</span>
                  </td>
                  <td v-for="c in displayColumns" :key="c" class="px-3 py-2 font-mono truncate max-w-[160px]" :title="String(row[c] ?? '')">{{ row[c] }}</td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>

        <div class="flex gap-2">
          <button class="ghost-button !py-1.5 !px-3 text-xs flex-1" @click="clearAll">清空对比</button>
          <button class="ghost-button !py-1.5 !px-3 text-xs flex items-center gap-1" @click="exportDiff"><Download class="w-3 h-3" /> 导出差异</button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { Download, Plus, Split, Trash2 } from 'lucide-vue-next'
import { ElMessage } from 'element-plus'

interface CompareItem {
  id: number
  name: string
  sql: string
  columns: string[]
  rows: unknown[][]
  created_at: string
}

const props = defineProps<{
  currentColumns: string[]
  currentRows: unknown[][]
  currentSql: string
}>()

const STORAGE = 'dbhub_compare_list'

const compareList = ref<CompareItem[]>([])
const diffFilter = ref<'all' | 'added' | 'removed' | 'modified'>('all')

try {
  const raw = localStorage.getItem(STORAGE)
  if (raw) compareList.value = JSON.parse(raw)
} catch {}

function persist() {
  try { localStorage.setItem(STORAGE, JSON.stringify(compareList.value.slice(0, 10))) } catch {}
}

function addCurrent() {
  if (!props.currentColumns.length) {
    ElMessage.warning('当前无结果可添加')
    return
  }
  if (compareList.value.length >= 2) {
    ElMessage.info('已满 2 个，先移除一个')
    return
  }
  const item: CompareItem = {
    id: Date.now(),
    name: `结果 ${compareList.value.length + 1} · ${new Date().toLocaleTimeString()}`,
    sql: props.currentSql.slice(0, 200),
    columns: [...props.currentColumns],
    rows: props.currentRows.map(r => [...(r as any[])]),
    created_at: new Date().toISOString(),
  }
  compareList.value.push(item)
  persist()
  ElMessage.success('已添加到对比')
}

function loadFromSnapshots() {
  try {
    const raw = localStorage.getItem('dbhub_result_snapshots')
    if (!raw) { ElMessage.warning('暂无快照'); return }
    const snaps = JSON.parse(raw)
    if (!snaps.length) { ElMessage.warning('暂无快照'); return }
    // 取最近2个
    const toAdd = snaps.slice(0, 2 - compareList.value.length)
    toAdd.forEach((s: any) => {
      compareList.value.push({
        id: s.id,
        name: s.name,
        sql: s.sql,
        columns: s.columns,
        rows: s.rows,
        created_at: s.created_at,
      })
    })
    persist()
    ElMessage.success(`已导入 ${toAdd.length} 个快照`)
  } catch { ElMessage.error('导入失败') }
}

function removeItem(id: number) {
  compareList.value = compareList.value.filter(x => x.id !== id)
  persist()
}

function clearAll() {
  compareList.value = []
  persist()
}

const diff = computed(() => {
  if (compareList.value.length < 2) return { added: [] as any[], removed: [] as any[], modified: [] as any[] }
  const a = compareList.value[0]!
  const b = compareList.value[1]!

  const mapA = new Map<string, unknown[]>()
  const mapB = new Map<string, unknown[]>()

  a.rows.forEach(r => mapA.set(JSON.stringify(r), r as any))
  b.rows.forEach(r => mapB.set(JSON.stringify(r), r as any))

  const added: any[] = []
  const removed: any[] = []
  const modified: any[] = []

  for (const [k, v] of mapB.entries()) if (!mapA.has(k)) added.push(v)
  for (const [k, v] of mapA.entries()) if (!mapB.has(k)) removed.push(v)

  // modified by first column
  if (a.columns.length) {
    const firstA = new Map<string, unknown[]>()
    a.rows.forEach(r => firstA.set(String((r as any)[0]), r as any))
    b.rows.forEach(r => {
      const key = String((r as any)[0])
      const old = firstA.get(key)
      if (old && JSON.stringify(old) !== JSON.stringify(r)) modified.push({ old, new: r })
    })
  }

  return { added, removed, modified }
})

const displayColumns = computed(() => {
  if (!compareList.value.length) return []
  return compareList.value[0]!.columns
})

const filteredDiffRows = computed(() => {
  const out: any[] = []
  if (diffFilter.value === 'all' || diffFilter.value === 'added') {
    diff.value.added.forEach((r: any) => {
      const obj: any = { _type: 'added' }
      displayColumns.value.forEach((c, i) => obj[c] = r[i])
      out.push(obj)
    })
  }
  if (diffFilter.value === 'all' || diffFilter.value === 'removed') {
    diff.value.removed.forEach((r: any) => {
      const obj: any = { _type: 'removed' }
      displayColumns.value.forEach((c, i) => obj[c] = r[i])
      out.push(obj)
    })
  }
  if (diffFilter.value === 'all' || diffFilter.value === 'modified') {
    diff.value.modified.forEach((m: any) => {
      const obj: any = { _type: 'modified' }
      displayColumns.value.forEach((c, i) => obj[c] = `${m.old[i]} => ${m.new[i]}`)
      out.push(obj)
    })
  }
  return out.slice(0, 50)
})

function exportDiff() {
  const blob = new Blob([JSON.stringify({ added: diff.value.added, removed: diff.value.removed, modified: diff.value.modified }, null, 2)], { type: 'application/json' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = `diff_${Date.now()}.json`
  a.click()
  URL.revokeObjectURL(url)
}
</script>
