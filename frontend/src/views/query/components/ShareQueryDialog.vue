<script setup lang="ts">
import LoadingState from '../../../components/base/LoadingState.vue'
import { useReadActivity } from '../../../composables/useReadActivity'
const { busy: readPending, run: trackRead } = useReadActivity()
import { reactive, ref, onMounted } from 'vue'
import { advancedApi, type QueryShare } from '../../../api/advanced'
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

const sharing = ref(false), shareError = ref('')
const shareLink = ref('')
const shareInfo = ref<QueryShare | null>(null)
const recentShares = ref<QueryShare[]>([])
async function loadShares() {
return trackRead(async () => {shareError.value='';try { recentShares.value = (await advancedApi.shares()).items } catch { recentShares.value=[];shareError.value='分享记录读取失败，请重新打开后重试' }
})
}
onMounted(loadShares)
async function createShare() {
 if (!form.name.trim() || sharing.value) return
 sharing.value=true
 try {
  const res = await advancedApi.createShare({name: form.name.trim(), connection_id: props.connectionId, database: props.database, sql: props.sql, params: form.includeParams ? props.params : {}, expire_days: form.expireDays, include_result: form.includeResult})
  shareLink.value = `${window.location.origin}/q/${encodeURIComponent(res.token)}`; shareInfo.value = res.share
  await loadShares(); ElMessage.success('分享链接已生成，请仅发给可信接收者'); emit('shared', shareLink.value)
 } catch(e: unknown) { ElMessage.error(e instanceof Error ? e.message : '创建分享失败') } finally { sharing.value=false }
}

function copyLink() {
  navigator.clipboard.writeText(shareLink.value).then(() => ElMessage.success('已复制'))
}


async function revokeShare(id: number) { try { await advancedApi.revokeShare(id); if(shareInfo.value?.id === id) shareLink.value = ''; await loadShares(); ElMessage.success('已撤销') } catch {} }

function formatTime(s: string) {
  return new Date(s).toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' })
}
</script>

<template>
  <div class="flex flex-col gap-4">
    <div class="rounded-xl bg-accent-soft border border-accent/20 p-3">
      <p class="text-xs font-medium text-accent flex items-center gap-1.5"><Share2 class="w-3.5 h-3.5" /> 分享查询</p>
      <p class="text-xs text-muted mt-1">生成可分享链接，包含 SQL、参数、数据库上下文，支持设置有效期</p>
    </div>

    <div class="space-y-3">
      <div>
        <p class="text-xs text-muted mb-1">分享名称</p>
        <el-input v-model="form.name" placeholder="例如：订单统计-近7天" maxlength="64" />
      </div>
      <div class="flex gap-3">
        <div class="flex-1">
          <p class="text-xs text-muted mb-1">有效期</p>
          <el-select v-model="form.expireDays" class="w-full" popper-class="glass-popper">
            <el-option :label="'1 天'" :value="1" />
            <el-option :label="'7 天'" :value="7" />
            <el-option :label="'30 天'" :value="30" />

          </el-select>
        </div>
        <div class="flex-1">
          <p class="text-xs text-muted mb-1">包含内容</p>
          <div class="flex flex-col gap-1.5 mt-1">
            <label class="flex items-center gap-2 text-xs text-muted"><input type="checkbox" v-model="form.includeParams" /> 参数值</label>
            <label class="flex items-center gap-2 text-xs text-muted"><input type="checkbox" v-model="form.includeResult" /> 重新执行只读查询，分享前100行快照</label>
          </div>
        </div>
      </div>
      <div>
        <p class="text-xs text-muted mb-1">SQL 预览</p>
        <pre class="text-xs font-mono bg-surface-inset rounded-xl p-3 max-h-24 overflow-auto whitespace-pre-wrap break-all">{{ sql.slice(0, 400) }}</pre>
      </div>
      <div v-if="shareLink" class="ui-inset-card ui-inset-card--success rounded-xl bg-success-soft border border-success/20 p-3 space-y-2">
        <p class="text-xs text-success font-medium">分享链接已生成</p>
        <div class="flex gap-2">
          <input :value="shareLink" readonly class="glass-input !py-1.5 text-xs flex-1 font-mono" />
          <button class="ghost-button !py-1.5 !px-3 text-xs flex items-center gap-1 ui-control-target" @click="copyLink"><Copy class="w-3 h-3" /> 复制</button>
        </div>
        <p class="text-xs text-subtle">有效期：{{ form.expireDays === 0 ? '永久' : form.expireDays + ' 天' }} · 访问 {{ shareInfo?.access_count ?? 0 }} 次</p>
      </div>
    </div>

    <div class="flex justify-end gap-2">
      <button class="ghost-button ui-control-target" @click="emit('close')">取消</button>
      <button class="liquid-button ui-control-target" :disabled="sharing || !form.name.trim()" @click="createShare">{{sharing?'生成中…':'生成链接'}}</button>
    </div>

    <p v-if="shareError" role="alert" class="text-xs text-danger">{{shareError}}</p><LoadingState v-if="readPending" variant="detail" label="正在读取分享记录…" /><div v-else-if="recentShares.length" class="border-t border-stroke pt-3 space-y-2">
      <p class="text-xs text-subtle">最近分享 · {{ recentShares.length }}</p>
      <div v-for="s in recentShares" :key="s.id" class="ui-inset-card flex items-center gap-2 px-2.5 py-2 rounded-lg bg-surface-muted border border-stroke">
        <div class="flex-1 min-w-0">
          <p class="text-xs text-muted truncate">{{ s.name }}</p>
          <p class="text-xs text-subtle">{{ formatTime(s.created_at) }} · {{ s.expire_at ? '至 ' + formatTime(s.expire_at) : '永久' }}</p>
        </div>
        <span class="text-xs text-subtle">{{ s.revoked ? "已撤销" : "有效" }}</span>
        <button class="ghost-button !py-1 !px-1.5 text-xs text-danger ui-control-target" @click="revokeShare(s.id)"><Trash2 class="w-3 h-3" /></button>
      </div>
    </div>
  </div>
</template>
