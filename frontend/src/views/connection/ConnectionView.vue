<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref, useId, watch } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  Database,
  Ellipsis,
  Library,
  Plus,
  Settings2,
  SquareTerminal,
  Zap,
} from 'lucide-vue-next'
import {
  connectionApi,
  sourceLabel,
  type ConnectionItem,
  type ConnectionPayload,
  type DbType,
  type EnvKind,
  type TestResult,
  type TestedVersion,
  type VersionCompatibility,
} from '../../api/datasource'
import { proxyApi, type ProxyItem } from '../../api/proxy'
import { useUserStore } from '../../stores/user'
import LoadingState from '../../components/base/LoadingState.vue'
import PageHeader from '../../components/base/PageHeader.vue'
import PageState from '../../components/base/PageState.vue'
import { pageTitles } from '../../navigation'
import { ApiRequestError } from '../../utils/request'
import { connectorApi, type DatasourceTemplate } from '../../api/connector'
import SourceCatalog from './SourceCatalog.vue'
import { sourceCatalogApi } from '../../api/source-catalog'
import ConnectionDiagnosis from './ConnectionDiagnosis.vue'
import SourceTasksView from './SourceTasksView.vue'
import ProxyView from '../proxy/ProxyView.vue'
import { featureAllowed } from '../../utils/permission'
import type { JobConnection } from '../../api/automation'

const router = useRouter()
const userStore = useUserStore()
const canManage = computed(() => featureAllowed(userStore, 'connection.manage', true))
const canUse = computed(() => featureAllowed(userStore, 'connection.use', true))
const canManageProxy = computed(() => featureAllowed(userStore, 'connection.proxy.manage', userStore.role !== 'readonly'))
const canViewTasks = computed(() => featureAllowed(userStore, 'connection.task.view', true))
const canEdit = (c: ConnectionItem) => canManage.value && userStore.user?.id === c.user_id
const id = useId()
const addOpen = ref(false)
const addStep = ref<'choice' | 'shared'>('choice')
const addButton = ref<HTMLButtonElement>()
const detailsID = ref<number>()
const detailsOpen = ref(false)
const taskConnection = ref<JobConnection>()
const tasksOpen = ref(false)
let taskTrigger: HTMLElement | null = null
const publishing = ref(false)
function openAdd() {
  addStep.value = 'choice'
  addOpen.value = true
}
async function addedFromCatalog() {
  addOpen.value = false
  await fetchConnections()
}
const canPublish = (c: ConnectionItem) => userStore.hasCapability('datasource_manage')
  && featureAllowed(userStore, 'source.definition.manage', true)
  && c.definition?.visibility !== 'global' && !c.proxy_id && !c.ssh_tunnel_id
async function publishDefinition(c: ConnectionItem) {
 if (publishing.value) return
 try { await ElMessageBox.confirm(`发布「${c.name}」的地址、库名和驱动配置到当前工作空间目录？使用者需要填写自己的数据库账号。`, '发布数据源定义', { type:'info',confirmButtonText:'发布定义' }) } catch { return }
 publishing.value = true
 try { await sourceCatalogApi.publish(c.id,'global'); ElMessage.success('数据源定义已发布'); await fetchConnections() } catch { /* API displays failure */ } finally { publishing.value=false }
}
const templates = ref<DatasourceTemplate[]>([])
const jdbcParameters = reactive<Record<string,string>>({})

const keyword = ref('')
const typeFilter = ref<DbType | ''>('')
const environmentFilter = ref<EnvKind | ''>('')
const connections = ref<ConnectionItem[]>([])
const proxies = ref<ProxyItem[]>([])
const connLoading = ref(true)
const connError = ref('')
const hasFilters = computed(() => !!(keyword.value.trim() || typeFilter.value || environmentFilter.value))
const filteredConnections = computed(() => {
  const search = keyword.value.trim().toLocaleLowerCase()
  return connections.value.filter(conn =>
    (!typeFilter.value || conn.type === typeFilter.value)
    && (!environmentFilter.value || (conn.environment || 'dev') === environmentFilter.value)
    && (!search || [conn.name, conn.host].some(value => value.toLocaleLowerCase().includes(search))),
  )
})
const selectedConnection = computed(() => connections.value.find(conn => conn.id === detailsID.value))
function resetFilters() {
  keyword.value = ''
  typeFilter.value = ''
  environmentFilter.value = ''
}
const connectionChecks = ref<Record<number, TestResult>>({})
const savedCheckErrors = ref<Record<number, string>>({})
const formTest = ref<TestResult | null>(null)
const formCheckError = ref('')
const testedVersions = ref<TestedVersion[]>([])
const testingSaved = ref<Record<number, boolean>>({})
const savedChecks = new AbortController()
const savedCheckVersions = new Map<number, number>()
let draftCheck: AbortController | undefined

