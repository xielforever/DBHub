<script setup lang="ts">
import {featureAllowed} from '../../utils/permission'
import { computed, nextTick, onMounted, onBeforeUnmount, reactive, ref, useId, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { sourceCatalogApi, type DefinitionPayload, type PersonalIdentity, type SourceDefinition } from '../../api/source-catalog'
import { connectorApi, type DatasourceTemplate } from '../../api/connector'
import { useUserStore } from '../../stores/user'
import SourceManageDialog from './SourceManageDialog.vue'
import ConnectionDiagnosis from './ConnectionDiagnosis.vue'
import LoadingState from '../../components/base/LoadingState.vue'
import PageState from '../../components/base/PageState.vue'
import { ApiRequestError } from '../../utils/request'
import type { DbType, EnvKind, TestResult } from '../../api/datasource'

const props = defineProps<{ sharedPicker?: boolean }>()
const emit = defineEmits<{ added: [] }>()
const user = useUserStore()
const canBind = computed(() => featureAllowed(user, 'connection.manage', true))
const canManage = computed(() => user.can('source.definition.manage'))
const filterId = `source-catalog-${useId()}`
const keywordInput = ref<HTMLInputElement>()
const items = ref<SourceDefinition[]>([])
const keyword = ref('')
const dbType = ref<DbType | ''>('')
const environment = ref<EnvKind | ''>('')
const status = ref<'enabled' | 'disabled' | ''>('')
const typeLabels: Record<DbType, string> = { postgres: 'PostgreSQL', mysql: 'MySQL', redis: 'Redis', jdbc: 'JDBC' }
const envLabels: Record<EnvKind, string> = { dev: '开发', test: '测试', prod: '生产' }
const hasFilters = computed(() => !!(keyword.value.trim() || dbType.value || environment.value || status.value))
const loading = ref(true)
const busy = ref(false)
const total = ref(0)
const page = ref(1)
const pageSize = 12
const loadError = ref('')
const manageOpen = ref(false)
const manageSource = ref<SourceDefinition>()
const manageInitial = ref<'profile' | 'access'>('profile')
const templates = ref<DatasourceTemplate[]>([])
let request: AbortController | undefined
let searchTimer: ReturnType<typeof setTimeout> | undefined
let manageTrigger: HTMLElement | undefined
let createTrigger: HTMLElement | undefined
const defaultDatabase = (d: SourceDefinition) => d.config.database || d.config.parameters?.database || ''
const endpoint = (d: SourceDefinition) => `${d.config.host}${d.config.port ? ':' + d.config.port : ''} / ${defaultDatabase(d)}`

watch([keyword, dbType, environment, status], () => {
  page.value = 1
  request?.abort()
  loading.value = true
  clearTimeout(searchTimer)
  searchTimer = setTimeout(refreshCatalog, 200)
}, { flush: 'sync' })

async function load(): Promise<void> {
  request?.abort()
  const current = new AbortController()
  request = current
  loading.value = true
  loadError.value = ''
  try {
    const result = await sourceCatalogApi.list({
      keyword: keyword.value.trim(), view: props.sharedPicker ? 'shared' : 'managed',
      type: dbType.value || undefined, environment: environment.value || undefined, status: status.value || undefined,
      page: page.value, page_size: pageSize,
    }, true, current.signal)
    if (current.signal.aborted) return
    const lastPage = Math.max(1, Math.ceil(result.total / pageSize))
    if (page.value > lastPage) {
      page.value = lastPage
      await load()
      return
    }
    items.value = result.items
    total.value = result.total
    const currentSource = result.items.find(item => item.family_id === manageSource.value?.family_id)
    if (currentSource) manageSource.value = currentSource
  } catch (error) {
    if (current.signal.aborted) return
    items.value = []
    total.value = 0
    loadError.value = error instanceof Error ? error.message : '请稍后重试。'
  } finally {
    if (!current.signal.aborted) {
      loading.value = false
      request = undefined
    }
  }
}
function refreshCatalog() {
  clearTimeout(searchTimer)
  void load()
}
function changePage(next: number) {
  page.value = next
  refreshCatalog()
}
function openManage(source: SourceDefinition, event: MouseEvent) {
  manageTrigger = event.currentTarget instanceof HTMLElement ? event.currentTarget : undefined
  manageSource.value = source
  manageInitial.value = 'profile'
  manageOpen.value = true
}
async function closeManage() {
  const family = manageSource.value?.family_id
  const trigger = manageTrigger
  manageSource.value = undefined
  manageTrigger = undefined
  await nextTick()
  const row = family && document.getElementById(`${filterId}-manage-${family}`)
  const focus = trigger?.isConnected ? trigger : (row || keywordInput.value)
  focus?.focus({ preventScroll: true })
}
function changedSource(update?: { id: number, enabled: boolean }) {
  if (update && manageSource.value?.id === update.id) manageSource.value = { ...manageSource.value, enabled: update.enabled }
  refreshCatalog()
}
onMounted(() => {
  refreshCatalog()
  if (props.sharedPicker) keywordInput.value?.focus()
  else void connectorApi.templates().then(r => { templates.value = r.items }).catch(() => {})
})
onBeforeUnmount(() => {
  clearTimeout(searchTimer)
  request?.abort()
  clearIdentityCheck()
})
const selected = ref<SourceDefinition>(), identityOpen = ref(false)
const identityCheck = ref<TestResult>()
const identityCheckError = ref('')
const testingIdentity = ref(false)
let identityRequest: AbortController | undefined
const emptyIdentity = (): PersonalIdentity => ({ name: '', username: '', password: '', confirmed: false })
const identity = reactive(emptyIdentity())
const identityReady = computed(() => identity.confirmed && !!identity.name.trim() && !!selected.value && (selected.value.config.type === 'redis' || !!identity.username.trim() && !!identity.password))
function clearIdentityCheck() {
  identityRequest?.abort()
  identityRequest = undefined
  testingIdentity.value = false
  identityCheck.value = undefined
  identityCheckError.value = ''
}
watch(() => JSON.stringify(identity), clearIdentityCheck, { flush: 'sync' })
watch(identityOpen, open => { if (!open) clearIdentityCheck() }, { flush: 'sync' })
function clearIdentity() { Object.assign(identity, emptyIdentity()); selected.value = undefined; clearIdentityCheck() }
function openIdentity(d: SourceDefinition) { clearIdentity(); selected.value = d; identity.name = d.name; identityOpen.value = true }
async function testIdentity() {
  if (!selected.value || busy.value || testingIdentity.value) return
  clearIdentityCheck()
  const current = new AbortController()
  identityRequest = current
  testingIdentity.value = true
  try {
    const result = await sourceCatalogApi.test(selected.value.id, { ...identity }, { signal: current.signal, silentError: true })
    if (!current.signal.aborted) identityCheck.value = result
  } catch (error) {
    if (!current.signal.aborted) {
      const result = error instanceof ApiRequestError ? error.data as TestResult | undefined : undefined
      if (result?.ok === false) identityCheck.value = result
      else identityCheckError.value = error instanceof ApiRequestError ? error.message : '未能取得连接检查回执，请稍后手动测试。'
    }
  } finally {
    if (!current.signal.aborted) {
      identityRequest = undefined
      testingIdentity.value = false
    }
  }
}
async function saveIdentity() {
  if (!selected.value || busy.value || testingIdentity.value) return
  busy.value = true
  try {
    const d = selected.value
    if (d.my_connection_id) await sourceCatalogApi.upgrade(d.my_connection_id, d.id, { ...identity })
    else await sourceCatalogApi.add(d.id, { ...identity })
    identityOpen.value = false
    ElMessage.success('我的连接已保存')
    emit('added')
  }
  catch { /* API displays the failure */ } finally { busy.value = false }
}
const revisionOpen = ref(false), revisionId = ref(0)
const revision = reactive<DefinitionPayload>({ name: '', description: '', visibility: 'global', access_mode: 'global', config: { type: 'postgres', host: '', port: 5432, database: '', ssl_mode: 'verify-full', connection_timeout: 10, environment: 'dev' } })
const revisionParameters = reactive<Record<string, string>>({})
const eligibleTemplates = computed(() => templates.value.filter(t => t.enabled && t.driver.enabled && (revision.visibility !== 'global' || t.visibility === 'global' && t.driver.visibility === 'global')))
const revisionTemplate = computed(() => templates.value.find(t => t.id === revision.config.template_revision_id))
function resetFields() { for (const k of Object.keys(revisionParameters)) delete revisionParameters[k]; for (const f of revisionTemplate.value?.fields || []) revisionParameters[f.name] = f.default || ''; if (revisionTemplate.value && !['org.opengauss.Driver', 'org.postgresql.Driver', 'com.mysql.cj.jdbc.Driver'].includes(revisionTemplate.value.driver.class_name)) revision.config.ssl_mode = 'disable' }
function resetType() { revision.config.port = { postgres: 5432, mysql: 3306, redis: 6379, jdbc: 0 }[revision.config.type]; revision.config.template_revision_id = null; delete revision.config.parameters; resetFields() }
function openRevision(d: SourceDefinition) { revisionId.value = d.id; Object.assign(revision, { name: d.name, description: d.description, visibility: d.visibility, access_mode: d.access_mode, config: JSON.parse(JSON.stringify(d.config)) }); for (const k of Object.keys(revisionParameters)) delete revisionParameters[k]; Object.assign(revisionParameters, d.config.parameters || {}); revisionOpen.value = true }
function openDefinition(event: MouseEvent) {
  createTrigger = event.currentTarget instanceof HTMLElement ? event.currentTarget : undefined
  revisionId.value = 0
  Object.assign(revision, { name: '', description: '', visibility: 'global', access_mode: 'global',
    config: { type: 'postgres', host: '', port: 5432, database: '', ssl_mode: 'verify-full', connection_timeout: 10, environment: 'dev' } })
  for (const key of Object.keys(revisionParameters)) delete revisionParameters[key]
  revisionOpen.value = true
}
async function closeEditor() {
  if (manageOpen.value || revisionId.value) return
  await nextTick()
  createTrigger?.focus({ preventScroll: true })
}
async function saveRevision() {
  if (busy.value || !canManage.value) return
  busy.value = true
  try {
    const config = { ...revision.config }
    if (config.type === 'jdbc') config.parameters = { ...revisionParameters }
    else { delete config.template_revision_id; delete config.parameters }
    const payload = { ...revision, config }
    const editing = revisionId.value > 0
    const saved = editing ? await sourceCatalogApi.revise(revisionId.value, payload)
      : await sourceCatalogApi.create(payload)
    revisionOpen.value = false
    if (editing && manageSource.value?.family_id === saved.family_id) manageSource.value = saved
    if (!editing && revision.access_mode === 'restricted') {
      manageSource.value = saved
      manageTrigger = createTrigger
      manageInitial.value = 'access'
      manageOpen.value = true
    }
    refreshCatalog()
    ElMessage.success(editing ? '新修订已发布' : '共享数据源已保存')
  } catch { /* 保存请求失败时保留表单。 */ }
  finally { busy.value = false }
}
</script>

<template>
  <section class="space-y-4 min-w-0" aria-label="共享数据源">
    <div class="ui-filter-bar shared-source-filters" role="search" aria-label="筛选共享数据源">
      <div class="ui-filter-field ui-filter-search">
        <label :for="`${filterId}-keyword`" class="text-sm text-muted">搜索共享数据源</label>
        <input :id="`${filterId}-keyword`" ref="keywordInput" v-model="keyword" type="search" class="glass-input"
          maxlength="100" :placeholder="sharedPicker ? '搜索名称、主机或数据库' : '搜索名称或默认数据库'" />
      </div>
      <template v-if="!sharedPicker">
        <div class="ui-filter-field">
          <label :for="filterId + '-type'" class="text-sm text-muted">类型</label>
          <select :id="filterId + '-type'" v-model="dbType" class="glass-input">
            <option value="">全部类型</option>
            <option v-for="(label, value) in typeLabels" :key="value" :value="value">{{ label }}</option>
          </select>
        </div>
        <div class="ui-filter-field">
          <label :for="filterId + '-environment'" class="text-sm text-muted">环境</label>
          <select :id="filterId + '-environment'" v-model="environment" class="glass-input">
            <option value="">全部环境</option>
            <option v-for="(label, value) in envLabels" :key="value" :value="value">{{ label }}</option>
          </select>
        </div>
        <div class="ui-filter-field">
          <label :for="filterId + '-status'" class="text-sm text-muted">修订状态</label>
          <select :id="filterId + '-status'" v-model="status" class="glass-input">
            <option value="">全部状态</option>
            <option value="enabled">已启用</option>
            <option value="disabled">已停用</option>
          </select>
        </div>
        <button v-if="canManage" type="button" class="liquid-button ui-control-target shared-source-new" @click="openDefinition">新建数据源</button>
      </template>
    </div>
    <div class="min-h-32" :aria-busy="loading">
      <LoadingState v-if="loading" label="正在读取共享数据源…" />
      <PageState v-else-if="loadError" title="无法读取共享数据源" :description="loadError" tone="danger">
        <template #actions><button type="button" class="ghost-button ui-control-target" @click="refreshCatalog">重试</button></template>
      </PageState>
      <PageState v-else-if="!items.length" :title="hasFilters ? '没有匹配的数据源' : '暂无可用的共享数据源'"
        :description="hasFilters ? '请调整搜索词或筛选条件。' : sharedPicker ? '可返回添加连接，选择手动配置。' : '可新建数据源并设置发布范围。'" />
      <ul v-else-if="sharedPicker" class="divide-y divide-divider">
        <li v-for="d in items" :key="d.id" class="py-3 flex flex-wrap items-center justify-between gap-2">
          <div class="min-w-0 break-words">
            <p class="font-medium">{{ d.name }}</p>
            <p class="text-xs text-muted">{{ typeLabels[d.config.type] }} · {{ envLabels[d.config.environment || 'dev'] }} · 修订 {{ d.revision }}</p>
          </div>
          <button v-if="canBind && d.enabled && (!d.my_connection_id || d.my_revision_id !== d.id)" type="button"
            class="ghost-button ui-control-target" :aria-label="`${d.my_connection_id ? '切换修订' : '选择'} ${d.name}`" @click="openIdentity(d)">
            {{ d.my_connection_id ? '切换修订' : '选择' }}
          </button>
          <span v-else-if="d.my_connection_id" class="text-sm text-muted">已添加</span>
        </li>
      </ul>
      <div v-else class="editorial-table-wrap shared-source-table-wrap">
        <table class="editorial-table shared-source-table" aria-label="共享数据源列表">
          <thead>
            <tr><th scope="col">数据源</th><th scope="col">类型 / 环境</th><th scope="col">发布范围</th>
              <th scope="col">最新修订</th><th scope="col">修订状态</th><th scope="col">操作</th></tr>
          </thead>
          <tbody>
            <tr v-for="d in items" :key="d.family_id">
              <th scope="row">
                <span class="text-sm font-medium text-content">{{ d.name }}</span>
                <p v-if="defaultDatabase(d)" class="editorial-table__meta">{{ defaultDatabase(d) }}</p>
              </th>
              <td data-label="类型 / 环境">
                <span>{{ typeLabels[d.config.type] }}</span>
                <span class="ml-2 text-xs" :class="d.config.environment === 'prod' ? 'text-danger' : 'text-muted'">{{ envLabels[d.config.environment || 'dev'] }}</span>
              </td>
              <td data-label="发布范围">{{ d.access_mode === 'restricted' ? '指定用户或用户组' : '空间内成员' }}</td>
              <td data-label="最新修订">修订 {{ d.revision }}</td>
              <td data-label="修订状态" :class="d.enabled ? 'text-success' : 'text-muted'">{{ d.enabled ? '已启用' : '已停用' }}</td>
              <td class="shared-source-action">
                <button :id="`${filterId}-manage-${d.family_id}`" type="button" class="ghost-button ui-control-target"
                  :aria-label="'管理 ' + d.name" @click="openManage(d, $event)">管理</button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>
    <div v-if="!loading && !loadError && total > 0" class="flex flex-wrap items-center justify-between gap-3 text-xs text-muted">
      <span role="status">共 {{ total }} 个共享数据源</span>
      <div v-if="total > pageSize" class="flex flex-wrap items-center gap-3">
        <button type="button" class="ghost-button text-xs ui-control-target" :disabled="page <= 1" @click="changePage(page - 1)">上一页</button>
        <span>第 {{ page }} 页</span>
        <button type="button" class="ghost-button text-xs ui-control-target" :disabled="page * pageSize >= total" @click="changePage(page + 1)">下一页</button>
      </div>
    </div>
    <SourceManageDialog v-if="!sharedPicker" v-model="manageOpen" :source="manageSource"
      :initial-tab="manageInitial" @changed="changedSource" @revise="openRevision" @closed="closeManage" />

    <el-dialog v-if="sharedPicker" v-model="identityOpen" append-to-body :title="selected?.my_connection_id ? '确认新修订与个人身份' : '添加到我的连接'" width="min(92vw, 560px)" :close-on-click-modal="false" class="glass-dialog" @closed="clearIdentity">
      <div v-if="selected" class="space-y-4">
        <div class="rounded-xl bg-surface-muted p-4 space-y-2"><p>{{ selected.name }} · 修订 {{ selected.revision }}</p><p class="text-xs text-muted break-all">{{ endpoint(selected) }}</p><p class="text-xs text-muted">TLS：{{ selected.config.ssl_mode }}。请核实目标后填写自己的数据库账号。</p><p v-if="selected.my_connection_id" class="text-xs text-warning">保存后将切换此个人连接的修订，并清理此前采集的结构。请重新输入密码。</p></div>
        <el-form label-position="top">
          <el-form-item label="我的连接名称"><el-input v-model="identity.name" maxlength="100" /></el-form-item>
          <el-form-item label="我的数据库账号"><el-input v-model="identity.username" autocomplete="off" aria-label="我的数据库账号" /></el-form-item>
          <el-form-item label="我的数据库密码"><el-input v-model="identity.password" type="password" show-password autocomplete="new-password" aria-label="我的数据库密码" /></el-form-item>
        </el-form>
        <el-checkbox v-model="identity.confirmed" class="identity-confirm" aria-label="确认连接目标">我已确认地址、数据库和修订，使用自己的数据库账号</el-checkbox>
        <p v-if="identityCheck?.ok" role="status" class="text-sm text-success">个人连接测试成功</p>
        <ConnectionDiagnosis :result="identityCheck" :error="identityCheckError" />
      </div>
      <template #footer><div class="flex flex-wrap justify-between gap-2"><button class="ghost-button ui-control-target" :disabled="busy || testingIdentity || !identityReady" @click="testIdentity">{{ testingIdentity ? '测试中…' : '测试我的连接' }}</button><button class="liquid-button ui-control-target" :disabled="busy || testingIdentity || !identityReady" @click="saveIdentity">{{ selected?.my_connection_id ? '确认并切换' : '保存我的连接' }}</button></div></template>
    </el-dialog>

    <el-dialog v-if="!sharedPicker" v-model="revisionOpen" append-to-body :title="revisionId ? '发布数据源新修订' : '新建共享数据源'" width="min(92vw, 620px)" :close-on-click-modal="false" class="glass-dialog source-definition-dialog" @closed="closeEditor">
      <p class="text-xs text-muted mb-4">已有个人连接和正式报表继续引用各自的修订。共享定义保存连接配置和发布范围。</p>
      <el-form label-position="top">
        <el-form-item label="数据源名称"><el-input v-model="revision.name" maxlength="100" /></el-form-item>
        <el-form-item label="说明"><el-input v-model="revision.description" type="textarea" maxlength="1000" /></el-form-item>
        <el-form-item v-if="!revisionId" label="初始发布范围"><el-select v-model="revision.access_mode" class="w-full" aria-label="初始发布范围"><el-option label="空间内成员" value="global" /><el-option label="指定用户或用户组（保存后设置）" value="restricted" /></el-select></el-form-item>
        <el-form-item label="类型"><el-select v-model="revision.config.type" class="w-full" @change="resetType"><el-option label="PostgreSQL" value="postgres" /><el-option label="MySQL" value="mysql" /><el-option label="Redis" value="redis" /><el-option label="自定义 JDBC" value="jdbc" /></el-select></el-form-item>
        <template v-if="revision.config.type === 'jdbc'">
          <el-form-item label="驱动连接规则"><el-select v-model="revision.config.template_revision_id" class="w-full" @change="resetFields"><el-option v-for="t in eligibleTemplates" :key="t.id" :value="t.id" :label="`${t.name} · 修订 ${t.revision}`" /></el-select></el-form-item>
          <el-form-item v-for="f in revisionTemplate?.fields || []" :key="f.name" :label="f.label"><el-input v-model="revisionParameters[f.name]" :type="f.type === 'integer' ? 'number' : 'text'" /></el-form-item>
        </template>
        <template v-else>
          <el-form-item label="主机"><el-input v-model="revision.config.host" /></el-form-item>
          <el-form-item label="端口"><el-input v-model.number="revision.config.port" type="number" /></el-form-item>
          <el-form-item label="数据库"><el-input v-model="revision.config.database" /></el-form-item>
        </template>
        <el-form-item label="TLS"><el-select v-model="revision.config.ssl_mode" class="w-full"><el-option label="验证服务器证书" value="verify-full" /><el-option label="禁用" value="disable" /></el-select></el-form-item>
        <el-form-item label="环境"><el-select v-model="revision.config.environment" class="w-full"><el-option label="开发" value="dev" /><el-option label="测试" value="test" /><el-option label="生产" value="prod" /></el-select></el-form-item>
      </el-form>
      <template #footer><button class="liquid-button ui-control-target" :disabled="busy || !revision.name.trim()" @click="saveRevision">{{ revisionId ? '发布新修订' : '保存数据源' }}</button></template>
    </el-dialog>
  </section>
</template>



<style scoped>
:global(.source-definition-dialog) {max-height: calc(100dvh - 32px); display: flex; flex-direction: column; margin-top: 16px !important;}
:global(.source-definition-dialog .el-dialog__body) {overflow-y: auto; min-height: 0;}
:global(.source-definition-dialog .el-dialog__header), :global(.source-definition-dialog .el-dialog__footer) {flex-shrink: 0;}

.identity-confirm { height: auto; align-items: flex-start; }
.identity-confirm :deep(.el-checkbox__label) { white-space: normal; line-height: 1.5; overflow-wrap: anywhere; }

.shared-source-filters { align-items: end; }
.shared-source-filters .ui-filter-field:not(.ui-filter-search) { flex: 0 1 8.5rem; }
.shared-source-table th[scope='row'] { overflow-wrap: anywhere; white-space: normal; }
@media (max-width: 736px) {
  .shared-source-table-wrap { border: 0; background: transparent; }
  .shared-source-table, .shared-source-table tbody { display: block; }
  .shared-source-table thead { position: absolute; width: 1px; height: 1px; overflow: hidden; clip-path: inset(50%); }
  .shared-source-table tbody tr {
    --source-action-width: 4rem;
    --source-card-inset: 11px;
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    gap: 8px 12px;
    padding: 10px var(--source-card-inset) 12px;
    border: 1px solid var(--db-divider);
    border-radius: var(--db-radius-control);
    background: var(--db-surface-data);
  }
  .shared-source-table tbody tr + tr { margin-block-start: 8px; }
  .shared-source-table tbody tr::before {
    content: '';
    grid-area: 2 / 1 / 3 / -1;
    margin-inline: calc(-1 * var(--source-card-inset));
    border-block-start: 1px solid var(--db-divider);
  }
  .shared-source-table tbody tr > :is(th[scope='row'], td[data-label], .shared-source-action) {
    padding: 0;
    border: 0;
    min-width: 0;
    white-space: normal;
    overflow-wrap: anywhere;
  }
  .shared-source-table tbody tr > th[scope='row'] {
    grid-area: 1 / 1 / 2 / -1;
    align-self: center;
    padding-inline-end: calc(var(--source-action-width) + 12px);
  }
  .shared-source-table th[scope='row'] > span { font-size: 1rem; font-weight: 600; }
  .shared-source-table td[data-label]::before { display: block; content: attr(data-label); font-size: 12px; color: var(--db-text-secondary); }
  .shared-source-action { grid-area: 1 / 2 / 2 / 3; align-self: center; justify-self: end; }
  .shared-source-action .ghost-button {
    inline-size: var(--source-action-width);
    min-block-size: 44px;
    padding-inline: 8px;
    color: var(--db-accent);
    white-space: nowrap;
  }
}
@media (max-width: 540px) {
  .shared-source-filters .ui-filter-field:not(.ui-filter-search) { flex-basis: calc(50% - 4px); width: auto; }
}
</style>
