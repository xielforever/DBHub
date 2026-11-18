<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  Database,
  Network,
  Pencil,
  Plus,
  Search,
  Server,
  SquareTerminal,
  Trash2,
  Waypoints,
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
import { pageTitles } from '../../navigation'
import { ApiRequestError } from '../../utils/request'
import { connectorApi, type DatasourceTemplate } from '../../api/connector'
import SourceCatalog from './SourceCatalog.vue'
import { sourceCatalogApi } from '../../api/source-catalog'
import ConnectionDiagnosis from './ConnectionDiagnosis.vue'

defineProps<{ embedded?: boolean }>()

const router = useRouter()
const userStore = useUserStore()
const canEdit = (c: ConnectionItem) => userStore.user?.id === c.user_id
const activeTab = ref<'mine'|'catalog'>('mine')
const publishing = ref(false)
async function addedFromCatalog() { activeTab.value = 'mine'; await fetchConnections() }
async function publishDefinition(c: ConnectionItem) {
 if (publishing.value) return
 try { await ElMessageBox.confirm(`发布「${c.name}」的地址、库名和驱动配置到当前工作空间目录？使用者需要填写自己的数据库账号。`, '发布数据源定义', { type:'info',confirmButtonText:'发布定义' }) } catch { return }
 publishing.value = true
 try { await sourceCatalogApi.publish(c.id,'global'); ElMessage.success('数据源定义已发布'); activeTab.value='catalog' } catch { /* API displays failure */ } finally { publishing.value=false }
}
const templates = ref<DatasourceTemplate[]>([])
const jdbcParameters = reactive<Record<string,string>>({})

const keyword = ref('')
const typeFilter = ref('all')
const connections = ref<ConnectionItem[]>([])
const proxies = ref<ProxyItem[]>([])
const connLoading = ref(false)
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

const typeMeta: Record<DbType, { label: string; color: string; tint: string; icon: unknown }> = {
  mysql: { label: 'MySQL', color: 'var(--db-chart-1)', tint: 'color-mix(in srgb, var(--db-chart-1) 16%, transparent)', icon: Database },
  postgres: { label: 'PostgreSQL', color: 'var(--db-chart-2)', tint: 'color-mix(in srgb, var(--db-chart-2) 16%, transparent)', icon: Database },
  redis: { label: 'Redis', color: 'var(--db-chart-5)', tint: 'color-mix(in srgb, var(--db-chart-5) 14%, transparent)', icon: Server },
  jdbc: { label: '自定义 JDBC', color: 'var(--db-chart-3)', tint: 'color-mix(in srgb, var(--db-chart-3) 14%, transparent)', icon: Database },
}

const envMeta: Record<EnvKind, { label: string; cls: string }> = {
  dev: { label: '开发', cls: 'bg-accent-soft text-accent' },
  test: { label: '测试', cls: 'bg-warning-soft text-warning' },
  prod: { label: '生产', cls: 'bg-danger-soft text-danger' },
}

async function fetchConnections() {
  connLoading.value = true
  const checkVersions = new Map(savedCheckVersions)
  try {
    const res = await connectionApi.list({
      type: typeFilter.value === 'all' ? '' : typeFilter.value,
      keyword: keyword.value,
    })
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
  } catch {
    /* 拦截器已统一提示 */
  } finally {
    connLoading.value = false
  }
}
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
  fetchProxies()
  void connectorApi.templates().then(result => { templates.value = result.items }).catch(() => {})
  void connectionApi.compatibility().then(result => { testedVersions.value = result.items }).catch(() => { /* 拦截器已提示 */ })
})

