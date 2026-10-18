<template>
  <button class="relative w-11 h-11 shrink-0 rounded-xl flex items-center justify-center text-white/65 hover:bg-white/10" :aria-label="label" :title="label" @click="open">
    <Bell class="w-5 h-5" />
    <span v-if="unread" class="absolute right-0 top-0 min-w-4 h-4 rounded-full bg-rose-500 text-white text-[10px] px-1">{{unread>99?'99+':unread}}</span>
    <span v-if="failed" class="absolute right-1 bottom-1 w-1.5 h-1.5 bg-amber-300 rounded-full" />
  </button>
</template>
<script setup lang="ts">
import {computed,onMounted,onScopeDispose,ref} from 'vue'
import {useRouter} from 'vue-router'
import {Bell} from 'lucide-vue-next'
import {automationApi} from '../api/automation'
import {useUserStore} from '../stores/user'
const router=useRouter(),user=useUserStore(),unread=ref(0),todos=ref(0),failed=ref(false),controller=new AbortController()
let busy=false,alive=true,timer:ReturnType<typeof setInterval>|undefined
const label=computed(()=>failed.value?'通知状态暂不可用，打开通知与待办':`通知与待办，${unread.value} 条未读，${todos.value} 项待处理`)
async function load(){if(busy||!user.workspaceId||!navigator.onLine||document.hidden)return;busy=true;try{const r=await automationApi.inbox({page_size:1,state:'unread'},controller.signal);if(alive){unread.value=r.unread;todos.value=r.todo_count;failed.value=false}}catch{if(alive){unread.value=0;todos.value=0;failed.value=true}}finally{busy=false}}
function open(){void router.push({path:'/inbox',query:{tab:todos.value?'todos':'notifications'}})}
onMounted(()=>{void load();timer=setInterval(()=>void load(),30000);window.addEventListener('dbhub-inbox-updated',load);window.addEventListener('online',load);document.addEventListener('visibilitychange',load)})
onScopeDispose(()=>{alive=false;controller.abort();clearInterval(timer);window.removeEventListener('dbhub-inbox-updated',load);window.removeEventListener('online',load);document.removeEventListener('visibilitychange',load)})
</script>