function versionInfo(result: TestResult): VersionCompatibility {
  return result.compatibility ?? { engine: result.type, product: typeMeta[result.type as DbType]?.label || '数据库', version: '', branch: '', status: 'unknown', message: '当前服务未返回版本验证信息，兼容性尚未确认。' }
}
function compatibilityText(result: TestResult) {
  if (!result.ok) return result.error || '连接测试失败'
  const info = versionInfo(result)
  const label = { verified: '版本已验证', branch_verified: '同分支已验证，当前补丁待验证', unverified: '兼容性未验证', unknown: '版本待确认' }[info.status]
  return `${info.product || result.type} ${info.version || '版本未知'} · ${label}`
}
function compatibilityClass(result: TestResult) {
  if (!result.ok) return 'text-danger'
  return versionInfo(result).status === 'verified' ? 'text-success' : 'text-warning'
}
function checkTime(value?: string) {
  const date = value ? new Date(value) : undefined
  return date && Number.isFinite(date.getTime()) ? date.toLocaleString('zh-CN', { hour12: false }) : ''
}

const typeMeta: Record<DbType, { label: string }> = {
  mysql: { label: 'MySQL' },
  postgres: { label: 'PostgreSQL' },
  redis: { label: 'Redis' },
  jdbc: { label: '自定义 JDBC' },
}

const envMeta: Record<EnvKind, { label: string; cls: string }> = {
  dev: { label: '开发', cls: 'bg-accent-soft text-accent' },
  test: { label: '测试', cls: 'bg-warning-soft text-warning' },
  prod: { label: '生产', cls: 'bg-danger-soft text-danger' },
}

async function fetchConnections() {
  if (savedChecks.signal.aborted) return
  const current = ++listVersion
  connLoading.value = true
  connError.value = ''
  const checkVersions = new Map(savedCheckVersions)
  try {
    const res = await connectionApi.list(undefined, { signal: savedChecks.signal, silentError: true })
    if (savedChecks.signal.aborted || current !== listVersion) return
    const nextChecks: Record<number, TestResult> = {}
    const nextErrors: Record<number, string> = {}
    for (const item of res.items) {
      const sameConfig = connections.value.some(current => current.id === item.id && current.updated_at === item.updated_at)
      const keepLocal = sameConfig && (testingSaved.value[item.id] || checkVersions.get(item.id) !== savedCheckVersions.get(item.id))
      const check = keepLocal ? connectionChecks.value[item.id] : item.last_check
      if (check) nextChecks[item.id] = check
      const localError = keepLocal ? savedCheckErrors.value[item.id] : undefined
      if (localError) nextErrors[item.id] = localError
    }
    connections.value = res.items
    connectionChecks.value = nextChecks
    savedCheckErrors.value = nextErrors
  } catch (error) {
    if (savedChecks.signal.aborted || current !== listVersion) return
    connError.value = error instanceof Error ? error.message : '请重试读取个人连接。'
  } finally {
    if (!savedChecks.signal.aborted && current === listVersion) connLoading.value = false
  }
}
let listVersion = 0
async function fetchProxies() {
  try {
    const res = await proxyApi.list()
    proxies.value = res.items
  } catch {
    /* 忽略 */
  }
}
onMounted(() => {
  fetchConnections()
})

function loadFormOptions() {
  if (canManageProxy.value) void fetchProxies()
  void connectorApi.templates().then(result => { templates.value = result.items }).catch(() => {})
  void connectionApi.compatibility().then(result => { testedVersions.value = result.items }).catch(() => {})
}
function runRowAction(action: string, conn: ConnectionItem) {
  if (action === 'tasks' && canViewTasks.value) {
    taskTrigger = document.getElementById(`${id}-more-${conn.id}`)
    taskConnection.value = conn
    tasksOpen.value = true
  } else if (action === 'edit' && canEdit(conn)) {
    openEdit(conn)
  } else if (action === 'publish' && canPublish(conn)) {
    void publishDefinition(conn)
  } else if (action === 'remove' && canEdit(conn)) {
    void remove(conn)
  }
}
function closeTasks() {
  taskConnection.value = undefined
  taskTrigger?.focus({ preventScroll: true })
  taskTrigger = null
}

