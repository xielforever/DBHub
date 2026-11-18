<script setup lang="ts">
import { computed, nextTick, onMounted, onScopeDispose, reactive, ref, useId, watch } from 'vue'
import { ElMessageBox } from 'element-plus'
import LoadingState from '../../components/base/LoadingState.vue'
import PageCategoryTabs from '../../components/base/PageCategoryTabs.vue'
import PageHeader from '../../components/base/PageHeader.vue'
import PageState from '../../components/base/PageState.vue'
import { connectorApi, type DatasourceTemplate, type DriverArtifact, type JDBCDriver, type TemplatePayload } from '../../api/connector'
import { useUserStore } from '../../stores/user'

type Section = 'driver' | 'template'
type ReadKind = Section | 'artifact'
const user = useUserStore()
const id = useId()
const categories = [{ id: 'driver', name: '驱动管理' }, { id: 'template', name: '数据源模板' }] as const
const active = ref<Section>('driver')
const isAdmin = computed(() => user.hasCapability('datasource_manage'))
const drivers = ref<JDBCDriver[]>([])
const artifacts = ref<DriverArtifact[]>([])
const templates = ref<DatasourceTemplate[]>([])
const loading = reactive({ driver: true, template: true, artifact: true })
const errors = reactive({ driver: '', template: '', artifact: '' })
const requests: Partial<Record<ReadKind, AbortController>> = {}
const busy = ref(false)
const confirming = ref(false)
const locked = computed(() => busy.value || confirming.value)
const notice = ref('')
const actionNotice = ref<HTMLParagraphElement>()
const selectedId = ref<number>()
const detailTitle = ref<HTMLHeadingElement>()
const editorTitle = ref<HTMLHeadingElement>()
const newButton = ref<HTMLButtonElement>()
let detailTrigger: HTMLElement | undefined
const editorTriggers: Partial<Record<Section, HTMLElement>> = {}
let disposed = false
const editors = reactive({ driver: false, template: false })
const editorOpen = computed(() => editors[active.value])

const searches = reactive({ driver: '', template: '' })
const statuses = reactive<{ driver: string; template: string }>({ driver: '', template: '' })
const driverScope = ref<'private' | 'global'>('private')
const artifactId = ref('')
const driverName = ref('')
const editingId = ref<number>()
const empty = (): TemplatePayload => ({
  visibility: 'private', template_key: '', name: '', product: '', driver_id: 0, dialect: '', url_template: '', fields: [],
})
const form = reactive<TemplatePayload>(empty())
const selectedArtifact = computed(() => artifacts.value.find(item => item.id === artifactId.value))
const driverDialect = computed(() => {
  const className = drivers.value.find(driver => driver.id === form.driver_id)?.class_name
  if (className === 'org.postgresql.Driver' || className === 'org.opengauss.Driver') return 'postgres'
  return className === 'com.mysql.cj.jdbc.Driver' ? 'mysql' : ''
})

function matches(name: string, kind: Section, enabled: boolean) {
  return name.toLowerCase().includes(searches[kind].trim().toLowerCase())
    && (!statuses[kind] || statuses[kind] === (enabled ? 'enabled' : 'disabled'))
}

const visibleDrivers = computed(() => drivers.value.filter(item => matches(item.name, 'driver', item.enabled)))
const visibleTemplates = computed(() => templates.value.filter(item => matches(item.name, 'template', item.enabled && item.driver.enabled)))
const currentItems = computed(() => active.value === 'driver' ? drivers.value : templates.value)
const visibleItems = computed(() => active.value === 'driver' ? visibleDrivers.value : visibleTemplates.value)
const selectedDriver = computed(() => active.value === 'driver' ? visibleDrivers.value.find(item => item.id === selectedId.value) : undefined)
const selectedTemplate = computed(() => active.value === 'template' ? visibleTemplates.value.find(item => item.id === selectedId.value) : undefined)
const linkedTemplates = computed(() => selectedDriver.value ? templates.value.filter(item => item.driver_id === selectedDriver.value?.id) : [])
const sectionName = computed(() => active.value === 'driver' ? '驱动管理' : '数据源模板')
const canRegister = computed(() => !locked.value && !loading.artifact && !!selectedArtifact.value?.ready
  && !!driverName.value.trim() && (driverScope.value === 'private' || isAdmin.value))
