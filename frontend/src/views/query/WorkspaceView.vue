<template>
  <div class="space-y-4">
    <section class="glass-panel px-4 py-3" aria-label="数据源选择">
      <div class="flex flex-wrap items-end gap-3">
        <label class="block min-w-0 flex-1 sm:max-w-md text-xs text-white/60">数据源
          <el-select :model-value="selected?.id" aria-label="选择数据源" placeholder="选择数据源" class="w-full mt-1" :loading="loading" :disabled="busy" popper-class="glass-popper" @change="select">
            <el-option v-for="item in sources" :key="item.id" :label="`${item.name} · ${sourceLabel(item)}`" :value="item.id" />
          </el-select>
        </label>
        <span v-if="selected" class="text-xs pb-2" :class="selected.environment === 'prod' ? 'text-rose-300' : 'text-white/45'">{{ selected.environment === 'prod' ? '生产环境' : sourceLabel(selected) }}</span>
        <button class="ghost-button text-xs" :disabled="busy || loading" @click="load">刷新</button>
      </div>
      <p v-if="error" class="text-xs text-rose-300 mt-2" role="alert">{{ error }}</p>
      <p v-else-if="!loading && !sources.length" class="text-sm text-white/55 mt-3">还没有数据源。<RouterLink to="/connections" class="text-indigo-300 underline">新建数据源</RouterLink></p>
    </section>
    <SqlWorkspace v-if="selected?.capabilities?.sql" :key="selected.id" :source="selected" @busy="busy = $event" />
    <RedisWorkspace v-else-if="selected?.capabilities?.keys" :key="selected.id" :source="selected" />
    <MetadataWorkspace v-else-if="selected?.capabilities?.metadata" :key="selected.id" :source="selected" />
    <section v-else-if="selected" class="glass-card p-6 space-y-3">
      <h2 class="font-medium">{{ selected.name }}</h2><p class="text-sm text-amber-200" role="status">{{ selected.capabilities?.reason || '该数据源尚无可用操作视图，请先检查配置。' }}</p>
      <RouterLink to="/connections" class="text-sm text-indigo-300 underline">返回数据源检查配置</RouterLink>
    </section>
  </div>
</template>

<script setup lang="ts">
import {computed,onMounted,ref,watch} from 'vue'
import {useRoute,useRouter} from 'vue-router'
import {connectionApi,sourceLabel,type ConnectionItem} from '../../api/datasource'
import SqlWorkspace from './SqlWorkspace.vue'
import MetadataWorkspace from './MetadataWorkspace.vue'
import RedisWorkspace from './RedisWorkspace.vue'
const route=useRoute(),router=useRouter(),sources=ref<ConnectionItem[]>([]),loading=ref(false),busy=ref(false),error=ref('')
const selected = computed(() => {
  const raw = route.query.connection
  if (raw === undefined) return sources.value[0]
  if (typeof raw !== 'string' || !/^[1-9][0-9]*$/.test(raw)) return undefined
  const id = Number(raw)
  return Number.isSafeInteger(id) ? sources.value.find(source => source.id === id) : undefined
})
let generation=0
async function load(){const run=++generation;loading.value=true;error.value='';try{const result=await connectionApi.list();if(run!==generation)return;sources.value=result.items;if(route.query.connection!==undefined&&!selected.value)error.value='指定数据源不可用，请重新选择。'}catch(e){if(run===generation)error.value=e instanceof Error?e.message:'数据源加载失败'}finally{if(run===generation)loading.value=false}}
function select(id:number){if(busy.value)return;error.value='';void router.replace({path:'/query',query:{...route.query,connection:String(id)}})}
watch(()=>selected.value?.id,()=>{busy.value=false})
onMounted(()=>void load())
</script>
