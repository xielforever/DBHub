<template>
  <div class="flex flex-col h-full min-h-0">
    <div class="px-3 py-2 border-b border-white/10 flex items-center gap-2 shrink-0 bg-white/[0.02]">
      <div class="w-6 h-6 rounded-lg bg-amber-500/20 border border-amber-400/30 flex items-center justify-center">
        <Gauge class="w-3.5 h-3.5 text-amber-300" />
      </div>
      <div class="flex-1 min-w-0">
        <p class="text-xs font-medium text-white/80">资源限制</p>
        <p class="text-[10px] text-white/40">超时 · 行数 · 保护</p>
      </div>
      <button class="ghost-button !py-1 !px-2.5 text-xs" @click="reset">重置</button>
      <button class="liquid-button !py-1 !px-3 text-xs" @click="save">保存</button>
    </div>

    <div class="flex-1 overflow-auto p-4 space-y-4">
      <div class="rounded-xl bg-amber-500/10 border border-amber-400/20 p-2.5 flex gap-2">
        <ShieldAlert class="w-3.5 h-3.5 text-amber-300 shrink-0 mt-0.5" />
        <p class="text-[11px] text-amber-200/70 leading-relaxed">限制仅作用于当前浏览器会话，超时将自动取消查询，避免长查询拖垮数据库</p>
      </div>

      <div class="space-y-3">
        <div class="flex items-center justify-between">
          <label class="text-xs text-white/60">执行超时</label>
          <span class="text-xs font-mono text-white/80">{{ form.timeoutMs / 1000 }}s</span>
        </div>
        <div class="flex items-center gap-3">
          <input type="range" :value="form.timeoutMs" min="5000" max="120000" step="5000" class="flex-1 accent-amber-400" @input="form.timeoutMs = Number(($event.target as HTMLInputElement).value)" />
        </div>
        <div class="flex gap-1.5">
          <button v-for="v in [10000,30000,60000,120000]" :key="v" class="ghost-button !py-1 !px-2 text-[11px]" :class="form.timeoutMs===v ? 'bg-white/10 text-white' : ''" @click="form.timeoutMs=v">{{ v/1000 }}s</button>
        </div>
        <p class="text-[11px] text-white/30">超时后前端自动 abort，后端若支持将被取消</p>
      </div>

      <div class="space-y-3 pt-3 border-t border-white/10">
        <div class="flex items-center justify-between">
          <label class="text-xs text-white/60">最大返回行数</label>
          <span class="text-xs font-mono text-white/80">{{ form.maxRows }}</span>
        </div>
        <input type="range" :value="form.maxRows" min="100" max="10000" step="100" class="w-full accent-indigo-400" @input="form.maxRows = Number(($event.target as HTMLInputElement).value)" />
        <div class="flex gap-1.5">
          <button v-for="v in [500,1000,5000,10000]" :key="v" class="ghost-button !py-1 !px-2 text-[11px]" :class="form.maxRows===v ? 'bg-white/10 text-white' : ''" @click="form.maxRows=v">{{ v }}</button>
        </div>
        <p class="text-[11px] text-white/30">超过行数将被截断并提示，实际以服务端为准</p>
      </div>

      <div class="space-y-2 pt-3 border-t border-white/10">
        <label class="flex items-center justify-between cursor-pointer">
          <span class="text-xs text-white/60">禁止无 WHERE 的 UPDATE/DELETE</span>
          <input type="checkbox" v-model="form.blockUnsafeWrite" class="accent-rose-400" />
        </label>
        <label class="flex items-center justify-between cursor-pointer">
          <span class="text-xs text-white/60">自动为 SELECT 添加 LIMIT</span>
          <input type="checkbox" v-model="form.autoLimit" class="accent-emerald-400" />
        </label>
        <label class="flex items-center justify-between cursor-pointer">
          <span class="text-xs text-white/60">生产环境写操作二次确认</span>
          <input type="checkbox" v-model="form.confirmProdWrite" class="accent-indigo-400" />
        </label>
      </div>

      <div class="rounded-xl bg-white/5 border border-white/10 p-3 space-y-2">
        <p class="text-[11px] text-white/40">当前生效</p>
        <div class="grid grid-cols-2 gap-2 text-[11px]">
          <div class="flex justify-between"><span class="text-white/40">超时</span><span class="font-mono text-white/70">{{ form.timeoutMs }}ms</span></div>
          <div class="flex justify-between"><span class="text-white/40">上限</span><span class="font-mono text-white/70">{{ form.maxRows }} 行</span></div>
          <div class="flex justify-between"><span class="text-white/40">防误写</span><span :class="form.blockUnsafeWrite ? 'text-emerald-300' : 'text-white/30'">{{ form.blockUnsafeWrite ? '开启' : '关闭' }}</span></div>
          <div class="flex justify-between"><span class="text-white/40">自动 LIMIT</span><span :class="form.autoLimit ? 'text-emerald-300' : 'text-white/30'">{{ form.autoLimit ? '开启' : '关闭' }}</span></div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { reactive, watch } from 'vue'
import { Gauge, ShieldAlert } from 'lucide-vue-next'
import { ElMessage } from 'element-plus'

export interface Limits {
  timeoutMs: number
  maxRows: number
  blockUnsafeWrite: boolean
  autoLimit: boolean
  confirmProdWrite: boolean
}

const STORAGE = 'dbhub_resource_limits'

const props = defineProps<{
  modelValue: Limits
}>()
const emit = defineEmits<{
  (e: 'update:modelValue', v: Limits): void
  (e: 'save', v: Limits): void
}>()

const form = reactive<Limits>({ ...props.modelValue })

watch(() => props.modelValue, v => Object.assign(form, v), { deep: true })

function save() {
  emit('update:modelValue', { ...form })
  emit('save', { ...form })
  try { localStorage.setItem(STORAGE, JSON.stringify(form)) } catch {}
  ElMessage.success('资源限制已保存')
}
function reset() {
  Object.assign(form, { timeoutMs: 30000, maxRows: 1000, blockUnsafeWrite: true, autoLimit: true, confirmProdWrite: true })
}
</script>
