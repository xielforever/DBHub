<script setup lang="ts">
import { computed, onMounted, onScopeDispose, ref } from 'vue'
import LoadingState from '../../components/base/LoadingState.vue'
import PageState from '../../components/base/PageState.vue'
import {
  managedDashboardApi as api,
  type DashboardReview,
  type DashboardTile,
  type DashboardView,
  type ManagedDashboard,
} from '../../api/managed-dashboard'
import { useUserStore } from '../../stores/user'

const props = defineProps<{ dashboard: ManagedDashboard }>()
const emit = defineEmits<{ close: []; published: [id: number]; draft: [id: number]; review: [id: number] }>()
const user = useUserStore()
const dashboard = ref(props.dashboard)
const published = ref<DashboardView>()
const review = ref<DashboardReview>()
const reportNames = ref<Record<number, string>>({})
const loading = ref(false)
const error = ref('')
const publishedError = ref('')
const leftTiles = computed(() => published.value?.tiles ?? [])
const rightTiles = computed(() => review.value?.definition.tiles ?? dashboard.value.draft.tiles)
const rightTitle = computed(() => review.value ? `冻结版本 v${review.value.number}` : `草稿修订 ${dashboard.value.draft_version}`)
const widths = { full: '整行', half: '半行', wide: '三分之二', narrow: '三分之一' }
let read: AbortController | undefined

function reportName(tile: DashboardTile) {
  return reportNames.value[tile.report_id] ?? `报表 #${tile.report_id}`
}
function changeLabel(tile: DashboardTile, index: number) {
  if (!published.value) return ''
  const previousIndex = leftTiles.value.findIndex(previous => previous.key === tile.key)
  const previous = leftTiles.value[previousIndex]
  if (!previous) return '新增'
  const changes = []
  if (previous.report_id !== tile.report_id) changes.push('来源变更')
  if (previousIndex !== index) changes.push('位置变更')
  if (previous.width !== tile.width) changes.push('宽度变更')
  return changes.join(' · ') || '沿用'
}
function hasTile(key: string) {
  return rightTiles.value.some(tile => tile.key === key)
}
function showFrozen(d: ManagedDashboard) {
  return d.review_id > 0 && (d.review_status === 'pending' || d.review_status === 'approved' && !d.review_was_published)
}
async function load() {
  read?.abort()
  const controller = new AbortController()
  read = controller
  const { signal } = controller
  loading.value = true
  error.value = ''
  publishedError.value = ''
  published.value = undefined
  review.value = undefined
  reportNames.value = {}
  try {
    const item = await api.get(props.dashboard.id, signal)
    const [live, frozen, names] = await Promise.allSettled([
      item.active_release_id ? api.publishedGet(item.id, signal) : undefined,
      showFrozen(item) ? api.review(item.id, item.review_id, signal) : undefined,
      api.catalog(signal),
    ])
    if (signal.aborted) return
    if (frozen.status === 'rejected') throw frozen.reason
    dashboard.value = item
    review.value = frozen.value
    const current = live.status === 'fulfilled' ? live.value : undefined
    if (current && current.publication_no === item.publication_no &&
      (!item.active_version || current.version === item.active_version)) {
      published.value = current
    } else if (item.active_release_id) {
      publishedError.value = live.status === 'rejected' ? '当前正式配置无法读取，请核对访问资格或重试。' : '当前发布已变化，请重新读取版本。'
    }
    const catalog = names.status === 'fulfilled' ? names.value.items : []
    reportNames.value = Object.fromEntries([
      ...catalog.map(report => [report.id, report.name] as const),
      ...leftTiles.value.flatMap(tile => tile.report ? [[tile.report.id, tile.report.name] as const] : []),
    ])
  } catch (failure) {
    if (!signal.aborted) error.value = failure instanceof Error ? failure.message : '版本读取失败，请重试。'
  } finally {
    if (!signal.aborted) loading.value = false
  }
}
onMounted(load)
onScopeDispose(() => read?.abort())
</script>

<template>
  <el-dialog :model-value="true" :title="'版本对照 · ' + dashboard.draft.name" width="min(860px, 96vw)"
    @update:model-value="(visible: boolean) => { if (!visible) emit('close') }">
    <p class="text-sm text-muted mb-4">{{ user.workspace?.name || '当前工作空间' }} · 仪表盘 #{{ dashboard.id }}</p>
    <LoadingState v-if="loading" variant="detail" label="正在读取仪表盘版本…" />
    <PageState v-else-if="error" title="无法对照版本" :description="error" tone="danger">
      <template #actions><button type="button" class="ghost-button ui-control-target" @click="load">重试读取版本</button></template>
    </PageState>
    <div v-else class="grid gap-3 sm:grid-cols-2">
      <section aria-label="当前正式版本" class="glass-panel ui-card-padding space-y-3 min-w-0">
        <h3 class="font-semibold">当前发布</h3>
        <p v-if="dashboard.active_release_id">
          <span v-if="dashboard.active_version">v{{ dashboard.active_version }} · </span>发布 #{{ dashboard.publication_no }}
        </p>
        <p v-else class="text-muted">当前无生效发布。</p>
        <p v-if="publishedError" class="text-danger text-sm" role="alert">{{ publishedError }}</p>
        <template v-if="published">
          <p class="text-sm text-muted">{{ leftTiles.length }} 张卡片</p>
          <ol class="divide-y divide-divider">
            <li v-for="(tile, index) in leftTiles" :key="tile.key" class="py-2 text-sm break-words">
              {{ index + 1 }}. {{ reportName(tile) }} · {{ widths[tile.width] }}
              <span v-if="!hasTile(tile.key)" class="text-danger"> · 本次移除</span>
            </li>
          </ol>
          <button type="button" class="ghost-button ui-control-target" @click="emit('published', dashboard.id)">打开正式仪表盘</button>
        </template>
        <button v-else-if="publishedError" type="button" class="ghost-button ui-control-target" @click="load">重试读取版本</button>
      </section>
      <section aria-label="本次开发版本" class="glass-panel ui-card-padding space-y-3 min-w-0">
        <h3 class="font-semibold">本次开发</h3>
        <p>{{ rightTitle }}</p>
        <p class="text-sm text-muted">
          {{ rightTiles.length }} 张卡片<span v-if="review"> · 审批 #{{ review.id }}</span>
        </p>
        <ol class="divide-y divide-divider">
          <li v-for="(tile, index) in rightTiles" :key="tile.key" class="py-2 text-sm break-words">
            {{ index + 1 }}. {{ reportName(tile) }} · {{ widths[tile.width] }}
            <span v-if="changeLabel(tile, index)" class="text-muted"> · {{ changeLabel(tile, index) }}</span>
          </li>
        </ol>
        <button v-if="review" type="button" class="ghost-button ui-control-target" @click="emit('review', review.id)">查看本次送审版本</button>
        <button v-else type="button" class="ghost-button ui-control-target" @click="emit('draft', dashboard.id)">预览已保存草稿</button>
      </section>
    </div>
  </el-dialog>
</template>