const canPublish = computed(() => !locked.value && !loading.driver && !errors.driver
  && drivers.value.some(item => item.id === form.driver_id && item.enabled)
  && !!form.template_key && !!form.name && (form.visibility !== 'global' || isAdmin.value))

function canManage(item: { created_by: number; visibility?: string }) {
  return (item.visibility === 'private' && item.created_by === user.user?.id)
    || (item.visibility === 'global' && isAdmin.value)
}

function scopeLabel(value?: string) {
  return value === 'global' ? '工作空间共享' : '仅自己'
}

async function read<T>(kind: ReadKind, fetch: (options: { signal: AbortSignal; silentError: boolean }) => Promise<{ items: T[] }>, setItems: (items: T[]) => void) {
  requests[kind]?.abort()
  const current = new AbortController()
  requests[kind] = current
  loading[kind] = true
  errors[kind] = ''
  try {
    const result = await fetch({ signal: current.signal, silentError: true })
    if (!current.signal.aborted) setItems(result.items)
  } catch (error) {
    if (!current.signal.aborted) {
      setItems([])
      errors[kind] = error instanceof Error ? error.message : '暂时无法读取，请稍后重试。'
    }
  } finally {
    if (!current.signal.aborted) {
      delete requests[kind]
      loading[kind] = false
    }
  }
}

const readDrivers = () => read('driver', connectorApi.drivers, items => { drivers.value = items })
const readTemplates = () => read('template', connectorApi.templates, items => { templates.value = items })
const readArtifacts = () => read('artifact', connectorApi.artifacts, items => { artifacts.value = items })

function refresh() {
  return Promise.all([readDrivers(), readTemplates(), readArtifacts()])
}

function resetTemplate() {
  editingId.value = undefined
  Object.assign(form, empty())
}

function selectCategory(value: string) {
  if (locked.value || value === active.value || (value !== 'driver' && value !== 'template')) return
  active.value = value
  selectedId.value = undefined
  notice.value = ''
}

async function openDetail(value: number, event: MouseEvent) {
  detailTrigger = event.currentTarget instanceof HTMLElement ? event.currentTarget : undefined
  selectedId.value = value
  await nextTick()
  detailTitle.value?.focus()
}

async function closeDetail() {
  const trigger = detailTrigger
  selectedId.value = undefined
  await nextTick()
  trigger?.focus()
}

async function openEditor(event: MouseEvent, template?: DatasourceTemplate) {
  if (event.currentTarget instanceof HTMLElement) editorTriggers[active.value] = event.currentTarget
  if (active.value === 'template') resetTemplate()
  if (template) {
    editingId.value = template.id
    const { visibility, template_key, name, product, driver_id, dialect, url_template, fields } = template
    Object.assign(form, { visibility, template_key, name, product, driver_id, dialect, url_template, fields: fields.map(field => ({ ...field })) })
  }
  editors[active.value] = true
  await nextTick()
  editorTitle.value?.focus()
}

async function closeEditor() {
  const kind = active.value
  editors[kind] = false
  if (kind === 'template') resetTemplate()
  else {
    artifactId.value = ''
    driverName.value = ''
  }
  await nextTick()
  const trigger = editorTriggers[kind]
  if (trigger?.isConnected) trigger.focus()
  else newButton.value?.focus()
  delete editorTriggers[kind]
}

function chooseArtifact() {
  if (selectedArtifact.value) driverName.value = selectedArtifact.value.name + ' JDBC'
}

