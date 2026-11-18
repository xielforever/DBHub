<script setup lang="ts">
import LoadingState from '../../components/base/LoadingState.vue'
import { computed, onScopeDispose, ref, watch } from 'vue'
import type { ChartKind, ChartConfig } from '../../components/business/chart/ChartCard.vue'
import { managedReportApi, type PublishedReport } from '../../api/managed-report'
const props=defineProps<{kind:ChartKind;columns:string[];metrics:string[];disabled?:boolean}>()
const pivot=defineModel<ChartConfig['pivot']>('pivot'),dualAxis=defineModel<ChartConfig['dual_axis']>('dualAxis'),drillthrough=defineModel<ChartConfig['drillthrough']>('drillthrough')
const pivotColumn=computed({get:()=>pivot.value?.column||'',set:column=>{pivot.value={column}}}),targets=ref<PublishedReport[]>([]),keyword=ref(''),loading=ref(false),error=ref('')
const selected=computed(()=>targets.value.find(target=>target.id===drillthrough.value?.report_id))
let alive=true,generation=0
watch(()=>props.kind,kind=>{if(kind==='dual_axis'&&!dualAxis.value)dualAxis.value={left_unit:'',right_unit:'',right_metrics:[]}},{immediate:true})
watch(()=>!!drillthrough.value,enabled=>{if(enabled)void loadTargets()},{immediate:true})
function toggleDrill(enabled:boolean){drillthrough.value=enabled?{report_id:0,bindings:{}}:undefined}
function selectTarget(id:number){drillthrough.value={report_id:id,bindings:{}}}
function bind(parameter:string,column:string){if(!drillthrough.value)return;const bindings={...drillthrough.value.bindings};if(column)bindings[parameter]=column;else delete bindings[parameter];drillthrough.value={...drillthrough.value,bindings}}
async function loadTargets(){const run=++generation;loading.value=true;error.value='';try{const result=await managedReportApi.published({q:keyword.value,page:1,page_size:100});const items=result.items.filter(target=>target.parameters?.length);const id=drillthrough.value?.report_id;if(id&&!items.some(target=>target.id===id))items.unshift(await managedReportApi.publishedGet(id));if(alive&&run===generation)targets.value=items}catch(e){if(alive&&run===generation)error.value=e instanceof Error?e.message:'下钻目标加载失败'}finally{if(alive&&run===generation)loading.value=false}}
onScopeDispose(()=>{alive=false;generation++})
</script>

<template>
  <fieldset class="glass-panel p-3 space-y-3" :disabled="disabled">
    <legend class="text-sm px-1">分析与明细</legend>
    <label v-if="kind==='pivot'" class="block text-xs">列维度<select v-model="pivotColumn" aria-label="透视列维度" class="glass-input w-full mt-1"><option value="">选择列维度</option><option v-for="column in columns" :key="column" :value="column">{{ column }}</option></select></label>
    <div v-if="kind==='dual_axis' && dualAxis" class="grid sm:grid-cols-2 gap-3">
      <label class="text-xs">左轴单位<input v-model="dualAxis.left_unit" aria-label="左轴单位" class="glass-input w-full mt-1" maxlength="32" placeholder="例如：人民币元" /></label><label class="text-xs">右轴单位<input v-model="dualAxis.right_unit" aria-label="右轴单位" class="glass-input w-full mt-1" maxlength="32" placeholder="例如：订单数" /></label>
      <label class="sm:col-span-2 text-xs">右轴折线指标<el-select v-model="dualAxis.right_metrics" multiple aria-label="右轴指标" class="w-full mt-1"><el-option v-for="metric in metrics" :key="metric" :value="metric" :label="metric" /></el-select></label><p class="sm:col-span-2 text-xs text-muted">其余指标使用左轴柱形。左右轴分别标注单位；所有指标由 SQL 计算，页面不换算。</p>
    </div>
    <p v-if="kind==='stacked_bar'" class="text-xs text-muted">请选择同单位、可相加的指标。含 NULL 时回退原始表格，避免将缺失数据误当作0。</p>
    <label class="flex gap-2 items-center text-xs"><input type="checkbox" :checked="!!drillthrough" aria-label="启用报表下钻" @change="toggleDrill(($event.target as HTMLInputElement).checked)" />允许跳转到正式明细报表</label>
    <div v-if="drillthrough" class="space-y-3">
      <div class="flex gap-2"><input v-model.trim="keyword" aria-label="搜索下钻目标" placeholder="搜索可访问的正式报表" class="glass-input min-w-0 flex-1" @keydown.enter.prevent="loadTargets" /><button class="ghost-button text-xs ui-control-target" type="button" :disabled="loading" @click="loadTargets">搜索目标</button></div>
      <LoadingState v-if="loading" variant="inline" label="正在加载下钻目标…" /><label class="block text-xs">目标报表<select :value="drillthrough.report_id" aria-label="下钻目标报表" :disabled="loading" class="glass-input w-full mt-1" @change="selectTarget(Number(($event.target as HTMLSelectElement).value))"><option :value="0">选择含参数的正式报表</option><option v-for="target in targets" :key="target.id" :value="target.id">{{ target.name }}</option></select></label>
      <label v-for="parameter in loading ? [] : selected?.parameters || []" :key="parameter.name" class="block text-xs">{{ parameter.label }}（{{ parameter.name }}）<select :value="drillthrough.bindings[parameter.name] || ''" :aria-label="`下钻参数${parameter.name}`" class="glass-input w-full mt-1" @change="bind(parameter.name,($event.target as HTMLSelectElement).value)"><option value="">使用目标报表默认值</option><option v-for="column in columns" :key="column" :value="column">{{ column }}</option></select></label>
      <p class="text-xs text-muted">仅传递当前查询结果中的字段。下钻设置随报表版本审批，目标报表每次重新检查查看者权限。</p>
    </div>
    <p v-if="error" role="alert" class="text-xs text-danger">{{ error }}</p>
  </fieldset>
</template>
