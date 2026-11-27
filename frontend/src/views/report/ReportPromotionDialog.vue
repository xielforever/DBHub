<script setup lang="ts">
import LoadingState from '../../components/base/LoadingState.vue'
import { computed, onMounted, onScopeDispose, ref } from 'vue'
import { connectionApi, type ConnectionItem } from '../../api/datasource'
import { managedReportApi, type ReportVersion } from '../../api/managed-report'
import { reportCommandKeys } from './reportCommands'
const props=defineProps<{reportId:number;version:ReportVersion}>(),emit=defineEmits<{close:[];created:[id:number]}>()
const name=ref((props.version.definition.name+'·目标草稿').slice(0,128)),connectionId=ref<number>(),sources=ref<ConnectionItem[]>([]),loading=ref(true),busy=ref(false),error=ref(''),clearDrill=ref(false),keys=reportCommandKeys(),target=computed(()=>sources.value.find(source=>source.id===connectionId.value))
let alive=true
onMounted(async()=>{try{const result=await connectionApi.list();if(alive)sources.value=result.items.filter(source=>source.capabilities?.sql&&source.capabilities.enforced_read_only&&source.definition?.visibility==='global')}catch(e){if(alive)error.value=e instanceof Error?e.message:'读取目标连接失败'}finally{if(alive)loading.value=false}})
async function promote(){if(busy.value||loading.value||!connectionId.value)return;busy.value=true;error.value='';const payload={review_id:props.version.review_id,target_connection_id:connectionId.value,name:name.value.trim(),clear_drillthrough:clearDrill.value};try{const result=await managedReportApi.promote(props.reportId,{...payload,request_id:keys.get('promotion',payload)});if(alive)emit('created',result.id)}catch(e){if(alive)error.value=e instanceof Error?e.message:'迁移失败，请沿用当前编号核对'}finally{if(alive)busy.value=false}}
onScopeDispose(()=>{alive=false;keys.clear()})
</script>

<template>
 <el-dialog :model-value="true" title="迁移获批版本到目标草稿" width="min(620px, 94vw)" :close-on-click-modal="false" :close-on-press-escape="!busy" :show-close="!busy" @close="emit('close')">
  <form class="space-y-4" @submit.prevent="promote">
   <p class="text-xs leading-6 text-muted">从 v{{version.number}} 复制已冻结的 SQL、参数、指标契约和导出策略。目标新建为你的未提交草稿，执行账号及受众需要重新选择，并由其他审批人验证后发布。</p>
   <p class="text-xs break-all text-muted">来源校验值：{{version.content_hash}}</p>
   <p v-if="error" role="alert" class="text-sm text-danger">{{error}}</p>
   <LoadingState v-if="loading" variant="inline" label="正在加载迁移目标连接…" /><fieldset class="space-y-4" :disabled="busy||loading">
    <label class="block text-xs text-muted space-y-1">目标个人连接<el-select v-model="connectionId" aria-label="迁移目标连接" :loading="loading" loading-text="正在加载目标连接…" class="w-full" placeholder="明确选择目标数据源与环境" :disabled="busy||loading"><el-option v-for="source in sources" :key="source.id" :value="source.id" :label="`${source.name} · ${source.environment} · ${source.database}`" /></el-select></label>
    <p v-if="target" class="text-xs text-warning">目标环境：{{target.environment==='prod'?'生产':target.environment==='test'?'测试':'开发'}} · {{target.database}} · 数据源修订 #{{target.definition_revision_id}}</p>
    <p v-if="!loading&&!error&&!sources.length" class="text-xs text-warning">暂无可用的全局数据源个人连接，请先在数据源中配置自己的目标连接。</p>
    <label class="block text-xs text-muted space-y-1">目标报表名称<el-input v-model="name" aria-label="迁移目标报表名称" maxlength="128" /></label>
    <label v-if="version.definition.chart_config.drillthrough" class="flex gap-2 text-xs text-muted"><input v-model="clearDrill" type="checkbox">移除来源环境的下钻目标，稍后在新草稿中重新配置</label>
    <button class="liquid-button text-xs ui-control-target" :disabled="!connectionId||!name.trim()||(!!version.definition.chart_config.drillthrough&&!clearDrill)">{{busy?'正在创建…':'创建目标草稿'}}</button>
   </fieldset>
  </form>
 </el-dialog>
</template>
