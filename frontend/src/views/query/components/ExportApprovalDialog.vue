<template>
  <div class="space-y-4">
    <div class="rounded-xl bg-amber-500/10 border border-amber-400/20 p-2.5 flex gap-2">
      <ShieldAlert class="w-4 h-4 text-amber-300 shrink-0 mt-0.5" />
      <p class="text-xs text-amber-200/70 leading-relaxed">检测到结果包含 <b class="text-amber-300">{{ sensitiveCount }} 列敏感字段</b>，可申请服务端导出快照；当前查询权限仍由后端策略控制。</p>
    </div>
    <div class="grid grid-cols-2 gap-3">
      <div>
        <p class="text-xs text-white/50 mb-1">导出格式</p>
        <el-select v-model="form.format" class="w-full" popper-class="glass-popper">
          <el-option label="CSV" value="csv" />
          <el-option label="JSON" value="json" />
        </el-select>
      </div>
      <div>
        <p class="text-xs text-white/50 mb-1">导出范围</p>
        <el-select v-model="form.scope" class="w-full" popper-class="glass-popper">
          <el-option label="全部行" value="all" />

        </el-select>
      </div>
    </div>
    <div>
      <p class="text-xs text-white/50 mb-1">敏感列处理</p>
      <el-radio-group v-model="form.maskMode" size="small">
        <el-radio-button value="masked">脱敏后导出</el-radio-button>
        <el-radio-button value="plain">申请原文导出</el-radio-button>
      </el-radio-group>
      <p class="text-[11px] text-white/30 mt-1">原文导出会重新运行当前 SQL，最多 1000 行，管理员批准后下载。</p>
    </div>
    <div>
      <p class="text-xs text-white/50 mb-1">申请原因</p>
      <el-input v-model="form.reason" type="textarea" :rows="3" placeholder="请说明导出用途，例如：用于对账、数据分析..." maxlength="200" show-word-limit />
    </div>
    <div class="rounded-xl bg-white/5 border border-white/10 p-2.5 flex justify-between text-xs">
      <span class="text-white/40">涉及行数</span><span class="font-mono text-white/70">{{ rows }} 行 · {{ columns.length }} 列</span>
    </div>
    <div class="flex justify-end gap-2">
      <button class="ghost-button" @click="emit('close')">取消</button>
      <button class="ghost-button" :disabled="!form.reason.trim() && form.maskMode==='plain'" @click="onMaskedExport">直接导出脱敏</button>
      <button class="liquid-button" :disabled="!form.reason.trim() && form.maskMode==='plain'" @click="onSubmit">{{ form.maskMode==='masked' ? '导出脱敏文件' : '提交审批' }}</button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { reactive } from 'vue'
import { ShieldAlert } from 'lucide-vue-next'

const props = defineProps<{
  columns: string[]
  rows: number
  sensitiveCount: number
}>()

const emit = defineEmits<{
  (e: 'close'): void
  (e: 'submit', payload: { format: string; scope: string; maskMode: string; reason: string }): void
  (e: 'maskedExport', format: string): void
}>()

const form = reactive({
  format: 'csv',
  scope: 'all',
  maskMode: 'masked' as 'masked'|'plain',
  reason: '',
})

function onSubmit() {
  emit('submit', { ...form })
}
function onMaskedExport() {
  if (form.maskMode==='masked') emit('maskedExport', form.format)
  else emit('submit', { ...form })
}
</script>
