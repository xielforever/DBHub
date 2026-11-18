<script setup lang="ts">
import { computed, nextTick, onMounted, onScopeDispose, ref } from 'vue'
import { ElMessageBox } from 'element-plus'
import LoadingState from '../../components/base/LoadingState.vue'
import request, { ApiRequestError } from '../../utils/request'
import { useUserStore } from '../../stores/user'
import {
  managedDashboardApi as api,
  type DashboardDefinition,
  type ManagedDashboard,
  type DashboardReport,
  type DashboardTile,
} from '../../api/managed-dashboard'
import { newRequestId } from '../../utils/request-id'
import DashboardAccessDialog from './DashboardAccessDialog.vue'
import { reportCommandKeys } from '../report/reportCommands'
import ReportParameterEditor from '../report/ReportParameterEditor.vue'
const props = defineProps<{ dashboard?: ManagedDashboard }>(),
  emit = defineEmits<{ close: []; saved: [] }>(),
  user = useUserStore(),
  keys = reportCommandKeys()
const form = ref<DashboardDefinition>(
  props.dashboard
    ? JSON.parse(JSON.stringify(props.dashboard.draft))
    : {
        name: '',
        description: '',
        tiles: [],
        user_ids: [],
        group_ids: [],
        quality: {
          allow_unknown_freshness: false,
          reason: '',
          max_watermark_skew_seconds: 300,
        },
      },
)
const accessTile = ref<string>()
const widthNames = { full: '整行', half: '半行', wide: '三分之二', narrow: '三分之一' }
function accessSubmitted() {
  accessTile.value = undefined
  error.value = '申请已提交，请在仪表盘的访问申请队列查看进度'
}
const globalParameters = computed({ get: () => form.value.parameters || [], set: parameters => { form.value.parameters = parameters } })
const reportParameters = (id: number) => reports.value.find(r => r.id === id)?.parameters || []
function bindParameter(tile: DashboardTile, name: string, source: string) { const bindings = { ...tile.parameter_bindings }; if (source) bindings[name] = source; else delete bindings[name]; tile.parameter_bindings = bindings }
const original = ref(JSON.stringify(form.value)),
  dirty = computed(() => JSON.stringify(form.value) !== original.value)
const version = ref(props.dashboard?.draft_version || 0),
  reports = ref<DashboardReport[]>([]),
  users = ref<{ id: number; username: string }[]>([]),
  groups = ref<{ id: number; name: string }[]>([]),
  selected = ref<number>(),
  busy = ref(false),
  error = ref('')
const catalogLoading = ref(true), audienceLoading = ref(true)
const catalogError = ref(''), audienceError = ref('')
const catalogHeading = ref<HTMLElement>()
const audienceHeading = ref<HTMLElement>()
const activeKey = ref(form.value.tiles[0]?.key)
const activeIndex = computed(() => form.value.tiles.findIndex(tile => tile.key === activeKey.value))
const layoutHeading = ref<HTMLElement>()
const layout = ref<HTMLElement>()
const closing = ref(false)
const draftConflict = ref(false)
const recoveryButton = ref<HTMLButtonElement>()
const propertiesHeading = ref<HTMLElement>()
const readController = new AbortController()
let alive = true
const available = computed(() =>
  reports.value.filter(
    (r) => !form.value.tiles.some((t) => t.report_id === r.id),
  ),
)
async function loadCatalog(event?: MouseEvent) {
  if (event && catalogLoading.value) return
  const trigger = event?.currentTarget
  catalogLoading.value = true
  try {
    const result = await api.catalog(readController.signal)
    if (!alive) return
    reports.value = result.items
    if (!available.value.some(report => report.id === selected.value)) selected.value = undefined
    catalogError.value = ''
    if (trigger) void focusAfterUpdate(() => catalogHeading.value, trigger)
  } catch (failure) {
    if (!alive) return
    reports.value = []
    selected.value = undefined
    catalogError.value = message(failure)
  } finally {
    if (alive) catalogLoading.value = false
  }
}
async function loadAudience(event?: MouseEvent) {
  if (event && audienceLoading.value) return
  const trigger = event?.currentTarget
  audienceLoading.value = true
  const options = { signal: readController.signal, silentError: true }
  try {
    const [u, g] = await Promise.all([
      request.get<unknown, { items: { id: number; username: string }[] }>(
        '/users/brief', options,
      ),
      request.get<unknown, { items: { id: number; name: string }[] }>(
        '/audience-groups', options,
      ),
    ])
    if (!alive) return
    users.value = u.items
    groups.value = g.items
    audienceError.value = ''
    if (trigger) void focusAfterUpdate(() => audienceHeading.value, trigger)
  } catch (failure) {
    if (!alive) return
    users.value = []
    groups.value = []
    audienceError.value = message(failure)
  } finally {
    if (alive) audienceLoading.value = false
  }
}
onMounted(() => {
  void loadCatalog()
  void loadAudience()
})
onScopeDispose(() => {
  alive = false
  readController.abort()
})
const message = (e: unknown) =>
  e instanceof Error ? e.message : '操作失败，请刷新后核对'
