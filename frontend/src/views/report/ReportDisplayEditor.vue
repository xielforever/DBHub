<template>
  <details class="rounded-xl border border-white/10 p-3 text-sm">
    <summary class="cursor-pointer min-h-11 flex items-center">
      业务展示：字段名称与数字格式
    </summary>
    <div class="space-y-3 pt-3">
      <p class="text-xs text-white/60">
        显示名称会用于指标和图例。数字格式只影响展示，查询明细保留原始值，单位按报表口径显示。
      </p>
      <div v-if="numeric !== false" class="grid sm:grid-cols-2 gap-3">
        <label class="flex gap-2 items-center text-xs"
          ><input
            type="checkbox"
            :checked="modelValue?.grouping !== false"
            :disabled="disabled"
            @change="
              update({ grouping: ($event.target as HTMLInputElement).checked })
            "
          />使用千分位分隔</label
        >
        <label class="text-xs"
          >小数位<el-select
            :model-value="modelValue?.fraction_digits ?? 'original'"
            aria-label="展示小数位"
            :disabled="disabled"
            @change="setPlaces"
            ><el-option label="保留原始精度" value="original" /><el-option
              v-for="n in [0, 2, 4, 6, 8, 12]"
              :key="n"
              :value="n"
              :label="`${n} 位小数`" /></el-select
        ></label>
      </div>
      <div class="grid sm:grid-cols-2 gap-3">
        <label v-for="column in fields" :key="column" class="text-xs space-y-1"
          ><span class="text-white/60">{{ column }}</span
          ><el-input
            :model-value="labelFor(column)"
            :aria-label="`字段 ${column} 展示名称`"
            placeholder="留空使用原字段名"
            maxlength="64"
            :disabled="disabled"
            @update:model-value="setLabel(column, $event)"
        /></label>
      </div>
      <p v-if="!fields.length" class="text-xs text-white/50">
        运行预览或配置报表字段后，可设置业务名称。
      </p>
      <p v-if="columns.length > 32" class="text-xs text-white/50">
        最多设置32个字段的展示名称。
      </p>
    </div>
  </details>
</template>
<script setup lang="ts">
import { computed } from 'vue'
import type { ReportDisplay } from './reportDisplay'
const props = defineProps<{
  modelValue?: ReportDisplay
  columns: string[]
  disabled?: boolean
  numeric?: boolean
}>()
const emit = defineEmits<{ 'update:modelValue': [value: ReportDisplay] }>()
const fields = computed(() =>
  [
    ...new Set([
      ...props.columns,
      ...Object.keys(props.modelValue?.labels || {}),
    ]),
  ].slice(0, 32),
)
function update(value: Partial<ReportDisplay>) {
  emit('update:modelValue', { ...props.modelValue, ...value })
}
function setPlaces(value: number | string) {
  update({ fraction_digits: typeof value === 'number' ? value : undefined })
}
function labelFor(column: string) {
  const labels = props.modelValue?.labels
  return labels && Object.prototype.hasOwnProperty.call(labels, column)
    ? labels[column] || ''
    : ''
}
function setLabel(column: string, value: string) {
  const labels = { ...props.modelValue?.labels, [column]: value }
  if (!value.trim()) delete labels[column]
  update({ labels })
}
</script>
