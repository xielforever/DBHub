<script setup lang="ts">
import { ref, reactive, computed, watch, onScopeDispose } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { useUserStore } from '../../../stores/user'
import { advancedApi, type QueryPolicy } from '../../../api/advanced'
const props=defineProps<{connectionId:number;database:string;schema?:string;table?:string;editable?:boolean}>()
const userStore=useUserStore(),canEdit=computed(()=>userStore.hasCapability('datasource_manage')&&props.editable!==false)
const policies=ref<QueryPolicy[]>([]),loading=ref(false),saving=ref(false),deleting=ref<number>(),error=ref(''),loadFailed=ref(false),columnJSON=ref('{}'),conditionJSON=ref('[]')
const busy=computed(()=>loading.value||saving.value||deleting.value!==undefined)
const form=reactive({database_name:props.database,schema_name:props.schema||'',table_name:props.table||'',role_code:'readonly'})
let alive=true,generation=0
async function load(){const run=++generation;policies.value=[];loading.value=true;error.value='';loadFailed.value=false;try{if(!props.connectionId)return;const result=await advancedApi.policies(props.connectionId);if(alive&&run===generation)policies.value=result.items}catch(e){if(alive&&run===generation){loadFailed.value=true;error.value=e instanceof Error?e.message:'读取访问策略失败'}}finally{if(alive&&run===generation)loading.value=false}}
watch(()=>[props.connectionId,props.database,props.schema,props.table],()=>{Object.assign(form,{database_name:props.database,schema_name:props.schema||'',table_name:props.table||''});columnJSON.value='{}';conditionJSON.value='[]';void load()},{immediate:true})
function edit(policy:QueryPolicy){Object.assign(form,{database_name:policy.database_name,schema_name:policy.schema_name,table_name:policy.table_name,role_code:policy.role_code});columnJSON.value=JSON.stringify(policy.column_permissions,null,2);conditionJSON.value=JSON.stringify(policy.row_conditions,null,2)}
async function save(){if(busy.value||!canEdit.value||loadFailed.value)return;error.value='';let columns:QueryPolicy['column_permissions'],conditions:QueryPolicy['row_conditions'];try{columns=JSON.parse(columnJSON.value);conditions=JSON.parse(conditionJSON.value);if(!columns||typeof columns!=='object'||Array.isArray(columns)||!Array.isArray(conditions))throw new Error()}catch{error.value='列权限必须是 JSON 对象，行条件必须是 JSON 数组。';return}saving.value=true;try{await advancedApi.savePolicy({connection_id:props.connectionId,...form,column_permissions:columns,row_conditions:conditions});if(alive){await load();ElMessage.success('访问策略已保存并生效')}}catch(e){if(alive)error.value=e instanceof Error?e.message:'保存访问策略失败'}finally{if(alive)saving.value=false}}
async function remove(policy:QueryPolicy){if(busy.value||!canEdit.value||!policy.id)return;try{await ElMessageBox.confirm(`删除 ${policy.role_code} 在 ${policy.database_name}.${policy.schema_name}.${policy.table_name} 的策略？若这是此角色的最后一条策略，连接将恢复为数据库账号本身的访问范围。`,'删除访问策略',{confirmButtonText:'确认删除',cancelButtonText:'取消',type:'warning'})}catch{return}if(!alive)return;deleting.value=policy.id;error.value='';try{await advancedApi.deletePolicy(policy.id);if(alive){await load();ElMessage.success('访问策略已删除')}}catch(e){if(alive)error.value=e instanceof Error?e.message:'删除访问策略失败'}finally{if(alive)deleting.value=undefined}}
onScopeDispose(()=>{alive=false;generation++})
</script>

<template>
 <section class="max-h-[65vh] overflow-auto space-y-4 p-1" :aria-busy="busy">
  <p class="text-xs leading-6 text-muted">策略由服务端执行。受限角色仅能查询配置过策略的表，且只支持单表 SELECT；隐藏列不可查询，掩码列返回 NULL，复杂查询被拒绝。全局数据源的发布者连接策略也适用于引用该修订的个人连接。</p>
  <p v-if="!connectionId" class="text-xs text-warning">请先选择数据源。</p>
  <p v-if="error" role="alert" class="text-sm text-danger">{{error}}</p>
  <button class="ghost-button text-xs ui-control-target" :disabled="busy" @click="load">{{loading?'加载中…':'刷新策略'}}</button>
  <div v-data-loading="loading" data-loading-label="正在加载行列访问策略…" data-loading-variant="table" class="space-y-4"><article v-for="policy in policies" :key="policy.id" class="glass-card p-3 space-y-2 text-xs" :aria-label="`策略：${policy.role_code} ${policy.schema_name}.${policy.table_name}`">
   <p class="text-accent">{{ policy.role_code }} · {{ policy.database_name }}.{{ policy.schema_name }}.{{ policy.table_name }}</p>
   <pre class="whitespace-pre-wrap break-words text-muted">{{ JSON.stringify({column_permissions:policy.column_permissions,row_conditions:policy.row_conditions},null,2) }}</pre>
   <div v-if="canEdit" class="flex gap-2"><button class="ghost-button text-xs ui-control-target" :disabled="busy" @click="edit(policy)">编辑此策略</button><button class="ghost-button text-xs text-danger ui-control-target" :disabled="busy" @click="remove(policy)">{{deleting===policy.id?'删除中…':'删除此策略'}}</button></div>
  </article>
  <p v-if="!loading&&!policies.length" class="text-xs text-muted">此连接没有适用于当前角色的行列策略。</p>
  </div><form v-if="canEdit" class="space-y-3 border-t border-stroke pt-4" @submit.prevent="save">
   <p class="text-xs text-warning">保存后，目标角色在此连接仅能查询配置过策略的表。请核对数据源、数据库和角色。</p>
   <fieldset :disabled="busy || loadFailed" class="space-y-3 disabled:opacity-60">
    <div class="grid grid-cols-1 sm:grid-cols-2 gap-3"><label class="text-xs text-muted space-y-1">数据库<el-input v-model="form.database_name" aria-label="策略数据库" required /></label><label class="text-xs text-muted space-y-1">模式<el-input v-model="form.schema_name" aria-label="策略模式" placeholder="PostgreSQL 默认 public" /></label></div>
    <div class="grid grid-cols-1 sm:grid-cols-2 gap-3"><label class="text-xs text-muted space-y-1">目标表名<el-input v-model="form.table_name" aria-label="策略表名" required /></label><label class="text-xs text-muted space-y-1">目标角色<el-select v-model="form.role_code" aria-label="策略目标角色" class="w-full"><el-option label="开发者" value="developer" /><el-option label="只读用户" value="readonly" /></el-select></label></div>
    <label class="block text-xs text-muted space-y-1">列权限 JSON（visible / masked / hidden）<el-input v-model="columnJSON" aria-label="列权限 JSON" type="textarea" :rows="3" placeholder='{"email":"masked","password":"hidden"}' /></label>
    <label class="block text-xs text-muted space-y-1">行条件 JSON（AND）；值 "$user_id" 为当前应用用户 ID<el-input v-model="conditionJSON" aria-label="行条件 JSON" type="textarea" :rows="3" placeholder='[{"column":"owner_id","operator":"=","value":"$user_id"}]' /></label>
    <button type="submit" class="liquid-button text-xs ui-control-target" :disabled="!form.table_name.trim() || !form.database_name.trim()">{{saving?'保存中…':'保存访问策略'}}</button>
   </fieldset>
  </form>
 </section>
</template>
