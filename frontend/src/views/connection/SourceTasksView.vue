<script setup lang="ts">
import { computed, nextTick, onMounted, onScopeDispose, ref, useId } from 'vue'
import { useRoute } from 'vue-router'
import LoadingState from '../../components/base/LoadingState.vue'
import PageCategoryTabs from '../../components/base/PageCategoryTabs.vue'
import PageHeader from '../../components/base/PageHeader.vue'
import PageState from '../../components/base/PageState.vue'
import { automationApi, type SourceTask, type SourceSchedule, type CheckHistory, type JobConnection, type TaskStatus } from '../../api/automation'
import { pageTitles } from '../../navigation'
import { useUserStore } from '../../stores/user'
import { activeTask, retryableTask, taskStatusLabels } from '../../utils/automation'
import { iamUiEnabled } from '../../utils/iam-mode'
import { featureAllowed } from '../../utils/permission'
import { reportCommandKeys } from '../report/reportCommands'

defineProps<{ embedded?: boolean }>()

const route = useRoute()
const user = useUserStore()
const id = useId()
const categories = [
  { id: 'tasks', name: '任务记录' },
  { id: 'schedules', name: '定时计划' },
  { id: 'history', name: '检查历史' },
] as const
type Tab = typeof categories[number]['id']

const tab = ref<Tab>('tasks')
const connections = ref<JobConnection[]>([])
const connectionID = ref(Number(route.query.connection_id) || undefined)
const kind = ref<'check' | 'collect'>(route.query.kind === 'collect' ? 'collect' : 'check')
const intervalMinutes = ref(60)
const editingVersion = ref(0)
const tasks = ref<SourceTask[]>([])
const schedules = ref<SourceSchedule[]>([])
const history = ref<CheckHistory[]>([])
const statusFilter = ref<TaskStatus | ''>('')
const page = ref(1)
const total = ref(0)
const pageSize = 20
const loading = ref(true)
const optionsLoading = ref(true)
const busy = ref(false)
const listError = ref('')
const optionsError = ref('')
const actionError = ref('')
const notice = ref('')
const selectedTaskId = ref<string>()
const taskPage = ref<HTMLElement>()
const actionNotice = ref<HTMLElement>()
const actionFailure = ref<HTMLElement>()
const detailHeading = ref<HTMLHeadingElement>()
const formHeading = ref<HTMLHeadingElement>()
let detailTrigger: HTMLElement | undefined

const selectedConnection = computed(() => connections.value.find(item => item.id === connectionID.value))
const canOpenConnections = computed(() => featureAllowed(user, 'connection.page', true))
const canReadConnections = computed(() => canOpenConnections.value || featureAllowed(user, 'connection.use', true))
const canEditTask = computed(() => canReadConnections.value && (canOperate('check') || canOperate('collect')))
const selectedTask = computed(() => tasks.value.find(item => item.id === selectedTaskId.value))
const canCollect = computed(() => canOperate('collect') && !!selectedConnection.value?.capabilities?.collect)
const canSubmit = computed(() => !busy.value && !optionsLoading.value && !optionsError.value && !!selectedConnection.value
  && canOperate(kind.value) && (kind.value !== 'collect' || canCollect.value))
const emptyTitle = computed(() => ({ tasks: '暂无任务记录', schedules: '暂无定时计划', history: '暂无检查历史' })[tab.value])
const loadingLabel = computed(() => ({ tasks: '正在读取任务记录…', schedules: '正在读取定时计划…', history: '正在读取检查历史…' })[tab.value])
const hasItems = computed(() => ({ tasks: tasks.value, schedules: schedules.value, history: history.value })[tab.value].length > 0)

const lifetime = new AbortController()
const keys = reportCommandKeys()
let listRequest: AbortController | undefined
let timer: ReturnType<typeof setInterval> | undefined

function formatTime(value: string | null) {
  return value ? new Date(value).toLocaleString() : '—'
}

function taskKind(value: SourceTask['kind']) {
  return value === 'check' ? '连接检查' : '结构采集'
}

function sourceName(connection: number) {
  return connections.value.find(item => item.id === connection)?.name
    || (optionsLoading.value || optionsError.value ? `连接 #${connection}` : '个人连接已移除')
}

function canOperate(value: SourceTask['kind']) {
  const manage = featureAllowed(user, 'source.definition.manage', user.hasCapability('datasource_manage'))
  if (value === 'collect') return manage || featureAllowed(user, 'connection.manage', ['admin', 'developer'].includes(user.role))
  return manage || featureAllowed(user, 'connection.use', true)
}

