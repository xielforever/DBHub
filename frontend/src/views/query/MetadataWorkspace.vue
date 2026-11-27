<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import LoadingState from '../../components/base/LoadingState.vue'
import PageState from '../../components/base/PageState.vue'
import type { ConnectionItem } from '../../api/datasource'
import { workbenchApi, type ColumnInfo, type TableInfo } from '../../api/workbench'
import { automationApi } from '../../api/automation'
import { useUserStore } from '../../stores/user'
import { featureAllowed } from '../../utils/permission'
import { reportCommandKeys } from '../report/reportCommands'
import { useReportRun } from '../report/useReportRun'

const props = defineProps<{ source: ConnectionItem; database: string }>()
const emit = defineEmits<{ blocked: [reason: string] }>()
const user = useUserStore()
const canCollect = computed(() => !!props.source.capabilities?.collect &&
  (featureAllowed(user, 'connection.manage', ['admin', 'developer'].includes(user.role)) ||
    featureAllowed(user, 'source.definition.manage', user.hasCapability('datasource_manage'))))
const schemaList = useReportRun<string[]>()
const objects = useReportRun<TableInfo[]>()
const fields = useReportRun<ColumnInfo[]>()
const collection = useReportRun<Awaited<ReturnType<typeof automationApi.createTask>>>()
const collectionReceipt = ref<HTMLElement>()
const collectionTask = computed(() => collection.state.value.status === 'success' ? collection.state.value.data : undefined)
const schema = ref('')
const selectedKey = ref('')
const objectNotice = ref('')
const search = ref('')
const type = ref('')
const fieldRefreshButton = ref<HTMLButtonElement>()
const fieldLoading = computed(() => fields.state.value.status === 'loading')
const requestKeys = reportCommandKeys()
const schemas = computed(() => schemaList.state.value.status === 'success' ? schemaList.state.value.data : [])
const tables = computed(() => objects.state.value.status === 'success' ? objects.state.value.data : [])
const objectKey = (item: TableInfo) => JSON.stringify([item.schema, item.name, item.type])
const selected = computed(() => tables.value.find(item => objectKey(item) === selectedKey.value))
const filtered = computed(() => {
  const needle = search.value.trim().toLocaleLowerCase()
  return tables.value.filter(item => (!type.value || item.type === type.value) && item.name.toLocaleLowerCase().includes(needle))
})
const typeLabel = (kind: string) => kind === 'view' ? '视图' : kind === 'table' ? '表' : kind
const collecting = computed(() => collection.state.value.status === 'loading')
watch(collecting, value => emit('blocked', value ? '采集任务提交中，暂不能切换目标' : ''), { flush: 'sync' })

function clearObject() {
  selectedKey.value = ''
  objectNotice.value = ''
  fields.reset()
}

async function loadSchemas() {
  objects.reset()
  clearObject()
  schema.value = ''
  const loaded = await schemaList.run(props.database, async signal => {
    const response = await workbenchApi.schemas(props.source.id, props.database, { signal, silentError: true })
    return response.items?.length ? response.items : ['']
  })
  if (!loaded || schemaList.state.value.status !== 'success') return
  schema.value = schemas.value[0] ?? ''
  void loadObjects()
}

async function loadObjects(preserveSelection = false) {
  if (preserveSelection) fields.reset()
  else clearObject()
  const params = { database: props.database, schema: schema.value }
  const loaded = await objects.run(JSON.stringify(params), async signal =>
    (await workbenchApi.tables(props.source.id, params, { signal, silentError: true })).items ?? [])
  if (!loaded || objects.state.value.status !== 'success') return
  if (preserveSelection && selectedKey.value) {
    const item = filtered.value.find(item => objectKey(item) === selectedKey.value)
    if (item) selectObject(item)
    else {
      clearObject()
      objectNotice.value = '原选中的表或视图已不可用，请从当前目录重新选择。'
    }
    return
  }
  if (!objectNotice.value && filtered.value[0]) selectObject(filtered.value[0])
}

function refreshObjects() {
  void loadObjects(true)
}

function loadFields() {
  const object = selected.value
  if (!object) return
  const params = { database: props.database, schema: object.schema || schema.value, table: object.name }
  return fields.run(JSON.stringify(params), async signal =>
    (await workbenchApi.columns(props.source.id, params, { signal, silentError: true })).items ?? [])
}

