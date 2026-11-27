<script setup lang="ts">
import { computed, nextTick, onMounted, onScopeDispose, ref, useId, watch } from 'vue'
import { ElMessageBox } from 'element-plus'
import { Plus } from 'lucide-vue-next'
import { proxyApi, type ProxyInput, type ProxyItem } from '../../api/proxy'
import LoadingState from '../../components/base/LoadingState.vue'
import PageHeader from '../../components/base/PageHeader.vue'
import PageState from '../../components/base/PageState.vue'
import { pageTitles } from '../../navigation'
import { useUserStore } from '../../stores/user'
import { featureAllowed } from '../../utils/permission'
import ProxyEditorForm, { type ProxyDraft } from './ProxyEditorForm.vue'

defineProps<{ embedded?: boolean }>()

const user = useUserStore()
const canWrite = computed(() => featureAllowed(user, 'connection.proxy.manage', user.role !== 'readonly'))
const protocols = [
  { value: 'http', label: 'HTTP CONNECT' },
  { value: 'https', label: 'HTTPS CONNECT' },
  { value: 'socks5', label: 'SOCKS5' },
  { value: 'db_proxy', label: 'DB Proxy' },
] as const
const protocolLabel = (value: string) => protocols.find(item => item.value === value)?.label ?? value
const emptyDraft = (): ProxyDraft => ({ name: '', type: 'http', host: '', port: 8080, username: '', password: '', description: '' })

const items = ref<ProxyItem[]>([])
const loading = ref(true)
const listError = ref('')
const keyword = ref('')
const protocol = ref('')
const status = ref('')
const page = ref(1)
const pageSize = 20
const filtered = computed(() => {
  const search = keyword.value.trim().toLocaleLowerCase()
  return items.value.filter(item =>
    (!search || [item.name, item.host].some(value => value.toLocaleLowerCase().includes(search))) &&
    (!protocol.value || item.type === protocol.value) &&
    (!status.value || item.status === status.value),
  )
})
const totalPages = computed(() => Math.max(1, Math.ceil(filtered.value.length / pageSize)))
const visibleItems = computed(() => filtered.value.slice((page.value - 1) * pageSize, page.value * pageSize))
watch([keyword, protocol, status], () => { page.value = 1 })
watch(totalPages, count => { page.value = Math.min(page.value, count) })

const id = useId()
const searchInput = ref<HTMLInputElement>()
const desktop = ref(false)
const editorOpen = ref(false)
const editorForm = ref<{ focus: () => void }>()
const editingId = ref<number>()
const editing = computed(() => editingId.value !== undefined)
const draft = ref(emptyDraft())
const saving = ref(false)
const editorError = ref('')
const activeAction = ref<number>()
const actionNotice = ref('')
const feedback = ref<Record<number, { proxy: ProxyItem; message: string; failed: boolean }>>({})
const busy = computed(() => loading.value || saving.value || activeAction.value !== undefined)
const errorText = (error: unknown, fallback: string) => error instanceof Error ? error.message : fallback
const localTime = (value: string) => Number.isFinite(Date.parse(value)) ? new Date(value).toLocaleString() : '—'
let editorTrigger: HTMLElement | undefined
let viewport: MediaQueryList | undefined
let alive = true

function sameConfiguration(old: ProxyItem, current: ProxyItem) {
  return old.updated_at === current.updated_at && old.host === current.host &&
    old.port === current.port && old.type === current.type && old.status === current.status &&
    old.username === current.username && old.has_password === current.has_password
}

async function reload() {
  loading.value = true
  listError.value = ''
  try {
    const response = await proxyApi.list(true)
    if (!alive) return
    items.value = response.items ?? []
    const currentFeedback: typeof feedback.value = {}
    for (const proxy of items.value) {
      const previous = feedback.value[proxy.id]
      if (previous && sameConfiguration(previous.proxy, proxy)) currentFeedback[proxy.id] = previous
    }
    feedback.value = currentFeedback
  } catch (error) {
    if (!alive) return
    items.value = []
    feedback.value = {}
    listError.value = errorText(error, '暂时无法读取代理配置')
  } finally {
    if (alive) loading.value = false
  }
}

