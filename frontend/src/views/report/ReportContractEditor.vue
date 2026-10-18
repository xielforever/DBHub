<template>
  <section
    class="rounded-xl border border-white/15 p-4 space-y-3"
    aria-label="报表数据契约"
  >
    <label class="flex gap-2 items-center text-sm"
      ><input
        v-model="enabled"
        type="checkbox"
        :disabled="disabled"
      />声明数据契约，允许审批后加入仪表盘</label
    >
    <p class="text-xs text-white/60">
      口径、单位、范围、执行身份或字段改变时，仪表盘需要重新确认。相同契约仍需另一位审批人核实
      SQL 和样本。
    </p>
    <template v-if="enabled">
      <div class="grid sm:grid-cols-2 gap-3 text-xs text-white/70">
        <label
          >指标标识<el-input
            v-model="contract.semantic_key"
            aria-label="契约指标标识"
            :disabled="disabled"
            maxlength="100"
        /></label>
        <label
          >业务含义<el-input
            v-model="contract.meaning"
            aria-label="契约业务含义"
            :disabled="disabled"
            maxlength="1000"
        /></label>
        <label
          >单位（明确倍率）<el-input
            v-model="contract.unit"
            aria-label="契约单位"
            placeholder="item_count / CNY_10000"
            :disabled="disabled"
        /></label>
        <label
          >币种（计数留空）<el-input
            v-model="contract.currency"
            aria-label="契约币种"
            placeholder="CNY"
            :disabled="disabled"
            maxlength="3"
        /></label>
        <label
          >业务时区<el-input
            v-model="contract.timezone"
            aria-label="契约业务时区"
            :disabled="disabled"
        /></label>
        <label
          >统计范围标识<el-input
            v-model="contract.scope_key"
            aria-label="契约统计范围标识"
            :disabled="disabled"
        /></label>
        <label class="sm:col-span-2"
          >统计范围说明<el-input
            v-model="contract.scope_description"
            aria-label="契约统计范围说明"
            placeholder="包含哪些仓库、是否包含冻结或在途数据"
            :disabled="disabled"
        /></label>
        <label
          >时间窗口<el-select
            v-model="windowKind"
            aria-label="契约时间窗口"
            :disabled="disabled"
            ><el-option label="当前时点" value="current_snapshot" /><el-option
              label="自然日"
              value="calendar_day" /><el-option
              label="固定期间"
              value="fixed_period" /><el-option
              label="滚动窗口"
              value="rolling_window" /></el-select
        ></label>
        <label
          >窗口边界说明<el-input
            v-model="windowDefinition"
            aria-label="契约窗口边界"
            placeholder="过去7个自然日，含当日"
            :disabled="disabled"
        /></label>
        <label
          >可比较指标族<el-input
            v-model="contract.comparison_key"
            aria-label="契约可比较指标族"
            placeholder="不需要比较可留空"
            :disabled="disabled"
        /></label>
        <label
          >时效验证<el-select
            v-model="contract.freshness.mode"
            aria-label="契约时效验证"
            :disabled="disabled"
            ><el-option
              label="SQL 返回业务水位列"
              value="watermark_column" /><el-option
              label="无法验证（未知）"
              value="unverified" /></el-select
        ></label>
        <template v-if="contract.freshness.mode === 'watermark_column'"
          ><label
            >水位列名<el-input
              v-model="contract.freshness.column"
              aria-label="契约水位列"
              :disabled="disabled" /></label
          ><label
            >最大业务数据年龄（秒）<el-input-number
              v-model="contract.freshness.max_data_age_seconds"
              :min="1"
              :max="2592000"
              aria-label="最大业务数据年龄"
              :disabled="disabled" /></label
        ></template>
      </div>
      <p class="text-xs text-white/55">
        水位须为带时区时间，且每行一致；查询完成时间不能替代业务截止时间。未知时效需空间策略允许并单独审批。
      </p>
      <div class="flex flex-wrap items-center gap-3">
        <span class="text-sm">结果字段与顺序</span
        ><button
          type="button"
          class="ghost-button text-xs"
          :disabled="disabled || !columns.length"
          @click="usePreview"
        >
          从预览填入字段
        </button>
      </div>
      <div
        v-for="(column, index) in contract.result_schema"
        :key="index"
        class="grid grid-cols-[1fr_1fr_auto] gap-2 items-center text-xs"
      >
        <el-input
          v-model="column.name"
          :aria-label="`契约字段${index + 1}名称`"
          :disabled="disabled"
        />
        <el-select
          v-model="column.logical_type"
          :aria-label="`契约字段${index + 1}类型`"
          :disabled="disabled"
          ><el-option
            v-for="type in types"
            :key="type.value"
            :label="type.label"
            :value="type.value"
        /></el-select>
        <label class="flex items-center gap-1"
          ><input
            v-model="column.nullable"
            type="checkbox"
            :disabled="disabled"
          />可空</label
        >
      </div>
      <p v-if="!contract.result_schema.length" class="text-xs text-amber-200">
        先运行预览，再填入并核对全部字段及类型；不自动猜测数值口径。
      </p>
    </template>
  </section>
</template>
<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import type { DataContract } from '../../api/managed-report'
const props = defineProps<{
  modelValue?: DataContract
  columns: string[]
  disabled: boolean
}>()
const emit = defineEmits<{
  'update:modelValue': [value: DataContract | undefined]
}>()
const enabled = ref(!!props.modelValue)
const contract = reactive<DataContract>(
  props.modelValue
    ? JSON.parse(JSON.stringify(props.modelValue))
    : {
        semantic_key: '',
        meaning: '',
        result_schema: [],
        dimensions: [],
        metrics: [],
        chart_type: 'table',
        unit: '',
        currency: '',
        timezone: 'Asia/Shanghai',
        scope_key: '',
        scope_description: '',
        time_window: { kind: 'current_snapshot' },
        comparison_key: '',
        freshness: {
          mode: 'watermark_column',
          column: '__data_as_of',
          max_data_age_seconds: 900,
          max_future_skew_seconds: 60,
        },
      },
)
const windowKind = computed({
  get: () => String(contract.time_window.kind),
  set: (kind: string) => {
    contract.time_window.kind = kind
  },
})
const windowDefinition = computed({
  get: () => String(contract.time_window.definition || ''),
  set: (value: string) => {
    contract.time_window.definition = value
  },
})
const types = [
  { value: 'string', label: '文本' },
  { value: 'integer', label: '整数' },
  { value: 'lossless_numeric', label: '精确数值' },
  { value: 'number', label: '数值' },
  { value: 'boolean', label: '布尔' },
  { value: 'date', label: '日期' },
  { value: 'timestamp_with_timezone', label: '带时区时间' },
]
function usePreview() {
  contract.result_schema = props.columns.map((name) => ({
    name,
    logical_type:
      name === contract.freshness.column ? 'timestamp_with_timezone' : 'string',
    nullable: false,
  }))
}
watch(
  () => [enabled.value, contract],
  () =>
    emit(
      'update:modelValue',
      enabled.value ? JSON.parse(JSON.stringify(contract)) : undefined,
    ),
  { deep: true },
)
</script>
