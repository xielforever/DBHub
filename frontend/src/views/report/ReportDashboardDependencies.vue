<template>
  <section class="space-y-3 text-xs" aria-label="报表仪表盘依赖">
    <div class="flex flex-wrap gap-2">
      <el-select
        v-model="scope"
        aria-label="看板引用阶段"
        :disabled="state.status === 'loading'"
        @change="
          () => {
            page = 1
            load()
          }
        "
        ><el-option label="当前线上" value="current" /><el-option
          label="开发与待发布"
          value="development" /><el-option
          label="历史版本"
          value="history" /></el-select
      ><button class="ghost-button text-xs" @click="load">刷新引用</button>
    </div>
    <p class="text-white/60">
      线上影响只计算当前正式引用。开发草稿、待审和历史引用不增加线上影响数量；受限看板仅显示安全摘要。
    </p>
    <p v-if="state.status === 'loading'" role="status">读取中…</p>
    <p v-else-if="state.status === 'error'" role="alert" class="text-rose-200">
      {{ state.message }}
    </p>
    <template v-else-if="state.status === 'success'"
      ><ul class="space-y-2">
        <li
          v-for="(row, index) in state.data.items"
          :key="`${row.dashboard_id}:${row.version}:${index}`"
          class="rounded-lg border border-white/10 p-3"
        >
          <span>{{ row.name }}</span
          ><span v-if="row.dashboard_id" class="text-white/55">
            · #{{ row.dashboard_id }} · v{{ row.version }} ·
            {{ row.lifecycle }}</span
          >
        </li>
      </ul>
      <p v-if="!state.data.items.length" class="text-white/60">
        当前阶段没有引用。
      </p>
      <div class="flex justify-between items-center gap-2">
        <span>共 {{ state.data.total }} 处引用</span>
        <div class="flex gap-2 items-center">
          <button
            class="ghost-button text-xs"
            :disabled="page <= 1"
            @click="
              () => {
                page--
                load()
              }
            "
          >
            上一页</button
          ><span>{{ page }}</span
          ><button
            class="ghost-button text-xs"
            :disabled="page * 10 >= state.data.total"
            @click="
              () => {
                page++
                load()
              }
            "
          >
            下一页
          </button>
        </div>
      </div></template
    >
  </section>
</template>
<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { managedReportApi } from '../../api/managed-report'
import { useReportRun } from './useReportRun'
const props = defineProps<{ id: number }>(),
  scope = ref('current'),
  page = ref(1),
  run = useReportRun<{
    items: {
      dashboard_id: number
      name: string
      version: number
      kind: string
      lifecycle: string
    }[]
    total: number
  }>(),
  state = computed(() => run.state.value)
function load() {
  return run.run(`${props.id}:${scope.value}:${page.value}`, (signal) =>
    managedReportApi.dashboardDependencies(
      props.id,
      scope.value,
      page.value,
      signal,
    ),
  )
}
onMounted(load)
</script>
