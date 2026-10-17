<template>
  <section class="glass-card p-4 sm:p-6 space-y-4">
    <header class="flex flex-wrap justify-between gap-3"><div><h2 class="font-medium">结构浏览</h2><p class="text-xs text-white/45 mt-1">{{ source.capabilities?.reason || '当前数据源支持的表和字段信息' }}</p></div><button v-if="canCollect" class="ghost-button text-xs" :disabled="collecting" @click="collect">{{ collecting ? '采集中…' : '采集结构' }}</button></header>
    <p v-if="error" class="text-sm text-rose-300 break-words" role="alert">{{ error }}</p><p v-if="collectionStatus" class="text-sm text-white/65" role="status">{{ collectionStatus }}</p>
    <div class="grid grid-cols-1 sm:grid-cols-3 gap-3">
      <label class="text-xs text-white/60">数据库<el-select v-model="database" class="w-full mt-1" aria-label="结构数据库" :loading="loading"><el-option v-for="d in databases" :key="d" :label="d" :value="d" /></el-select></label>
      <label class="text-xs text-white/60">Schema<el-select v-model="schema" class="w-full mt-1" aria-label="结构 Schema" :loading="loading"><el-option v-for="s in schemas" :key="s" :label="s || '默认'" :value="s" /></el-select></label>
      <label class="text-xs text-white/60">表或视图<el-select v-model="table" class="w-full mt-1" aria-label="结构表" :loading="loading"><el-option v-for="t in tables" :key="t.name" :label="`${t.name} · ${t.type === 'view' ? '视图' : '表'}`" :value="t.name" /></el-select></label>
    </div>
    <div class="overflow-x-auto" v-loading="loading"><table v-if="columns.length" class="w-full text-left text-xs"><thead class="text-white/45"><tr><th class="p-3">字段</th><th class="p-3">类型</th><th class="p-3">主键</th><th class="p-3">允许空值</th><th class="p-3">说明</th></tr></thead><tbody><tr v-for="c in columns" :key="c.name" class="border-t border-white/10"><td class="p-3 font-mono">{{ c.name }}</td><td class="p-3">{{ c.data_type }}</td><td class="p-3">{{ c.is_primary ? '是' : '否' }}</td><td class="p-3">{{ c.is_nullable ? '是' : '否' }}</td><td class="p-3">{{ c.comment || '—' }}</td></tr></tbody></table><p v-else-if="!loading" class="py-8 text-center text-sm text-white/40">{{ tables.length ? '请选择表查看字段' : '当前范围暂无可见表' }}</p></div>
    <p class="text-xs text-white/40">SQL 尚未启用。采集完成后可在数据目录查看快照；DDL、统计等信息按驱动实际能力提供。</p>
  </section>
</template>

<script setup lang="ts">
import {computed,onBeforeUnmount,onMounted,ref,watch} from 'vue'
import type {ConnectionItem} from '../../api/datasource'
import {workbenchApi,type TableInfo,type ColumnInfo} from '../../api/workbench'
import {assetApi} from '../../api/asset'
import {useUserStore} from '../../stores/user'
const props=defineProps<{source:ConnectionItem}>(),user=useUserStore()
const canCollect=computed(()=>!!props.source.capabilities?.collect && ['admin','developer'].includes(user.role))
const databases=ref<string[]>([]),schemas=ref<string[]>([]),tables=ref<TableInfo[]>([]),columns=ref<ColumnInfo[]>([])
const database=ref(''),schema=ref(''),table=ref(''),loading=ref(false),collecting=ref(false),error=ref(''),collectionStatus=ref('')
let live=true,revision=0
async function load(kind:'databases'|'schemas'|'tables'|'columns'){const run=++revision;loading.value=true;error.value='';try{
 if(kind==='databases'){const r=await workbenchApi.databases(props.source.id);if(!live||run!==revision)return;databases.value=r.items.map(d=>d.name);database.value=databases.value.includes(props.source.database)?props.source.database:databases.value[0]||'';if(!database.value){tables.value=[];columns.value=[]}}
 if(kind==='schemas'){tables.value=[];columns.value=[];table.value='';const r=await workbenchApi.schemas(props.source.id,database.value);if(!live||run!==revision)return;schemas.value=r.items.length?r.items:[''];schema.value='';schema.value=schemas.value[0]!;if(schema.value==='')void load('tables')}
 if(kind==='tables'){columns.value=[];table.value='';const r=await workbenchApi.tables(props.source.id,{database:database.value,schema:schema.value});if(!live||run!==revision)return;tables.value=r.items;table.value=r.items[0]?.name||''}
 if(kind==='columns'&&table.value){const r=await workbenchApi.columns(props.source.id,{database:database.value,schema:schema.value,table:table.value});if(!live||run!==revision)return;columns.value=r.items}
 }catch(e){if(live&&run===revision)error.value=e instanceof Error?e.message:'结构读取失败'}finally{if(live&&run===revision)loading.value=false}}
watch(database,()=>{schema.value='';schemas.value=[];if(database.value)void load('schemas')})
watch(schema,()=>{if(schema.value)void load('tables')})
watch(table,()=>{if(table.value)void load('columns')})
async function collect(){if(collecting.value)return;collecting.value=true;collectionStatus.value='';try{const r=await assetApi.sync(props.source.id);if(live)collectionStatus.value=r.connections.map(c=>c.status==='succeeded'?`已采集 ${c.tables} 张表、${c.views} 个视图`:c.errors?.join('；')||'采集未完成').join('；')}catch(e){if(live)collectionStatus.value=e instanceof Error?e.message:'采集失败'}finally{if(live)collecting.value=false}}
onMounted(()=>void load('databases'));onBeforeUnmount(()=>{live=false;revision++})
</script>