function chooseDriver() {
  const driver = drivers.value.find(item => item.id === form.driver_id)
  if (!driver) return
  const h2 = driver.class_name === 'org.h2.Driver'
  const openGauss = driver.class_name === 'org.opengauss.Driver'
  const postgres = driver.class_name === 'org.postgresql.Driver'
  const mysql = driver.class_name === 'com.mysql.cj.jdbc.Driver'
  form.product = h2 ? 'H2' : openGauss ? 'openGauss' : postgres ? 'PostgreSQL' : mysql ? 'MySQL' : driver.name
  form.dialect = ''
  const protocol = openGauss ? 'opengauss' : postgres ? 'postgresql' : mysql ? 'mysql' : 'vendor'
  form.url_template = h2 ? 'jdbc:h2:mem:{database};DB_CLOSE_DELAY=-1' : `jdbc:${protocol}://{host}:{port}/{database}`
  form.fields = h2 ? [] : [
    { name: 'host', label: '主机', type: 'string', required: true },
    { name: 'port', label: '端口', type: 'integer', required: true, default: postgres || openGauss ? '5432' : mysql ? '3306' : '' },
  ]
  form.fields.push({ name: 'database', label: '数据库', type: 'string', required: true })
}

async function action(operation: () => Promise<unknown>, message: string, after?: () => Promise<void> | void,
  trigger?: EventTarget | null) {
  if (locked.value || disposed) return
  busy.value = true
  notice.value = ''
  try {
    await operation()
    if (disposed) return
    notice.value = message
    await after?.()
    const refreshNeedsFocus = trigger === document.activeElement
    await refresh()
    if (refreshNeedsFocus && document.activeElement === document.body) actionNotice.value?.focus()
  } catch {
    // 写请求错误沿用现有统一反馈。
  } finally {
    if (!disposed) busy.value = false
  }
}

function register() {
  if (!canRegister.value) return
  void action(() => connectorApi.register(artifactId.value, driverName.value.trim(), driverScope.value), '驱动已登记', closeEditor)
}

function publish() {
  if (!canPublish.value) return
  void action(() => connectorApi.publish({ ...form, fields: form.fields.map(field => ({ ...field })) }, editingId.value),
    editingId.value ? '模板修订已发布' : '模板已发布', closeEditor)
}

async function confirm(message: string, title: string) {
  if (locked.value) return false
  confirming.value = true
  try {
    await ElMessageBox.confirm(message, title, { type: 'warning' })
    return !disposed
  } catch {
    return false
  } finally {
    confirming.value = false
  }
}

async function setStatus(kind: Section, id: number, enabled: boolean, trigger: EventTarget | null) {
  if (!enabled && !(await confirm('停用后将阻止使用此版本建立新会话，已有操作不会重派。', '停用确认'))) return
  await action(() => connectorApi.status(kind, id, enabled), enabled ? '已启用' : '已停用', undefined, trigger)
}

async function remove(kind: Section, id: number, name: string, trigger: EventTarget | null) {
  if (await confirm(`删除「${name}」？引用中的版本不能删除。`, '删除确认')) {
    await action(() => connectorApi.remove(kind, id), '已删除', undefined, trigger)
  }
}

function resetFilters() {
  searches[active.value] = ''
  statuses[active.value] = ''
}

watch(visibleItems, items => {
  if (!items.some(item => item.id === selectedId.value)) selectedId.value = undefined
})
onMounted(() => void refresh())
onScopeDispose(() => {
  disposed = true
  for (const current of Object.values(requests)) current.abort()
})
</script>

