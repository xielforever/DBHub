<script setup lang="ts">
import { computed, ref, useId } from 'vue'
import PageCategoryTabs from '../../../components/base/PageCategoryTabs.vue'
import { postgresPlanNodes } from '../explain'
import ResultGrid from './ResultGrid.vue'

const props = defineProps<{ columns: string[]; rows: unknown[][]; sql: string }>()
const id = useId()
const nodes = computed(() => postgresPlanNodes(props.columns, props.rows))
const chosen = ref<string>()
const categories = computed(() => [
  ...(nodes.value.length ? [{ id: 'nodes', name: '计划节点' }] : []),
  { id: 'table', name: '计划表格' },
  { id: 'raw', name: '原始计划' },
  { id: 'sql', name: '执行 SQL' },
])
const active = computed(() => categories.value.some(item => item.id === chosen.value) ? chosen.value : categories.value[0]?.id)
const raw = computed(() => props.columns.length === 1 && props.rows.every(row => typeof row[0] === 'string')
  ? props.rows.map(row => row[0]).join('\n')
  : JSON.stringify({ columns: props.columns, rows: props.rows }, null, 2))
</script>

<template>
  <section class="min-h-0 flex-1 overflow-auto p-3 space-y-2" :aria-labelledby="id + '-heading'">
    <h3 :id="id + '-heading'" class="font-semibold text-sm">执行解释</h3>
    <p v-if="!rows.length" class="text-sm text-muted">本次执行未返回计划记录。</p>
    <PageCategoryTabs v-else label="执行解释视图" :categories="categories" :active-id="active" @select="chosen = $event">
      <template v-if="active === 'nodes'">
        <p class="my-2 text-xs text-muted">估计成本与实际耗时分别展示。实际行数和节点时间按数据库返回的每轮值显示。</p>
        <div class="editorial-table-wrap" role="region" tabindex="0" aria-label="执行计划节点表格">
          <table class="editorial-table min-w-[650px]" aria-label="执行计划节点">
            <thead><tr>
              <th scope="col">节点与对象</th><th scope="col">估计成本</th><th scope="col">估计行数</th>
              <th scope="col">实际行数</th><th scope="col">实际时间（ms）</th><th scope="col">循环</th>
            </tr></thead>
            <tbody>
              <tr v-for="node in nodes" :key="node.index">
                <th scope="row" class="max-w-72 !whitespace-normal [overflow-wrap:anywhere]">
                  <span class="inline-block font-mono" :style="{ paddingInlineStart: Math.min(6, node.depth) * 12 + 'px' }">{{ node.label }}</span>
                </th>
                <td>{{ node.cost }}</td><td>{{ node.estimatedRows }}</td>
                <td>{{ node.actualRows ?? '—' }}</td><td>{{ node.actualTime ?? '—' }}</td><td>{{ node.loops ?? '—' }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </template>
      <div v-else-if="active === 'table'" class="mt-2 flex h-48 min-h-0 min-w-0">
        <ResultGrid :columns="columns" :rows="rows" />
      </div>
      <pre v-else class="mt-2 data-surface p-3 text-xs whitespace-pre-wrap [overflow-wrap:anywhere]">{{ active === 'sql' ? sql : raw }}</pre>
    </PageCategoryTabs>
  </section>
</template>