// ---------- 连接表单 ----------
interface ConnForm extends ConnectionPayload {
  id: number | null
}
const emptyConnForm = (): ConnForm => ({
  id: null,
  name: '',
  type: 'mysql',
  host: '',
  port: 3306,
  database: '',
  username: '',
  password: '',
  ssl_mode: 'verify-full',
  connection_timeout: 10,
  color_label: '',
  environment: 'dev',
  ssh_tunnel_id: null,
  proxy_id: null,
  template_revision_id: null,
})
const connDialog = ref(false)
const advancedOpen = ref(false)
const proxyDialog = ref(false)
const connForm = reactive<ConnForm>(emptyConnForm())
const passwordInput = ref<{focus: () => void}>()
const focusPasswordOnOpen = ref(false)
function clearFormSecrets() {
  connForm.password = ''
  clearDraftCheck()
  focusPasswordOnOpen.value = false
  if (!connForm.id) addButton.value?.focus()
}
function openCredentials(c: ConnectionItem) { openEdit(c); focusPasswordOnOpen.value = true }
function focusRecoveryPassword() { if (focusPasswordOnOpen.value) passwordInput.value?.focus(); focusPasswordOnOpen.value = false }
const publicTargetLocked = computed(() => connections.value.find(c => c.id === connForm.id)?.definition?.visibility === 'global')
const saving = ref(false)
const testing = ref(false)
const selectedTemplate = computed(() => templates.value.find(t => t.id === connForm.template_revision_id))
const versionHint = computed(() => {
  const branches = testedVersions.value.filter(item => item.engine === connForm.type && item.tested_version).map(item => item.branch)
  return branches.length ? `已验证分支：${branches.join(' / ')}。测试连接可确认当前服务端版本。` : '测试连接后显示版本识别结果；其他版本的兼容性需要单独确认。'
})
function clearDraftCheck() {
  draftCheck?.abort()
  draftCheck = undefined
  testing.value = false
  formTest.value = null
  formCheckError.value = ''
}
watch(() => JSON.stringify({ id: connForm.id, payload: payloadOf() }), clearDraftCheck, { flush: 'sync' })
watch(connDialog, open => { if (!open) clearDraftCheck() }, { flush: 'sync' })

function onTypeChange(type: DbType) {
  connForm.port = { mysql: 3306, postgres: 5432, redis: 6379, jdbc: 0 }[type]
  connForm.template_revision_id = null
  for (const key of Object.keys(jdbcParameters)) delete jdbcParameters[key]
  if (type === 'jdbc') { connForm.proxy_id = null; connForm.ssh_tunnel_id = null }
}
function onTemplateChange() {
  for (const key of Object.keys(jdbcParameters)) delete jdbcParameters[key]
  for (const field of selectedTemplate.value?.fields || []) jdbcParameters[field.name] = field.default || ''
  if (selectedTemplate.value && !['org.opengauss.Driver', 'org.postgresql.Driver','com.mysql.cj.jdbc.Driver'].includes(selectedTemplate.value.driver.class_name)) connForm.ssl_mode = 'disable'
}
function openCreate() {
  addOpen.value = false
  focusPasswordOnOpen.value = false
  formTest.value = null
  Object.assign(connForm, emptyConnForm())
  for (const key of Object.keys(jdbcParameters)) delete jdbcParameters[key]
  advancedOpen.value = false
  connDialog.value = true
  loadFormOptions()
}
function openEdit(c: ConnectionItem) {
  focusPasswordOnOpen.value = false
  formTest.value = null
  Object.assign(connForm, {
    id: c.id,
    name: c.name,
    type: c.type,
    host: c.host,
    port: c.port,
    database: c.database,
    username: c.username,
    password: '',
    ssl_mode: c.ssl_mode || 'verify-full',
    connection_timeout: c.connection_timeout || 10,
    color_label: c.color_label || '',
    environment: c.environment || 'dev',
    ssh_tunnel_id: c.ssh_tunnel_id,
    proxy_id: c.proxy_id ?? null,
    template_revision_id: c.template_revision_id ?? null,
  })
  for (const key of Object.keys(jdbcParameters)) delete jdbcParameters[key]
  Object.assign(jdbcParameters, c.parameters || {})
  advancedOpen.value = false
  connDialog.value = true
  loadFormOptions()
}

function payloadOf(): ConnectionPayload {
  return {
    name: connForm.name.trim(),
    type: connForm.type,
    host: connForm.type === 'jdbc' ? jdbcParameters.host || 'jdbc-memory' : connForm.host.trim(),
    port: connForm.type === 'jdbc' ? Number(jdbcParameters.port || 0) : Number(connForm.port),
    database: connForm.type === 'jdbc' ? jdbcParameters.database || '' : connForm.database?.trim() || '',
    username: connForm.username?.trim() || '',
    password: connForm.password ?? '',
    ssl_mode: connForm.ssl_mode,
    connection_timeout: Number(connForm.connection_timeout) || 10,
    color_label: connForm.color_label || '',
    environment: connForm.environment || 'dev',
    ssh_tunnel_id: connForm.ssh_tunnel_id ?? null,
    proxy_id: connForm.proxy_id ?? null,
    ...(connForm.type === 'jdbc' ? { template_revision_id: connForm.template_revision_id, parameters: {...jdbcParameters} } : {}),
  }
}

async function saveConnection() {
  if (saving.value) return
  if (!connForm.name.trim() || (connForm.type !== 'jdbc' && !connForm.host.trim()) || (connForm.type === 'jdbc' && !selectedTemplate.value)) {
    ElMessage.warning('请填写数据源名称与主机')
    return
  }
  saving.value = true
  try {
    if (connForm.id) {
      await connectionApi.update(connForm.id, payloadOf())
      ElMessage.success('数据源已更新')
    } else {
      await connectionApi.create(payloadOf())
      ElMessage.success('数据源已创建')
    }
    connDialog.value = false
    await fetchConnections()
  } catch {
    /* 拦截器已提示 */
  } finally {
    saving.value = false
  }
}

