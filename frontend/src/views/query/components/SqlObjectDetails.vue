<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { Clipboard, X } from 'lucide-vue-next'
import LoadingState from '../../../components/base/LoadingState.vue'
import { workbenchApi, type ColumnInfo, type TableInfo } from '../../../api/workbench'
import { useReportRun } from '../../report/useReportRun'

const props = defineProps<{ source: number; database: string; dialect: string; object: TableInfo }>()
const emit = defineEmits<{ close: [] }>()
const fields = useReportRun<ColumnInfo[]>()
const heading = ref<HTMLElement>()
const copyStatus = ref('')
const objectType = computed(() => props.object.type === 'view' ? '视图' : '表')
const objectPath = computed(() => `${props.database} · ${props.object.schema || '默认模式'} · ${objectType.value}`)

const qualified = computed(() => {
  const quote = props.dialect === 'mysql' ? '`' : '"'
  const namespace = props.dialect === 'mysql' ? props.database : props.object.schema
  return [namespace, props.object.name].filter(Boolean).map(name => quote + name.split(quote).join(quote + quote) + quote).join('.')
})

function loadFields() {
  const params = { database: props.database, schema: props.object.schema, table: props.object.name }
  return fields.run(JSON.stringify([props.source, props.object.type, params]), async signal =>
    (await workbenchApi.columns(props.source, params, { signal, silentError: true })).items ?? [])
}

watch(() => [props.source, props.database, props.object], async () => {
  copyStatus.value = ''
  void loadFields()
  await nextTick()
  heading.value?.focus({ preventScroll: true })
}, { immediate: true })

async function copyName() {
  const value = qualified.value
  let message = '已复制限定对象名。'
  try { await navigator.clipboard.writeText(value) }
  catch { message = '无法复制，可直接选择名称。' }
  if (value === qualified.value) copyStatus.value = message
}

function closeOnEscape(event: KeyboardEvent) {
  event.stopPropagation()
  emit('close')
}

function constraints(field: ColumnInfo) {
  return [field.is_primary ? '主键' : '', field.is_nullable ? '' : '非空'].filter(Boolean).join(' · ') || '—'
}
</script>

<template>
  <section class="flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden" aria-labelledby="sql-object-detail-title" @keydown.esc="closeOnEscape">
    <header class="flex shrink-0 items-start gap-2 border-b border-stroke p-3">
      <div class="min-w-0 flex-1">
        <h3 id="sql-object-detail-title" ref="heading" tabindex="-1" class="break-all font-mono ui-focus-target">{{ object.name }}</h3>
        <p class="break-all text-xs text-muted">{{ objectPath }}</p>
      </div>
      <button type="button" class="ghost-button ui-control-target" aria-label="关闭对象详情" @click="emit('close')"><X class="size-4" aria-hidden="true" /></button>
    </header>
    <div class="min-h-0 flex-1 space-y-3 overflow-auto p-3">
      <p v-if="object.comment" class="break-words text-sm text-muted">{{ object.comment }}</p>
      <div class="flex flex-wrap items-center gap-2">
        <code class="select-text break-all text-xs">{{ qualified }}</code>
        <button type="button" class="ghost-button ui-control-target inline-flex items-center gap-1 text-xs" @click="copyName"><Clipboard class="size-4" aria-hidden="true" />复制限定名</button>
        <span class="text-xs text-muted" role="status">{{ copyStatus }}</span>
      </div>
      <LoadingState v-if="fields.state.value.status === 'loading'" variant="inline" label="正在读取字段…" />
      <div v-else-if="fields.state.value.status === 'error'" class="space-y-2">
        <p role="alert" class="text-sm text-danger">{{ fields.state.value.message }}</p>
        <button type="button" class="ghost-button ui-control-target" @click="loadFields">重试读取字段</button>
      </div>
      <template v-else-if="fields.state.value.status === 'success'">
        <div v-if="fields.state.value.data.length" class="editorial-table-wrap" role="region" aria-label="对象字段" tabindex="0">
          <table class="editorial-table" aria-label="对象字段结构">
            <thead><tr><th scope="col">字段</th><th scope="col">类型</th><th scope="col">约束</th><th scope="col">说明</th></tr></thead>
            <tbody>
              <tr v-for="field in fields.state.value.data" :key="field.name">
                <th scope="row" class="font-mono">{{ field.name }}</th>
                <td class="font-mono">{{ field.data_type }}</td>
                <td>{{ constraints(field) }}</td>
                <td>{{ field.comment || '—' }}</td>
              </tr>
            </tbody>
          </table>
        </div>
        <p v-else class="text-sm text-muted">当前对象没有返回可见字段。</p>
      </template>
    </div>
  </section>
</template>
