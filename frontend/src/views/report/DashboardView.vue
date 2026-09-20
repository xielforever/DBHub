<template>
  <div class="space-y-5">
    <header class="flex flex-col sm:flex-row sm:items-center justify-between gap-4">
      <div>
        <h1 class="text-xl font-semibold">仪表盘</h1>
        <p class="text-sm text-white/45 mt-1">12 栅格布局，组合多个报表，支持共享</p>
      </div>
      <div class="flex items-center gap-2">
        <el-select v-model="scope" class="w-32" popper-class="glass-popper" @change="reload(1)">
          <el-option label="全部" value="all" />
          <el-option label="我的" value="mine" />
          <el-option label="共享" value="shared" />
          <el-option label="收藏" value="starred" />
        </el-select>
        <button v-if="canWrite" class="liquid-button text-sm flex items-center gap-2" @click="openCreate">
          <Plus class="w-4 h-4" />新建仪表盘
        </button>
      </div>
    </header>

    <div v-loading="loading" class="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4">
      <article v-for="dash in items" :key="dash.id" class="glass-card p-4 flex flex-col gap-3 hover:-translate-y-0.5 transition-transform">
        <div class="flex items-start justify-between">
          <div class="min-w-0">
            <h3 class="font-medium truncate flex items-center gap-1.5">{{ dash.name }} <Star v-if="dash.starred" class="w-3 h-3 text-amber-300 fill-amber-300" /></h3>
            <p class="text-[11px] text-white/40 truncate mt-0.5">{{ dash.description || '无描述' }}</p>
          </div>
          <span class="px-2 py-0.5 rounded-full text-[10px]" :class="dash.visibility==='shared' ? 'bg-emerald-400/15 text-emerald-300' : 'bg-white/8 text-white/50'">{{ dash.visibility==='shared' ? '共享' : '私有' }}</span>
        </div>
        <div class="text-[11px] text-white/30 flex items-center gap-2">
          <span>{{ dash.owner_name }}</span>
          <span class="flex-1" />
          <span>{{ formatTime(dash.updated_at) }}</span>
        </div>
        <div class="flex items-center gap-1.5 pt-2 border-t border-white/10">
          <button class="ghost-button flex-1 text-xs py-1.5" @click="openView(dash)">查看</button>
          <button class="w-8 h-8 rounded-lg flex items-center justify-center text-white/40 hover:bg-amber-500/15 hover:text-amber-300" @click="toggleStar(dash)">
            <Star class="w-4 h-4" :class="dash.starred ? 'fill-amber-300 text-amber-300' : ''" />
          </button>
          <button v-if="canWrite" class="w-8 h-8 rounded-lg flex items-center justify-center text-white/40 hover:bg-white/10" @click="openEdit(dash)"><Pencil class="w-4 h-4" /></button>
          <button v-if="canWrite" class="w-8 h-8 rounded-lg flex items-center justify-center text-white/40 hover:bg-rose-500/20 hover:text-rose-300" @click="remove(dash)"><Trash2 class="w-4 h-4" /></button>
        </div>
      </article>
      <div v-if="!loading && !items.length" class="col-span-full glass-card p-12 text-center">
        <LayoutDashboard class="w-10 h-10 text-white/20 mx-auto" />
        <p class="text-white/50 text-sm mt-3">暂无仪表盘，创建后可组合报表</p>
      </div>
    </div>

    <div class="flex items-center justify-between text-xs text-white/50">
      <span>共 {{ total }} 个仪表盘</span>
      <div class="flex items-center gap-1">
        <button class="ghost-button px-2.5 py-1" :disabled="page<=1" @click="reload(page-1)">上一页</button>
        <span class="px-2">{{ page }} / {{ totalPages }}</span>
        <button class="ghost-button px-2.5 py-1" :disabled="page>=totalPages" @click="reload(page+1)">下一页</button>
      </div>
    </div>

    <!-- 创建/编辑 -->
    <el-dialog v-model="editOpen" :title="editingId ? '编辑仪表盘' : '新建仪表盘'" width="560px" class="glass-dialog" :close-on-click-modal="false">
      <div class="space-y-4">
        <div>
          <p class="text-xs text-white/50 mb-1">名称</p>
          <el-input v-model="editForm.name" placeholder="仪表盘名称" maxlength="128" />
        </div>
        <div>
          <p class="text-xs text-white/50 mb-1">描述</p>
          <el-input v-model="editForm.description" type="textarea" :rows="2" maxlength="512" />
        </div>
        <div>
          <p class="text-xs text-white/50 mb-1">可见性</p>
          <el-select v-model="editForm.visibility" class="w-full" popper-class="glass-popper">
            <el-option label="私有" value="private" />
            <el-option label="共享" value="shared" />
          </el-select>
        </div>
        <div>
          <p class="text-xs text-white/50 mb-1">布局（JSON，M4 简化版：report_id 列表，12栅格）</p>
          <el-input v-model="editForm.layoutText" type="textarea" :rows="6" placeholder='[{"report_id":1,"x":0,"y":0,"w":6,"h":4}]' />
        </div>
      </div>
      <template #footer>
        <div class="flex justify-end gap-2">
          <button class="ghost-button" @click="editOpen=false">取消</button>
          <button class="liquid-button" :disabled="saving" @click="saveDashboard">{{ saving ? '保存中…' : '保存' }}</button>
        </div>
      </template>
    </el-dialog>

    <!-- 查看仪表盘 -->
    <el-dialog v-model="viewOpen" :title="viewDash?.name || '仪表盘'" width="90vw" class="glass-dialog" top="5vh">
      <template v-if="viewDash">
        <div class="flex flex-wrap gap-2 text-xs mb-4">
          <span class="px-2 py-0.5 rounded-full bg-white/8 text-white/60">{{ viewDash.owner_name }}</span>
          <span class="px-2 py-0.5 rounded-full" :class="viewDash.visibility==='shared' ? 'bg-emerald-400/15 text-emerald-300' : 'bg-white/8 text-white/50'">{{ viewDash.visibility }}</span>
          <span class="flex-1" />
          <button class="ghost-button text-xs py-1 px-2 flex items-center gap-1" @click="openShare(viewDash)"><Share2 class="w-3.5 h-3.5" />分享</button>
          <button class="ghost-button text-xs py-1 px-2" @click="refreshAll">刷新全部</button>
        </div>

        <div class="grid grid-cols-12 gap-4">
          <!-- Tailwind safelist for dynamic col-span -->
          <div class="hidden col-span-1 col-span-2 col-span-3 col-span-4 col-span-5 col-span-6 col-span-7 col-span-8 col-span-9 col-span-10 col-span-11 col-span-12 md:col-span-1 md:col-span-2 md:col-span-3 md:col-span-4 md:col-span-5 md:col-span-6 md:col-span-7 md:col-span-8 md:col-span-9 md:col-span-10 md:col-span-11 md:col-span-12"></div>
          <div v-for="cell in parsedLayout" :key="cell.report_id" :class="`col-span-12 md:col-span-${Math.min(12, cell.w || 6)}`" class="glass-card p-3">
            <div class="flex items-center justify-between mb-2">
              <p class="text-sm font-medium truncate">{{ reportMap[cell.report_id]?.name || `报表 #${cell.report_id}` }}</p>
              <span class="text-[10px] text-white/30">{{ reportMap[cell.report_id]?.chart_type }}</span>
            </div>
            <div v-loading="reportLoading[cell.report_id]">
              <ChartCard
                v-if="reportData[cell.report_id]"
                :chart-type="reportMap[cell.report_id]?.chart_type || 'table'"
                :columns="reportData[cell.report_id]!.columns"
                :rows="reportData[cell.report_id]!.rows"
                :config="reportMap[cell.report_id]?.chart_config"
                height="260px"
              />
              <p v-else class="text-xs text-white/30 py-8 text-center">暂无数据</p>
            </div>
          </div>
          <div v-if="!parsedLayout.length" class="col-span-12 text-center text-white/30 py-12">
            空仪表盘，请编辑添加报表（layout 中填 report_id）
          </div>
        </div>
      </template>
    </el-dialog>

    <!-- 分享 Dialog -->
    <el-dialog v-model="shareOpen" title="分享仪表盘/报表" width="520px" class="glass-dialog">
      <div class="space-y-4">
        <div class="flex gap-2">
          <el-select v-model="shareForm.expireDays" class="w-40" popper-class="glass-popper">
            <el-option label="1天" :value="1" />
            <el-option label="7天" :value="7" />
            <el-option label="30天" :value="30" />
            <el-option label="永久" :value="undefined" />
          </el-select>
          <button class="liquid-button text-xs flex items-center gap-1" :disabled="sharing" @click="createShare"><Share2 class="w-3.5 h-3.5" />生成链接</button>
        </div>
        <div v-if="lastShareToken" class="space-y-2">
          <p class="text-xs text-white/50">分享链接（仅明文显示一次，请复制保存）：</p>
          <div class="flex gap-2">
            <el-input :model-value="shareLink" readonly />
            <button class="ghost-button text-xs" @click="copyLink">复制</button>
          </div>
        </div>
        <div v-loading="shareLoading" class="space-y-2">
          <p class="text-xs text-white/40">已生成链接</p>
          <div v-for="s in shareList" :key="s.id" class="flex items-center gap-2 text-xs bg-white/5 rounded-lg p-2">
            <span class="font-mono truncate flex-1">{{ s.id }} · {{ s.subject_type }} #{{ s.subject_id }} · 访问 {{ s.access_count }} 次</span>
            <span v-if="s.revoked" class="text-rose-300">已吊销</span>
            <span v-else-if="s.expire_at && new Date(s.expire_at) < new Date()" class="text-amber-300">已过期</span>
            <button v-if="!s.revoked" class="ghost-button text-[11px] py-0.5 px-2" @click="revokeShare(s.id)">吊销</button>
          </div>
        </div>
      </div>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { LayoutDashboard, Pencil, Plus, Share2, Star, Trash2 } from 'lucide-vue-next'
