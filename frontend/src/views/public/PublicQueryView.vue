<template>
 <main class="min-h-screen bg-[#0a0a0f] text-white p-6"><article class="glass-card p-6 max-w-5xl mx-auto space-y-4">
  <h1 class="text-lg">DBHub · 查询分享</h1><p v-if="error" class="text-rose-300 text-sm">{{ error }}</p>
  <template v-else-if="data"><h2>{{ data.name }}</h2><p class="text-xs text-white/40">只读快照 · {{ data.database }}</p><pre class="text-xs whitespace-pre-wrap bg-white/5 rounded-xl p-4">{{ data.sql }}</pre>
   <div class="overflow-auto"><table v-if="data.columns" class="w-full text-xs"><thead><tr><th v-for="(col,i) in data.columns" :key="i" class="text-left p-2 border-b border-white/10">{{ col }}</th></tr></thead><tbody><tr v-for="(row,i) in data.rows" :key="i"><td v-for="(value,j) in row" :key="j" class="p-2 border-b border-white/5">{{ value ?? 'NULL' }}</td></tr></tbody></table></div>
  </template><p v-else class="text-sm text-white/40">加载中…</p>
 </article></main>
</template>
<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRoute } from 'vue-router'
const route=useRoute(), error=ref('')
const data=ref<{name:string;sql:string;database:string;columns?:string[];rows?:unknown[][]}|null>(null)
onMounted(async()=>{try{const res=await fetch(`/api/v1/public/query-shares/${encodeURIComponent(String(route.params.token))}`,{cache:'no-store',credentials:'omit'});const body=await res.json();if(!res.ok||body.code!==0)throw new Error(body.message||'分享不可用');data.value=body.data}catch(e:unknown){error.value=e instanceof Error?e.message:'分享加载失败'}})
</script>
