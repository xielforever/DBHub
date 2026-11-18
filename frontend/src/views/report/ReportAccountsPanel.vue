<script setup lang="ts">
import LoadingState from '../../components/base/LoadingState.vue'
import PageState from '../../components/base/PageState.vue'
import { computed, nextTick, onMounted, onScopeDispose, reactive, ref, useId, watch } from 'vue'
import { ElMessageBox } from 'element-plus'
import { managedReportApi, type ReportAccount } from '../../api/managed-report'
import { sourceCatalogApi, type SourceDefinition, type DefinitionRevision } from '../../api/source-catalog'
import ReportAccountDetails from './ReportAccountDetails.vue'
import { reportCommandKeys } from './reportCommands'

const filterId = useId()
const accounts = ref<ReportAccount[]>([])
const loading = ref(true)
const listError = ref('')
const keyword = ref('')
const sourceFilter = ref('')
const statusFilter = ref('')
const page = ref(1)
const pageSize = 20
const sourceNames = computed(() => [...new Set(accounts.value.map(account => account.source_name))]
  .sort((a, b) => a.localeCompare(b, 'zh-CN')))
const filtered = computed(() => {
  const search = keyword.value.trim().toLocaleLowerCase()
  return accounts.value.filter(account =>
    (!search || [account.name, account.username].some(value => value.toLocaleLowerCase().includes(search))) &&
    (!sourceFilter.value || account.source_name === sourceFilter.value) &&
    (!statusFilter.value || account.enabled === (statusFilter.value === 'enabled')),
  )
})
const totalPages = computed(() => Math.max(1, Math.ceil(filtered.value.length / pageSize)))
const pageAccounts = computed(() => filtered.value.slice((page.value - 1) * pageSize, page.value * pageSize))
const selectedId = ref<number>()
const selectedAccount = computed(() => filtered.value.find(account => account.id === selectedId.value))
const desktop = ref(false)
const drawerOpen = ref(false)
const searchInput = ref<HTMLInputElement>()
let detailTrigger: HTMLElement | null = null
let restoreFocus = false

const busy = ref(false)
const actionError = ref('')
const actionMessage = ref('')
const editorOpen = ref(false)
const saving = ref(false)
const formError = ref('')
const rotating = ref<ReportAccount>()
const form = reactive({ name: '', definition_revision_id: undefined as number | undefined, username: '', password: '' })
const sources = ref<SourceDefinition[]>([])
const revisions = ref<DefinitionRevision[]>([])
const familySource = ref<number>()
const sourcesLoading = ref(false)
const revisionsLoading = ref(false)
const sourcesError = ref('')
const revisionsError = ref('')
const revisionPage = ref(1)
const revisionTotal = ref(0)
const sourceKeyword = ref('')
const keys = reportCommandKeys()
let alive = true
let sourceGeneration = 0
let revisionGeneration = 0
let listGeneration = 0

const errorText = (error: unknown) => error instanceof Error ? error.message : '操作失败，请刷新状态后核对'

async function load() {
  const generation = ++listGeneration
  loading.value = true
  listError.value = ''
  try {
    const data = await managedReportApi.accounts()
    if (!alive || generation !== listGeneration) return
    accounts.value = data.items
    if (sourceFilter.value && !sourceNames.value.includes(sourceFilter.value)) sourceFilter.value = ''
  } catch (error) {
    if (!alive || generation !== listGeneration) return
    accounts.value = []
    listError.value = errorText(error)
  } finally {
    if (alive && generation === listGeneration) loading.value = false
  }
}

function selectAccount(account: ReportAccount, event: MouseEvent) {
  if (selectedId.value !== account.id) {
    actionError.value = ''
    actionMessage.value = ''
  }
  selectedId.value = account.id
  if (!desktop.value) {
    detailTrigger = event.currentTarget as HTMLElement
    restoreFocus = true
    drawerOpen.value = true
  }
}