import ChartCard from '../../components/business/chart/ChartCard.vue'
import { reportDashboardApi as dashboardApi, type DashboardItem } from '../../api/dashboard'
import { reportApi, type ReportItem } from '../../api/report'
import { shareApi, type ShareItem } from '../../api/share'
import { workbenchApi } from '../../api/workbench'
import { useUserStore } from '../../stores/user'

const userStore = useUserStore()
const canWrite = computed(() => userStore.role !== 'readonly')

const scope = ref<'all' | 'mine' | 'shared' | 'starred'>('all')
const items = ref<DashboardItem[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = 12
const loading = ref(false)
const totalPages = computed(() => Math.max(1, Math.ceil(total.value / pageSize)))

async function reload(p?: number) {
  if (p) page.value = p
  loading.value = true
  try {
    const res = await dashboardApi.list({ scope: scope.value, page: page.value, page_size: pageSize })
    items.value = res.items
    total.value = res.total
  } finally { loading.value = false }
}
function formatTime(s: string) {
  if (!s) return '—'
  try { const d = new Date(s); return `${d.getMonth()+1}/${d.getDate()} ${String(d.getHours()).padStart(2,'0')}:${String(d.getMinutes()).padStart(2,'0')}` } catch { return s.slice(0,16) }
}

// 创建/编辑
const editOpen = ref(false)
const editingId = ref<number | null>(null)
const saving = ref(false)
const editForm = reactive({ name: '', description: '', visibility: 'private' as 'private' | 'shared', layoutText: '[]' })

function openCreate() {
  editingId.value = null
  editForm.name = ''
  editForm.description = ''
  editForm.visibility = 'private'
  editForm.layoutText = '[]'
  editOpen.value = true
}
function openEdit(d: DashboardItem) {
  editingId.value = d.id
  editForm.name = d.name
  editForm.description = d.description
  editForm.visibility = d.visibility
  editForm.layoutText = JSON.stringify(d.layout || [], null, 2)
  editOpen.value = true
}
async function saveDashboard() {
  if (!editForm.name.trim()) { ElMessage.warning('名称必填'); return }
  let layout: unknown = []
  try { layout = JSON.parse(editForm.layoutText || '[]') } catch { ElMessage.warning('布局 JSON 非法'); return }
  saving.value = true
  try {
    if (editingId.value) {
      await dashboardApi.update(editingId.value, { name: editForm.name.trim(), description: editForm.description.trim(), visibility: editForm.visibility, layout })
      ElMessage.success('已更新')
    } else {
      await dashboardApi.create({ name: editForm.name.trim(), description: editForm.description.trim(), visibility: editForm.visibility, layout })
      ElMessage.success('已创建')
    }
    editOpen.value = false
    reload()
  } finally { saving.value = false }
}
async function remove(d: DashboardItem) {
  try { await ElMessageBox.confirm(`确认删除仪表盘「${d.name}」？`, '删除确认', { type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消' }) } catch { return }
  await dashboardApi.remove(d.id)
  ElMessage.success('已删除')
  reload()
}
async function toggleStar(d: DashboardItem) {
  const res = await dashboardApi.star(d.id, !d.starred)
  d.starred = res.starred
  ElMessage.success(res.starred ? '已收藏' : '已取消收藏')
}

// 查看
const viewOpen = ref(false)
const viewDash = ref<DashboardItem | null>(null)
const reportMap = ref<Record<number, ReportItem>>({})
const reportData = ref<Record<number, { columns: string[]; rows: unknown[][] }>>({})
const reportLoading = ref<Record<number, boolean>>({})
const parsedLayout = computed(() => {
  if (!viewDash.value) return []
  const lay = viewDash.value.layout as { report_id: number; x?: number; y?: number; w?: number; h?: number }[]
  if (!Array.isArray(lay)) return []
  return lay
})
async function openView(d: DashboardItem) {
  viewDash.value = d
  viewOpen.value = true
  reportMap.value = {}
  reportData.value = {}
  reportLoading.value = {}
  // 加载报表元数据
  for (const cell of parsedLayout.value) {
    try {
      const rep = await reportApi.get(cell.report_id)
      reportMap.value[cell.report_id] = rep
    } catch { /* ignore */ }
  }
  await refreshAll()
}
async function refreshAll() {
  for (const cell of parsedLayout.value) {
    const rep = reportMap.value[cell.report_id]
    if (!rep) continue
    reportLoading.value[cell.report_id] = true
    try {
      const res = await workbenchApi.execute(rep.connection_id, rep.sql_text, rep.database_name)
      if (res.kind === 'query') {
        reportData.value[cell.report_id] = { columns: res.columns ?? [], rows: res.rows ?? [] }
      }
    } catch { /* ignore */ } finally { reportLoading.value[cell.report_id] = false }
  }
}

// 分享
const shareOpen = ref(false)
const shareForm = reactive<{ expireDays?: number }>({ expireDays: 7 })
const shareList = ref<ShareItem[]>([])
const shareLoading = ref(false)
const sharing = ref(false)
const lastShareToken = ref('')
const shareSubject = ref<{ type: 'report' | 'dashboard'; id: number } | null>(null)
const shareLink = computed(() => lastShareToken.value ? `${window.location.origin}/s/${lastShareToken.value}` : '')

function openShare(d: DashboardItem | ReportItem) {
  // 兼容报表与仪表盘
  const isReport = (d as ReportItem).sql_text !== undefined
  shareSubject.value = { type: isReport ? 'report' : 'dashboard', id: d.id }
  lastShareToken.value = ''
  shareOpen.value = true
  loadShares()
}
async function loadShares() {
  if (!shareSubject.value) return
  shareLoading.value = true
  try {
    const res = await shareApi.list(shareSubject.value.type, shareSubject.value.id)
    shareList.value = res.items
  } finally { shareLoading.value = false }
}
async function createShare() {
  if (!shareSubject.value) return
  sharing.value = true
  try {
    const res = await shareApi.create({ subject_type: shareSubject.value.type, subject_id: shareSubject.value.id, expire_days: shareForm.expireDays })
    lastShareToken.value = res.token
    ElMessage.success('分享链接已生成')
    loadShares()
  } finally { sharing.value = false }
}
function copyLink() {
  navigator.clipboard.writeText(shareLink.value)
  ElMessage.success('已复制')
}
async function revokeShare(id: number) {
  await shareApi.revoke(id)
  ElMessage.success('已吊销')
  loadShares()
}

onMounted(() => reload(1))
</script>