async function testUnsaved() {
  if (testing.value) return
  if ((connForm.type !== 'jdbc' && !connForm.host.trim()) || (connForm.type === 'jdbc' && !selectedTemplate.value)) {
    ElMessage.warning('请先填写主机与端口')
    return
  }
  clearDraftCheck()
  const current = new AbortController()
  draftCheck = current
  testing.value = true
  const id = connForm.id
  const payload = payloadOf()
  const options = { signal: current.signal, silentError: true }
  try {
    const result = id ? await connectionApi.testSaved(id, payload, options) : await connectionApi.test(payload, options)
    if (!current.signal.aborted) formTest.value = result
  } catch (error) {
    if (!current.signal.aborted) {
      const result = error instanceof ApiRequestError ? error.data as TestResult | undefined : undefined
      if (result?.ok === false) formTest.value = result
      else formCheckError.value = error instanceof ApiRequestError ? error.message : '未能取得连接检查回执，请稍后手动测试。'
    }
  } finally {
    if (!current.signal.aborted) {
      draftCheck = undefined
      testing.value = false
    }
  }
}

async function testSaved(c: ConnectionItem) {
  if (testingSaved.value[c.id]) return
  testingSaved.value[c.id] = true
  savedCheckVersions.set(c.id, (savedCheckVersions.get(c.id) ?? 0) + 1)
  delete connectionChecks.value[c.id]
  delete savedCheckErrors.value[c.id]
  const currentConnection = () => savedChecks.signal.aborted
    ? undefined
    : connections.value.find(item => item.id === c.id && item.updated_at === c.updated_at)
  function record(result: TestResult) {
    const current = currentConnection()
    if (!current) return
    if (result.recorded !== true) {
      savedCheckErrors.value[c.id] = '服务端未确认保存本次检查结果，请刷新连接后核对。'
      return
    }
    connectionChecks.value[c.id] = result
    current.last_check = result
  }
  try {
    record(await connectionApi.testSaved(c.id, undefined, { signal: savedChecks.signal, silentError: true }))
  } catch (error) {
    if (!currentConnection()) return
    const result = error instanceof ApiRequestError ? error.data as TestResult | undefined : undefined
    if (result?.ok === false) record(result)
    else savedCheckErrors.value[c.id] = error instanceof ApiRequestError ? error.message : '未能取得连接检查回执，请稍后手动测试。'
  } finally {
    savedCheckVersions.set(c.id, (savedCheckVersions.get(c.id) ?? 0) + 1)
    delete testingSaved.value[c.id]
  }
}

async function remove(c: ConnectionItem) {
  try {
    await ElMessageBox.confirm(`确认移除我的连接「${c.name}」？本人的关联历史会清理，公共数据源定义会保留。`, '删除确认', {
      type: 'warning',
      confirmButtonText: '删除',
      cancelButtonText: '取消',
    })
  } catch {
    return
  }
  await connectionApi.remove(c.id)
  ElMessage.success('已删除')
  fetchConnections()
}

function goQuery(c: ConnectionItem) {
  router.push({ path: '/query', query: { connection: String(c.id) } })
}

// ---------- 弹窗宽度自适应 ----------
const viewportWidth = ref(window.innerWidth)
const dialogWidth = computed(() => (viewportWidth.value < 640 ? '92vw' : '600px'))
function onResize() {
  viewportWidth.value = window.innerWidth
}
onMounted(() => window.addEventListener('resize', onResize))
onBeforeUnmount(() => {
  clearDraftCheck()
  savedChecks.abort()
  window.removeEventListener('resize', onResize)
})
</script>

