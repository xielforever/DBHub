<template>
 <div class="flex flex-col h-full overflow-auto p-4 gap-3">
  <div class="flex items-center gap-2"><KeyRound class="w-4 h-4 text-indigo-300" /><h3 class="text-sm flex-1">行列访问策略</h3><span class="text-xs text-white/40">{{ userStore.role }}</span></div>
  <p class="text-xs text-white/45">策略在服务端执行。受限角色仅支持单表 SELECT；隐藏列不可查询，掩码列返回 NULL。复杂查询会被拒绝。</p>
  <p v-if="!connectionId" class="text-xs text-amber-300">请先选择数据源。</p>
  <template v-else>
   <div class="space-y-2" v-for="p in policies" :key="p.id"><div class="glass-card p-3 text-xs"><p class="text-indigo-200">{{ p.role_code }} · {{ p.database_name }}.{{ p.schema_name }}.{{ p.table_name }}</p><pre class="text-white/60 whitespace-pre-wrap mt-2">{{ JSON.stringify({columns:p.column_permissions,conditions:p.row_conditions},null,2) }}</pre><div v-if="isAdmin" class="flex gap-2 mt-2"><button class="ghost-button text-xs" @click="edit(p)">编辑</button><button class="ghost-button text-xs text-rose-300" @click="remove(p.id!)">删除</button></div></div></div>
   <div v-if="isAdmin" class="space-y-3 border-t border-white/10 pt-3">
    <p class="text-xs text-white/60">保存策略后，目标角色在此连接仅能查询已配置策略的表。</p>
    <div class="grid grid-cols-2 gap-2"><el-input v-model="form.database_name" placeholder="数据库" /><el-input v-model="form.schema_name" placeholder="模式（PostgreSQL 默认 public）" /></div>
    <div class="flex gap-2"><el-input v-model="form.table_name" placeholder="目标表名" /><el-select v-model="form.role_code" class="w-36"><el-option label="开发者" value="developer" /><el-option label="只读用户" value="readonly" /></el-select></div>
    <label class="block text-xs text-white/50">列权限 JSON：visible / masked / hidden</label><el-input v-model="columnJSON" type="textarea" :rows="3" placeholder='{"email":"masked","password":"hidden"}' />
    <label class="block text-xs text-white/50">行条件 JSON（AND）；值 "$user_id" 为当前用户 ID</label><el-input v-model="conditionJSON" type="textarea" :rows="3" placeholder='[{"column":"owner_id","operator":"=","value":"$user_id"}]' />
    <button class="liquid-button text-xs" :disabled="saving || !form.table_name" @click="save">{{ saving ? '保存中…' : '保存到服务端' }}</button>
   </div>
   <p v-if="!policies.length && !isAdmin" class="text-xs text-white/40">当前角色尚未配置行列策略。</p>
  </template>
 </div>
</template>
<script setup lang="ts">
import { ref, reactive, computed, watch } from 'vue'
import { KeyRound } from 'lucide-vue-next'
import { ElMessage } from 'element-plus'
import { useUserStore } from '../../../stores/user'
import { advancedApi, type QueryPolicy } from '../../../api/advanced'
const props = defineProps<{connectionId:number;database:string;schema?:string;table?:string}>()
const userStore=useUserStore(), isAdmin=computed(()=>userStore.role==='admin')
const policies=ref<QueryPolicy[]>([]), saving=ref(false), columnJSON=ref('{}'), conditionJSON=ref('[]')
const form=reactive({database_name:props.database,schema_name:props.schema || '',table_name:props.table || '',role_code:'readonly'})
async function load(){if(!props.connectionId){policies.value=[];return};try{policies.value=(await advancedApi.policies(props.connectionId)).items}catch{}}
watch(()=>[props.connectionId,props.database,props.schema,props.table],()=>{Object.assign(form,{database_name:props.database,schema_name:props.schema||'',table_name:props.table||''});load()},{immediate:true})
function edit(p:QueryPolicy){Object.assign(form,{database_name:p.database_name,schema_name:p.schema_name,table_name:p.table_name,role_code:p.role_code});columnJSON.value=JSON.stringify(p.column_permissions,null,2);conditionJSON.value=JSON.stringify(p.row_conditions,null,2)}
async function save(){saving.value=true;try{let columns:QueryPolicy['column_permissions'],conditions:QueryPolicy['row_conditions'];try{columns=JSON.parse(columnJSON.value);conditions=JSON.parse(conditionJSON.value)}catch{ElMessage.warning('JSON 格式错误');return};await advancedApi.savePolicy({connection_id:props.connectionId,...form,column_permissions:columns,row_conditions:conditions});await load();ElMessage.success('策略已在服务端生效')}catch(e:unknown){ElMessage.error(e instanceof Error?e.message:'保存失败')}finally{saving.value=false}}
async function remove(id:number){try{await advancedApi.deletePolicy(id);await load();ElMessage.success('策略已删除')}catch{}}
</script>
