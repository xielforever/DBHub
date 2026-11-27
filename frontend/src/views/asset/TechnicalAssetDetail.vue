<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { ArrowLeft, ChevronRight } from 'lucide-vue-next'
import { assetApi, type TechnicalElement, type TechnicalElementPage, type TechnicalElementQuery } from '../../api/asset'
import LoadingState from '../../components/base/LoadingState.vue'
import CatalogCopyButton from '../../components/catalog/CatalogCopyButton.vue'
import { useReportRun } from '../report/useReportRun'
import { elementAttribute, elementKey, elementKinds, elementLevels, elementPath } from './technicalElements'

const props = defineProps<{ element: TechnicalElement; canGoBack: boolean }>()
const emit = defineEmits<{
  back: []
  'open-related': [element: TechnicalElement]
  'open-object': [element: TechnicalElement]
}>()

const page = ref(1)
const relation = useReportRun<TechnicalElementPage>()
const related = computed(() => relation.state.value.status === 'success' ? relation.state.value.data : undefined)
const isObject = computed(() => ['table', 'view'].includes(props.element.kind))
const isLeaf = computed(() => ['field', 'index', 'constraint'].includes(props.element.kind))
const relationHeading = computed(() => ({
  database: 'Schema', schema: '表和视图', table: '字段、索引与约束', view: '字段、索引与约束',
  field: '所属对象', index: '所属对象', constraint: '所属对象',
})[props.element.kind])
const structure = computed((): [string, string][] => {
  const element = props.element
  const p = element.properties
  const attribute = elementAttribute(element)
  switch (element.kind) {
    case 'database':
      return [['数据源', element.connection_name], ['数据库引擎', attribute], ['Schema', String(p.schema_count ?? 0)]]
    case 'schema':
      return [['数据库', element.database], ['表和视图', attribute]]
    case 'table':
    case 'view':
      return [['字段', attribute], ['负责人', element.owner_name || '未指派']]
    case 'field':
      return [['数据类型', attribute], ['字段序号', String(p.ordinal ?? '—')], ['可为空', p.is_nullable === undefined ? '—' : p.is_nullable ? '是' : '否'], ['所属对象', element.table]]
    case 'index':
      return [['索引类型', attribute], ['索引字段', p.columns?.join('、') || '—'], ['所属表', element.table]]
    case 'constraint': {
      const rows: [string, string][] = [['约束类型', attribute], ['字段', p.columns?.join('、') || '—'], ['所属表', element.table]]
      if (p.ref_table) rows.push(['引用', [p.ref_schema, p.ref_table].filter(Boolean).join('.') + (p.ref_columns?.length ? ` (${p.ref_columns.join('、')})` : '')])
      return rows
    }
  }
})
const captured = computed(() => new Intl.DateTimeFormat('zh-CN', {
  dateStyle: 'medium', timeStyle: 'short',
}).format(new Date(props.element.synced_at)))

function readRelated() {
  const element = props.element
  const query: TechnicalElementQuery = {
    connection_id: element.connection_id,
    database: element.database,
    page: page.value,
    page_size: 8,
  }
  if (element.kind === 'database') query.type = ['schema']
  if (element.kind !== 'database') {
    query.schema = element.schema
    query.type = ['table', 'view']
  }
  if (element.table) query.table = element.table
  if (isObject.value) query.type = ['field', 'index', 'constraint']
  return relation.run(JSON.stringify(query), signal => assetApi.elements(query, signal))
}
function move(direction: number) {
  page.value += direction
  void readRelated()
}
watch(() => elementKey(props.element), () => {
  page.value = 1
  void readRelated()
}, { immediate: true })
</script>

<template>
  <div class="space-y-4">
    <button v-if="canGoBack" type="button" class="editorial-text-action ui-control-target inline-flex items-center gap-1.5" @click="emit('back')">
      <ArrowLeft class="size-4" aria-hidden="true" />返回上级
    </button>
    <div class="flex items-start justify-between gap-3">
      <div class="min-w-0 space-y-2">
        <p class="text-sm text-muted [overflow-wrap:anywhere]">{{ elementPath(element) }}</p>
        <p v-if="element.comment" class="text-sm [overflow-wrap:anywhere]">{{ element.comment }}</p>
      </div>
      <CatalogCopyButton class="shrink-0" :value="element.name" :label="'复制名称 ' + element.name" />
    </div>
    <dl class="grid grid-cols-[max-content_minmax(0,1fr)] gap-x-4 gap-y-2 border-t border-divider pt-3 text-sm">
      <dt class="text-muted">元素类型</dt><dd>{{ elementKinds[element.kind].label }}</dd>
      <template v-for="[label, value] in structure" :key="label">
        <dt class="text-muted">{{ label }}</dt><dd class="[overflow-wrap:anywhere]">{{ value }}</dd>
      </template>
      <template v-if="isObject || element.kind === 'field'">
        <dt class="text-muted">分级</dt><dd>{{ elementLevels[element.level] }}</dd>
      </template>
      <template v-if="element.tags.length">
        <dt class="text-muted">标签</dt><dd class="[overflow-wrap:anywhere]">{{ element.tags.join(' · ') }}</dd>
      </template>
      <dt class="text-muted">最近采集</dt><dd>{{ captured }}</dd>
    </dl>
    <button v-if="isObject" type="button" class="ghost-button ui-control-target"
      @click="emit('open-object', element)">查看完整对象详情</button>
    <section class="space-y-2 border-t border-divider pt-3" :aria-label="relationHeading">
      <h4 class="text-sm font-semibold">{{ relationHeading }}</h4>
      <LoadingState v-if="relation.state.value.status === 'loading'" variant="inline" label="正在读取…"/>
      <div v-else-if="relation.state.value.status === 'error'" class="space-y-2">
        <p class="text-sm text-danger" role="alert">相关元素暂不可用</p>
        <button type="button" class="ghost-button ui-control-target" @click="readRelated">重试</button>
      </div>
      <p v-else-if="!related?.items.length" class="text-sm text-muted">暂无已采集元素</p>
      <ul v-else class="divide-y divide-divider">
        <li v-for="item in related.items" :key="elementKey(item)">
          <button type="button" class="editorial-text-action ui-control-target flex w-full items-center gap-2 text-left"
            :aria-label="'打开' + elementKinds[item.kind].label + ' ' + item.name" @click="emit('open-related', item)">
            <component :is="elementKinds[item.kind].icon" class="size-3.5 shrink-0 text-muted" aria-hidden="true" />
            <span class="min-w-0 flex-1 font-mono [overflow-wrap:anywhere]">{{ item.name }}</span>
            <ChevronRight class="size-3.5 shrink-0 text-muted" aria-hidden="true" />
          </button>
        </li>
      </ul>
      <nav v-if="related && related.total > related.page_size && !isLeaf" class="flex flex-wrap items-center gap-2 text-xs text-muted" aria-label="相关元素分页">
        <button type="button" class="ghost-button ui-control-target" :disabled="page === 1" @click="move(-1)">上一页</button>
        <span>第 {{ page }} 页</span>
        <button type="button" class="ghost-button ui-control-target" :disabled="page * related.page_size >= related.total" @click="move(1)">下一页</button>
      </nav>
    </section>
  </div>
</template>
