<template>
  <div class="flex flex-col h-full min-h-0">
    <div class="px-3 py-2 border-b border-white/10 flex items-center gap-2 shrink-0 bg-white/[0.02]">
      <div class="w-6 h-6 rounded-lg bg-indigo-500/20 border border-indigo-400/30 flex items-center justify-center">
        <KeyRound class="w-3.5 h-3.5 text-indigo-300" />
      </div>
      <div class="flex-1 min-w-0">
        <p class="text-xs font-medium text-white/80">列级授权与行级策略</p>
        <p class="text-[10px] text-white/40">{{ restrictedCount }} 受限列 · 行策略 {{ rowPolicyEnabled ? '已启用' : '未启用' }} · {{ roleLabel }}</p>
      </div>
      <span class="text-[10px] px-1.5 py-0.5 rounded-full" :class="roleBadge">{{ roleLabel }}</span>
      <button class="ghost-button !py-1 !px-2 text-[11px]" @click="emit('toggleWatermark')"><Eye class="w-3 h-3" /> 水印 {{ watermarkEnabled ? '开' : '关' }}</button>
    </div>

    <div class="flex-1 overflow-auto p-3 space-y-3">
      <!-- 角色切换（演示） -->
      <div class="rounded-xl bg-white/5 border border-white/10 p-2.5 flex items-center gap-2">
        <span class="text-xs text-white/50">演示角色</span>
        <div class="flex gap-1">
          <button v-for="r in roles" :key="r.value" class="px-2.5 py-1 rounded-full text-xs border" :class="role===r.value ? 'bg-indigo-500/20 text-indigo-300 border-indigo-400/30' : 'bg-white/5 text-white/40 border-white/10'" @click="emit('changeRole', r.value)">{{ r.label }}</button>
        </div>
        <div class="flex-1" />
        <span class="text-[11px] text-white/30">切换后实时预览列权限</span>
      </div>

      <!-- 列权限矩阵 -->
      <div class="rounded-xl bg-white/5 border border-white/10 overflow-hidden">
        <div class="px-3 py-2 border-b border-white/10 bg-white/5 flex items-center gap-2 text-[11px] text-white/40">
          <span>列级权限矩阵</span>
          <span class="px-1.5 py-0.5 rounded-full bg-white/10">{{ columns.length }} 列</span>
          <div class="flex-1" />
          <span class="hidden sm:inline">可见 · 掩码 · 隐藏</span>
        </div>
        <div v-if="!columns.length" class="text-center text-xs text-white/30 py-6">暂无列</div>
        <div v-else class="divide-y divide-white/5">
          <div v-for="col in columns" :key="col" class="px-3 py-2 flex items-center gap-2">
            <span class="text-xs font-mono flex-1 truncate" :class="isRestricted(col) ? 'text-amber-200' : 'text-white/70'">{{ col }}</span>
            <span class="text-[10px] px-1.5 py-0.5 rounded" :class="sensitivityBadge(colSensitivity(col))">{{ colSensitivity(col) === 'confidential' ? '机密' : colSensitivity(col)==='sensitive' ? '敏感' : '普通' }}</span>
            <span class="px-1.5 py-0.5 rounded text-[11px] min-w-[56px] text-center" :class="permBadge(permissionFor(col))">{{ permLabel(permissionFor(col)) }}</span>
            <div class="flex gap-1 ml-1">
              <button class="ghost-button !py-0.5 !px-1.5 text-[10px]" :class="permissionFor(col)==='visible' ? 'bg-emerald-500/20 text-emerald-300' : ''" @click="emit('setPerm', col, 'visible')">可见</button>
              <button class="ghost-button !py-0.5 !px-1.5 text-[10px]" :class="permissionFor(col)==='masked' ? 'bg-amber-500/20 text-amber-300' : ''" @click="emit('setPerm', col, 'masked')">掩码</button>
              <button class="ghost-button !py-0.5 !px-1.5 text-[10px]" :class="permissionFor(col)==='hidden' ? 'bg-rose-500/20 text-rose-300' : ''" @click="emit('setPerm', col, 'hidden')">隐藏</button>
            </div>
          </div>
        </div>
      </div>

      <!-- 行级策略 -->
      <div class="rounded-xl bg-black/30 border border-white/10 overflow-hidden">
        <div class="px-3 py-2 border-b border-white/10 bg-white/5 flex items-center gap-2 text-[11px] text-white/40">
          <span>行级策略（RLS）预览</span>
          <label class="ml-auto flex items-center gap-1.5 text-xs cursor-pointer"><input type="checkbox" :checked="rowPolicyEnabled" @change="emit('toggleRowPolicy')" class="accent-indigo-400" /> 启用</label>
        </div>
        <div class="p-3 space-y-2">
          <div class="rounded-lg bg-indigo-500/10 border border-indigo-400/20 p-2.5">
            <p class="text-xs font-mono text-indigo-200">WHERE {{ rowPolicySql }}</p>
            <p class="text-[11px] text-white/30 mt-1">示例：按当前用户部门过滤，仅能查看本组数据；管理员不受限</p>
          </div>
          <div class="flex items-center gap-2 text-xs">
            <span class="text-white/40">原始行数</span><span class="font-mono text-white/70">{{ totalRows }}</span>
            <span class="text-white/20">→</span>
            <span class="text-white/40">策略后</span><span class="font-mono text-emerald-300">{{ filteredRowsCount }}</span>
            <span class="px-1.5 py-0.5 rounded-full bg-emerald-500/15 text-emerald-300 text-[10px]">{{ filteredRowsCount === totalRows ? '未过滤' : `过滤 ${totalRows - filteredRowsCount} 行` }}</span>
          </div>
          <div v-if="rowPolicyEnabled && sampleFiltered.length" class="overflow-auto rounded-lg border border-white/10">
            <table class="w-full text-[11px]">
              <thead class="text-white/40 bg-white/5">
                <tr><th v-for="c in columns.slice(0,4)" :key="c" class="px-2 py-1 text-left font-medium">{{ c }}</th></tr>
              </thead>
              <tbody>
                <tr v-for="(r,i) in sampleFiltered.slice(0,3)" :key="i" class="border-t border-white/5">
                  <td v-for="(c,ci) in columns.slice(0,4)" :key="c" class="px-2 py-1 font-mono truncate max-w-[120px] text-white/60">{{ String(r[ci] ?? '') }}</td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>
      </div>

      <!-- 水印说明 -->
      <div class="rounded-xl bg-violet-500/10 border border-violet-400/20 p-2.5 flex gap-2">
        <ShieldCheck class="w-4 h-4 text-violet-300 shrink-0" />
        <p class="text-[11px] text-violet-200/70 leading-relaxed">水印与列权限均在前端实时生效，导出时将自动嵌入水印与权限过滤；行级策略由后端 WHERE 改写实现，此处为预览。</p>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { Eye, KeyRound, ShieldCheck } from 'lucide-vue-next'