async function restoreDetailFocus() {
  if (!restoreFocus || drawerOpen.value) return
  restoreFocus = false
  await nextTick()
  if (!alive || desktop.value) return
  if (detailTrigger?.isConnected && !detailTrigger.hasAttribute('disabled')) detailTrigger.focus()
  else searchInput.value?.focus()
  detailTrigger = null
}

const syncViewport = (event: MediaQueryList | MediaQueryListEvent) => {
  desktop.value = event.matches
  if (event.matches) {
    restoreFocus = false
    drawerOpen.value = false
    detailTrigger = null
  }
}

async function searchSources(keyword = '') {
  if (!editorOpen.value || rotating.value) return
  sourceKeyword.value = keyword
  const generation = ++sourceGeneration
  sourcesLoading.value = true
  sourcesError.value = ''
  try {
    const data = await sourceCatalogApi.list({ keyword, page: 1, page_size: 100 }, true)
    if (!alive || generation !== sourceGeneration) return
    sources.value = data.items.filter(source => source.visibility === 'global' && source.enabled &&
      ['postgres', 'mysql', 'jdbc'].includes(source.config.type))
  } catch (error) {
    if (!alive || generation !== sourceGeneration) return
    sources.value = []
    sourcesError.value = errorText(error)
  } finally {
    if (alive && generation === sourceGeneration) sourcesLoading.value = false
  }
}

async function loadRevisions(targetPage = 1) {
  const source = familySource.value
  if (!source) return
  const generation = ++revisionGeneration
  revisionPage.value = targetPage
  revisionsLoading.value = true
  revisionsError.value = ''
  revisions.value = []
  form.definition_revision_id = undefined
  try {
    const data = await sourceCatalogApi.revisions(source, targetPage, true)
    if (!alive || generation !== revisionGeneration) return
    revisions.value = data.items.filter(item => item.visibility === 'global')
    revisionTotal.value = data.total
    form.definition_revision_id = revisions.value.find(item => item.enabled)?.id
  } catch (error) {
    if (alive && generation === revisionGeneration) revisionsError.value = errorText(error)
  } finally {
    if (alive && generation === revisionGeneration) revisionsLoading.value = false
  }
}

function create() {
  rotating.value = undefined
  Object.assign(form, { name: '', username: '', password: '', definition_revision_id: undefined })
  familySource.value = undefined
  sources.value = []
  revisions.value = []
  revisionTotal.value = 0
  formError.value = ''
  editorOpen.value = true
  void searchSources()
}

function rotate(account: ReportAccount) {
  rotating.value = account
  form.password = ''
  formError.value = ''
  editorOpen.value = true
}

async function save() {
  if (saving.value || sourcesLoading.value || revisionsLoading.value) return
  const account = rotating.value
  const revision = revisions.value.find(item => item.id === form.definition_revision_id && item.enabled)
  formError.value = ''
  if (!form.password || (!account && (!form.name.trim() || !form.username.trim() || !revision))) {
    formError.value = '请填写完整的账号配置'
    return
  }
  saving.value = true
  try {
    if (account) {
      const payload = { version: account.version, password: form.password }
      await managedReportApi.updateAccount(account.id, payload, keys.get(`rotate:${account.id}`, payload))
    } else if (revision) {
      const payload = { name: form.name.trim(), definition_revision_id: revision.id, username: form.username.trim(), password: form.password }
      await managedReportApi.createAccount(payload, keys.get('create', payload))
    }
    if (!alive) return
    editorOpen.value = false
    await load()
  } catch (error) {
    if (alive) formError.value = errorText(error)
  } finally {
    if (alive) saving.value = false
  }
}

