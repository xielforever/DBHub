<template>
 <div class="flex items-center gap-2 text-[11px] text-white/45" aria-live="polite">
  <span class="w-1.5 h-1.5 rounded-full" :class="isLive ? 'bg-emerald-400' : 'bg-white/30'" />
  <span>{{ isLive ? `${users.length} 人在线` : '在线状态不可用' }}</span>
  <span v-for="user in users" :key="user.name" class="px-1.5 py-0.5 rounded-full bg-indigo-500/15 text-indigo-200">{{ user.name }}{{ user.self ? '（我）' : '' }}</span>
 </div>
</template>
<script setup lang="ts">
import { ref, watch, onBeforeUnmount } from 'vue'
import { advancedApi } from '../../../api/advanced'
const props = defineProps<{connectionId: number}>()
const users = ref<{name: string; self: boolean}[]>([]), isLive = ref(false)
let timer: ReturnType<typeof setInterval> | undefined
let generation = 0
watch(() => props.connectionId, id => {const version = ++generation; clearInterval(timer); users.value = []; isLive.value = false; async function beat() { try { const res = await advancedApi.presence(id); if(version === generation) { users.value = res.items; isLive.value = true } } catch { if(version === generation) isLive.value = false } }; if(id) {beat(); timer = setInterval(beat, 15000)} }, {immediate: true})
onBeforeUnmount(() => { generation++; clearInterval(timer) })
</script>