<template>
  <section class="ui-page-stack min-w-0">
    <PageHeader title="驱动与模板" heading="h2" description="登记已安装的 JDBC 驱动，管理当前空间可见的连接模板。">
      <template #actions>
        <button type="button" class="ghost-button ui-control-target" :disabled="locked || loading[active]" @click="refresh">刷新</button>
      </template>
    </PageHeader>
    <p v-if="notice" ref="actionNotice" role="status" tabindex="-1" class="text-sm text-success ui-focus-target">{{ notice }}</p>

    <PageCategoryTabs label="驱动与模板分类" :categories="categories" :active-id="active" @select="selectCategory">
      <section :aria-label="sectionName" class="space-y-4 min-w-0">
        <div class="ui-filter-bar">
          <div class="ui-filter-field ui-filter-search">
            <label :for="id + '-search'" class="text-sm text-muted">搜索名称</label>
            <input :id="id + '-search'" v-model="searches[active]" type="search" class="glass-input w-full"
              :placeholder="active === 'driver' ? '驱动名称' : '模板名称'" />
          </div>
          <div class="ui-filter-field">
            <label :for="id + '-status'" class="text-sm text-muted">状态</label>
            <el-select :id="id + '-status'" v-model="statuses[active]" class="w-full" placeholder="全部状态">
              <el-option value="" label="全部状态" />
              <el-option value="enabled" label="已启用" />
              <el-option value="disabled" :label="active === 'driver' ? '已停用' : '不可用'" />
            </el-select>
          </div>
          <button ref="newButton" type="button" class="liquid-button ui-control-target self-end" :disabled="locked || editorOpen"
            @click="openEditor($event)">{{ active === 'driver' ? '登记驱动' : '新建模板' }}</button>
        </div>

        <LoadingState v-if="loading[active]" :label="'正在读取' + sectionName + '…'" />
        <PageState v-else-if="errors[active]" heading="h3" :title="'无法读取' + sectionName" :description="errors[active]" tone="danger">
          <template #actions>
            <button type="button" class="ghost-button ui-control-target"
              @click="active === 'driver' ? readDrivers() : readTemplates()">重试</button>
          </template>
        </PageState>
        <PageState v-else-if="!currentItems.length" heading="h3"
          :title="active === 'driver' ? '尚未登记 JDBC 驱动' : '暂无数据源模板'"
          :description="active === 'driver' ? '可以登记 JDBC 服务已安装的制品。' : '发布后可在新建自定义 JDBC 数据源时选择。'" />
        <PageState v-else-if="!visibleItems.length" heading="h3" title="没有匹配记录" description="请调整名称或状态筛选。">
          <template #actions><button type="button" class="ghost-button ui-control-target" @click="resetFilters">清除筛选</button></template>
        </PageState>
        <div v-else class="editorial-table-wrap" role="region" tabindex="0" :aria-label="sectionName + '表格'">
          <table v-if="active === 'driver'" class="editorial-table min-w-[720px]" aria-label="已登记驱动">
            <thead><tr>
              <th scope="col">序号</th><th scope="col">驱动名称</th><th scope="col">版本</th>
              <th scope="col">可见范围</th><th scope="col">状态</th><th scope="col">操作</th>
            </tr></thead>
            <tbody>
              <tr v-for="(driver, index) in visibleDrivers" :key="driver.id" :class="{ 'bg-interactive': selectedId === driver.id }">
                <td class="editorial-table__number">{{ index + 1 }}</td>
                <th scope="row" class="max-w-56 !whitespace-normal [overflow-wrap:anywhere]">{{ driver.name }}</th>
                <td>{{ driver.version }}</td>
                <td>{{ scopeLabel(driver.visibility) }}</td>
                <td><span :class="driver.enabled ? 'text-success' : 'text-muted'">{{ driver.enabled ? '已启用' : '已停用' }}</span></td>
                <td><div class="editorial-table__actions">
                  <button type="button" class="editorial-text-action ui-control-target" :aria-expanded="selectedId === driver.id"
                    :aria-controls="selectedDriver?.id === driver.id ? id + '-detail' : undefined" :aria-label="'查看详情 ' + driver.name"
                    @click="openDetail(driver.id, $event)">查看详情</button>
                  <template v-if="canManage(driver)">
                    <button type="button" class="ghost-button text-xs ui-control-target" :aria-disabled="locked || undefined"
                      @click="setStatus('driver', driver.id, !driver.enabled, $event.currentTarget)">{{ driver.enabled ? '停用' : '启用' }}</button>
                    <button type="button" class="ghost-button text-xs text-danger ui-control-target" :aria-disabled="locked || undefined"
                      @click="remove('driver', driver.id, driver.name, $event.currentTarget)">删除</button>
                  </template>
                </div></td>
              </tr>
            </tbody>
          </table>
          <table v-else class="editorial-table min-w-[800px]" aria-label="数据源模板列表">
            <thead><tr>
              <th scope="col">序号</th><th scope="col">模板名称</th><th scope="col">产品</th>
              <th scope="col">驱动版本</th><th scope="col">可见范围</th><th scope="col">状态</th><th scope="col">操作</th>
            </tr></thead>
            <tbody>
              <tr v-for="(template, index) in visibleTemplates" :key="template.id" :class="{ 'bg-interactive': selectedId === template.id }">
                <td class="editorial-table__number">{{ index + 1 }}</td>
                <th scope="row" class="max-w-56 !whitespace-normal [overflow-wrap:anywhere]">
                  {{ template.name }}<p class="editorial-table__meta">修订 {{ template.revision }}</p>
                </th>
                <td>{{ template.product }}</td>
                <td>{{ template.driver.name }} {{ template.driver.version }}</td>
                <td>{{ scopeLabel(template.visibility) }}</td>
                <td><span :class="template.enabled && template.driver.enabled ? 'text-success' : 'text-muted'">
                  {{ template.enabled && template.driver.enabled ? '已启用' : '不可用' }}
                </span></td>
                <td><div class="editorial-table__actions">
                  <button type="button" class="editorial-text-action ui-control-target" :aria-expanded="selectedId === template.id"
                    :aria-controls="selectedTemplate?.id === template.id ? id + '-detail' : undefined" :aria-label="'查看详情 ' + template.name"
                    @click="openDetail(template.id, $event)">查看详情</button>
                  <template v-if="canManage(template)">
                    <button type="button" class="ghost-button text-xs ui-control-target"
                      :disabled="locked || editorOpen" @click="openEditor($event, template)">新修订</button>
                    <button type="button" class="ghost-button text-xs ui-control-target" :aria-disabled="locked || undefined"
                      @click="setStatus('template', template.id, !template.enabled, $event.currentTarget)">{{ template.enabled ? '停用' : '启用' }}</button>
                    <button type="button" class="ghost-button text-xs text-danger ui-control-target" :aria-disabled="locked || undefined"
                      @click="remove('template', template.id, template.name, $event.currentTarget)">删除</button>
                  </template>
                </div></td>
              </tr>
            </tbody>
          </table>
        </div>

        <section v-if="!loading[active] && (selectedDriver || selectedTemplate)" :id="id + '-detail'"
          :aria-labelledby="id + '-detail-heading'" class="data-surface ui-card-padding space-y-4 [overflow-wrap:anywhere]">
          <div class="flex flex-wrap items-center justify-between gap-3">
            <h3 :id="id + '-detail-heading'" ref="detailTitle" tabindex="-1" class="font-semibold">
              {{ selectedDriver ? '驱动详情' : '模板详情' }}
            </h3>
            <button type="button" class="ghost-button ui-control-target" @click="closeDetail">关闭详情</button>
          </div>
          <dl v-if="selectedDriver" class="grid grid-cols-[auto_minmax(0,1fr)] gap-2 text-sm">
            <dt class="text-muted">驱动名称</dt><dd>{{ selectedDriver.name }}</dd>
            <dt class="text-muted">驱动类</dt><dd class="font-mono">{{ selectedDriver.class_name }}</dd>
            <dt class="text-muted">制品</dt><dd>{{ selectedDriver.artifact_id }} · {{ selectedDriver.version }}</dd>
            <dt class="text-muted">SHA-256</dt><dd class="font-mono">{{ selectedDriver.sha256 }}</dd>
            <dt class="text-muted">可见范围</dt><dd>{{ scopeLabel(selectedDriver.visibility) }}</dd>
            <dt class="text-muted">关联模板</dt>
            <dd v-if="loading.template">正在读取关联模板…</dd>
            <dd v-else-if="errors.template">暂时无法读取模板目录。</dd>
            <dd v-else>{{ linkedTemplates.map(item => item.name).join('、') || '暂无可见关联模板' }}</dd>
          </dl>
          <dl v-if="selectedTemplate" class="grid grid-cols-[auto_minmax(0,1fr)] gap-2 text-sm">
            <dt class="text-muted">模板名称</dt><dd>{{ selectedTemplate.name }} · 修订 {{ selectedTemplate.revision }}</dd>
            <dt class="text-muted">模板标识</dt><dd>{{ selectedTemplate.template_key }}</dd>
            <dt class="text-muted">驱动</dt><dd>{{ selectedTemplate.driver.name }} {{ selectedTemplate.driver.version }}</dd>
            <dt class="text-muted">SQL 能力</dt><dd>{{ selectedTemplate.dialect ? 'SQL 与结构浏览' : '仅结构浏览' }}</dd>
            <dt class="text-muted">JDBC URL 规则</dt><dd class="font-mono">{{ selectedTemplate.url_template }}</dd>
            <dt class="text-muted">连接字段</dt><dd>{{ selectedTemplate.fields.map(field => field.label || field.name).join('、') || '无' }}</dd>
          </dl>
        </section>

        <form v-if="editorOpen && active === 'driver'" class="data-surface ui-card-padding space-y-4" @submit.prevent="register">
          <h3 ref="editorTitle" tabindex="-1" class="font-semibold">登记已安装驱动</h3>
          <div v-if="errors.artifact" class="flex flex-wrap gap-2 items-center text-sm">
            <p class="text-warning" role="alert">{{ errors.artifact }}</p>
            <button type="button" class="ghost-button ui-control-target" @click="readArtifacts">重试读取制品</button>
          </div>
          <p v-else-if="!loading.artifact && !artifacts.length" class="text-sm text-muted">JDBC 服务没有可登记的制品。</p>
          <fieldset :disabled="locked" class="min-w-0 space-y-3">
            <div class="grid gap-3 sm:grid-cols-2">
              <label class="text-sm text-muted">可用制品
                <el-select v-model="artifactId" :loading="loading.artifact" :disabled="locked || loading.artifact || !!errors.artifact"
                  class="w-full mt-1" placeholder="选择已安装的驱动" aria-label="可用驱动制品" @change="chooseArtifact">
                  <el-option v-for="item in artifacts" :key="item.id" :value="item.id" :label="item.name + ' ' + item.version" :disabled="!item.ready" />
                </el-select>
              </label>
              <label class="text-sm text-muted">驱动名称
                <input v-model="driverName" class="glass-input mt-1 w-full" maxlength="100" placeholder="例如 PostgreSQL JDBC" />
              </label>
            </div>
            <p v-if="selectedArtifact" class="text-xs text-muted [overflow-wrap:anywhere]">{{ selectedArtifact.class_name }} · SHA-256 {{ selectedArtifact.sha256 }}</p>
            <label class="block text-sm text-muted">可见范围
              <el-select v-model="driverScope" :disabled="locked" aria-label="驱动可见范围" class="w-full mt-1">
                <el-option label="仅自己" value="private" />
                <el-option v-if="isAdmin" label="工作空间共享" value="global" />
              </el-select>
            </label>
          </fieldset>
          <div class="flex flex-wrap gap-2">
            <button type="submit" class="liquid-button ui-control-target" :disabled="!canRegister">确认登记</button>
            <button type="button" class="ghost-button ui-control-target" :disabled="locked" @click="closeEditor">取消编辑</button>
          </div>
        </form>

        <form v-if="editorOpen && active === 'template'" class="data-surface ui-card-padding space-y-4" @submit.prevent="publish">
          <h3 ref="editorTitle" tabindex="-1" class="font-semibold">{{ editingId ? '编辑模板修订' : '新建连接模板' }}</h3>
          <p class="text-sm text-muted">发布新修订后，现有实例继续使用原版本。</p>
          <div v-if="errors.driver" class="flex flex-wrap items-center gap-2 text-sm">
            <p role="alert" class="text-danger">暂时无法选择驱动。{{ errors.driver }}</p>
            <button type="button" class="ghost-button ui-control-target" @click="readDrivers">重试读取驱动</button>
          </div>
          <fieldset :disabled="locked" class="min-w-0 space-y-3">
            <div class="grid gap-3 sm:grid-cols-2">
              <label class="text-sm text-muted">模板标识
                <input v-model="form.template_key" class="glass-input mt-1 w-full" :disabled="!!editingId" maxlength="80" />
              </label>
              <label class="text-sm text-muted">模板名称
                <input v-model="form.name" class="glass-input mt-1 w-full" maxlength="100" />
              </label>
              <label class="text-sm text-muted">产品名称
                <input v-model="form.product" class="glass-input mt-1 w-full" maxlength="80" />
              </label>
              <label class="text-sm text-muted">驱动版本
                <el-select :model-value="form.driver_id || undefined" :loading="loading.driver" :disabled="locked || !!errors.driver"
                  aria-label="模板驱动版本" class="w-full mt-1" placeholder="选择驱动版本"
                  @update:model-value="form.driver_id = $event" @change="chooseDriver">
                  <el-option v-for="item in drivers.filter(item => item.enabled)" :key="item.id" :value="item.id" :label="item.name + ' ' + item.version" />
                </el-select>
              </label>
            </div>
            <label class="block text-sm text-muted">配置可见范围
              <el-select v-model="form.visibility" :disabled="locked || !!editingId" aria-label="连接规则可见范围" class="w-full mt-1">
                <el-option label="仅自己" value="private" />
                <el-option v-if="isAdmin" label="工作空间共享" value="global" />
              </el-select>
            </label>
            <label class="block text-sm text-muted">JDBC URL 模板
              <input v-model="form.url_template" aria-label="JDBC URL 模板" :aria-describedby="id + '-url-hint'"
                class="glass-input mt-1 w-full font-mono text-xs" spellcheck="false" />
              <span :id="id + '-url-hint'" class="block mt-1 text-xs">使用所选驱动支持的 URL 规则。用户名和密码由实例独立填写。</span>
            </label>
            <label class="block text-sm text-muted">SQL 能力
              <el-select v-model="form.dialect" :disabled="locked" aria-label="模板 SQL 能力" class="w-full mt-1"
                placeholder="仅结构浏览（未启用 SQL）">
                <el-option label="仅结构浏览（未启用 SQL）" value="" />
                <el-option v-if="driverDialect" :label="(driverDialect === 'postgres' ? 'PostgreSQL' : 'MySQL') + ' 方言与基础执行'" :value="driverDialect" />
              </el-select>
            </label>
            <div v-if="form.fields.length" class="space-y-2">
              <h4 class="text-sm font-medium">连接字段</h4>
              <div v-for="(field, index) in form.fields" :key="index" class="grid sm:grid-cols-3 gap-2">
                <input v-model="field.name" class="glass-input text-xs" :aria-label="'字段 ' + (index + 1) + ' 标识'" placeholder="字段标识" />
                <input v-model="field.label" class="glass-input text-xs" :aria-label="'字段 ' + (index + 1) + ' 显示名称'" placeholder="显示名称" />
                <div class="flex gap-2 min-w-0">
                  <input v-model="field.default" class="glass-input min-w-0 text-xs" :aria-label="'字段 ' + (index + 1) + ' 默认值'" placeholder="默认值（可选）" />
                  <button type="button" class="ghost-button text-xs ui-control-target shrink-0" :aria-label="'移除字段 ' + (index + 1)"
                    @click="form.fields.splice(index, 1)">移除</button>
                </div>
              </div>
            </div>
            <button type="button" class="ghost-button ui-control-target" :disabled="form.fields.length >= 20"
              @click="form.fields.push({ name: '', label: '', type: 'string', required: true })">添加连接字段</button>
          </fieldset>
          <div class="flex flex-wrap gap-2">
            <button type="submit" class="liquid-button ui-control-target" :disabled="!canPublish">{{ editingId ? '发布新修订' : '发布模板' }}</button>
            <button type="button" class="ghost-button ui-control-target" :disabled="locked" @click="closeEditor">取消编辑</button>
          </div>
        </form>
      </section>
    </PageCategoryTabs>
  </section>
</template>

<style scoped>
.ghost-button[aria-disabled='true'] {
  cursor: not-allowed;
  opacity: .6;
  background: var(--db-surface-muted);
  box-shadow: none;
}
</style>