async function setEnabled(account: ReportAccount) {
  if (busy.value) return
  busy.value = true
  try {
    await ElMessageBox.confirm(
      account.enabled ? `停用后，当前 ${account.report_count} 份已发布报表将无法查询。确认停用？` : `确认启用「${account.name}」？`,
      '执行账号状态', { confirmButtonText: '确认', cancelButtonText: '取消', type: 'warning' },
    )
    if (!alive) return
    actionError.value = ''
    actionMessage.value = ''
    const payload = {
      version: account.version,
      enabled: !account.enabled,
      ...(account.enabled ? { expected_reports: account.report_count } : {}),
    }
    await managedReportApi.updateAccount(account.id, payload, keys.get(`status:${account.id}`, payload))
    if (alive) await load()
  } catch (error) {
    if (alive && error !== 'cancel' && error !== 'close') actionError.value = errorText(error)
  } finally {
    if (alive) busy.value = false
  }
}

async function test(account: ReportAccount) {
  if (busy.value || !account.enabled) return
  busy.value = true
  actionError.value = ''
  actionMessage.value = ''
  try {
    await managedReportApi.testAccount(account.id)
    if (alive && selectedId.value === account.id) actionMessage.value = `「${account.name}」连接及强制只读查询验证通过`
  } catch (error) {
    if (alive && selectedId.value === account.id) actionError.value = errorText(error)
  } finally {
    if (alive) busy.value = false
  }
}

watch([keyword, sourceFilter, statusFilter], () => { page.value = 1 })
watch(totalPages, count => { page.value = Math.min(page.value, count) })
watch(selectedAccount, account => {
  if (!account) {
    drawerOpen.value = false
    selectedId.value = undefined
  }
})
watch(editorOpen, open => {
  if (open) return
  form.password = ''
  keys.clear()
  sourceGeneration++
  revisionGeneration++
  sourcesLoading.value = false
  revisionsLoading.value = false
  sourcesError.value = ''
  revisionsError.value = ''
}, { flush: 'sync' })

let viewport: MediaQueryList | undefined
onMounted(() => {
  viewport = window.matchMedia('(min-width: 1024px)')
  syncViewport(viewport)
  viewport.addEventListener('change', syncViewport)
  void load()
})
onScopeDispose(() => {
  alive = false
  viewport?.removeEventListener('change', syncViewport)
  form.password = ''
  keys.clear()
})
</script>

