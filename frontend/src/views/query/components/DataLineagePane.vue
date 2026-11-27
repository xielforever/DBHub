<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import LoadingState from '../../../components/base/LoadingState.vue'
import PageState from '../../../components/base/PageState.vue'
import { assetApi } from '../../../api/asset'
import { useUserStore } from '../../../stores/user'
import { featureAllowed } from '../../../utils/permission'
import { useReportRun } from '../../report/useReportRun'

const props = defineProps<{ currentTable: string; currentDb: string; fallbackTable: string; connectionId?: number; schema?: string }>()
const emit = defineEmits<{ createTicket: [] }>()
const user = useUserStore()
const canParse = computed(() => ['admin', 'developer'].includes(user.role))
const canApply = computed(() => featureAllowed(user, 'change.apply', canParse.value))
const graph = useReportRun<Awaited<ReturnType<typeof assetApi.lineage>>>()
const table = computed(() => props.currentTable || props.fallbackTable)
const depth = ref(2)
const filter = ref('')
const parsing = ref(false)
const commandError = ref('')
const data = computed(() => graph.state.value.status === 'success' ? graph.state.value.data : undefined)
const typeLabel: Record<string, string> = { table: '表', view: '视图', report: '报表', dashboard: '仪表盘' }
const impactLabels = {
  upstream: '上游对象', downstream: '下游对象', downstream_tables: '下游表',
  downstream_views: '下游视图', downstream_reports: '下游报表', total: '图谱对象总数',
}
const impacts = computed(() => Object.entries(impactLabels).flatMap(([key, label]) => {
  const count = data.value?.impact?.[key]
  return typeof count === 'number' ? [{ key, label, count }] : []
}))
const center = computed(() => {
  const nodes = data.value?.nodes ?? []
  const expected = [props.connectionId ?? 0, props.currentDb, props.schema ?? '', table.value].join('.').toLowerCase()
  if (props.connectionId) return nodes.find(item => item.id === expected && item.desc === '当前')
  return nodes.find(item => item.desc === '当前' && item.name === table.value)
})
const groups = computed(() => {
  if (!center.value || !data.value) return []
  const root = center.value.id
  const { nodes, edges } = data.value
  const needle = filter.value.trim().toLocaleLowerCase()
  function connected(side: 'upstream' | 'downstream') {
    const reached = new Set([root])
    let frontier = [root]
    for (let level = 0; level < depth.value && frontier.length; level++) {
      const current = new Set(frontier)
      frontier = []
      for (const edge of edges ?? []) {
        const from = side === 'upstream' ? edge.to : edge.from
        const to = side === 'upstream' ? edge.from : edge.to
        if (!current.has(from) || reached.has(to)) continue
        reached.add(to)
        frontier.push(to)
      }
    }
    return nodes.filter(node => node.id !== root && reached.has(node.id))
  }
  return (['upstream', 'downstream'] as const).map(side => {
    const all = connected(side)
    const shown = all.filter(item => (item.name + ' ' + item.db).toLocaleLowerCase().includes(needle))
    return { side, name: side === 'upstream' ? '上游' : '下游', all, shown }
  })
})

function load() {
  if (!table.value) return
  const params = { connection_id: props.connectionId ?? 0, database: props.currentDb, schema: props.schema ?? '', table: table.value, depth: depth.value }
  return graph.run(JSON.stringify(params), signal => assetApi.lineage(params, { signal, silentError: true }))
}

async function parse() {
  if (!canParse.value || parsing.value) return
  parsing.value = true
  commandError.value = ''
  try {
    await assetApi.lineageRefresh()
    await load()
  } catch (error) {
    commandError.value = error instanceof Error ? error.message : '血缘解析结果待核对。'
  } finally {
    parsing.value = false
  }
}

watch([table, depth, () => props.connectionId, () => props.currentDb, () => props.schema], () => { void load() }, { immediate: true })
</script>

<template>
  <section class="space-y-3 py-3 min-w-0" aria-label="数据血缘">
    <div class="flex flex-wrap items-end gap-2">
      <label class="text-sm">关系深度<select v-model.number="depth" class="glass-input block mt-1"><option :value="1">1 层</option><option :value="2">2 层</option><option :value="3">3 层</option></select></label>
      <label class="flex-1 basis-40 min-w-0 text-sm">过滤关系<input v-model="filter" class="glass-input block w-full mt-1" placeholder="对象名称或数据库" /></label>
      <button class="ghost-button ui-control-target" :disabled="graph.state.value.status === 'loading'" @click="load">刷新血缘</button>
      <button v-if="canParse" class="ghost-button ui-control-target" :disabled="parsing" @click="parse">重新解析</button>
    </div>
    <p v-if="commandError" class="text-sm text-danger" role="alert">{{ commandError }}</p>
    <LoadingState v-if="graph.state.value.status === 'loading'" label="正在读取血缘…" />
    <PageState v-else-if="graph.state.value.status === 'error'" heading="h3" title="读取血缘失败" :description="graph.state.value.message || '请重新核对当前对象的血缘。'" tone="danger">
      <template #actions><button class="ghost-button ui-control-target" @click="load">重试血缘</button></template>
    </PageState>
    <PageState v-else-if="data && !center" heading="h3" title="血缘未返回当前对象" description="请刷新并核对当前对象的采集快照。" />
    <template v-else-if="data && center">
      <p class="text-sm break-all">当前对象 {{ center.name }} · {{ center.db }}<span v-if="data.cached" class="text-muted"> · 已缓存</span></p>
      <div class="grid gap-4 md:grid-cols-2">
        <section v-for="group in groups" :key="group.side" class="min-w-0 space-y-2">
          <h4 class="editorial-object-title">{{ group.name }} {{ group.all.length }} 项</h4>
          <ul v-if="group.shown.length" class="divide-y divide-divider">
            <li v-for="item in group.shown" :key="item.id" class="py-2 text-sm break-all">
              <p>{{ item.name }} <span class="text-xs text-muted">{{ typeLabel[item.type] || item.type }}</span></p>
              <p class="text-xs text-muted">{{ item.db }}<span v-if="item.desc"> · {{ item.desc }}</span></p>
            </li>
          </ul>
          <p v-else class="text-sm text-muted">{{ group.all.length ? '没有匹配的关系。' : '当前图谱未返回关系。' }}</p>
        </section>
      </div>
      <p v-if="impacts.length" class="flex flex-wrap gap-x-3 gap-y-1 text-sm text-muted">
        <span>影响统计</span>
        <span v-for="impact in impacts" :key="impact.key">{{ impact.label }} {{ impact.count }} 项</span>
      </p>
      <button v-if="canApply" class="ghost-button ui-control-target" @click="emit('createTicket')">创建变更工单</button>
    </template>
  </section>
</template>