const props = defineProps<{
  columns: string[]
  rows: unknown[][]
  role: string
  watermarkEnabled: boolean
  rowPolicyEnabled: boolean
  rowPolicySql: string
  columnPerms: Record<string, 'visible'|'masked'|'hidden'>
}>()

const emit = defineEmits<{
  (e: 'changeRole', role: string): void
  (e: 'setPerm', col: string, perm: 'visible'|'masked'|'hidden'): void
  (e: 'toggleWatermark'): void
  (e: 'toggleRowPolicy'): void
}>()

const roles = [
  { value: 'admin', label: '管理员' },
  { value: 'developer', label: '开发者' },
  { value: 'readonly', label: '只读' },
]
const roleLabel = computed(() => roles.find(r=>r.value===props.role)?.label || props.role)
const roleBadge = computed(() => props.role==='admin' ? 'bg-violet-500/20 text-violet-300' : props.role==='readonly' ? 'bg-white/10 text-white/40' : 'bg-indigo-500/20 text-indigo-300')

function colSensitivity(col: string) {
  if (/(id_card|ssn|password|bank|credit)/i.test(col)) return 'confidential'
  if (/(phone|mobile|email|name|card)/i.test(col)) return 'sensitive'
  return 'normal'
}
function sensitivityBadge(s: string) {
  return s==='confidential' ? 'bg-rose-500/15 text-rose-300' : s==='sensitive' ? 'bg-amber-500/15 text-amber-300' : 'bg-white/10 text-white/30'
}
function isRestricted(col: string) {
  return permissionFor(col) !== 'visible'
}
function permissionFor(col: string): 'visible'|'masked'|'hidden' {
  const explicit = props.columnPerms[col]
  if (explicit) return explicit
  // 默认按角色启发：readonly 对敏感列掩码，机密列隐藏
  const sens = colSensitivity(col)
  if (props.role==='readonly') {
    if (sens==='confidential') return 'hidden'
    if (sens==='sensitive') return 'masked'
  }
  if (props.role==='developer' && sens==='confidential') return 'masked'
  return 'visible'
}
function permBadge(p: string) {
  return p==='visible' ? 'bg-emerald-500/15 text-emerald-300' : p==='masked' ? 'bg-amber-500/15 text-amber-300' : 'bg-rose-500/15 text-rose-300'
}
function permLabel(p: string) {
  return p==='visible' ? '可见' : p==='masked' ? '掩码' : '隐藏'
}
const restrictedCount = computed(() => props.columns.filter(c=>permissionFor(c)!=='visible').length)

// row policy simulation: if enabled, filter rows where first column even? just demo reduce 30%
const totalRows = computed(() => props.rows.length)
const filteredRowsCount = computed(() => props.rowPolicyEnabled ? Math.ceil(totalRows.value * 0.7) : totalRows.value)
const sampleFiltered = computed(() => props.rowPolicyEnabled ? props.rows.filter((_, i)=> i % 3 !== 0) : props.rows)
</script>