function supportedTask(task: SourceTask) {
  return !task.permission_bridge || ['legacy-frozen-v1', 'phase1-bridge-2'].includes(task.permission_bridge)
}

function permissionLabel(task: SourceTask) {
  if (!supportedTask(task)) return '权限版本不支持，已暂停'
  return task.permission_bridge === 'phase1-bridge-2' ? '角色权限任务' : '原策略任务'
}

function resetPlan() {
  editingVersion.value = 0
}

function clearDetail() {
  selectedTaskId.value = undefined
  detailTrigger = undefined
}

function selectTab(value: string) {
  const category = categories.find(item => item.id === value)
  if (!category || category.id === tab.value) return
  tab.value = category.id
  page.value = 1
  notice.value = ''
  actionError.value = ''
  clearDetail()
  void load()
}

function changePage(value: number) {
  page.value = value
  clearDetail()
  void load()
}

async function readConnections() {
  if (!canReadConnections.value) {
    optionsLoading.value = false
    return
  }
  optionsLoading.value = true
  optionsError.value = ''
  try {
    const result = await automationApi.connections(lifetime.signal)
    if (lifetime.signal.aborted) return
    connections.value = result.items
    if (!selectedConnection.value) connectionID.value = result.items[0]?.id
  } catch (error) {
    if (!lifetime.signal.aborted) optionsError.value = error instanceof Error ? error.message : '读取个人连接失败'
  } finally {
    if (!lifetime.signal.aborted) optionsLoading.value = false
  }
}

async function load(silent = false) {
  if (silent && listRequest) return
  listRequest?.abort()
  const current = new AbortController()
  listRequest = current
  const currentTab = tab.value
  if (!silent) loading.value = true
  listError.value = ''
  try {
    if (currentTab === 'tasks') {
      const result = await automationApi.tasks({ status: statusFilter.value || undefined, page: page.value }, current.signal)
      if (current.signal.aborted) return
      tasks.value = result.items
      total.value = result.total
      if (!selectedTask.value) clearDetail()
    } else if (currentTab === 'schedules') {
      const result = await automationApi.schedules(current.signal)
      if (!current.signal.aborted) schedules.value = result.items
    } else {
      const result = await automationApi.history({ page: page.value }, current.signal)
      if (current.signal.aborted) return
      history.value = result.items
      total.value = result.total
    }
  } catch (error) {
    if (current.signal.aborted) return
    tasks.value = []
    schedules.value = []
    history.value = []
    total.value = 0
    clearDetail()
    listError.value = error instanceof Error ? error.message : '读取失败，请重试。'
  } finally {
    if (!current.signal.aborted) {
      listRequest = undefined
      loading.value = false
    }
  }
}

async function openDetail(task: SourceTask, event: MouseEvent) {
  detailTrigger = event.currentTarget instanceof HTMLElement ? event.currentTarget : undefined
  selectedTaskId.value = task.id
  await nextTick()
  detailHeading.value?.focus()
}

async function closeDetail() {
  const trigger = detailTrigger
  clearDetail()
  await nextTick()
  trigger?.focus()
}

async function act<T>(operation: () => Promise<T>, message: string): Promise<T | undefined> {
  if (busy.value) return
  busy.value = true
  actionError.value = ''
  notice.value = ''
  try {
    const result = await operation()
    keys.clear()
    if (lifetime.signal.aborted) return
    notice.value = message
    page.value = 1
    clearDetail()
    await load()
    if (!lifetime.signal.aborted) window.dispatchEvent(new Event('dbhub-inbox-updated'))
    return result
  } catch (error) {
    if (!lifetime.signal.aborted) actionError.value = error instanceof Error ? error.message : '提交失败，请刷新后核对'
  } finally {
    if (!lifetime.signal.aborted) busy.value = false
  }
}

function createTask() {
  if (!canSubmit.value || !connectionID.value) return
  const body = { connection_id: connectionID.value, kind: kind.value }
  void act(() => automationApi.createTask({ ...body, request_id: keys.get('create', body) }, lifetime.signal), '任务已加入队列')
}

function cancelTask(task: SourceTask) {
  void act(() => automationApi.cancelTask(task.id, lifetime.signal), '取消请求已提交；已完成的操作不会撤回')
}

