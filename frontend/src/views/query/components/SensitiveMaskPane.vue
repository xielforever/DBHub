<template>
  <div class="flex flex-col h-full min-h-0">
    <div class="px-3 py-2 border-b border-white/10 flex items-center gap-2 shrink-0 bg-white/[0.02]">
      <div class="w-6 h-6 rounded-lg bg-violet-500/20 border border-violet-400/30 flex items-center justify-center">
        <ShieldCheck class="w-3.5 h-3.5 text-violet-300" />
      </div>
      <div class="flex-1 min-w-0">
        <p class="text-xs font-medium text-white/80">脱敏与导出管控</p>
        <p class="text-[10px] text-white/40">{{ sensitiveCols.length }} 敏感列 · {{ maskEnabled ? '已开启掩码' : '未脱敏' }} · {{ exportRequests.length }} 审批</p>
      </div>
      <label class="flex items-center gap-1.5 text-xs cursor-pointer">
        <span class="text-white/50">脱敏</span>
        <input type="checkbox" :checked="maskEnabled" @change="emit('toggleMask')" class="accent-violet-400" />
      </label>
      <button class="ghost-button !py-1 !px-2 text-[11px]" @click="emit('requestExport')">申请导出</button>
    </div>

    <div class="flex-1 overflow-auto p-3 space-y-3">
      <div v-if="!columns.length" class="text-center text-xs text-white/35 py-10">
        暂无查询结果，执行 SQL 后可检测敏感列
      </div>
      <template v-else>
        <!-- 敏感列检测 -->
        <div class="rounded-xl bg-white/5 border border-white/10 overflow-hidden">
          <div class="px-3 py-2 border-b border-white/10 bg-white/5 flex items-center gap-2 text-[11px] text-white/40">
            <span>敏感列检测</span>
            <span class="px-1.5 py-0.5 rounded-full" :class="sensitiveCols.length ? 'bg-amber-500/15 text-amber-300' : 'bg-emerald-500/15 text-emerald-300'">{{ sensitiveCols.length ? sensitiveCols.length + ' 列' : '无' }}</span>
            <div class="flex-1" />
            <span v-if="sensitiveCols.length" class="text-violet-300/70 flex items-center gap-1"><EyeOff class="w-3 h-3" /> 自动掩码</span>
          </div>
          <div class="divide-y divide-white/5">
            <div v-for="c in columns" :key="c" class="px-3 py-2 flex items-center gap-2">
              <span class="text-xs font-mono flex-1 truncate" :class="isSensitive(c) ? 'text-amber-200' : 'text-white/60'">{{ c }}</span>
              <span v-if="isSensitive(c)" class="px-1.5 py-0.5 rounded text-[10px] flex items-center gap-1" :class="sensitivityBadge(colSensitivity(c))">
                <AlertTriangle class="w-3 h-3" />{{ sensitivityLabel(colSensitivity(c)) }}
              </span>
              <span v-else class="text-[10px] text-white/30">普通</span>
              <span class="text-[11px] font-mono text-white/30">{{ maskExample(c) }}</span>
            </div>
          </div>
        </div>

        <!-- 规则预览 -->
        <div class="rounded-xl bg-black/30 border border-white/10 overflow-hidden">
          <div class="px-3 py-2 border-b border-white/10 bg-white/5 text-[11px] text-white/40">掩码规则</div>
          <div class="p-3 grid grid-cols-2 gap-2 text-[11px]">
            <div v-for="r in rules" :key="r.type" class="rounded-lg bg-white/5 border border-white/10 p-2">
              <p class="text-white/60">{{ r.type }}</p>
              <p class="font-mono text-violet-200/80">{{ r.pattern }}</p>
              <p class="text-white/30">例：{{ r.example }}</p>
            </div>
          </div>
        </div>

        <!-- 预览 -->
        <div v-if="rows.length" class="rounded-xl bg-white/5 border border-white/10 overflow-hidden">
          <div class="px-3 py-2 border-b border-white/10 bg-white/5 flex items-center gap-2 text-[11px] text-white/40">
            <span>脱敏预览（前 5 行）</span>
            <div class="flex-1" />
            <span class="px-1.5 py-0.5 rounded bg-white/10">{{ maskEnabled ? '掩码视图' : '原文视图' }}</span>
          </div>
          <div class="overflow-auto">
            <table class="w-full text-xs">
              <thead class="text-white/40">
                <tr class="border-b border-white/10">
                  <th v-for="c in columns" :key="c" class="px-2 py-1.5 text-left font-medium whitespace-nowrap">{{ c }}</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="(row, i) in rows.slice(0,5)" :key="i" class="border-b border-white/5 last:border-0">
                  <td v-for="(c, ci) in columns" :key="c" class="px-2 py-1.5 font-mono truncate max-w-[140px]" :class="isSensitive(c) && maskEnabled ? 'text-violet-300' : 'text-white/70'">
                    {{ displayCell(c, row[ci]) }}
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
          <p class="text-[10px] text-white/25 px-3 py-1.5 border-t border-white/5">切换上方脱敏开关可对比原文/掩码</p>
        </div>

        <!-- 导出审批 -->
        <div class="rounded-xl bg-white/5 border border-white/10 overflow-hidden">
          <div class="px-3 py-2 border-b border-white/10 bg-white/5 flex items-center gap-2 text-[11px] text-white/40">
            <span>导出审批</span>
            <span class="px-1.5 py-0.5 rounded bg-amber-500/15 text-amber-300">{{ pendingCount }} 待审批</span>
            <div class="flex-1" />
            <button class="ghost-button !py-0.5 !px-2 text-[10px]" @click="clearRequests">刷新申请</button>
          </div>
          <div v-if="!exportRequests.length" class="text-center text-[11px] text-white/30 py-6">暂无导出申请</div>
          <div v-else class="divide-y divide-white/5">
            <div v-for="req in exportRequests" :key="req.id" class="px-3 py-2.5 flex items-center gap-2">
              <span class="px-1.5 py-0.5 rounded text-[10px]" :class="req.status==='pending' ? 'bg-amber-500/15 text-amber-300' : req.status==='approved' ? 'bg-emerald-500/15 text-emerald-300' : 'bg-rose-500/15 text-rose-300'">{{ statusLabel(req.status) }}</span>
              <span class="text-xs text-white/60 flex-1 truncate">{{ req.reason || '未填写原因' }} · {{ req.format }} · {{ req.rows }} 行</span>
              <span class="text-[10px] text-white/30">{{ formatTime(req.created_at) }}</span>
              <button v-if="req.status==='approved'" class="ghost-button text-xs" @click="emit('download', req.id)">下载</button>
              <button v-if="req.status==='pending' && isAdmin" class="ghost-button !py-0.5 !px-2 text-[10px] text-emerald-300" @click="updateStatus(req.id,'approved')">批准</button>
              <button v-if="req.status==='pending' && isAdmin" class="ghost-button !py-0.5 !px-2 text-[10px] text-rose-300" @click="updateStatus(req.id,'rejected')">拒绝</button>
            </div>
          </div>
        </div>
      </template>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useUserStore } from '../../../stores/user'
