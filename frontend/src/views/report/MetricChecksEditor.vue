<script setup lang="ts">
import { computed } from 'vue'
import type { MetricCheck } from '../../api/managed-report'
const props=defineProps<{modelValue?:MetricCheck[];numericColumns:string[];disabled:boolean}>(),emit=defineEmits<{'update:modelValue':[value:MetricCheck[]|undefined]}>()
const values=computed(()=>props.modelValue||[])
function add(){emit('update:modelValue',[...values.value,{id:'check_'+(values.value.length+1),label:'',kind:'all_equal',column:'',expected:'0',tolerance:'0'}])}
function remove(index:number){const next=values.value.filter((_,i)=>i!==index);emit('update:modelValue',next.length?next:undefined)}
function normalize(check:MetricCheck){if(check.kind==='range'){delete check.expected;delete check.tolerance}else{delete check.min;delete check.max;if(check.expected===undefined)check.expected='0'}if(check.kind==='row_count'){delete check.column;delete check.tolerance}if(check.kind!=='all_equal'&&check.kind!=='range')delete check.allow_empty}
</script>

<template>
  <fieldset class="space-y-3 border-t border-stroke pt-3" :disabled="disabled">
    <legend class="text-sm px-1">指标回归校验</legend>
    <p class="text-xs text-muted">规则随版本独立审批，在审批验证、正式查询、仪表盘和导出中执行。数值按十进制精确比较；NULL、截断均不通过。逐行规则可明确允许空结果，默认仍拒绝空结果。预期值应为业务恒等式或固定样本基线；动态指标请设置合理范围。</p>
    <article v-for="(check,index) in values" :key="index" class="glass-card p-3 space-y-3" :aria-label="`指标校验${index+1}`">
      <div class="grid sm:grid-cols-2 gap-3 text-xs"><label>规则标识<el-input v-model="check.id" :aria-label="`校验${index+1}标识`" maxlength="64" /></label><label>业务说明<el-input v-model="check.label" :aria-label="`校验${index+1}说明`" maxlength="128" /></label></div>
      <div class="grid sm:grid-cols-2 gap-3 text-xs"><label>规则类型<select v-model="check.kind" :aria-label="`校验${index+1}类型`" class="glass-input w-full" @change="normalize(check)"><option value="all_equal">每行等于预期值</option><option value="sum_equals">整列合计等于预期值</option><option value="row_count">结果行数</option><option value="range">每行数值范围</option></select></label><label v-if="check.kind!=='row_count'">数值字段<select v-model="check.column" :aria-label="`校验${index+1}字段`" class="glass-input w-full"><option value="">选择字段</option><option v-for="column in numericColumns" :key="column" :value="column">{{column}}</option></select></label></div>
      <div class="grid sm:grid-cols-2 gap-3 text-xs"><template v-if="check.kind==='range'"><label>最小值（可留空）<el-input v-model="check.min" :aria-label="`校验${index+1}最小值`" maxlength="256" /></label><label>最大值（可留空）<el-input v-model="check.max" :aria-label="`校验${index+1}最大值`" maxlength="256" /></label></template><template v-else><label>预期值<el-input v-model="check.expected" :aria-label="`校验${index+1}预期值`" maxlength="256" /></label><label v-if="check.kind!=='row_count'">允许绝对误差<el-input v-model="check.tolerance" :aria-label="`校验${index+1}误差`" placeholder="0" maxlength="256" /></label></template></div>
      <button type="button" class="ghost-button text-xs text-danger ui-control-target" @click="remove(index)">移除此校验</button>
      <label v-if="check.kind==='all_equal'||check.kind==='range'" class="flex gap-2 text-xs"><input v-model="check.allow_empty" type="checkbox" :aria-label="`校验${index+1}允许空结果`" />明确允许空结果（仍检查字段、NULL 和截断）</label>
    </article>
    <button type="button" class="ghost-button text-xs ui-control-target" :disabled="values.length>=16" @click="add">添加指标校验</button>
  </fieldset>
</template>