function refreshFields(event: MouseEvent) {
  if (fieldLoading.value) return
  if (event.currentTarget === document.activeElement) fieldRefreshButton.value?.focus()
  void loadFields()
}

function selectObject(item: TableInfo) {
  if (!tables.value.includes(item)) return
  objectNotice.value = ''
  selectedKey.value = objectKey(item)
  void loadFields()
}

watch([search, type], () => {
  if (!filtered.value.some(item => objectKey(item) === selectedKey.value)) clearObject()
})
watch(() => [props.source.id, props.database], () => {
  search.value = ''
  type.value = ''
  void loadSchemas()
})
async function collect(event: MouseEvent) {
  if (!canCollect.value || collecting.value) return
  const trigger = event.currentTarget
  const body = { connection_id: props.source.id, kind: 'collect' as const }
  const ok = await collection.run('collect', signal => automationApi.createTask({ ...body, request_id: requestKeys.get('collect', body) }, signal))
  if (!ok) return
  requestKeys.clear()
  await nextTick()
  if (trigger && trigger === document.activeElement) collectionReceipt.value?.focus()
}

onMounted(() => { void loadSchemas() })
onBeforeUnmount(() => requestKeys.clear())
</script>

<template>
  <section class="space-y-4 min-w-0" aria-label="元数据工作台">
    <header class="flex flex-wrap justify-between gap-3 border-b border-stroke pb-3">
      <div class="min-w-0 space-y-2">
        <h2 class="editorial-object-title">结构浏览</h2>
        <p v-if="source.capabilities?.reason" class="text-sm text-muted break-words">{{ source.capabilities.reason }}</p>
      </div>
      <button v-if="canCollect" type="button" class="ghost-button ui-control-target aria-disabled:opacity-50"
        :aria-disabled="collecting || undefined" @click="collect">{{ collecting ? '提交中…' : '采集结构' }}</button>
    </header>
    <p v-if="collectionTask" ref="collectionReceipt" tabindex="-1" class="text-sm text-success ui-focus-target [overflow-wrap:anywhere]" role="status">
      采集任务已加入后台队列 · 任务标识 {{ collectionTask.id }}。
      <RouterLink :to="{ path: '/source-tasks', query: { workspace_id: String(user.workspaceId), connection_id: String(collectionTask.connection_id), kind: collectionTask.kind } }"
        class="editorial-text-action">查看任务记录</RouterLink>
    </p>
    <p v-else-if="collection.state.value.status === 'error'" class="text-sm text-danger" role="alert">{{ collection.state.value.message || '提交结果待核对。' }}</p>

    <LoadingState v-if="schemaList.state.value.status === 'loading'" label="正在读取模式…" />
    <PageState v-else-if="schemaList.state.value.status === 'error'" heading="h3" title="读取模式失败" :description="schemaList.state.value.message || '请重新读取当前数据库模式。'" tone="danger">
      <template #actions><button class="ghost-button ui-control-target" @click="loadSchemas">重试读取模式</button></template>
    </PageState>
    <template v-else-if="schemaList.state.value.status === 'success'">
      <label class="block text-sm max-w-xs">数据库模式
        <select v-model="schema" class="glass-input block w-full mt-1" aria-label="结构 Schema" @change="loadObjects()">
          <option v-for="item in schemas" :key="item" :value="item">{{ item || '默认范围' }}</option>
        </select>
      </label>
      <div class="grid grid-cols-1 items-start gap-4 lg:grid-cols-[minmax(260px,1fr)_minmax(0,2fr)]">
        <section class="glass-panel ui-card-padding space-y-3 min-w-0" aria-labelledby="structure-objects-title">
          <div class="flex flex-wrap justify-between gap-2">
            <h3 id="structure-objects-title" class="editorial-object-title">数据库对象</h3>
            <button class="ghost-button ui-control-target" :disabled="objects.state.value.status === 'loading'" @click="refreshObjects">刷新对象</button>
          </div>
          <LoadingState v-if="objects.state.value.status === 'loading'" label="正在读取数据库对象…" />
          <PageState v-else-if="objects.state.value.status === 'error'" heading="h3" title="读取数据库对象失败" :description="objects.state.value.message || '请重新读取当前模式的对象。'" tone="danger">
            <template #actions><button class="ghost-button ui-control-target" @click="refreshObjects">重试读取对象</button></template>
          </PageState>
          <template v-else-if="objects.state.value.status === 'success'">
            <div class="grid gap-2">
              <label class="block text-sm">对象名<input v-model="search" class="glass-input block w-full mt-1" placeholder="搜索表或视图" /></label>
              <label class="block text-sm">对象类型<select v-model="type" class="glass-input block w-full mt-1"><option value="">全部类型</option><option value="table">表</option><option value="view">视图</option></select></label>
            </div>
            <p class="text-xs text-muted">显示 {{ filtered.length }} / {{ tables.length }} 项</p>
            <ul v-if="filtered.length" class="max-h-96 overflow-y-auto divide-y divide-divider">
              <li v-for="item in filtered" :key="objectKey(item)">
                <button type="button" class="query-target-option" :aria-pressed="selectedKey === objectKey(item)" :data-current="selectedKey === objectKey(item) || undefined" @click="selectObject(item)">
                  <span class="min-w-0 font-mono break-all">{{ item.name }}</span><span class="shrink-0 text-xs text-muted">{{ typeLabel(item.type) }}</span>
                </button>
              </li>
            </ul>
            <p v-else class="text-sm text-muted">{{ tables.length ? '没有匹配的表或视图。' : '当前模式没有可见对象。' }}</p>
          </template>
        </section>

        <section class="glass-panel ui-card-padding space-y-3 min-w-0" aria-labelledby="structure-detail-title">
          <div class="flex flex-wrap items-start justify-between gap-2">
            <div class="min-w-0">
              <h3 id="structure-detail-title" class="editorial-object-title break-all">{{ selected?.name || '对象字段' }}</h3>
              <p v-if="selected" class="text-sm text-muted break-all">{{ database }} · {{ selected.schema || schema || '默认范围' }} · {{ typeLabel(selected.type) }}</p>
            </div>
            <button v-if="selected" ref="fieldRefreshButton" class="ghost-button ui-control-target aria-disabled:opacity-50"
              :aria-disabled="fieldLoading || undefined" @click="refreshFields">刷新字段</button>
          </div>
          <LoadingState v-if="fields.state.value.status === 'loading'" label="正在读取字段…" />
          <PageState v-else-if="fields.state.value.status === 'error'" heading="h3" title="读取字段失败" :description="fields.state.value.message || '请重新读取当前对象字段。'" tone="danger">
            <template #actions><button class="ghost-button ui-control-target" @click="refreshFields">重试读取字段</button></template>
          </PageState>
          <template v-else-if="selected && fields.state.value.status === 'success'">
            <div v-if="fields.state.value.data.length" class="editorial-table-wrap" tabindex="0" role="region" aria-label="字段表格">
              <table class="editorial-table min-w-[600px]" aria-label="字段结构">
                <thead><tr><th scope="col">序号</th><th scope="col">字段</th><th scope="col">类型</th><th scope="col">主键</th><th scope="col">允许空值</th><th scope="col">默认值</th><th scope="col">说明</th></tr></thead>
                <tbody><tr v-for="(column, index) in fields.state.value.data" :key="column.name">
                  <td class="editorial-table__number">{{ column.ordinal || index + 1 }}</td>
                  <th scope="row" class="font-mono break-all">{{ column.name }}</th>
                  <td class="font-mono">{{ column.data_type }}</td><td>{{ column.is_primary ? '是' : '否' }}</td><td>{{ column.is_nullable ? '是' : '否' }}</td><td class="font-mono break-all">{{ column.default ?? '—' }}</td><td>{{ column.comment || '—' }}</td>
                </tr></tbody>
              </table>
            </div>
            <p v-else class="text-sm text-muted">当前对象没有返回可见字段。</p>
          </template>
          <p v-else-if="objectNotice" class="text-sm text-muted" role="status">{{ objectNotice }}</p>
          <p v-else class="text-sm text-muted">从数据库对象目录选择一项。</p>
        </section>
      </div>
    </template>
  </section>
</template>

<style scoped>
button[data-current] {
  background: var(--db-selected);
  border-left-color: var(--db-accent);
}
</style>