const reportName = (id: number) =>
  reports.value.find((r) => r.id === id)?.name || `报表 #${id}（暂不可用）`
const contractChanged = (tile: DashboardTile) =>
  !!reports.value.find(
    (r) =>
      r.id === tile.report_id && r.data_contract.hash !== tile.contract_hash,
  )
function add(event: MouseEvent) {
  if (busy.value || catalogLoading.value || catalogError.value || form.value.tiles.length >= 8) return
  const p = reports.value.find((r) => r.id === selected.value)
  if (!p) return
  const bindings: Record<string, string> = {}
  for (const parameter of p.parameters || []) {
    const existing = globalParameters.value.find(g => g.name === parameter.name)
    if (!existing) globalParameters.value = [...globalParameters.value, JSON.parse(JSON.stringify(parameter))]
    if (!existing || existing.type === parameter.type) bindings[parameter.name] = parameter.name
  }
  const key = newRequestId()
  form.value.tiles.push({
    parameter_bindings: bindings,
    key,
    report_id: p.id,
    required: true,
    width: 'half',
    comparison_group: '',
    contract_hash: p.data_contract.hash,
    binding_policy: 'follow_published',
  })
  activeKey.value = key
  selected.value = undefined
  void focusLayout(event.currentTarget)
}
function move(index: number, direction: number, event: MouseEvent) {
  const tiles = form.value.tiles
  const destination = index + direction
  if (busy.value || destination < 0 || destination >= tiles.length) return
  ;[tiles[index], tiles[destination]] = [
    tiles[destination]!,
    tiles[index]!,
  ]
  const nextDestination = destination + direction
  if (nextDestination < 0 || nextDestination >= tiles.length) void focusLayout(event.currentTarget)
}
function remove(index: number) {
  if (busy.value) return
  form.value.tiles.splice(index, 1)
  activeKey.value = form.value.tiles[Math.min(index, form.value.tiles.length - 1)]?.key
  void focusLayout()
}
function focusLayout(trigger?: EventTarget | null) {
  return focusAfterUpdate(
    () => layout.value?.querySelector<HTMLElement>('[aria-pressed="true"]') ?? layoutHeading.value,
    trigger,
  )
}
async function focusAfterUpdate(target: () => HTMLElement | undefined, trigger?: EventTarget | null) {
  if (trigger && trigger !== document.activeElement) return
  await nextTick()
  if (!alive || (trigger && document.activeElement !== trigger && document.activeElement !== document.body)) return
  target()?.focus()
}
async function close(done?: () => void) {
  if (busy.value || closing.value) return
  closing.value = true
  try {
    if (dirty.value) await ElMessageBox.confirm('退出会放弃当前未保存的布局与配置。', '放弃未保存的草稿？', {
      type: 'warning', confirmButtonText: '放弃修改', cancelButtonText: '继续编辑',
    })
    if (alive) {
      if (done) done()
      else emit('close')
    }
  } catch {
    // Cancellation keeps the current editor and its draft.
  } finally {
    if (alive) closing.value = false
  }
}
async function save(event: SubmitEvent) {
  if (busy.value || closing.value || draftConflict.value || !form.value.name.trim()) return
  busy.value = true
  error.value = ''
  const payload = { version: version.value, definition: form.value }
  try {
    await api.save(
      props.dashboard?.id || 0,
      version.value,
      form.value,
      keys.get('save', payload),
    )
    if (alive) emit('saved')
  } catch (e) {
    if (!alive) return
    if (props.dashboard && e instanceof ApiRequestError && e.status === 409) {
      draftConflict.value = true
      error.value = '草稿或编辑权限已变化，请载入最新草稿核对。'
      void focusAfterUpdate(() => recoveryButton.value, event.submitter)
    } else {
      error.value = message(e)
    }
  } finally {
    if (alive) busy.value = false
  }
}
function applyDraft(next: ManagedDashboard) {
  form.value = next.draft
  version.value = next.draft_version
  original.value = JSON.stringify(next.draft)
  if (!next.draft.tiles.some(item => item.key === activeKey.value)) activeKey.value = next.draft.tiles[0]?.key
  if (!available.value.some(report => report.id === selected.value)) selected.value = undefined
}
async function reloadDraft(event: MouseEvent) {
  const id = props.dashboard?.id
  if (!id || busy.value || closing.value || !draftConflict.value) return
  const trigger = event.currentTarget
  const restoreFocus = trigger === document.activeElement
  const inRecovery = () => {
    const active = document.activeElement
    return active === trigger || active === document.body || !!active?.closest('.dashboard-draft-confirmation')
  }
  busy.value = true
  try {
    if (dirty.value) {
      const confirmed = await ElMessageBox.confirm('载入最新草稿会放弃本次未保存的布局与配置。', '放弃未保存的草稿？', {
        type: 'warning', confirmButtonText: '载入最新草稿', cancelButtonText: '继续编辑', modalClass: 'dashboard-draft-confirmation',
      }).then(() => true, () => false)
      if (!confirmed || !alive) return
    }
    error.value = ''
    const next = await api.get(id, readController.signal)
    if (!alive) return
    if (next.owner_id !== user.user?.id || next.lifecycle !== 'active') {
      error.value = '此草稿当前不可编辑，请返回目录核对状态。'
      return
    }
    applyDraft(next)
    draftConflict.value = false
  } catch (e) {
    if (alive) error.value = message(e)
  } finally {
    if (alive) {
      const focusWasRetained = restoreFocus && inRecovery()
      busy.value = false
      await nextTick()
      if (alive && focusWasRetained && inRecovery()) {
        (draftConflict.value ? recoveryButton.value : propertiesHeading.value)?.focus()
      }
    }
  }
}
async function adopt(tile: DashboardTile) {
  if (!props.dashboard || busy.value || dirty.value || draftConflict.value || catalogLoading.value || catalogError.value) return
  const report = reports.value.find((r) => r.id === tile.report_id)
  if (!report) return
  busy.value = true
  error.value = ''
  try {
    await ElMessageBox.confirm(
      `采纳「${report.name}」当前正式发布 #${report.publication_no} 的新口径？此操作修改草稿，必须重新审批发布。`,
      '确认口径变化',
    )
    if (!alive) return
    const payload = {
      version: version.value,
      report_id: report.id,
      publication_no: report.publication_no,
    }
    await api.command(
      props.dashboard.id,
      'adopt-contract',
      payload,
      keys.get('adopt', payload),
    )
    const next = await api.get(props.dashboard.id, readController.signal)
    if (alive) applyDraft(next)
  } catch (e) {
    if (e instanceof Error && alive) error.value = message(e)
  } finally {
    if (alive) busy.value = false
  }
}
</script>

