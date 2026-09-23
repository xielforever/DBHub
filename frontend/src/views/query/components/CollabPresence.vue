<template>
  <div class="flex items-center gap-2">
    <div class="flex -space-x-2">
      <div v-for="u in users" :key="u.id" class="w-6 h-6 rounded-full border-2 border-[#0a0a14] flex items-center justify-center text-[10px] font-medium" :style="{ background: u.color + '30', color: u.color, borderColor: '#0a0a14' }" :title="`${u.name} · ${u.status}`">
        {{ u.name.slice(0, 1).toUpperCase() }}
      </div>
    </div>
    <span v-if="users.length" class="text-[11px] text-white/40">{{ users.length }} 人正在查看</span>
    <span v-else class="text-[11px] text-white/30">仅自己</span>
    <div class="w-px h-3 bg-white/10 mx-1" />
    <span class="text-[11px] px-1.5 py-0.5 rounded-full flex items-center gap-1" :class="isLive ? 'bg-emerald-500/20 text-emerald-300' : 'bg-white/10 text-white/40'">
      <span class="w-1.5 h-1.5 rounded-full" :class="isLive ? 'bg-emerald-400 animate-pulse' : 'bg-white/30'" />
      {{ isLive ? '实时协作' : '离线' }}
    </span>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, onBeforeUnmount } from 'vue'

interface User {
  id: number
  name: string
  color: string
  status: 'viewing' | 'editing'
}

const users = ref<User[]>([
  { id: 1, name: 'admin', color: '#A78BFA', status: 'editing' },
  { id: 2, name: 'dev1', color: '#34D399', status: 'viewing' },
])
const isLive = ref(true)

let timer: any = null
onMounted(() => {
  // 模拟协作用户进出
  timer = setInterval(() => {
    if (Math.random() > 0.7) {
      if (users.value.length < 4 && Math.random() > 0.5) {
        const names = ['readonly', 'analyst', 'ops']
        const colors = ['#FBBF24', '#60A5FA', '#F472B6']
        const idx = Math.floor(Math.random() * names.length)
        users.value.push({ id: Date.now(), name: names[idx]!, color: colors[idx]!, status: Math.random() > 0.5 ? 'viewing' : 'editing' })
      } else if (users.value.length > 1) {
        users.value.splice(1, 1)
      }
    }
  }, 5000)
})
onBeforeUnmount(() => clearInterval(timer))
</script>
