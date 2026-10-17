<template>
 <div class="flex flex-col h-full overflow-auto p-4 gap-3">
  <div class="flex items-center gap-2"><Flame class="w-4 h-4 text-orange-300" /><h3 class="text-sm flex-1">执行计划成本</h3><span v-if="duration!==null" class="text-xs text-white/40">本次请求 {{ duration }}ms</span></div>
  <p class="text-xs text-white/40">宽度来自 PostgreSQL EXPLAIN 返回的估计成本，不代表实际耗时。</p>
  <p v-if="!nodes.length" class="text-xs text-white/40 py-8 text-center">请先运行 PostgreSQL EXPLAIN。当前结果未提供可解析的成本数据。</p>
  <div v-for="(node,i) in nodes" :key="i" class="space-y-1" :style="{marginLeft:Math.min(node.depth,8)*12+'px'}"><div class="flex justify-between text-xs text-white/65"><span>{{ node.name }}</span><span>{{ node.cost }}</span></div><div class="h-5 bg-white/5 rounded"><div class="h-full rounded bg-orange-400/30" :style="{width:Math.max(1,node.cost/maxCost*100)+'%'}" /></div></div>
 </div>
</template>
<script setup lang="ts">
import { computed } from 'vue'
import { Flame } from 'lucide-vue-next'
const props=defineProps<{duration:number|null;sql:string;rows:unknown[][];columns:string[]}>()
const nodes=computed(()=>{
 if(props.columns[0]!=='QUERY PLAN')return []
 return props.rows.flatMap(row=>{const text=String(row[0]??'');const match=text.match(/^(\s*)(?:->\s*)?(.+?)\s+\(cost=[\d.]+\.\.([\d.]+)/);return match?[{name:match[2]!,depth:Math.floor(match[1]!.length/2),cost:Number(match[3])}]:[]})
})
const maxCost=computed(()=>Math.max(1,...nodes.value.map(n=>n.cost)))
</script>