<template>
  <div class="ui-page-stack">
    <PageHeader :title="pageTitles.connections">
      <template #actions>
        <button v-if="canManage" ref="addButton" type="button" class="liquid-button inline-flex items-center gap-2 ui-control-target" @click="openAdd">
          <Plus class="size-4" aria-hidden="true" />添加连接
        </button>
      </template>
    </PageHeader>

    <div class="ui-filter-bar" role="search" aria-label="筛选我的连接">
      <div class="ui-filter-field ui-filter-search">
        <label :for="id + '-keyword'" class="text-sm text-muted">搜索连接</label>
        <input :id="id + '-keyword'" v-model="keyword" type="search" class="glass-input" placeholder="搜索连接名称或主机" />
      </div>
      <div class="ui-filter-field">
        <label :for="id + '-type'" class="text-sm text-muted">数据库类型</label>
        <select :id="id + '-type'" v-model="typeFilter" class="glass-input">
          <option value="">全部类型</option>
          <option v-for="(meta, kind) in typeMeta" :key="kind" :value="kind">{{ meta.label }}</option>
        </select>
      </div>
      <div class="ui-filter-field">
        <label :for="id + '-environment'" class="text-sm text-muted">环境</label>
        <select :id="id + '-environment'" v-model="environmentFilter" class="glass-input">
          <option value="">全部环境</option>
          <option v-for="(meta, environment) in envMeta" :key="environment" :value="environment">{{ meta.label }}</option>
        </select>
      </div>
    </div>

    <section class="min-h-32 min-w-0" aria-label="我的连接列表" :aria-busy="connLoading">
      <LoadingState v-if="connLoading" label="正在读取个人连接…" />
      <PageState v-else-if="connError" title="无法读取个人连接" :description="connError" tone="danger">
        <template #actions><button type="button" class="ghost-button ui-control-target" @click="fetchConnections">重试</button></template>
      </PageState>
      <PageState v-else-if="!filteredConnections.length" :title="hasFilters ? '没有匹配的连接' : '暂无个人连接'"
        :description="hasFilters ? '请调整搜索词或筛选条件。' : canManage ? '通过页头的添加连接开始。' : '当前工作空间没有你的连接。'">
        <template v-if="hasFilters" #actions>
          <button type="button" class="ghost-button ui-control-target" @click="resetFilters">清除筛选</button>
        </template>
      </PageState>
      <div v-else class="editorial-table-wrap">
        <table class="editorial-table connections-table" aria-label="我的连接">
          <thead><tr>
            <th scope="col">连接名称</th><th scope="col">连接类型</th><th scope="col">环境</th>
            <th scope="col">最近检查</th><th scope="col">操作</th>
          </tr></thead>
          <tbody>
            <tr v-for="conn in filteredConnections" :key="conn.id">
              <th scope="row" class="connection-name">
                <button type="button" class="connection-name-button ui-control-target"
                  @click="detailsID = conn.id; detailsOpen = true">
                  <Database class="size-4 shrink-0 text-accent" aria-hidden="true" />{{ conn.name }}
                </button>
                <p class="editorial-table__meta">{{ conn.host }}<template v-if="conn.port">:{{ conn.port }}</template><template v-if="conn.database"> / {{ conn.database }}</template></p>
              </th>
              <td><span class="connection-field-label" aria-hidden="true">连接类型</span><span class="[overflow-wrap:anywhere]">{{ sourceLabel(conn) }}</span></td>
              <td>
                <span class="connection-field-label" aria-hidden="true">环境</span>
                <span class="rounded px-2 py-0.5 text-xs" :class="(envMeta[conn.environment] || envMeta.dev).cls">{{ (envMeta[conn.environment] || envMeta.dev).label }}</span>
              </td>
              <td>
                <span class="connection-field-label" aria-hidden="true">最近检查</span>
                <div role="status" class="text-sm">
                  <span v-if="testingSaved[conn.id]" class="text-muted">检查中…</span>
                  <button v-else-if="savedCheckErrors[conn.id] || connectionChecks[conn.id]?.ok === false"
                    type="button" class="connection-name-button text-danger ui-control-target"
                    @click="detailsID = conn.id; detailsOpen = true">{{ savedCheckErrors[conn.id] ? '未取得回执' : '检查失败' }}</button>
                  <span v-else-if="connectionChecks[conn.id]?.ok" class="text-success">检查通过</span>
                  <span v-else class="text-muted">未检查</span>
                  <p v-if="!testingSaved[conn.id] && !savedCheckErrors[conn.id] && checkTime(connectionChecks[conn.id]?.checked_at)"
                    class="editorial-table__meta">{{ checkTime(connectionChecks[conn.id]?.checked_at) }}</p>
                </div>
              </td>
              <td class="connection-actions-cell">
                <div class="flex flex-wrap items-center gap-2">
                  <button v-if="canUse" type="button" class="ghost-button inline-flex items-center gap-1 ui-control-target"
                    :aria-label="'打开工作台 ' + conn.name" @click="goQuery(conn)">
                    <SquareTerminal class="size-4" aria-hidden="true" />打开工作台
                  </button>
                  <button v-if="canUse" type="button" class="ghost-button ui-control-target aria-disabled:opacity-50"
                    :aria-label="'测试连接 ' + conn.name" :aria-disabled="testingSaved[conn.id] || undefined"
                    :aria-busy="!!testingSaved[conn.id]" @click="testSaved(conn)">测试</button>
                  <el-dropdown v-if="canViewTasks || canEdit(conn) || canPublish(conn)" trigger="click" @command="runRowAction($event, conn)">
                    <button :id="id + '-more-' + conn.id" type="button" class="ghost-button ui-control-target ui-icon-button"
                      :aria-label="'更多操作 ' + conn.name">
                      <Ellipsis class="size-4" aria-hidden="true" />
                    </button>
                    <template #dropdown>
                      <el-dropdown-menu>
                        <el-dropdown-item v-if="canViewTasks" command="tasks">任务与计划</el-dropdown-item>
                        <el-dropdown-item v-if="canEdit(conn)" command="edit">编辑连接</el-dropdown-item>
                        <el-dropdown-item v-if="canPublish(conn)" command="publish" :disabled="publishing">发布数据源定义</el-dropdown-item>
                        <el-dropdown-item v-if="canEdit(conn)" command="remove" divided>删除连接</el-dropdown-item>
                      </el-dropdown-menu>
                    </template>
                  </el-dropdown>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </section>
    <p v-if="!connLoading && !connError && filteredConnections.length" class="text-sm text-muted">共 {{ filteredConnections.length }} 条连接</p>

    <el-dialog v-model="addOpen" :title="addStep === 'choice' ? '添加连接' : '选择共享数据源'"
      width="min(92vw, 640px)" class="glass-dialog" destroy-on-close :close-on-click-modal="false"
      @closed="!connDialog && addButton?.focus()">
      <div v-if="addStep === 'choice'" class="grid gap-3">
        <button type="button" class="ghost-button connection-entry-button ui-control-target" @click="addStep = 'shared'">
          <Library class="size-5 shrink-0 text-accent" aria-hidden="true" />
          <span><strong class="block">从共享数据源添加</strong><span class="block text-sm text-muted">选择已发布的连接目标，绑定自己的数据库账号。</span></span>
        </button>
        <button type="button" class="ghost-button connection-entry-button ui-control-target" @click="openCreate">
          <Settings2 class="size-5 shrink-0 text-accent" aria-hidden="true" />
          <span><strong class="block">手动配置连接</strong><span class="block text-sm text-muted">填写数据库地址、类型和账号。</span></span>
        </button>
      </div>
      <template v-else>
        <SourceCatalog shared-picker @added="addedFromCatalog" />
        <button type="button" class="ghost-button mt-4 ui-control-target" @click="addStep = 'choice'">返回添加连接</button>
      </template>
    </el-dialog>

    <el-dialog v-model="detailsOpen" :title="selectedConnection?.name || '连接详情'"
      width="min(92vw, 640px)" class="glass-dialog" destroy-on-close>
      <template v-if="selectedConnection">
        <dl class="connection-facts">
          <dt>连接类型</dt><dd>{{ sourceLabel(selectedConnection) }}</dd>
          <dt>环境</dt><dd>{{ (envMeta[selectedConnection.environment] || envMeta.dev).label }}</dd>
          <dt>连接目标</dt><dd>{{ selectedConnection.host }}<template v-if="selectedConnection.port">:{{ selectedConnection.port }}</template></dd>
          <dt>默认数据库</dt><dd>{{ selectedConnection.database || '未设置' }}</dd>
          <dt>数据库账号</dt><dd>{{ selectedConnection.username || '未设置' }}</dd>
          <dt>连接来源</dt><dd>{{ selectedConnection.definition?.visibility === 'global' ? '共享数据源 · 修订 ' + selectedConnection.definition.revision : '个人连接' }}</dd>
          <dt>代理</dt><dd>{{ selectedConnection.proxy_name || selectedConnection.tunnel_name || '不使用代理' }}</dd>
        </dl>
        <p v-if="selectedConnection.capabilities?.reason" class="mt-4 text-sm text-warning">{{ selectedConnection.capabilities.reason }}</p>
        <section class="mt-4 border-t border-divider pt-3 space-y-2" aria-label="最近检查详情">
          <h3 class="font-semibold">最近检查</h3>
          <p v-if="testingSaved[selectedConnection.id]" role="status" class="text-sm text-muted">正在检查连接…</p>
          <ConnectionDiagnosis v-else-if="savedCheckErrors[selectedConnection.id] || connectionChecks[selectedConnection.id]?.ok === false"
            :result="savedCheckErrors[selectedConnection.id] ? undefined : connectionChecks[selectedConnection.id]"
            :error="savedCheckErrors[selectedConnection.id]" :can-update-credentials="canEdit(selectedConnection)" @recover="openCredentials(selectedConnection)" />
          <p v-else-if="connectionChecks[selectedConnection.id]?.ok" role="status" class="text-sm" :class="compatibilityClass(connectionChecks[selectedConnection.id]!)">
            {{ compatibilityText(connectionChecks[selectedConnection.id]!) }}
            <span v-if="checkTime(connectionChecks[selectedConnection.id]?.checked_at)" class="block text-muted">
              {{ checkTime(connectionChecks[selectedConnection.id]?.checked_at) }} · {{ connectionChecks[selectedConnection.id]?.elapsed_ms }} ms
            </span>
          </p>
          <p v-else class="text-sm text-muted">此连接尚无检查记录。</p>
        </section>
      </template>
      <PageState v-else title="此连接已不可用" description="关闭详情后可查看当前连接列表。" />
    </el-dialog>

    <el-dialog v-model="tasksOpen" :title="(taskConnection?.name || '连接') + ' · 任务与计划'"
      width="min(94vw, 1040px)" class="glass-dialog" destroy-on-close @closed="closeTasks">
      <SourceTasksView v-if="taskConnection && canViewTasks" :key="taskConnection.id" embedded :scoped-connection="taskConnection" />
    </el-dialog>

    <!-- 连接弹窗 -->
    <el-dialog
      v-model="connDialog"
      :title="connForm.id ? '编辑连接' : '手动配置连接'"
      :width="dialogWidth"
      :close-on-click-modal="false"
      class="glass-dialog"
      @closed="clearFormSecrets"
      @opened="focusRecoveryPassword"
    >
      <el-form label-position="top" class="space-y-1">
        <p class="text-xs text-muted mb-3">基本信息</p>
        <el-form-item label="数据源名称">
          <el-input v-model="connForm.name" placeholder="例如：生产环境-订单库" />
        </el-form-item>
        <p v-if="publicTargetLocked" class="text-xs text-muted mb-3">此连接使用共享数据源的地址和驱动。切换修订时，从「添加连接」选择「从共享数据源添加」。</p>
        <el-form-item v-if="!publicTargetLocked" label="数据源类型">
          <el-select v-model="connForm.type" class="w-full" popper-class="glass-popper" @change="onTypeChange">
            <el-option label="MySQL" value="mysql" />
            <el-option label="PostgreSQL" value="postgres" />
            <el-option label="Redis" value="redis" />
            <el-option label="自定义 JDBC" value="jdbc" />
          </el-select>
        </el-form-item>
        <p v-if="!publicTargetLocked" class="text-xs text-muted leading-relaxed" data-testid="version-support-hint">{{ versionHint }}</p>

        <template v-if="!publicTargetLocked && connForm.type === 'jdbc'">
          <el-form-item label="数据源模板">
            <el-select v-model="connForm.template_revision_id" class="w-full" aria-label="数据源模板" placeholder="选择已发布模板" @change="onTemplateChange">
              <el-option v-for="t in templates" :key="t.id" :value="t.id" :label="`${t.name} · 修订 ${t.revision}`" :disabled="!t.enabled || !t.driver.enabled" />
            </el-select>
          </el-form-item>
          <p v-if="!templates.length" class="text-xs text-warning">暂无已发布模板。<RouterLink v-if="featureAllowed(userStore, 'source.driver.manage', userStore.hasCapability('datasource_manage'))" :to="{ path: '/sources', query: { tab: 'drivers', workspace_id: userStore.workspaceId } }" class="underline">前往数据管理配置驱动与模板</RouterLink><span v-else>请联系管理员配置驱动与数据源模板。</span></p>
          <p v-if="selectedTemplate" class="text-xs text-muted">{{ selectedTemplate.product }} · {{ selectedTemplate.driver.name }} {{ selectedTemplate.driver.version }} · {{ selectedTemplate.dialect ? 'SQL 与结构浏览' : '仅结构浏览' }}</p>
          <el-form-item v-for="field in selectedTemplate?.fields || []" :key="field.name" :label="field.label">
            <el-input v-model="jdbcParameters[field.name]" :type="field.type === 'integer' ? 'number' : 'text'" :placeholder="field.default || field.label" />
          </el-form-item>
        </template>

        <p class="text-xs text-muted mb-3 mt-4">连接信息</p>
        <template v-if="!publicTargetLocked && connForm.type !== 'jdbc'">
        <div class="grid grid-cols-1 sm:grid-cols-3 gap-3">
          <el-form-item label="主机" class="sm:col-span-2">
            <el-input v-model="connForm.host" placeholder="192.168.1.10" />
          </el-form-item>
          <el-form-item label="端口">
            <el-input v-model.number="connForm.port" type="number" :min="1" :max="65535" />
          </el-form-item>
        </div>
        <el-form-item :label="connForm.type === 'redis' ? '数据库编号（0-15，可选）' : '默认数据库（可选）'">
          <el-input v-model="connForm.database" :placeholder="connForm.type === 'redis' ? '0' : '业务库名'" />
        </el-form-item>
        </template>
        <div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
          <el-form-item :label="connForm.type === 'redis' ? 'ACL 用户名（可选）' : '用户名'">
            <el-input v-model="connForm.username" />
          </el-form-item>
          <el-form-item label="密码">
            <el-input
              ref="passwordInput"
              v-model="connForm.password"
              autocomplete="new-password"
              type="password"
              show-password
              :placeholder="connForm.id && !connForm.password ? '已保存，留空表示不修改' : 'AES-256-GCM 加密存储'"
            />
            <p v-if="connForm.id" class="text-xs text-subtle mt-1">留空使用已保存口令；修改连接目标、JDBC 模板或参数、账号、TLS 或代理后，测试需重新输入口令。</p>
          </el-form-item>
        </div>
        <el-form-item v-if="!publicTargetLocked" label="环境">
          <el-select v-model="connForm.environment" class="w-full" popper-class="glass-popper">
            <el-option label="开发" value="dev" />
            <el-option label="测试" value="test" />
            <el-option label="生产" value="prod" />
          </el-select>
        </el-form-item>
        <div v-if="!publicTargetLocked" class="border-t border-divider pt-2 mb-3">
          <button type="button" class="ghost-button ui-control-target"
            :aria-expanded="advancedOpen" :aria-controls="id + '-advanced'"
            @click="advancedOpen = !advancedOpen">高级设置</button>
          <div v-show="advancedOpen" :id="id + '-advanced'" class="pt-3">
            <div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
          <el-form-item label="SSL 模式">
            <el-select v-model="connForm.ssl_mode" class="w-full" popper-class="glass-popper">
              <el-option label="禁用（内网明文）" value="disable" />
              <el-option label="加密并验证证书" value="verify-full" :disabled="selectedTemplate && !['org.opengauss.Driver', 'org.postgresql.Driver','com.mysql.cj.jdbc.Driver'].includes(selectedTemplate.driver.class_name)" />
            </el-select>
          </el-form-item>
          <el-form-item label="连接超时（秒）">
            <el-input v-model.number="connForm.connection_timeout" type="number" :min="1" :max="30" />
          </el-form-item>
            </div>
        <el-form-item v-if="connForm.type !== 'jdbc' && canManageProxy" label="代理（可选）">
          <el-select v-model="connForm.proxy_id" class="w-full" popper-class="glass-popper" clearable placeholder="不使用代理">
            <el-option label="不使用代理" :value="null" />
            <el-option v-for="p in proxies" :key="p.id" :label="`${p.name}（${p.type} ${p.host}:${p.port}）`" :value="p.id" />
          </el-select>
          <button type="button" class="ghost-button mt-2 ui-control-target" @click="proxyDialog = true">管理代理</button>
        </el-form-item>
          </div>
        </div>
        <p v-if="formTest?.ok" role="status" class="text-xs leading-relaxed" :class="compatibilityClass(formTest)" data-testid="connection-version-result">
          {{ compatibilityText(formTest) }}
          。{{ versionInfo(formTest).message }}
          <span v-if="versionInfo(formTest).tested_version && versionInfo(formTest).status === 'branch_verified'">实测版本 {{ versionInfo(formTest).tested_version }}。</span>
        </p>
        <ConnectionDiagnosis :result="formTest" :error="formCheckError" />
      </el-form>

      <template #footer>
        <div class="flex flex-wrap items-center justify-between gap-2">
          <button type="button" class="ghost-button flex items-center gap-2 aria-disabled:opacity-50 ui-control-target" :aria-disabled="testing || undefined" @click="testUnsaved">
            <Zap class="w-4 h-4" /> {{ testing ? '测试中…' : '测试连接' }}
          </button>
          <div class="flex gap-2 ml-auto">
            <button class="ghost-button ui-control-target" @click="connDialog = false">取消</button>
            <button class="liquid-button ui-control-target" :disabled="saving" @click="saveConnection">{{ saving ? '保存中…' : '保存' }}</button>
          </div>
        </div>
      </template>
    </el-dialog>
    <el-dialog v-model="proxyDialog" title="连接代理" width="min(94vw, 1040px)" class="glass-dialog"
      destroy-on-close :close-on-click-modal="false" @closed="fetchProxies">
      <ProxyView v-if="proxyDialog && canManageProxy" embedded />
    </el-dialog>
  </div>