<template>
  <section class="min-w-0 space-y-4" aria-label="报表执行账号管理">
    <div class="flex flex-wrap items-center justify-between gap-3">
      <p class="text-sm text-muted">维护数据库只读账号，供经过审批的正式报表使用。</p>
      <div class="flex flex-wrap gap-2">
        <button type="button" class="ghost-button ui-control-target" :disabled="busy || loading || saving" @click="load">刷新</button>
        <button type="button" class="liquid-button ui-control-target" :disabled="busy || saving" @click="create">新增执行账号</button>
      </div>
    </div>
    <div class="ui-filter-bar" role="search" aria-label="执行账号筛选">
      <div class="ui-filter-field ui-filter-search">
        <label :for="`${filterId}-search`" class="text-sm text-muted">搜索执行账号</label>
        <input :id="`${filterId}-search`" ref="searchInput" v-model="keyword" type="search" class="glass-input w-full" placeholder="账号名称或数据库用户名" />
      </div>
      <div class="ui-filter-field">
        <label :for="`${filterId}-source`" class="text-sm text-muted">所属数据源</label>
        <el-select :id="`${filterId}-source`" v-model="sourceFilter" aria-label="所属数据源" placeholder="全部数据源"
          class="w-full" :disabled="loading || !!listError">
          <el-option label="全部数据源" value="" />
          <el-option v-for="name in sourceNames" :key="name" :label="name" :value="name" />
        </el-select>
      </div>
      <div class="ui-filter-field">
        <label :for="`${filterId}-status`" class="text-sm text-muted">状态</label>
        <el-select :id="`${filterId}-status`" v-model="statusFilter" aria-label="状态" placeholder="全部状态" class="w-full">
          <el-option label="全部状态" value="" />
          <el-option label="已启用" value="enabled" />
          <el-option label="已停用" value="disabled" />
        </el-select>
      </div>
    </div>

    <div :aria-busy="loading">
      <LoadingState v-if="loading && !drawerOpen" label="正在加载执行账号…" />
      <PageState v-else-if="listError" title="无法读取执行账号" :description="listError" tone="danger">
        <template #actions><button type="button" class="ghost-button ui-control-target" @click="load">重试</button></template>
      </PageState>
      <PageState v-else-if="!loading && !accounts.length" title="暂无执行账号" description="新增执行账号后，获准的正式报表可选用它。" />
      <PageState v-else-if="!loading && !filtered.length" title="没有匹配的执行账号" description="请调整账号名称、数据源或状态筛选。" />
      <div v-else class="account-layout">
        <div class="min-w-0 space-y-3">
          <div class="editorial-table-wrap" tabindex="0" role="region" aria-label="执行账号表格">
            <table class="editorial-table" aria-label="执行账号列表">
              <thead>
                <tr><th scope="col">序号</th><th scope="col">账号名称</th><th scope="col">所属数据源</th><th scope="col">状态</th></tr>
              </thead>
              <tbody>
                <tr v-for="(account, index) in pageAccounts" :key="account.id" :class="{ 'account-selected': selectedAccount?.id === account.id }">
                  <td class="editorial-table__number">{{ String((page - 1) * pageSize + index + 1).padStart(2, '0') }}</td>
                  <td class="account-name">
                    <button type="button" class="account-link ui-control-target" :aria-current="selectedAccount?.id === account.id ? 'true' : undefined"
                      :aria-haspopup="desktop ? undefined : 'dialog'" :disabled="busy" @click="selectAccount(account, $event)">
                      {{ account.name }}
                    </button>
                  </td>
                  <td class="break-words">{{ account.source_name }}</td>
                  <td class="whitespace-nowrap" :class="account.enabled ? 'text-success' : 'text-muted'">{{ account.enabled ? '已启用' : '已停用' }}</td>
                </tr>
              </tbody>
            </table>
          </div>
          <nav class="flex flex-wrap items-center justify-between gap-2 text-sm text-muted" aria-label="执行账号分页">
            <span>共 {{ filtered.length }} 项</span>
            <div class="flex flex-wrap items-center gap-2">
              <button type="button" class="ghost-button ui-control-target" :disabled="page <= 1" @click="page--">上一页</button>
              <span>第 {{ page }} / {{ totalPages }} 页</span>
              <button type="button" class="ghost-button ui-control-target" :disabled="page >= totalPages" @click="page++">下一页</button>
            </div>
          </nav>
        </div>
        <div v-if="desktop" class="account-detail-panel">
          <ReportAccountDetails v-if="selectedAccount" :account="selectedAccount" :busy="busy || loading || saving"
            :message="actionMessage" :error="actionError" @test="test(selectedAccount)"
            @rotate="rotate(selectedAccount)" @toggle="setEnabled(selectedAccount)" />
          <p v-else class="text-sm text-muted">请选择一个执行账号查看详情。</p>
        </div>
      </div>
    </div>
    <el-drawer v-model="drawerOpen" title="执行账号详情" size="100%" append-to-body destroy-on-close
      :close-on-press-escape="!busy" :close-on-click-modal="!busy" :show-close="!busy" @closed="restoreDetailFocus">
      <ReportAccountDetails v-if="selectedAccount" :account="selectedAccount" :busy="busy || loading || saving"
        :message="actionMessage" :error="actionError" embedded @test="test(selectedAccount)"
        @rotate="rotate(selectedAccount)" @toggle="setEnabled(selectedAccount)" />
    </el-drawer>

    <el-dialog v-model="editorOpen" :title="rotating?'轮换执行账号密码':'新增报表执行账号'" width="min(600px, calc(100vw - 24px))" class="glass-dialog" append-to-body :close-on-click-modal="false" :close-on-press-escape="!saving" :show-close="!saving">
      <form class="space-y-4" @submit.prevent="save">
        <template v-if="!rotating">
          <label class="block text-xs text-muted space-y-1">账号名称<el-input v-model="form.name" aria-label="执行账号名称" maxlength="100" :disabled="saving" /></label>
          <label class="block text-xs text-muted space-y-1">数据源<el-select v-model="familySource" aria-label="执行账号数据源" filterable remote :remote-method="searchSources" :loading="sourcesLoading" class="w-full" :disabled="saving" @change="loadRevisions(1)"><el-option v-for="source in sources" :key="source.id" :value="source.id" :label="source.name" /></el-select></label>
          <div v-if="sourcesError" class="flex flex-wrap items-center gap-2">
            <p role="alert" class="text-sm text-danger">{{ sourcesError }}</p>
            <button type="button" class="ghost-button ui-control-target" @click="searchSources(sourceKeyword)">重试读取数据源</button>
          </div>
          <label v-if="familySource" class="block text-xs text-muted space-y-1">固定修订<el-select v-model="form.definition_revision_id" aria-label="执行账号修订" class="w-full" :loading="revisionsLoading" :disabled="saving || revisionsLoading || !!revisionsError"><el-option v-for="source in revisions" :key="source.id" :value="source.id" :label="`修订 ${source.revision} · ${source.enabled?'启用':'停用'}`" :disabled="!source.enabled" /></el-select></label>
          <div v-if="revisionsError" class="flex flex-wrap items-center gap-2">
            <p role="alert" class="text-sm text-danger">{{ revisionsError }}</p>
            <button type="button" class="ghost-button ui-control-target" @click="loadRevisions(revisionPage)">重试读取修订</button>
          </div>
          <div v-if="revisionTotal>10" class="flex gap-2 text-xs"><button type="button" class="ghost-button ui-control-target" :disabled="revisionPage<=1 || saving || revisionsLoading" @click="loadRevisions(revisionPage-1)">较新修订</button><button type="button" class="ghost-button ui-control-target" :disabled="revisionPage*10>=revisionTotal || saving || revisionsLoading" @click="loadRevisions(revisionPage+1)">较早修订</button></div>
          <label class="block text-xs text-muted space-y-1">数据库用户名<el-input v-model="form.username" aria-label="执行账号用户名" maxlength="128" autocomplete="off" :disabled="saving" /></label>
        </template>
        <p v-else class="text-sm text-muted">{{ rotating.name }} · {{ rotating.username }}。保持数据库身份不变，仅更新密码。</p>
        <label class="block text-xs text-muted space-y-1">数据库密码<el-input v-model="form.password" aria-label="执行账号密码" type="password" show-password autocomplete="new-password" :disabled="saving" /></label>
        <p class="text-xs text-muted">密码加密保存。请在数据库中限制账号的读取范围；正式报表必须使用选定账号验证成功，才能批准发布。</p>
        <p v-if="formError" role="alert" class="text-sm text-danger">{{ formError }}</p>
        <div class="flex justify-end gap-2"><button type="button" class="ghost-button ui-control-target" :disabled="saving" @click="editorOpen=false">取消</button><button class="liquid-button ui-control-target" :disabled="saving || sourcesLoading || revisionsLoading">{{ saving?'保存中…':'保存账号' }}</button></div>
      </form>
    </el-dialog>
  </section>
</template>

<style scoped>
.account-layout { display: grid; grid-template-columns: minmax(0, 1fr); gap: 16px; }
.account-selected, .editorial-table tbody tr.account-selected:hover { background: var(--db-selected); }
.account-name { min-width: 8rem; }
.account-link { text-align: start; color: var(--db-accent); font-family: var(--db-font-editorial); overflow-wrap: anywhere; }
.account-link:hover { text-decoration: underline; text-underline-offset: 3px; }
.account-link:disabled { cursor: not-allowed; }
.account-detail-panel { border: 1px solid var(--db-divider); background: var(--db-surface-data); padding: 16px; min-width: 0; }
@media (min-width: 1024px) {
  .account-layout { grid-template-columns: minmax(0, 3fr) minmax(0, 2fr); align-items: start; }
}
</style>
