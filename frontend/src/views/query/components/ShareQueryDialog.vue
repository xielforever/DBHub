<template>
  <div class="flex flex-col gap-4">
    <div class="rounded-xl bg-indigo-500/10 border border-indigo-400/20 p-3">
      <p class="text-xs font-medium text-indigo-300 flex items-center gap-1.5"><Share2 class="w-3.5 h-3.5" /> 分享查询</p>
      <p class="text-[11px] text-white/50 mt-1">生成可分享链接，包含 SQL、参数、数据库上下文，支持设置有效期</p>
    </div>

    <div class="space-y-3">
      <div>
        <p class="text-xs text-white/50 mb-1">分享名称</p>
        <el-input v-model="form.name" placeholder="例如：订单统计-近7天" maxlength="64" />
      </div>
      <div class="flex gap-3">
        <div class="flex-1">
          <p class="text-xs text-white/50 mb-1">有效期</p>
          <el-select v-model="form.expireDays" class="w-full" popper-class="glass-popper">
            <el-option :label="'1 天'" :value="1" />
            <el-option :label="'7 天'" :value="7" />
            <el-option :label="'30 天'" :value="30" />
            <el-option :label="'永久'" :value="0" />
          </el-select>
        </div>
        <div class="flex-1">
          <p class="text-xs text-white/50 mb-1">包含内容</p>
          <div class="flex flex-col gap-1.5 mt-1">
            <label class="flex items-center gap-2 text-xs text-white/60"><input type="checkbox" v-model="form.includeParams" /> 参数值</label>
            <label class="flex items-center gap-2 text-xs text-white/60"><input type="checkbox" v-model="form.includeResult" /> 结果快照（前100行）</label>
          </div>
        </div>
      </div>
      <div>
        <p class="text-xs text-white/50 mb-1">SQL 预览</p>
        <pre class="text-[11px] font-mono bg-black/30 rounded-xl p-3 max-h-24 overflow-auto whitespace-pre-wrap break-all">{{ sql.slice(0, 400) }}</pre>
      </div>
      <div v-if="shareLink" class="rounded-xl bg-emerald-500/10 border border-emerald-400/20 p-3 space-y-2">
        <p class="text-[11px] text-emerald-300 font-medium">分享链接已生成</p>
        <div class="flex gap-2">
          <input :value="shareLink" readonly class="glass-input !py-1.5 text-xs flex-1 font-mono" />
          <button class="ghost-button !py-1.5 !px-3 text-xs flex items-center gap-1" @click="copyLink"><Copy class="w-3 h-3" /> 复制</button>
        </div>
        <p class="text-[10px] text-white/30">有效期：{{ form.expireDays === 0 ? '永久' : form.expireDays + ' 天' }} · 访问 {{ shareInfo?.access_count ?? 0 }} 次</p>
      </div>
    </div>

    <div class="flex justify-end gap-2">
      <button class="ghost-button" @click="emit('close')">取消</button>
      <button class="liquid-button" :disabled="!form.name.trim()" @click="createShare">生成链接</button>
    </div>

    <div v-if="recentShares.length" class="border-t border-white/10 pt-3 space-y-2">
      <p class="text-[11px] text-white/40">最近分享 · {{ recentShares.length }}</p>
      <div v-for="s in recentShares" :key="s.id" class="flex items-center gap-2 px-2.5 py-2 rounded-lg bg-white/5 border border-white/10">
        <div class="flex-1 min-w-0">
          <p class="text-xs text-white/70 truncate">{{ s.name }}</p>
          <p class="text-[10px] text-white/30">{{ formatTime(s.created_at) }} · {{ s.expire_at ? '至 ' + formatTime(s.expire_at) : '永久' }}</p>
        </div>
        <button class="ghost-button !py-1 !px-2 text-[10px]" @click="copyText(s.link)">复制</button>
        <button class="ghost-button !py-1 !px-1.5 text-[10px] text-rose-300/60" @click="revokeShare(s.id)"><Trash2 class="w-3 h-3" /></button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { Copy, Share2, Trash2 } from 'lucide-vue-next'

const props = defineProps<{
  sql: string
  params: Record<string, string>
  database: string
  connectionId: number
  columns: string[]
  rows: unknown[][]
}>()

const emit = defineEmits<{
  (e: 'close'): void
  (e: 'shared', link: string): void
}>()

const form = reactive({
  name: '',
  expireDays: 7,
  includeParams: true,
  includeResult: false,
})

const shareLink = ref('')
const shareInfo = ref<any>(null)
const recentShares = ref<any[]>([])

try {
  const raw = localStorage.getItem('dbhub_query_shares')
  if (raw) recentShares.value = JSON.parse(raw)
} catch {}

function persist() {
  try { localStorage.setItem('dbhub_query_shares', JSON.stringify(recentShares.value.slice(0, 20))) } catch {}
}

async function createShare() {
  if (!form.name.trim()) return
  // mock 生成链接
  const token = Math.random().toString(36).slice(2, 10) + Math.random().toString(36).slice(2, 6)
  const link = `${window.location.origin}/public/s/${token}?sql=${encodeURIComponent(props.sql.slice(0, 100))}`
  shareLink.value = link
  shareInfo.value = { access_count: 0 }

  const item = {
    id: Date.now(),
    name: form.name.trim(),
    link,
    token,
    sql: props.sql.slice(0, 200),
    params: form.includeParams ? { ...props.params } : {},
    result: form.includeResult ? { columns: props.columns, rows: props.rows.slice(0, 100) } : null,
    expire_at: form.expireDays ? new Date(Date.now() + form.expireDays * 86400000).toISOString() : null,
    created_at: new Date().toISOString(),
  }
  recentShares.value.unshift(item)
  persist()

  // 调用后端 mock
  try {
    const { shareApi } = await import('../../../api/share')
    await shareApi.create({ subject_type: 'query', subject_id: props.connectionId, expire_days: form.expireDays || undefined } as any)
  } catch {}

  ElMessage.success('分享链接已生成')
  emit('shared', link)
}

function copyLink() {
  navigator.clipboard.writeText(shareLink.value).then(() => ElMessage.success('已复制'))
}

function copyText(t: string) {
  navigator.clipboard.writeText(t).then(() => ElMessage.success('已复制'))
}

function revokeShare(id: number) {
  recentShares.value = recentShares.value.filter(x => x.id !== id)
  persist()
  ElMessage.success('已撤销')
}

function formatTime(s: string) {
  return new Date(s).toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' })
}
</script>