const isAdmin=computed(()=>useUserStore().role==='admin')
import { AlertTriangle, EyeOff, ShieldCheck } from 'lucide-vue-next'

const props = defineProps<{
  columns: string[]
  rows: unknown[][]
  maskEnabled: boolean
  exportRequests: { id: number; format: string; rows: number; reason: string; status: 'pending'|'approved'|'rejected'; created_at: string }[]
}>()

const emit = defineEmits<{
  (e: 'toggleMask'): void
  (e: 'requestExport'): void
  (e: 'updateStatus', id: number, status: string): void
  (e: 'clearRequests'): void
  (e: 'download', id:number): void
}>()

const sensitivePattern = /(phone|mobile|tel|email|id_card|idcard|ssn|password|passwd|credit|bank|card_no|real_name|name)/i

function isSensitive(col: string) {
  return sensitivePattern.test(col)
}
function colSensitivity(col: string): 'sensitive'|'confidential' {
  if (/(id_card|ssn|password|bank|credit)/i.test(col)) return 'confidential'
  return 'sensitive'
}
const sensitiveCols = computed(() => props.columns.filter(isSensitive))
const pendingCount = computed(() => props.exportRequests.filter(r => r.status==='pending').length)

function sensitivityBadge(s: string) {
  return s==='confidential' ? 'bg-rose-500/15 text-rose-300' : 'bg-amber-500/15 text-amber-300'
}
function sensitivityLabel(s: string) {
  return s==='confidential' ? '机密' : '敏感'
}
function maskExample(col: string) {
  if (/email/i.test(col)) return 'a***@b.com'
  if (/phone|mobile|tel/i.test(col)) return '138****5678'
  if (/id_card/i.test(col)) return '1101**********12'
  if (/bank|credit|card/i.test(col)) return '**** **** **** 1234'
  if (/name/i.test(col)) return '张*'
  return '***'
}
const rules = [
  { type: '手机号', pattern: '保留前3后4', example: '138****5678' },
  { type: '邮箱', pattern: '保留首字符与域名', example: 'a***@example.com' },
  { type: '身份证', pattern: '保留前4后2', example: '1101**********12' },
  { type: '银行卡', pattern: '保留后4', example: '**** **** **** 1234' },
]

function maskValue(col: string, val: unknown) {
  const s = String(val ?? '')
  if (!s) return s
  if (/email/i.test(col)) {
    const parts = s.split('@')
    if (parts.length===2) return parts[0]!.charAt(0) + '***@' + parts[1]
    return '***'
  }
  if (/phone|mobile|tel/i.test(col)) {
    if (s.length>=7) return s.slice(0,3) + '****' + s.slice(-4)
    return s.slice(0,2) + '***'
  }
  if (/id_card|idcard|ssn/i.test(col)) {
    if (s.length>6) return s.slice(0,4) + '*'.repeat(s.length-6) + s.slice(-2)
    return '***'
  }
  if (/bank|credit|card/i.test(col)) {
    return '**** **** **** ' + s.slice(-4)
  }
  if (/name/i.test(col) && s.length>=2) return s.charAt(0) + '*'.repeat(s.length-1)
  return s.slice(0,1) + '***'
}

function displayCell(col: string, val: unknown) {
  if (val===null || val===undefined) return 'NULL'
  const s = String(val)
  if (!props.maskEnabled || !isSensitive(col)) return s
  return maskValue(col, s)
}

function statusLabel(s: string) {
  return s==='pending' ? '待审批' : s==='approved' ? '已批准' : '已拒绝'
}
function formatTime(s: string) {
  return new Date(s).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' })
}
function updateStatus(id: number, status: string) {
  emit('updateStatus', id, status)
}
function clearRequests() {
  emit('clearRequests')
}
</script>
