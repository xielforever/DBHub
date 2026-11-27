<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import LoadingState from '../../../components/base/LoadingState.vue'
import { PanelsTopLeft, Table2 } from 'lucide-vue-next'
import { workbenchApi, type TableInfo } from '../../../api/workbench'
import { useReportRun } from '../../report/useReportRun'

const props = defineProps<{ source: number; database: string; selected?: TableInfo }>()
const emit = defineEmits<{ select: [item: TableInfo, trigger: HTMLElement]; clear: [] }>()
const schemas = useReportRun<string[]>()
const objects = useReportRun<TableInfo[]>()
const schema = ref('')
const search = ref('')
const list = computed(() => {
  if (objects.state.value.status !== 'success') return []
  const needle = search.value.trim().toLocaleLowerCase()
  return objects.state.value.data.filter(item => item.name.toLocaleLowerCase().includes(needle))
})

async function loadObjects() {
  const params = { database: props.database, schema: schema.value }
  const loaded = await objects.run(JSON.stringify(params), async signal =>
    (await workbenchApi.tables(props.source, params, { signal, silentError: true })).items ?? [])
  if (!loaded || objects.state.value.status !== 'success' || !props.selected) return
  if (!objects.state.value.data.some(item => sameObject(item))) emit('clear')
}

function sameObject(item: TableInfo) {
  const selection = props.selected
  return !!selection && selection.schema === (item.schema || schema.value) && selection.type === item.type && selection.name === item.name
}

function select(item: TableInfo, event: MouseEvent) {
  if (event.currentTarget instanceof HTMLElement) emit('select', { ...item, schema: item.schema || schema.value }, event.currentTarget)
}

async function loadSchemas() {
  objects.reset()
  const loaded = await schemas.run(JSON.stringify([props.source, props.database]), async signal => {
    const { items } = await workbenchApi.schemas(props.source, props.database, { signal, silentError: true })
    return items?.length ? items : ['']
  })
  if (loaded && schemas.state.value.status === 'success') {
    schema.value = schemas.state.value.data[0] ?? ''
    void loadObjects()
  }
}

watch(() => [props.source, props.database], () => {
  search.value = ''
  void loadSchemas()
}, { immediate: true })
</script>

<template>
  <div class="space-y-3 min-w-0" aria-label="SQL 数据库对象目录">
    <LoadingState v-if="schemas.state.value.status === 'loading'" variant="inline" label="正在读取模式…" />
    <div v-else-if="schemas.state.value.status === 'error'" class="space-y-2">
      <p role="alert" class="text-sm text-danger break-words">{{ schemas.state.value.message || '读取模式失败。' }}</p>
      <button class="ghost-button ui-control-target" @click="loadSchemas">重试读取模式</button>
    </div>
    <template v-else-if="schemas.state.value.status === 'success'">
      <label class="block text-sm">对象目录模式
        <select v-model="schema" class="glass-input block w-full mt-1" @change="loadObjects">
          <option v-for="item in schemas.state.value.data" :key="item" :value="item">{{ item || '默认范围' }}</option>
        </select>
      </label>
      <LoadingState v-if="objects.state.value.status === 'loading'" variant="inline" label="正在读取数据库对象…" />
      <div v-else-if="objects.state.value.status === 'error'" class="space-y-2">
        <p role="alert" class="text-sm text-danger break-words">{{ objects.state.value.message || '读取数据库对象失败。' }}</p>
        <button class="ghost-button ui-control-target" @click="loadObjects">重试读取对象</button>
      </div>
      <template v-else-if="objects.state.value.status === 'success'">
        <label class="block text-sm">搜索对象<input v-model="search" class="glass-input block w-full mt-1" /></label>
        <ul v-if="list.length" class="divide-y divide-divider text-sm max-h-96 overflow-y-auto">
          <li v-for="item in list" :key="item.schema + ':' + item.type + ':' + item.name">
            <button type="button" class="query-target-option w-full"
              :title="item.name" :aria-pressed="sameObject(item)" :aria-label="`${item.name} ${item.type === 'view' ? '视图' : '表'}`" @click="select(item, $event)">
              <component :is="item.type === 'view' ? PanelsTopLeft : Table2" class="size-4 shrink-0 text-muted" aria-hidden="true" />
              <span class="min-w-0 truncate font-mono font-medium">{{ item.name }}</span>
            </button>
          </li>
        </ul>
        <p v-else class="text-sm text-muted">{{ objects.state.value.data.length ? '没有匹配的数据库对象。' : '当前模式没有可见对象。' }}</p>
      </template>
    </template>
  </div>
</template>
