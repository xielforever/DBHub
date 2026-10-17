<template>
  <section class="space-y-4">
    <header class="glass-card p-4 flex flex-wrap items-center justify-between gap-3"><div><h2 class="font-medium">键值浏览</h2><p v-if="overview" class="text-xs text-white/45 mt-1">Redis {{ overview.version }} · {{ overview.used_memory_mb }} MB · {{ overview.total_keys }} 个键</p></div><button v-if="canWrite" class="liquid-button text-xs" :disabled="busy" @click="createOpen = true">新建字符串键</button></header>
    <p v-if="error" class="text-sm text-rose-300 break-words" role="alert">{{ error }}</p>
    <div class="grid grid-cols-1 lg:grid-cols-[minmax(220px,1fr)_minmax(0,2fr)] gap-4">
      <section class="glass-card p-4 min-w-0 space-y-3">
        <form class="flex gap-2" @submit.prevent="loadKeys"><input v-model="pattern" aria-label="键名筛选" class="glass-input min-w-0" placeholder="键名模式，例如 orders:*" /><button class="ghost-button shrink-0 text-xs" :disabled="loading">刷新</button></form>
        <p class="text-xs text-white/40">最多显示 200 个匹配键</p>
        <ul v-loading="loading" class="max-h-96 overflow-y-auto divide-y divide-white/10"><li v-for="k in keys" :key="k.key"><button class="w-full text-left py-3 text-xs" :class="selected === k.key ? 'text-indigo-200' : 'text-white/65'" :disabled="busy" @click="inspect(k.key)"><span class="block break-all font-mono">{{ k.key }}</span><span class="text-white/40">{{ k.type }} · TTL {{ k.ttl }}</span></button></li></ul><p v-if="!keys.length && !loading" class="py-5 text-sm text-white/40">暂无匹配键</p>
      </section>
      <section class="glass-card p-4 min-w-0 space-y-4">
        <template v-if="detail"><header class="flex flex-wrap justify-between items-start gap-3"><div class="min-w-0"><h3 class="font-mono text-sm break-all">{{ selected }}</h3><p class="text-xs text-white/45 mt-1">{{ detail.type }} · {{ detail.size }} 字节 · TTL {{ detail.ttl === -1 ? '永久' : `${detail.ttl} 秒` }}</p></div><button v-if="canWrite" class="ghost-button text-xs text-rose-300" :disabled="busy" @click="remove">删除键</button></header>
          <pre class="max-h-96 overflow-auto whitespace-pre-wrap break-all text-xs leading-6 text-white/75" data-testid="redis-value">{{ valueText }}</pre>
          <form v-if="canWrite" class="flex flex-wrap items-end gap-2 border-t border-white/10 pt-4" @submit.prevent="updateTTL"><label class="text-xs text-white/60">TTL 秒数（-1 为永久）<input v-model.number="ttl" type="number" min="-1" class="glass-input mt-1" aria-label="TTL 秒数" /></label><button class="ghost-button text-xs" :disabled="busy">更新 TTL</button></form>
        </template><p v-else class="py-10 text-center text-sm text-white/40">选择键查看类型、值和 TTL</p>
      </section>
    </div>
    <el-dialog v-model="createOpen" title="新建字符串键" :width="'min(92vw, 520px)'" class="glass-dialog" :close-on-click-modal="false">
      <div class="space-y-3"><label class="block text-xs text-white/60">键名<input v-model="newKey" class="glass-input mt-1" /></label><label class="block text-xs text-white/60">值<textarea v-model="newValue" class="glass-input mt-1 min-h-24" /></label><p class="text-xs text-white/40">新键默认永久保存；已存在的键不会覆盖。</p></div><template #footer><button class="liquid-button" :disabled="busy || !newKey" @click="create">{{ busy ? '创建中…' : '创建' }}</button></template>
    </el-dialog>
  </section>
</template>

<script setup lang="ts">
import {computed,onBeforeUnmount,onMounted,ref} from 'vue'
import {ElMessageBox,ElMessage} from 'element-plus'
import type {ConnectionItem} from '../../api/datasource'
import {workbenchApi,type RedisKey,type RedisOverview,type RedisValue} from '../../api/workbench'
import {useUserStore} from '../../stores/user'
const props=defineProps<{source:ConnectionItem}>(),user=useUserStore(),canWrite=computed(()=>['admin','developer'].includes(user.role))
const keys=ref<RedisKey[]>([]),overview=ref<RedisOverview>(),detail=ref<RedisValue>(),pattern=ref('*'),selected=ref(''),error=ref(''),loading=ref(false),busy=ref(false),ttl=ref(-1),createOpen=ref(false),newKey=ref(''),newValue=ref('')
let live=true,keyRequest=0,valueRequest=0
const valueText=computed(()=>typeof detail.value?.value==='string'?detail.value.value:JSON.stringify(detail.value?.value,null,2))
async function loadKeys(){const run=++keyRequest;loading.value=true;error.value='';try{const r=await workbenchApi.redisKeys(props.source.id,pattern.value,200);if(!live||run!==keyRequest)return;keys.value=r.items;if(selected.value&&!r.items.some(k=>k.key===selected.value)){selected.value='';detail.value=undefined;valueRequest++}}catch(e){if(live&&run===keyRequest)error.value=e instanceof Error?e.message:'键读取失败'}finally{if(live&&run===keyRequest)loading.value=false}}
async function inspect(key:string){const run=++valueRequest;selected.value=key;detail.value=undefined;error.value='';try{const r=await workbenchApi.redisValue(props.source.id,key);if(!live||run!==valueRequest)return;detail.value=r;ttl.value=r.ttl;keys.value=keys.value.map(item=>item.key===key?{...item,ttl:r.ttl,type:r.type}:item)}catch(e){if(live&&run===valueRequest)error.value=e instanceof Error?e.message:'值读取失败'}}
async function updateTTL(){if(busy.value||!selected.value)return;if(!Number.isInteger(ttl.value)||ttl.value< -1){error.value='TTL 必须为 -1 或非负整数';return}busy.value=true;try{await workbenchApi.redisUpdateTTL(props.source.id,selected.value,ttl.value);if(live)await inspect(selected.value)}catch{}finally{if(live)busy.value=false}}
async function remove(){if(busy.value||!selected.value)return;const key=selected.value;try{await ElMessageBox.confirm(`删除键「${key}」？此操作不能撤销。`,'删除确认',{type:'warning'})}catch{return}if(busy.value)return;busy.value=true;try{await workbenchApi.redisDeleteKey(props.source.id,key);if(live){detail.value=undefined;selected.value='';await loadKeys()}}catch{}finally{if(live)busy.value=false}}
async function create(){if(busy.value||!newKey.value)return;busy.value=true;try{await workbenchApi.redisCreateKey(props.source.id,{key:newKey.value,type:'string',value:newValue.value,ttl:-1});if(live){createOpen.value=false;ElMessage.success('键已创建');await loadKeys();await inspect(newKey.value);newKey.value='';newValue.value=''}}catch{}finally{if(live)busy.value=false}}
onMounted(()=>{void loadKeys();void workbenchApi.redisOverview(props.source.id).then(r=>{if(live)overview.value=r}).catch(()=>{})});onBeforeUnmount(()=>{live=false;keyRequest++;valueRequest++})
</script>