function openEditor(event: MouseEvent, proxy?: ProxyItem) {
  if (!canWrite.value || busy.value || editorOpen.value) return
  editorTrigger = event.currentTarget as HTMLElement
  editingId.value = proxy?.id
  draft.value = proxy ? {
    name: proxy.name, type: proxy.type, host: proxy.host, port: proxy.port,
    username: proxy.username ?? '', password: '', description: proxy.description,
  } : emptyDraft()
  actionNotice.value = ''
  editorError.value = ''
  editorOpen.value = true
  if (desktop.value) void nextTick(() => editorForm.value?.focus())
}

async function restoreEditorFocus() {
  if (!alive || editorOpen.value || !editorTrigger) return
  const trigger = editorTrigger
  editorTrigger = undefined
  await nextTick()
  if (!alive || editorOpen.value) return
  if (trigger.isConnected && !trigger.hasAttribute('disabled')) trigger.focus()
  else searchInput.value?.focus()
}

function closeEditor() {
  if (saving.value) return
  editorOpen.value = false
  if (desktop.value) void restoreEditorFocus()
}

async function save() {
  if (!editorOpen.value || !canWrite.value || saving.value) return
  const { name, host, port, type, username, password, description } = draft.value
  editorError.value = ''
  if (!name.trim() || !host.trim() || typeof port !== 'number' || !Number.isInteger(port) || port < 1 || port > 65535) {
    editorError.value = '请填写代理名称、主机和 1 至 65535 的端口。'
    return
  }
  const payload: ProxyInput = {
    name: name.trim(), type, host: host.trim(), port, username: username.trim(),
    password: password || undefined, description: description.trim(),
  }
  const proxyId = editingId.value
  saving.value = true
  try {
    if (proxyId === undefined) await proxyApi.create(payload)
    else await proxyApi.update(proxyId, payload)
    if (!alive) return
    if (proxyId !== undefined) delete feedback.value[proxyId]
    actionNotice.value = proxyId === undefined ? '代理已创建' : '代理配置已保存'
    editorOpen.value = false
    await reload()
  } catch (error) {
    if (alive) editorError.value = errorText(error, '无法保存代理配置')
  } finally {
    if (alive) {
      saving.value = false
      if (desktop.value && !editorOpen.value) void restoreEditorFocus()
    }
  }
}

async function remove(proxy: ProxyItem) {
  if (!canWrite.value || busy.value || editorOpen.value) return
  activeAction.value = proxy.id
  try {
    await ElMessageBox.confirm(`确认删除代理「${proxy.name}」？`, '删除确认', {
      type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消',
    })
    if (!alive || !canWrite.value) return
    actionNotice.value = ''
    await proxyApi.remove(proxy.id)
    if (!alive) return
    actionNotice.value = '代理已删除'
    await reload()
    await nextTick()
    if (alive) searchInput.value?.focus()
  } catch (error) {
    if (alive && error !== 'cancel' && error !== 'close') {
      feedback.value[proxy.id] = { proxy, message: errorText(error, '无法删除代理'), failed: true }
    }
  } finally {
    if (alive) activeAction.value = undefined
  }
}

function validTarget(value: string) {
  if (typeof value !== 'string') return false
  const match = /^([^:\s]+):(\d+)$/.exec(value.trim())
  if (!match) return false
  const port = Number(match[2])
  return port >= 1 && port <= 65535
}

async function testProxy(proxy: ProxyItem, event: MouseEvent) {
  if (busy.value || editorOpen.value) return
  const trigger = event.currentTarget
  const returnFocus = trigger instanceof HTMLButtonElement && document.activeElement === trigger
  activeAction.value = proxy.id
  try {
    let target_host: string | undefined
    let target_port: number | undefined
    if (proxy.type !== 'db_proxy') {
      const input = await ElMessageBox.prompt('输入可通过该代理访问的目标，格式 host:port。', '测试代理', {
        inputPlaceholder: 'database.example.invalid:5432',
        inputValidator: (value: string) => validTarget(value) || '请输入主机和 1 至 65535 的端口',
        confirmButtonText: '测试', cancelButtonText: '取消',
      })
      const [host, port] = input.value.trim().split(':')
      target_host = host
      target_port = Number(port)
    }
    if (!alive) return
    actionNotice.value = ''
    delete feedback.value[proxy.id]
    const result = await proxyApi.test({
      id: proxy.id, type: proxy.type, host: proxy.host, port: proxy.port, target_host, target_port,
    })
    if (!alive) return
    feedback.value[proxy.id] = {
      proxy, failed: !result.ok, message: result.ok ? `本次拨号成功，耗时 ${result.latency_ms} ms` : '本次代理拨号未通过',
    }
  } catch (error) {
    if (alive && error !== 'cancel' && error !== 'close') {
      feedback.value[proxy.id] = { proxy, message: errorText(error, '代理拨号失败'), failed: true }
    }
  } finally {
    if (alive) {
      activeAction.value = undefined
      await nextTick()
      if (alive && returnFocus && trigger.isConnected && document.activeElement === document.body) trigger.focus()
    }
  }
}