async function retryTask(task: SourceTask, event: MouseEvent) {
  if (busy.value) return
  const trigger = event.currentTarget
  const restoreFocus = trigger instanceof HTMLElement && trigger === document.activeElement
  const result = await act(
    () => automationApi.retryTask(task.id, keys.get('retry:' + task.id, { id: task.id }), lifetime.signal),
    '已创建新的重试任务',
  )
  if (!restoreFocus || lifetime.signal.aborted || tab.value !== 'tasks') return
  await nextTick()
  const active = document.activeElement
  if (active !== trigger && active !== document.body) return
  const retryDetail = result && taskPage.value?.querySelector<HTMLButtonElement>(`[data-task-detail-id="${CSS.escape(result.id)}"]`)
  const target = retryDetail || (result ? actionNotice.value : actionFailure.value)
  target?.focus()
}

async function editSchedule(schedule: SourceSchedule) {
  connectionID.value = schedule.connection_id
  kind.value = schedule.kind
  intervalMinutes.value = schedule.interval_minutes
  editingVersion.value = schedule.version
  notice.value = '修改后保存，将绑定当前配置'
  await nextTick()
  formHeading.value?.focus()
}

function saveSchedule() {
  if (!canSubmit.value || !connectionID.value) return
  const body = { connection_id: connectionID.value, kind: kind.value, interval_minutes: intervalMinutes.value, enabled: true, version: editingVersion.value }
  void act(async () => {
    await automationApi.saveSchedule(body, lifetime.signal)
    if (!lifetime.signal.aborted) resetPlan()
  }, '定时计划已启用')
}

function pauseSchedule(schedule: SourceSchedule) {
  const body = { connection_id: schedule.connection_id, kind: schedule.kind, interval_minutes: schedule.interval_minutes, enabled: false, version: schedule.version }
  void act(() => automationApi.saveSchedule(body, lifetime.signal), '定时计划已暂停')
}

onMounted(() => {
  void readConnections()
  void load()
  timer = setInterval(() => {
    if (!loading.value && !busy.value && !document.hidden && navigator.onLine && tab.value === 'tasks'
      && tasks.value.some(task => activeTask(task.status))) void load(true)
  }, 3000)
})

onScopeDispose(() => {
  lifetime.abort()
  listRequest?.abort()
  clearInterval(timer)
  keys.clear()
})
</script>