<template>
  <el-dialog
    :model-value="true" :title="dashboard ? '编辑仪表盘草稿' : '新建仪表盘'"
    width="min(1200px,calc(100vw - 24px))" :close-on-click-modal="false" :show-close="!busy"
    :close-on-press-escape="!busy" :before-close="close" @close="emit('close')"
  >
    <form class="space-y-4" @submit.prevent="save">
      <section class="glass-panel ui-card-padding space-y-3" aria-labelledby="dashboard-properties">
        <h2 id="dashboard-properties" ref="propertiesHeading" tabindex="-1" class="font-semibold ui-focus-target">仪表盘配置</h2>
        <p v-if="dashboard" class="text-sm text-muted" role="status">草稿修订 {{ version }}</p>
        <div class="grid gap-3 sm:grid-cols-2">
          <label class="block text-sm">名称
            <el-input v-model="form.name" aria-label="仪表盘名称" maxlength="128" :disabled="busy" />
          </label>
          <label class="block text-sm">说明
            <el-input v-model="form.description" aria-label="仪表盘说明" maxlength="512" :disabled="busy" />
          </label>
        </div>
        <p class="text-sm text-muted">每张必需报表须覆盖仪表盘受众。送审后由具有报表读取权限的独立审阅人复核。</p>
      </section>

      <div class="dashboard-editor-grid">
        <section class="glass-panel ui-card-padding min-w-0 space-y-3" aria-labelledby="dashboard-report-library">
          <h2 id="dashboard-report-library" ref="catalogHeading" class="font-semibold ui-focus-target" tabindex="-1">已发布报表库</h2>
          <LoadingState v-if="catalogLoading" label="正在读取已发布报表…" variant="inline" />
          <div v-if="catalogError" class="space-y-2">
            <p v-if="!catalogLoading" role="alert" class="text-sm text-danger">{{ catalogError }}</p>
            <button type="button" class="ghost-button ui-control-target" :aria-disabled="catalogLoading || undefined"
              @click="loadCatalog">重试读取报表库</button>
          </div>
          <template v-else-if="!catalogLoading">
            <label class="text-sm">选择报表卡片
              <el-select v-model="selected" filterable placeholder="按名称查找已发布报表" aria-label="添加报表卡片"
                class="w-full" :disabled="busy || form.tiles.length >= 8 || !available.length">
                <el-option v-for="report in available" :key="report.id" :label="report.name" :value="report.id" />
              </el-select>
            </label>
            <button type="button" class="ghost-button ui-control-target" :disabled="!selected || busy || form.tiles.length >= 8" @click="add">添加卡片</button>
            <p v-if="!available.length" class="text-sm text-muted">
              {{ reports.length ? '当前可用报表已全部加入。' : '暂无获准查看且已批准数据契约的报表。' }}
            </p>
          </template>
        </section>

        <section ref="layout" class="glass-panel ui-card-padding min-w-0 space-y-3" aria-labelledby="dashboard-layout-heading">
          <div class="flex flex-wrap items-center justify-between gap-2">
            <h2 id="dashboard-layout-heading" ref="layoutHeading" class="font-semibold ui-focus-target" tabindex="-1">布局设计</h2>
            <span class="text-sm text-muted">{{ form.tiles.length }} / 8 张</span>
          </div>
          <div v-if="form.tiles.length" class="dashboard-tile-plan">
            <button v-for="(tile, index) in form.tiles" :key="tile.key" type="button"
              class="ghost-button dashboard-plan-card ui-control-target" :class="'plan-' + tile.width"
              :aria-label="'配置卡片 ' + (index + 1) + ' · ' + reportName(tile.report_id)"
              :aria-pressed="activeKey === tile.key" :disabled="busy" @click="activeKey = tile.key">
              <span class="text-xs text-muted">{{ index + 1 }} · {{ widthNames[tile.width] }}</span>
              <span class="block break-words font-medium">{{ reportName(tile.report_id) }}</span>
              <span class="text-xs text-muted">{{ tile.required ? '必需卡片' : '可选卡片' }}</span>
            </button>
          </div>
          <p v-else class="text-sm text-muted">从报表库添加卡片后，可设置顺序和宽度。</p>
        </section>

        <section class="glass-panel ui-card-padding min-w-0 space-y-3" aria-labelledby="dashboard-card-settings">
          <h2 id="dashboard-card-settings" class="font-semibold">卡片配置</h2>
          <p v-if="activeIndex < 0" class="text-sm text-muted">选择布局中的卡片开始配置。</p>
          <template v-for="(tile, index) in form.tiles" :key="tile.key">
            <div v-if="tile.key === activeKey" class="space-y-3">
              <p class="font-medium break-words">{{ reportName(tile.report_id) }}</p>
              <div class="flex flex-wrap gap-2">
                <button type="button" class="ghost-button ui-control-target" :disabled="busy || index === 0"
                  :aria-label="'上移卡片' + (index + 1)" @click="move(index, -1, $event)">上移</button>
                <button type="button" class="ghost-button ui-control-target" :disabled="busy || index === form.tiles.length - 1"
                  :aria-label="'下移卡片' + (index + 1)" @click="move(index, 1, $event)">下移</button>
                <button type="button" class="ghost-button ui-control-target" :disabled="busy" @click="remove(index)">移除</button>
              </div>
              <label class="flex items-center gap-2 text-sm"><input v-model="tile.required" type="checkbox" :disabled="busy" />必需卡片</label>
              <div>
                <label for="dashboard-card-width" class="block text-sm">宽度</label>
                <select id="dashboard-card-width" v-model="tile.width" class="glass-input block w-full mt-1"
                  :aria-label="'卡片' + (index + 1) + '宽度'" :disabled="busy">
                  <option v-for="(name, value) in widthNames" :key="value" :value="value">{{ name }}</option>
                </select>
              </div>
              <label class="block text-sm">比较组（可选）
                <el-input v-model="tile.comparison_group" :aria-label="'卡片' + (index + 1) + '比较组'" :disabled="busy" maxlength="64" />
              </label>
              <label v-for="parameter in reportParameters(tile.report_id)" :key="parameter.name" class="block text-sm">
                {{ parameter.label }}映射
                <select :value="tile.parameter_bindings?.[parameter.name] || ''" :aria-label="'卡片' + (index + 1) + parameter.label + '映射'"
                  class="glass-input block w-full mt-1" :disabled="busy" @change="bindParameter(tile, parameter.name, ($event.target as HTMLSelectElement).value)">
                  <option value="">使用报表默认值</option>
                  <option v-for="global in globalParameters.filter(item => item.type === parameter.type)" :key="global.name" :value="global.name">{{ global.label }}</option>
                </select>
              </label>
              <p class="text-xs text-muted break-words">跟随兼容的正式发布 · 契约 {{ tile.contract_hash?.slice(0, 12) || '保存时由服务端选定' }}</p>
              <button v-if="dashboard" type="button" class="ghost-button ui-control-target" :disabled="busy || dirty || draftConflict" @click="accessTile = tile.key">为已保存的拟受众申请报表权限</button>
              <div v-if="contractChanged(tile)" class="space-y-2">
                <p class="text-sm text-warning">当前正式报表口径已变化。明确采纳后需要重新送审。</p>
                <button type="button" class="ghost-button ui-control-target" :disabled="busy || dirty || draftConflict" @click="adopt(tile)">明确采纳当前口径</button>
                <p v-if="dirty && !draftConflict" class="text-xs text-muted">请先保存布局修改。</p>
              </div>
            </div>
          </template>
        </section>
      </div>

      <section class="glass-panel ui-card-padding space-y-3" aria-labelledby="dashboard-audience-heading">
        <h2 id="dashboard-audience-heading" ref="audienceHeading" class="font-semibold ui-focus-target" tabindex="-1">受众与筛选</h2>
        <LoadingState v-if="audienceLoading" label="正在读取受众选项…" variant="inline" />
        <div v-if="audienceError" class="flex flex-wrap items-center gap-2">
          <p v-if="!audienceLoading" role="alert" class="text-sm text-danger">{{ audienceError }}</p>
          <button type="button" class="ghost-button ui-control-target" :aria-disabled="audienceLoading || undefined"
            @click="loadAudience">重试读取受众</button>
        </div>
        <div v-else-if="!audienceLoading" class="grid gap-3 sm:grid-cols-2">
          <label class="block text-sm">个人受众
            <el-select v-model="form.user_ids" multiple filterable aria-label="仪表盘个人受众" class="w-full" :disabled="busy">
              <el-option v-for="item in users" :key="item.id" :value="item.id" :label="item.username" />
            </el-select>
          </label>
          <label class="block text-sm">用户组受众
            <el-select v-model="form.group_ids" multiple filterable aria-label="仪表盘用户组受众" class="w-full" :disabled="busy">
              <el-option v-for="item in groups" :key="item.id" :value="item.id" :label="item.name" />
            </el-select>
          </label>
        </div>
        <ReportParameterEditor v-model="globalParameters" :disabled="busy" title="全局筛选参数" />
      </section>

      <section class="glass-panel ui-card-padding space-y-3" aria-labelledby="dashboard-freshness-heading">
        <h2 id="dashboard-freshness-heading" class="font-semibold">时效与比较</h2>
        <label class="flex items-start gap-2 text-sm">
          <input v-model="form.quality.allow_unknown_freshness" type="checkbox" :disabled="busy || !user.workspace?.allow_unknown_freshness" />
          允许未知时效（需空间策略许可及单独审阅）
        </label>
        <label v-if="form.quality.allow_unknown_freshness" class="block text-sm">业务理由
          <el-input v-model="form.quality.reason" aria-label="允许未知时效的理由" :disabled="busy" maxlength="1000" />
        </label>
        <label class="flex flex-wrap items-center gap-2 text-sm">比较组最大水位差（秒）
          <el-input-number v-model="form.quality.max_watermark_skew_seconds" :min="0" :max="3600" aria-label="比较组最大水位差" :disabled="busy" />
        </label>
      </section>
      <p v-if="error" role="alert" class="text-sm text-danger">{{ error }}</p>
      <div class="flex flex-wrap justify-end gap-2">
        <button type="button" class="ghost-button ui-control-target" :disabled="busy || closing" @click="close()">取消</button>
        <button v-if="draftConflict" ref="recoveryButton" type="button" class="liquid-button ui-control-target" :aria-disabled="busy || closing" @click="reloadDraft">
          {{ busy ? '读取中…' : '载入最新草稿' }}
        </button>
        <button type="submit" class="ui-control-target" :class="draftConflict ? 'ghost-button' : 'liquid-button'"
          :disabled="closing || !form.name.trim()" :aria-disabled="busy || draftConflict">{{ busy && !draftConflict ? '保存中…' : '保存草稿' }}</button>
      </div>
    </form>
    <DashboardAccessDialog v-if="accessTile && dashboard" :dashboard="{ ...dashboard, draft: form, draft_version: version }"
      :tile="accessTile" @close="accessTile = undefined" @submitted="accessSubmitted" />
  </el-dialog>
</template>

<style scoped>
.dashboard-editor-grid { display: grid; gap: 12px; align-items: start; }
.dashboard-tile-plan { display: grid; grid-template-columns: repeat(12, minmax(0, 1fr)); gap: 8px; }
.dashboard-plan-card { min-width: 0; text-align: left; align-content: start; display: grid; gap: 4px; }
.dashboard-plan-card[aria-pressed='true'] { border-color: var(--db-accent); background: var(--db-selected); }
.plan-full { grid-column: span 12; }
.plan-half { grid-column: span 6; }
.plan-wide { grid-column: span 8; }
.plan-narrow { grid-column: span 4; }
@media (min-width: 1000px) {
  .dashboard-editor-grid { grid-template-columns: minmax(0, 1fr) minmax(0, 2fr) minmax(0, 1.25fr); }
}
@media (max-width: 520px) {
  .dashboard-plan-card { grid-column: span 12; }
}
</style>
