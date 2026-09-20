<template>
  <div class="space-y-5">
    <header class="flex flex-col sm:flex-row sm:items-center justify-between gap-4">
      <div>
        <h1 class="text-xl font-semibold">报表中心</h1>
        <p class="text-sm text-white/45 mt-1">工作台另存的图表沉淀，支持私有/共享与收藏</p>
      </div>
      <div class="flex items-center gap-2">
        <el-select v-model="scope" class="w-32" popper-class="glass-popper" @change="reload(1)">
          <el-option label="全部" value="all" />
          <el-option label="我的" value="mine" />
          <el-option label="共享" value="shared" />
          <el-option label="收藏" value="starred" />
        </el-select>
        <button class="ghost-button text-xs py-1.5 px-3 flex items-center gap-1" @click="reload(1)">
          <RefreshCw class="w-3.5 h-3.5" />刷新
        </button>
      </div>
    </header>

    <div class="flex flex-wrap items-center gap-2.5">
      <div class="relative w-full sm:w-72">
        <Search class="w-4 h-4 text-white/40 absolute left-3 top-1/2 -translate-y-1/2" />
        <input v-model.trim="keyword" class="glass-input pl-9 py-1.5 text-sm w-full" placeholder="搜索报表名称/描述/SQL" @input="reload(1)" />
      </div>
    </div>

    <div v-loading="loading" class="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4">
      <article v-for="rep in items" :key="rep.id" class="glass-card p-4 flex flex-col gap-3 hover:-translate-y-0.5 transition-transform">
        <div class="flex items-start justify-between gap-2">
          <div class="min-w-0">
            <h3 class="font-medium truncate flex items-center gap-1.5">
              {{ rep.name }}
              <Star v-if="rep.starred" class="w-3 h-3 text-amber-300 fill-amber-300" />
            </h3>
            <p class="text-[11px] text-white/40 truncate mt-0.5">{{ rep.description || '无描述' }}</p>
          </div>
          <span class="px-2 py-0.5 rounded-full text-[10px] shrink-0" :class="rep.visibility==='shared' ? 'bg-emerald-400/15 text-emerald-300' : 'bg-white/8 text-white/50'">
            {{ rep.visibility==='shared' ? '共享' : '私有' }}
          </span>
        </div>

        <div class="rounded-xl bg-black/30 border border-white/5 p-2 h-[180px] overflow-hidden">
          <ChartCard
            :chart-type="rep.chart_type"
            :columns="previewColumns(rep)"
            :rows="previewRows(rep)"
            :config="rep.chart_config"
            height="160px"
          />
        </div>

        <div class="flex items-center gap-2 text-[11px] text-white/40">
          <span class="px-1.5 py-0.5 rounded bg-white/5">{{ rep.chart_type }}</span>
          <span>{{ rep.owner_name }}</span>
          <span class="flex-1" />
          <span>{{ formatTime(rep.updated_at) }}</span>
        </div>

        <div class="flex items-center gap-1.5 pt-2 border-t border-white/10">
          <button class="ghost-button flex-1 text-xs py-1.5" @click="openReport(rep)">查看</button>
          <button class="w-8 h-8 rounded-lg flex items-center justify-center text-white/40 hover:bg-amber-500/15 hover:text-amber-300" title="收藏" @click="toggleStar(rep)">
            <Star class="w-4 h-4" :class="rep.starred ? 'fill-amber-300 text-amber-300' : ''" />
          </button>
          <button v-if="canWrite" class="w-8 h-8 rounded-lg flex items-center justify-center text-white/40 hover:bg-white/10 hover:text-white" title="编辑" @click="openEdit(rep)">
            <Pencil class="w-4 h-4" />
          </button>
          <button v-if="canWrite" class="w-8 h-8 rounded-lg flex items-center justify-center text-white/40 hover:bg-rose-500/20 hover:text-rose-300" title="删除" @click="remove(rep)">
            <Trash2 class="w-4 h-4" />
          </button>
        </div>
      </article>
      <div v-if="!loading && !items.length" class="col-span-full glass-card p-12 flex flex-col items-center gap-3 text-center">
        <BarChart3 class="w-10 h-10 text-white/20" />
        <p class="text-white/50 text-sm">暂无报表，在 SQL 工作台「图表」Tab 另存为报表</p>
      </div>
    </div>

    <div class="flex items-center justify-between text-xs text-white/50">
      <span>共 {{ total }} 个报表</span>
      <div class="flex items-center gap-1">
        <button class="ghost-button px-2.5 py-1" :disabled="page<=1" @click="reload(page-1)">上一页</button>
        <span class="px-2">{{ page }} / {{ totalPages }}</span>
        <button class="ghost-button px-2.5 py-1" :disabled="page>=totalPages" @click="reload(page+1)">下一页</button>
      </div>
    </div>

    <!-- 详情/编辑 Dialog -->
    <el-dialog v-model="detailOpen" :title="current?.name || '报表详情'" width="760px" class="glass-dialog">
      <template v-if="current">
        <div class="space-y-4">
          <div class="flex flex-wrap gap-2 text-xs">
            <span class="px-2 py-0.5 rounded-full bg-white/8 text-white/60">数据源 #{{ current.connection_id }}</span>
            <span class="px-2 py-0.5 rounded-full bg-white/8 text-white/60">{{ current.database_name || '默认库' }}</span>
            <span class="px-2 py-0.5 rounded-full" :class="current.visibility==='shared' ? 'bg-emerald-400/15 text-emerald-300' : 'bg-white/8 text-white/50'">{{ current.visibility==='shared' ? '共享' : '私有' }}</span>
          </div>
          <pre class="text-xs font-mono bg-black/30 rounded-xl p-3 overflow-x-auto whitespace-pre-wrap">{{ current.sql_text }}</pre>

          <div v-loading="detailLoading" class="glass-card p-3">
            <ChartCard
              v-if="detailColumns.length"
              :chart-type="current.chart_type"
              :columns="detailColumns"
              :rows="detailRows"
              :config="current.chart_config"
              height="360px"
            />
            <p v-else class="text-sm text-white/40 py-8 text-center">加载中或执行失败</p>
          </div>

          <div v-if="editing" class="space-y-3 border-t border-white/10 pt-4">
            <p class="text-xs text-white/40 uppercase tracking-widest">编辑</p>
            <el-input v-model="editForm.name" placeholder="报表名称" />
            <el-input v-model="editForm.description" type="textarea" :rows="2" placeholder="描述" />
            <el-select v-model="editForm.visibility" class="w-full" popper-class="glass-popper">
              <el-option label="私有" value="private" />
              <el-option label="共享" value="shared" />
            </el-select>
            <div class="flex justify-end gap-2">
              <button class="ghost-button" @click="editing=false">取消</button>
              <button class="liquid-button" :disabled="saving" @click="saveEdit">保存</button>
            </div>
          </div>
        </div>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { BarChart3, Pencil, RefreshCw, Search, Star, Trash2 } from 'lucide-vue-next'