<template>
  <section ref="taskPage" class="ui-page-stack min-w-0">
    <PageHeader :title="pageTitles.sourceTasks" :heading="embedded ? 'h2' : 'h1'"
      description="查看当前工作空间内自己的连接检查与结构采集。离开页面后，后台任务继续执行。">
      <template #actions>
        <RouterLink v-if="canOpenConnections" :to="{ path: '/connections', query: { workspace_id: route.query.workspace_id } }"
          class="ghost-button text-sm inline-flex items-center ui-control-target">返回我的连接</RouterLink>
      </template>
    </PageHeader>

    <p v-if="actionError" ref="actionFailure" role="alert" tabindex="-1" class="text-sm text-danger ui-focus-target">{{ actionError }}</p>
    <p v-if="notice" ref="actionNotice" role="status" tabindex="-1" class="text-sm text-success ui-focus-target">{{ notice }}</p>
    <PageCategoryTabs label="数据源任务分类" :categories="categories" :active-id="tab" @select="selectTab">
      <div class="space-y-4 min-w-0">
        <form v-if="tab !== 'history' && canEditTask" class="data-surface ui-card-padding space-y-3"
          @submit.prevent="tab === 'tasks' ? createTask() : saveSchedule()">
          <component :is="embedded ? 'h3' : 'h2'" ref="formHeading" tabindex="-1" class="font-semibold">
            {{ tab === 'tasks' ? '新建后台任务' : editingVersion ? '编辑定时计划' : '新建定时计划' }}
          </component>
          <div v-if="optionsError" class="flex flex-wrap items-center gap-3 text-sm">
            <p role="alert" class="text-danger">无法读取个人连接。{{ optionsError }}</p>
            <button type="button" class="ghost-button ui-control-target" @click="readConnections">重试读取连接</button>
          </div>
          <p v-else-if="!optionsLoading && !connections.length" class="text-sm text-muted">暂无可选的个人连接。</p>
          <div class="ui-filter-bar">
            <div class="ui-filter-field">
              <label :for="id + '-connection'" class="text-sm text-muted">个人数据源</label>
              <el-select :id="id + '-connection'" v-model="connectionID" aria-label="任务数据源" class="w-full" filterable
                :loading="optionsLoading" loading-text="正在加载个人数据源…" :disabled="busy || optionsLoading || !!optionsError"
                @change="resetPlan">
                <el-option v-for="conn in connections" :key="conn.id" :value="conn.id" :label="conn.name" />
              </el-select>
            </div>
            <div class="ui-filter-field">
              <label :for="id + '-kind'" class="text-sm text-muted">任务类型</label>
              <el-select :id="id + '-kind'" v-model="kind" class="w-full" :disabled="busy" @change="resetPlan">
                <el-option value="check" label="连接检查" :disabled="!canOperate('check')" />
                <el-option value="collect" label="采集结构" :disabled="!canCollect" />
              </el-select>
            </div>
            <div v-if="tab === 'schedules'" class="ui-filter-field">
              <label :for="id + '-interval'" class="text-sm text-muted">间隔（分钟）</label>
              <input :id="id + '-interval'" v-model.number="intervalMinutes" aria-label="计划间隔分钟" class="glass-input w-full"
                type="number" :min="kind === 'collect' ? 60 : 5" max="10080" required :disabled="busy" />
            </div>
          </div>
          <div class="flex flex-wrap items-center gap-2">
            <button type="submit" class="liquid-button text-sm ui-control-target" :disabled="!canSubmit">
              {{ busy ? '提交中…' : tab === 'tasks' ? '加入队列' : '保存并启用' }}
            </button>
            <button v-if="tab === 'schedules' && editingVersion" type="button" class="ghost-button text-sm ui-control-target"
              :disabled="busy" @click="resetPlan">取消编辑</button>
          </div>
          <p v-if="tab === 'schedules'" class="text-xs text-muted">检查最短 5 分钟，采集最短 60 分钟。按保存后的间隔开始，不补跑停机期间的时段。</p>
          <p v-if="kind === 'collect' && !canCollect" class="text-sm text-muted">当前身份或连接不支持采集。</p>
        </form>

        <div class="flex flex-wrap items-end gap-3">
          <div v-if="tab === 'tasks'" class="w-full sm:w-52 space-y-2">
            <label :for="id + '-status'" class="block text-sm text-muted">任务状态</label>
            <el-select :id="id + '-status'" v-model="statusFilter" aria-label="任务状态筛选" placeholder="全部状态"
              class="w-full" @change="changePage(1)">
              <el-option value="" label="全部状态" />
              <el-option v-for="(label, key) in taskStatusLabels" :key="key" :value="key" :label="label" />
            </el-select>
          </div>
          <button type="button" class="ghost-button text-sm ui-control-target" :disabled="loading || busy" @click="load()">刷新列表</button>
        </div>

        <LoadingState v-if="loading" :label="loadingLabel" />
        <PageState v-else-if="listError" title="读取列表失败" :description="listError" tone="danger" :heading="embedded ? 'h3' : 'h2'">
          <template #actions><button type="button" class="ghost-button ui-control-target" @click="load()">重试读取列表</button></template>
        </PageState>
        <PageState v-else-if="!hasItems" :title="emptyTitle" description="当前列表没有记录。" :heading="embedded ? 'h3' : 'h2'" />
        <div v-else class="editorial-table-wrap" role="region" tabindex="0" :aria-label="categories.find(item => item.id === tab)?.name + '表格'">
          <table v-if="tab === 'tasks'" class="editorial-table min-w-[860px]" aria-label="任务记录">
            <thead><tr>
              <th scope="col">序号</th><th scope="col">任务标识</th><th scope="col">来源</th><th scope="col">类型</th>
              <th scope="col">状态与阶段</th><th scope="col">提交时间</th><th scope="col">操作</th>
            </tr></thead>
            <tbody>
              <tr v-for="(task, index) in tasks" :key="task.id" :class="{ 'bg-interactive': selectedTaskId === task.id }">
                <td class="editorial-table__number">{{ (page - 1) * pageSize + index + 1 }}</td>
                <th scope="row" class="max-w-52 !whitespace-normal [overflow-wrap:anywhere] font-mono">{{ task.id }}</th>
                <td class="max-w-44 [overflow-wrap:anywhere]">{{ task.connection_name }}</td>
                <td class="whitespace-nowrap">{{ taskKind(task.kind) }}</td>
                <td class="min-w-32">
                  <span class="inline-block rounded-sm px-2 py-0.5 text-xs"
                    :class="task.status === 'succeeded' ? 'bg-success-soft text-success'
                      : ['failed', 'unknown', 'interrupted'].includes(task.status) ? 'bg-warning-soft text-warning' : 'bg-surface-muted text-content'">
                    {{ taskStatusLabels[task.status] }}
                  </span>
                  <p class="editorial-table__meta">{{ task.phase }}</p>
                </td>
                <td class="text-xs text-muted whitespace-nowrap"><time :datetime="task.created_at">{{ formatTime(task.created_at) }}</time></td>
                <td>
                  <div class="editorial-table__actions">
                    <button type="button" class="editorial-text-action ui-control-target"
                      :aria-label="'查看详情 ' + task.id" :aria-expanded="selectedTaskId === task.id"
                      :data-task-detail-id="task.id"
                      :aria-controls="selectedTaskId === task.id ? id + '-detail' : undefined"
                      @click="openDetail(task, $event)">查看详情</button>
                    <button v-if="supportedTask(task) && canOperate(task.kind) && activeTask(task.status) && task.status !== 'cancel_requested'"
                      type="button" class="ghost-button text-xs ui-control-target" :disabled="busy" @click="cancelTask(task)">取消任务</button>
                    <button v-if="supportedTask(task) && canOperate(task.kind) && retryableTask(task.status) && task.connection_id"
                      type="button" class="ghost-button text-xs ui-control-target" :disabled="busy" @click="retryTask(task, $event)">重新执行</button>
                    <RouterLink v-if="task.status === 'succeeded' && task.kind === 'collect'" class="editorial-text-action inline-flex items-center ui-control-target"
                      :to="{ path: '/assets', query: { workspace_id: user.workspaceId, connection_id: String(task.connection_id) } }">查看数据目录</RouterLink>
                  </div>
                </td>
              </tr>
            </tbody>
          </table>

          <table v-else-if="tab === 'schedules'" class="editorial-table min-w-[720px]" aria-label="定时计划">
            <thead><tr>
              <th scope="col">序号</th><th scope="col">来源</th><th scope="col">类型</th><th scope="col">间隔</th>
              <th scope="col">状态</th><th scope="col">下次运行</th><th scope="col">操作</th>
            </tr></thead>
            <tbody>
              <tr v-for="(schedule, index) in schedules" :key="schedule.id">
                <td class="editorial-table__number">{{ index + 1 }}</td>
                <th scope="row" class="max-w-48 !whitespace-normal [overflow-wrap:anywhere]">{{ sourceName(schedule.connection_id) }}</th>
                <td class="whitespace-nowrap">{{ taskKind(schedule.kind) }}</td>
                <td class="whitespace-nowrap">{{ schedule.interval_minutes }} 分钟</td>
                <td>
                  <span :class="schedule.enabled ? 'text-success' : 'text-muted'">{{ schedule.enabled ? '已启用' : '已暂停' }}</span>
                  <p v-if="schedule.last_status" class="editorial-table__meta">上次：{{ taskStatusLabels[schedule.last_status as TaskStatus] || schedule.last_status }}</p>
                </td>
                <td class="whitespace-nowrap text-xs text-muted">{{ schedule.enabled ? formatTime(schedule.next_run_at) : '—' }}</td>
                <td>
                  <div class="editorial-table__actions">
                    <button v-if="canEditTask" type="button" class="editorial-text-action ui-control-target"
                      :disabled="busy || optionsLoading || !!optionsError || !connections.some(conn => conn.id === schedule.connection_id) || !canOperate(schedule.kind)"
                      @click="editSchedule(schedule)">编辑计划</button>
                    <button v-if="schedule.enabled" type="button" class="ghost-button text-xs ui-control-target"
                      :disabled="busy || !schedule.connection_id" @click="pauseSchedule(schedule)">暂停计划</button>
                  </div>
                </td>
              </tr>
            </tbody>
          </table>

          <table v-else class="editorial-table min-w-[640px]" aria-label="检查历史">
            <thead><tr>
              <th scope="col">序号</th><th scope="col">来源</th><th scope="col">结果</th><th scope="col">版本或错误</th>
              <th scope="col">耗时</th><th scope="col">检查时间</th>
            </tr></thead>
            <tbody>
              <tr v-for="(check, index) in history" :key="check.id">
                <td class="editorial-table__number">{{ (page - 1) * pageSize + index + 1 }}</td>
                <th scope="row" class="max-w-48 !whitespace-normal [overflow-wrap:anywhere]">{{ check.connection_name || '已删除的个人连接' }}</th>
                <td>
                  <span :class="check.result.ok ? 'text-success' : 'text-warning'">{{ check.result.ok ? '检查通过' : '检查失败' }}</span>
                  <p v-if="!check.current_config" class="editorial-table__meta">旧配置</p>
                </td>
                <td class="max-w-64 [overflow-wrap:anywhere]">{{ check.result.error || check.result.version || '—' }}</td>
                <td class="whitespace-nowrap">{{ check.result.elapsed_ms }} 毫秒</td>
                <td class="whitespace-nowrap text-xs text-muted"><time :datetime="check.result.checked_at">{{ formatTime(check.result.checked_at) }}</time></td>
              </tr>
            </tbody>
          </table>
        </div>

        <div v-if="!loading && !listError && tab !== 'schedules' && (hasItems || page > 1)"
          class="flex flex-wrap items-center justify-between gap-3 text-sm text-muted">
          <span aria-live="polite">共 {{ total }} 条</span>
          <nav class="flex flex-wrap items-center gap-2" aria-label="数据源任务分页">
            <button type="button" class="ghost-button ui-control-target" :disabled="page <= 1 || busy" @click="changePage(page - 1)">上一页</button>
            <span>第 {{ page }} 页</span>
            <button type="button" class="ghost-button ui-control-target" :disabled="page * pageSize >= total || busy" @click="changePage(page + 1)">下一页</button>
          </nav>
        </div>

        <section v-if="tab === 'tasks' && !loading && !listError && selectedTask"
          :id="id + '-detail'" :aria-labelledby="id + '-detail-title'"
          class="data-surface ui-card-padding space-y-3 min-w-0 [overflow-wrap:anywhere]">
          <div class="flex flex-wrap items-center justify-between gap-3">
            <component :is="embedded ? 'h3' : 'h2'" :id="id + '-detail-title'" ref="detailHeading"
              tabindex="-1" class="text-lg font-semibold ui-focus-target">任务详情</component>
            <button type="button" class="ghost-button ui-control-target" @click="closeDetail">关闭详情</button>
          </div>
          <dl class="grid grid-cols-[auto_minmax(0,1fr)] sm:grid-cols-[auto_minmax(0,1fr)_auto_minmax(0,1fr)] gap-x-3 gap-y-2 text-sm">
            <dt class="text-muted">任务标识</dt><dd class="font-mono">{{ selectedTask.id }}</dd>
            <dt class="text-muted">来源</dt><dd>{{ selectedTask.connection_name }}</dd>
            <dt class="text-muted">类型</dt><dd>{{ taskKind(selectedTask.kind) }}</dd>
            <dt class="text-muted">状态</dt><dd>{{ taskStatusLabels[selectedTask.status] }}</dd>
            <dt class="text-muted">当前阶段</dt><dd>{{ selectedTask.phase || '—' }}</dd>
            <dt class="text-muted">提交时间</dt><dd>{{ formatTime(selectedTask.created_at) }}</dd>
            <dt class="text-muted">开始时间</dt><dd>{{ formatTime(selectedTask.started_at) }}</dd>
            <dt class="text-muted">结束时间</dt><dd>{{ formatTime(selectedTask.finished_at) }}</dd>
            <dt v-if="selectedTask.result?.elapsed_ms != null" class="text-muted">耗时</dt>
            <dd v-if="selectedTask.result?.elapsed_ms != null">{{ selectedTask.result.elapsed_ms }} 毫秒</dd>
            <dt v-if="selectedTask.result?.version" class="text-muted">服务版本</dt>
            <dd v-if="selectedTask.result?.version">{{ selectedTask.result.version }}</dd>
            <dt v-if="selectedTask.status === 'succeeded' && selectedTask.kind === 'collect'" class="text-muted">采集结果</dt>
            <dd v-if="selectedTask.status === 'succeeded' && selectedTask.kind === 'collect'">
              已采集 {{ selectedTask.result?.tables ?? '—' }} 张表、{{ selectedTask.result?.views ?? '—' }} 个视图
            </dd>
            <dt v-if="selectedTask.parent_id" class="text-muted">原任务标识</dt><dd v-if="selectedTask.parent_id">{{ selectedTask.parent_id }}</dd>
            <dt v-if="iamUiEnabled" class="text-muted">权限版本</dt><dd v-if="iamUiEnabled">{{ permissionLabel(selectedTask) }}</dd>
          </dl>
        </section>

        <p v-if="tab === 'schedules'" class="text-xs text-muted">配置或成员权限变化后，旧计划会暂停；重新保存后绑定当前配置。</p>
        <p v-if="tab === 'history'" class="text-xs text-muted">历史检查反映执行时的状态。</p>
      </div>
    </PageCategoryTabs>
  </section>
</template>