function goProxies() {
  router.push('/proxies')
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
const connForm = reactive<ConnForm>(emptyConnForm())
const passwordInput = ref<{focus: () => void}>()
const focusPasswordOnOpen = ref(false)
function clearFormSecrets() { connForm.password = ''; clearDraftCheck(); focusPasswordOnOpen.value = false }
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
  focusPasswordOnOpen.value = false
  activeTab.value = 'mine'
  formTest.value = null
  Object.assign(connForm, emptyConnForm())
  for (const key of Object.keys(jdbcParameters)) delete jdbcParameters[key]
  connDialog.value = true
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
    proxy_id: (c as any).proxy_id ?? null,
    template_revision_id: c.template_revision_id ?? null,
  })
  for (const key of Object.keys(jdbcParameters)) delete jdbcParameters[key]
  Object.assign(jdbcParameters, c.parameters || {})
  connDialog.value = true
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
    proxy_id: (connForm as any).proxy_id ?? null,
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
    <PageHeader :title="embedded ? '个人连接' : pageTitles.connections" :heading="embedded ? 'h2' : 'h1'"
      description="从目录添加数据源，使用自己的数据库账号连接。也可以创建个人数据源。">
      <template #actions>
        <RouterLink to="/source-tasks" class="ghost-button text-sm inline-flex items-center ui-control-target">巡检与采集任务</RouterLink>
        <button class="liquid-button inline-flex items-center justify-center gap-2 ui-control-target" @click="openCreate">
          <Plus class="w-4 h-4" aria-hidden="true" />新建个人数据源
        </button>
      </template>
    </PageHeader>

    <div class="segmented-control" role="tablist" aria-label="数据源范围">
      <button id="my-connections-tab" role="tab" :aria-selected="activeTab === 'mine'" :class="activeTab === 'mine' ? 'liquid-button' : 'ghost-button'" @click="activeTab = 'mine'">我的连接</button>
      <button id="source-catalog-tab" role="tab" :aria-selected="activeTab === 'catalog'" :class="activeTab === 'catalog' ? 'liquid-button' : 'ghost-button'" @click="activeTab = 'catalog'">数据源目录</button>
    </div>
    <SourceCatalog v-if="activeTab === 'catalog'" @added="addedFromCatalog" />
    <div v-if="activeTab === 'mine'" class="flex flex-wrap items-center gap-3">
      <div class="relative w-full sm:w-72 shrink-0">
        <Search class="w-4 h-4 text-subtle absolute left-3.5 top-1/2 -translate-y-1/2" />
        <input
          v-model.trim="keyword"
          aria-label="搜索数据源名称或主机"
          class="glass-input pl-10"
          placeholder="搜索数据源名称或主机"
          @input="fetchConnections"
        />
      </div>
      <div class="w-full sm:w-44 shrink-0">
        <el-select
          v-model="typeFilter"
          aria-label="按数据库类型筛选"
          class="w-full"
          popper-class="glass-popper"
          @change="fetchConnections"
        >
          <el-option label="全部类型" value="all" />
          <el-option label="MySQL" value="mysql" />
          <el-option label="PostgreSQL" value="postgres" />
          <el-option label="Redis" value="redis" />
          <el-option label="自定义 JDBC" value="jdbc" />
        </el-select>
      </div>
      <button class="ghost-button text-xs py-1.5 px-3 flex items-center gap-1 ui-control-target" @click="goProxies">
        <Waypoints class="w-3.5 h-3.5" />连接代理
      </button>
    </div>

    <div v-if="activeTab === 'mine'" :aria-busy="connLoading" class="min-h-[160px]" role="tabpanel" aria-labelledby="my-connections-tab">
      <LoadingState v-if="connLoading" label="正在读取个人连接…" />
      <section v-else-if="connections.length" class="editorial-directory">
        <article v-for="conn in connections" :key="conn.id" class="glass-card editorial-connection-row ui-card-padding">
          <div class="flex items-start gap-3.5">
            <div class="w-11 h-11 rounded-xl flex items-center justify-center shrink-0" :style="{ background: (typeMeta[conn.type] || typeMeta.jdbc).tint }">
              <component :is="(typeMeta[conn.type] || typeMeta.jdbc).icon" class="w-5 h-5" :style="{ color: (typeMeta[conn.type] || typeMeta.jdbc).color }" />
            </div>
            <div class="min-w-0 flex-1">
              <h3 class="font-medium truncate">{{ conn.name }}</h3>
              <p class="text-xs text-muted mt-0.5 truncate">{{ conn.port ? `${conn.host}:${conn.port}` : conn.database }}</p>
            </div>
          </div>

          <div class="flex flex-wrap items-center gap-2 text-xs">
            <span class="px-2 py-0.5 rounded-full bg-surface-muted text-muted">{{ sourceLabel(conn) }}{{ conn.type === 'jdbc' ? ' · JDBC' : '' }}</span>
            <span class="px-2 py-0.5 rounded-full" :class="envMeta[conn.environment || 'dev'].cls">
              {{ envMeta[conn.environment || 'dev'].label }}
            </span>
            <span v-if="conn.database" class="px-2 py-0.5 rounded-full bg-surface-muted text-muted max-w-[180px] truncate">{{ conn.database }}</span>
            <span v-if="conn.proxy_name" class="px-2 py-0.5 rounded-full bg-accent-soft text-accent flex items-center gap-1">
              <Waypoints class="w-3 h-3" />{{ conn.proxy_name }}
            </span>
            <span v-if="conn.tunnel_name" class="px-2 py-0.5 rounded-full bg-accent-soft text-accent flex items-center gap-1 opacity-60">
              <Network class="w-3 h-3" />{{ conn.tunnel_name }}(废弃)
            </span>
            <span class="px-2 py-0.5 rounded-full" :class="conn.has_password ? 'bg-success-soft text-success' : 'bg-warning-soft text-warning'">
              {{ conn.has_password ? '凭据已保存' : '无口令' }}
            </span>
          </div>

          <p class="text-xs text-muted break-all">数据库账号：{{ conn.username || '未设置' }}<span v-if="conn.definition"> · 修订 {{ conn.definition.revision }}</span></p>
          <p v-if="conn.capabilities?.reason" class="text-xs text-warning" role="status">{{ conn.capabilities.reason }}</p>
          <p v-if="testingSaved[conn.id]" role="status" class="text-sm text-muted">正在检查连接…</p>
          <p v-else-if="!savedCheckErrors[conn.id] && connectionChecks[conn.id]?.ok"
            role="status" class="text-xs leading-relaxed" :class="compatibilityClass(connectionChecks[conn.id]!)">
            最近检查：{{ compatibilityText(connectionChecks[conn.id]!) }}
            <span v-if="checkTime(connectionChecks[conn.id]!.checked_at)" class="block text-subtle">
              {{ checkTime(connectionChecks[conn.id]!.checked_at) }} · {{ connectionChecks[conn.id]!.elapsed_ms }} ms
            </span>
          </p>
          <ConnectionDiagnosis v-if="!testingSaved[conn.id]" :result="savedCheckErrors[conn.id] ? undefined : connectionChecks[conn.id]"
            :error="savedCheckErrors[conn.id]" :can-update-credentials="canEdit(conn)" @recover="openCredentials(conn)" />

          <button v-if="userStore.hasCapability('datasource_manage') && conn.definition?.visibility !== 'global' && !conn.proxy_id && !conn.ssh_tunnel_id" class="ghost-button text-xs ui-control-target" :disabled="publishing" @click="publishDefinition(conn)">发布数据源定义</button>
          <div class="flex items-center gap-2 pt-2 border-t border-stroke">
            <button class="ghost-button flex-1 text-xs py-2 flex items-center justify-center gap-1.5 ui-control-target" @click="goQuery(conn)">
              <SquareTerminal class="w-3.5 h-3.5" />打开
            </button>
            <button
              type="button"
              class="rounded-lg flex items-center justify-center text-muted hover:bg-success-soft hover:text-success transition-colors ui-control-target ui-icon-button aria-disabled:opacity-50"
              :aria-label="`测试连接 ${conn.name}`"
              :aria-disabled="testingSaved[conn.id] || undefined"
              :aria-busy="!!testingSaved[conn.id]"
              title="测试连接"
              @click="testSaved(conn)"
            >
              <Zap class="w-4 h-4" />
            </button>
            <button
              v-if="canEdit(conn)"
              class="rounded-lg flex items-center justify-center text-muted hover:bg-interactive hover:text-content transition-colors ui-control-target ui-icon-button"
              aria-label="编辑数据源"
              title="编辑"
              @click="openEdit(conn)"
            >
              <Pencil class="w-4 h-4" />
            </button>
            <button
              v-if="canEdit(conn)"
              class="rounded-lg flex items-center justify-center text-muted hover:bg-danger-soft hover:text-danger transition-colors ui-control-target ui-icon-button"
              aria-label="删除数据源"
              title="删除"
              @click="remove(conn)"
            >
              <Trash2 class="w-4 h-4" />
            </button>
          </div>
        </article>
      </section>
      <div v-else class="glass-card p-12 flex flex-col items-center justify-center gap-3 text-center">
        <Database class="w-10 h-10 text-subtle" />
        <p class="text-muted text-sm">还没有个人连接。前往“数据源目录”添加，或新建个人数据源。</p>
      </div>
    </div>

    <!-- 连接弹窗 -->
    <el-dialog
      v-model="connDialog"
      :title="connForm.id ? '编辑数据源' : '新建数据源'"
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
        <p v-if="publicTargetLocked" class="text-xs text-muted mb-3">连接目标来自共享定义，地址和驱动不可在这里覆盖。新修订请到数据源目录确认切换。</p>
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
          <p v-if="!templates.length" class="text-xs text-warning">暂无已发布模板。<RouterLink v-if="userStore.hasCapability('datasource_manage')" to="/settings" class="underline">前往系统设置登记驱动并发布模板</RouterLink><span v-else>请联系管理员配置驱动与数据源模板。</span></p>
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
        <div v-if="!publicTargetLocked" class="grid grid-cols-1 sm:grid-cols-2 gap-3">
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
        <el-form-item v-if="!publicTargetLocked" label="所属环境（资产徽标与高危操作提示依据）">
          <el-select v-model="connForm.environment" class="w-full" popper-class="glass-popper">
            <el-option label="开发环境（dev）" value="dev" />
            <el-option label="测试环境（test）" value="test" />
            <el-option label="生产环境（prod）" value="prod" />
          </el-select>
        </el-form-item>
        <el-form-item v-if="!publicTargetLocked && connForm.type !== 'jdbc'" label="代理（可选）">
          <el-select v-model="connForm.proxy_id" class="w-full" popper-class="glass-popper" clearable placeholder="不使用代理">
            <el-option label="不使用代理" :value="null" />
            <el-option v-for="p in proxies" :key="p.id" :label="`${p.name}（${p.type} ${p.host}:${p.port}）`" :value="p.id" />
          </el-select>
          <p class="text-xs text-subtle mt-1">在连接代理创建代理后在此选用，连接测试和查询均使用该代理。</p>
        </el-form-item>
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
  </div>
</template>