watch(editorOpen, open => { if (!open) draft.value.password = '' }, { flush: 'sync' })
const syncViewport = (event: MediaQueryList | MediaQueryListEvent) => { desktop.value = event.matches }
onMounted(() => {
  viewport = window.matchMedia('(min-width: 1024px)')
  syncViewport(viewport)
  viewport.addEventListener('change', syncViewport)
  void reload()
})
onScopeDispose(() => {
  alive = false
  draft.value.password = ''
  viewport?.removeEventListener('change', syncViewport)
})
</script>

<template>
  <section class="ui-page-stack min-w-0" aria-label="个人代理管理">
    <PageHeader :title="embedded ? '我的代理' : pageTitles.proxies" :heading="embedded ? 'h2' : 'h1'"
      description="管理自己的代理配置，供支持代理的数据源连接使用。">
      <template #actions>
        <button type="button" class="ghost-button ui-control-target" :disabled="busy || editorOpen" @click="reload">刷新</button>
        <button v-if="canWrite" type="button" class="liquid-button ui-control-target inline-flex items-center gap-2"
          :disabled="busy || editorOpen" @click="openEditor($event)">
          <Plus class="h-4 w-4" aria-hidden="true" />新建代理
        </button>
      </template>
    </PageHeader>
    <p v-if="actionNotice" role="status" class="text-sm text-success">{{ actionNotice }}</p>
    <div class="proxy-layout" :class="{ 'proxy-layout--editing': editorOpen && desktop }">
      <section class="min-w-0 space-y-4" aria-label="代理目录">
        <div class="ui-filter-bar" role="search" aria-label="筛选代理">
          <div class="ui-filter-field ui-filter-search">
            <label :for="`${id}-search`" class="text-sm text-muted">搜索代理</label>
            <input :id="`${id}-search`" ref="searchInput" v-model="keyword" type="search"
              class="glass-input w-full" placeholder="代理名称或主机" />
          </div>
          <div class="ui-filter-field">
            <label :for="`${id}-protocol`" class="text-sm text-muted">协议</label>
            <el-select :id="`${id}-protocol`" v-model="protocol" placeholder="全部协议" class="w-full">
              <el-option label="全部协议" value="" />
              <el-option v-for="item in protocols" :key="item.value" :label="item.label" :value="item.value" />
            </el-select>
          </div>
          <div class="ui-filter-field">
            <label :for="`${id}-status`" class="text-sm text-muted">状态</label>
            <el-select :id="`${id}-status`" v-model="status" placeholder="全部状态" class="w-full">
              <el-option label="全部状态" value="" />
              <el-option label="已启用" value="active" />
              <el-option label="已停用" value="inactive" />
            </el-select>
          </div>
        </div>
        <LoadingState v-if="loading" label="正在读取代理配置…" />
        <PageState v-else-if="listError" title="无法读取代理配置" :description="listError" tone="danger">
          <template #actions><button type="button" class="ghost-button ui-control-target" @click="reload">重试</button></template>
        </PageState>
        <PageState v-else-if="!items.length" title="暂无代理" description="保存的个人代理将显示在此处。" />
        <PageState v-else-if="!filtered.length" title="没有匹配的代理" description="请调整搜索、协议或状态筛选。" />
        <template v-else>
          <div class="editorial-table-wrap" role="region" tabindex="0" aria-label="代理表格">
            <table class="editorial-table proxy-table" aria-label="我的代理列表">
              <thead><tr>
                <th scope="col">代理名称</th><th scope="col">协议</th><th scope="col">状态</th>
                <th scope="col">配置更新</th><th scope="col">操作</th>
              </tr></thead>
              <tbody>
                <tr v-for="proxy in visibleItems" :key="proxy.id" :class="{ 'proxy-selected': editorOpen && editingId === proxy.id }">
                  <th scope="row" class="proxy-name">
                    <span class="font-editorial text-sm font-semibold text-content break-words">{{ proxy.name }}</span>
                    <div class="editorial-table__meta">
                      {{ proxy.host }} · {{ proxy.port }}<span v-if="proxy.has_password"> · 已保存密码</span>
                    </div>
                    <div v-if="proxy.description" class="editorial-table__meta">{{ proxy.description }}</div>
                    <p v-if="feedback[proxy.id]" :role="feedback[proxy.id]?.failed ? 'alert' : 'status'"
                      class="mt-1 whitespace-normal break-words" :class="feedback[proxy.id]?.failed ? 'text-danger' : 'text-success'">
                      {{ feedback[proxy.id]?.message }}
                    </p>
                  </th>
                  <td class="whitespace-nowrap">{{ protocolLabel(proxy.type) }}</td>
                  <td class="whitespace-nowrap" :class="proxy.status === 'active' ? 'text-success' : 'text-muted'">
                    {{ proxy.status === 'active' ? '已启用' : proxy.status === 'inactive' ? '已停用' : '未知' }}
                  </td>
                  <td class="whitespace-nowrap text-sm text-muted">{{ localTime(proxy.updated_at) }}</td>
                  <td>
                    <div class="flex flex-wrap gap-1">
                      <button type="button" class="ghost-button ui-control-target aria-disabled:opacity-50"
                        :aria-disabled="busy || editorOpen || undefined"
                        :aria-label="`测试代理 ${proxy.name}`" @click="testProxy(proxy, $event)">测试</button>
                      <button v-if="canWrite" type="button" class="ghost-button ui-control-target" :disabled="busy || editorOpen"
                        :aria-label="`编辑代理 ${proxy.name}`" @click="openEditor($event, proxy)">编辑</button>
                      <button v-if="canWrite" type="button" class="ghost-button ui-control-target" :disabled="busy || editorOpen"
                        :aria-label="`删除代理 ${proxy.name}`" @click="remove(proxy)">删除</button>
                    </div>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
          <nav aria-label="代理列表分页" class="flex flex-wrap items-center justify-between gap-2 text-sm text-muted">
            <span>共 {{ filtered.length }} 项</span>
            <div class="flex flex-wrap items-center gap-2">
              <button type="button" class="ghost-button ui-control-target" :disabled="page <= 1" @click="page--">上一页</button>
              <span>第 {{ page }} / {{ totalPages }} 页</span>
              <button type="button" class="ghost-button ui-control-target" :disabled="page >= totalPages" @click="page++">下一页</button>
            </div>
          </nav>
        </template>
      </section>
      <section v-if="desktop && editorOpen" class="proxy-editor space-y-4" aria-label="代理编辑器" @keydown.esc.prevent="closeEditor">
        <h3 class="font-editorial text-lg font-semibold">{{ editing ? '编辑代理' : '新建代理' }}</h3>
        <ProxyEditorForm ref="editorForm" v-model="draft" :editing="editing" :saving="saving" :error="editorError" :protocols="protocols"
          @save="save" @cancel="closeEditor" />
      </section>
    </div>
    <el-drawer v-if="!desktop" v-model="editorOpen" :title="editing ? '编辑代理' : '新建代理'" size="100%"
      append-to-body destroy-on-close :close-on-click-modal="false" :close-on-press-escape="!saving" :show-close="!saving"
      @opened="editorForm?.focus()" @closed="restoreEditorFocus">
      <ProxyEditorForm ref="editorForm" v-model="draft" :editing="editing" :saving="saving" :error="editorError" :protocols="protocols"
        @save="save" @cancel="closeEditor" />
    </el-drawer>
  </section>
</template>

<style scoped>
.proxy-layout { display: grid; grid-template-columns: minmax(0, 1fr); gap: 16px; align-items: start; }
.proxy-layout--editing { grid-template-columns: minmax(0, 3fr) minmax(0, 2fr); }
.proxy-table { min-width: 540px; }
.proxy-table .proxy-name { min-width: 8rem; max-width: 20rem; white-space: normal; }
.proxy-table .proxy-selected, .proxy-table tbody tr.proxy-selected:hover { background: var(--db-selected); }
.proxy-table tbody tr:last-child th { border-bottom: 0; }
.proxy-editor { border: 1px solid var(--db-divider); background: var(--db-surface-data); padding: 16px; min-width: 0; }
</style>