import ChartCard from '../../components/business/chart/ChartCard.vue'
import { reportApi, type ReportItem } from '../../api/report'
import { workbenchApi } from '../../api/workbench'
import { useUserStore } from '../../stores/user'

const userStore = useUserStore()
const canWrite = computed(() => userStore.role !== 'readonly')

const scope = ref<'all' | 'mine' | 'shared' | 'starred'>('all')
const keyword = ref('')
const items = ref<ReportItem[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = 12
const loading = ref(false)
const totalPages = computed(() => Math.max(1, Math.ceil(total.value / pageSize)))

async function reload(p?: number) {
  if (p) page.value = p
  loading.value = true
  try {
    const res = await reportApi.list({
      q: keyword.value || undefined,
      scope: scope.value,
      page: page.value,
      page_size: pageSize,
    })
    items.value = res.items
    total.value = res.total
  } catch {
    /* 拦截器提示 */
  } finally {
    loading.value = false
  }
}

function formatTime(s: string): string {
  if (!s) return '—'
  try {
    const d = new Date(s)
    return `${d.getMonth()+1}/${d.getDate()} ${String(d.getHours()).padStart(2,'0')}:${String(d.getMinutes()).padStart(2,'0')}`
  } catch { return s.slice(0,16) }
}

function previewColumns(rep: ReportItem): string[] {
  // 报表卡片缩略图用配置维度+指标作列头，实际渲染由 ChartCard 按真实数据
  // 这里若无真实数据则用占位
  return [rep.chart_config?.dimension || '维度', ...(rep.chart_config?.metrics || ['指标'])]
}
function previewRows(_rep: ReportItem): unknown[][] {
  // 缩略图不执行真实 SQL，避免列表页 N+1 查询；用空数据让 ChartCard 显示空态或占位
  // 真实数据在详情页加载
  return []
}

// 详情
const detailOpen = ref(false)
const current = ref<ReportItem | null>(null)
const detailColumns = ref<string[]>([])
const detailRows = ref<unknown[][]>([])
const detailLoading = ref(false)
const editing = ref(false)
const saving = ref(false)
const editForm = reactive({ name: '', description: '', visibility: 'private' as 'private' | 'shared' })

async function openReport(rep: ReportItem) {
  current.value = rep
  detailOpen.value = true
  editing.value = false
  detailColumns.value = []
  detailRows.value = []
  detailLoading.value = true
  try {
    // 执行固化 SQL 获取最新结果（复用工作台执行约束）
    const res = await workbenchApi.execute(rep.connection_id, rep.sql_text, rep.database_name)
    if (res.kind === 'query') {
      detailColumns.value = res.columns ?? []
      detailRows.value = res.rows ?? []
    }
  } catch {
    /* 拦截器提示 */
  } finally {
    detailLoading.value = false
  }
}
function openEdit(rep: ReportItem) {
  current.value = rep
  editForm.name = rep.name
  editForm.description = rep.description
  editForm.visibility = rep.visibility
  editing.value = true
  detailOpen.value = true
}
async function saveEdit() {
  if (!current.value) return
  if (!editForm.name.trim()) {
    ElMessage.warning('名称必填')
    return
  }
  saving.value = true
  try {
    const updated = await reportApi.update(current.value.id, {
      name: editForm.name.trim(),
      description: editForm.description.trim(),
      visibility: editForm.visibility,
    })
    current.value = updated
    ElMessage.success('已更新')
    editing.value = false
    reload()
  } catch {
    /* 拦截器提示 */
  } finally {
    saving.value = false
  }
}
async function toggleStar(rep: ReportItem) {
  try {
    const res = await reportApi.star(rep.id, !rep.starred)
    rep.starred = res.starred
    ElMessage.success(res.starred ? '已收藏' : '已取消收藏')
  } catch {
    /* 拦截器提示 */
  }
}
async function remove(rep: ReportItem) {
  try {
    await ElMessageBox.confirm(`确认删除报表「${rep.name}」？`, '删除确认', { type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消' })
  } catch { return }
  await reportApi.remove(rep.id)
  ElMessage.success('已删除')
  reload()
}

onMounted(() => reload(1))
</script>
