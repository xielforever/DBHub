<script setup lang="ts">
import { ref, watch } from 'vue'
import { workbenchApi, type QueryHistoryItem } from '../../../api/workbench'
import { useReportRun } from '../../report/useReportRun'
import LoadingState from '../../../components/base/LoadingState.vue'

const props = defineProps<{ revision?: string; locked?: boolean }>()
const emit = defineEmits<{ load: [record: QueryHistoryItem] }>()
const history = useReportRun<Awaited<ReturnType<typeof workbenchApi.history>>>()
const page = ref(1)
const pageSize = 20

function reload() {
  const current = page.value
  return history.run(`${current}:${props.revision ?? ''}`, signal => workbenchApi.history({ page: current, page_size: pageSize }, signal))
}
function possiblyTruncated(sql: string) {
  return new TextEncoder().encode(sql).length >= 9997
}
watch([page, () => props.revision], () => { void reload() }, { immediate: true })
</script>

<template>
  <div class="min-w-0 space-y-2">
    <LoadingState v-if="history.state.value.status === 'loading'" variant="inline" label="正在读取查询历史…" />
    <template v-else-if="history.state.value.status === 'error'">
      <p class="break-words text-sm text-danger" role="alert">{{ history.state.value.message }}</p>
      <button type="button" class="ghost-button ui-control-target" @click="reload">重试读取历史</button>
    </template>
    <template v-else-if="history.state.value.status === 'success'">
      <ul v-if="history.state.value.data.items.length" class="divide-y divide-divider">
        <li v-for="record in history.state.value.data.items" :key="record.id" class="space-y-1 py-2">
          <div class="flex flex-wrap justify-between gap-1 text-xs text-muted">
            <time :datetime="record.created_at">{{ new Date(record.created_at).toLocaleString() }}</time>
            <span :class="record.status === 1 ? 'text-success' : 'text-danger'">{{ record.status === 1 ? '成功' : '失败' }}</span>
          </div>
          <p class="break-all text-xs text-muted">{{ record.connection_name || `连接 ${record.connection_id}` }} / {{ record.database_name || '未记录数据库' }}</p>
          <pre class="max-h-24 overflow-auto whitespace-pre-wrap break-all font-mono text-xs" tabindex="0">{{ record.sql_text }}</pre>
          <p v-if="possiblyTruncated(record.sql_text)" class="text-xs text-warning">历史文本可能已截断，载入后请检查。</p>
          <button type="button" class="ghost-button ui-control-target text-xs" :disabled="locked" @click="emit('load', record)">载入 SQL</button>
        </li>
      </ul>
      <p v-else class="text-sm text-muted">暂无查询历史。</p>
      <div v-if="history.state.value.data.total > pageSize" class="flex items-center justify-between text-xs">
        <button type="button" class="ghost-button ui-control-target" :disabled="page === 1" @click="page--">上一页</button>
        <span>第 {{ page }} 页</span>
        <button type="button" class="ghost-button ui-control-target" :disabled="page * pageSize >= history.state.value.data.total" @click="page++">下一页</button>
      </div>
    </template>
  </div>
</template>