</template>

<style scoped>
.connection-name { min-width: 180px; }
.editorial-table .connection-name { white-space: normal; }
.connection-name-button {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  max-width: 100%;
  text-align: left;
  overflow-wrap: anywhere;
}
.connection-name-button:hover { text-decoration: underline; text-underline-offset: 3px; }
.connection-entry-button {
  display: flex;
  align-items: flex-start;
  justify-content: flex-start;
  gap: 12px;
  padding: 12px;
  text-align: left;
  white-space: normal;
}
.connection-field-label { display: none; }
.connection-facts {
  display: grid;
  grid-template-columns: max-content minmax(0, 1fr);
  gap: 8px 16px;
  overflow-wrap: anywhere;
}
.connection-facts dt { color: var(--db-text-secondary); }
@media (max-width: 736px) {
  .connections-table { display: block; }
  .connections-table thead { position: absolute; width: 1px; height: 1px; overflow: hidden; clip-path: inset(50%); }
  .connections-table tbody, .connections-table tr { display: block; }
  .connections-table tr { padding: 12px 16px; border-bottom: 1px solid var(--db-divider); }
  .connections-table tr:last-child { border-bottom: 0; }
  .connections-table :is(th, td) { border: 0; padding: 3px 0; }
  .connections-table th { display: block; min-width: 0; }
  .connections-table td { display: grid; grid-template-columns: 5rem minmax(0, 1fr); align-items: start; }
  .connections-table .connection-actions-cell { display: block; margin-top: 6px; }
  .connection-field-label { display: block; color: var(--db-text-secondary); font-size: 12px; }
  .connections-table td > :is(span, div) { justify-self: start; }
}
</style>
