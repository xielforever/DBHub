<template>
  <section id="operations" class="glass-card p-5 space-y-4">
    <div class="flex flex-wrap items-center justify-between gap-3"><div><h2 class="font-medium">运行与备份</h2><p class="text-xs text-white/45 mt-1">仅平台管理员可见，显示最近一次后台检查。</p></div><button class="ghost-button text-xs min-h-11" :disabled="loading" @click="load">刷新状态</button></div>
    <p v-if="error" role="alert" class="text-sm text-rose-300">{{error}}</p>
    <template v-if="snapshot">
      <div v-if="snapshot.alerts.some(a=>a.active)" role="alert" class="rounded-xl border border-amber-300/30 p-3 space-y-1"><p v-for="alert in snapshot.alerts.filter(a=>a.active)" :key="alert.code" class="text-sm text-amber-200">{{alertLabels[alert.code]||alert.code}} · {{formatTime(alert.changed_at)}}</p></div>
      <p v-else class="text-xs text-emerald-200">最近一次检查没有活动告警</p>
      <div class="grid sm:grid-cols-2 gap-3"><div class="rounded-xl border border-white/10 p-3"><p class="text-xs text-white/45">审计待写</p><p class="text-lg mt-1">{{snapshot.audit?.pending??'—'}} <span class="text-xs text-white/40">/ {{snapshot.audit?.capacity??'—'}}</span></p><p class="text-xs text-white/50 mt-1">{{snapshot.audit?.ready?'持久化队列可接收':'队列状态不可用'}}</p></div><div class="rounded-xl border border-white/10 p-3"><p class="text-xs text-white/45">备份监测</p><p class="text-sm mt-2">{{snapshot.config.backup_expected_hours?`预期间隔 ${snapshot.config.backup_expected_hours} 小时`:'未启用自动备份监测'}}</p><p class="text-xs text-white/45 mt-1">备份服务由部署配置启用</p></div></div>
      <article v-for="state in snapshot.states" :key="state.name" class="border-t border-white/10 pt-3 space-y-1"><div class="flex flex-wrap justify-between gap-2"><h3 class="text-sm">{{operationLabels[state.name]||state.name}}</h3><span class="text-xs" :class="state.status==='failed'?'text-amber-200':'text-white/60'">{{state.status==='succeeded'?'最近执行成功':state.status==='running'?'正在执行':'最近执行失败'}}</span></div><p class="text-xs text-white/45">最近成功：{{state.last_success_at?formatTime(state.last_success_at):'尚无成功记录'}}</p><p v-if="state.name==='backup'&&state.details.file" class="text-xs text-white/50 break-all">{{state.details.file}} · {{formatBytes(state.details.bytes)}}</p><p v-if="state.name==='retention'" class="text-xs text-white/50">本轮清理：审计 {{state.details.audit??0}} 条、查询历史 {{state.details.query_history??0}} 条、检查历史 {{state.details.source_checks??0}} 条、任务历史 {{state.details.source_tasks??0}} 条、已读通知 {{state.details.read_notifications??0}} 条</p></article>
      <div class="border-t border-white/10 pt-3 text-xs text-white/50 space-y-2"><p>审计留存：{{retention(snapshot.config.audit_retention_days)}}；查询历史：{{retention(snapshot.config.history_retention_days)}}</p><p>已读通知：{{retention(snapshot.config.read_notification_retention_days)}}。每小时每类最多清理 500 条，未知执行结果及版本审批历史保留。</p><p>留存周期通过部署配置设置；元数据库备份不替代审计持久卷及业务数据库备份。</p></div>
      <p class="text-xs text-white/35">页面更新于 {{checkedAt}}</p>
    </template>
  </section>
</template>
<script setup lang="ts">
import {onMounted,onScopeDispose,ref} from 'vue'
import {useRoute} from 'vue-router'
import {automationApi,type OperationsSnapshot} from '../../api/automation'
const route=useRoute(),snapshot=ref<OperationsSnapshot>(),error=ref(''),loading=ref(false),checkedAt=ref(''),controller=new AbortController()
let alive=true,timer:ReturnType<typeof setInterval>|undefined
const alertLabels:Record<string,string>={audit_delivery:'审计待写队列异常',retention_failed:'历史留存维护失败',backup_failed:'元数据库自动备份失败',backup_stale:'元数据库备份超期'}
const operationLabels:Record<string,string>={backup:'元数据库自动备份',retention:'历史留存维护',source_worker:'数据源后台任务'}
const formatTime=(v:string)=>new Date(v).toLocaleString(),retention=(days:number)=>days?`保留 ${days} 天`:'自动清理关闭'
const formatBytes=(v:unknown)=>typeof v==='number'?`${(v/1048576).toFixed(2)} MiB`:'大小未记录'
async function load(){if(loading.value)return;loading.value=true;error.value='';try{const r=await automationApi.operations(controller.signal);if(alive){snapshot.value=r;checkedAt.value=new Date().toLocaleString()}}catch(e){if(alive){snapshot.value=undefined;error.value=e instanceof Error?e.message:'读取运维状态失败'}}finally{if(alive)loading.value=false}}
onMounted(()=>{void load();if(route.query.section==='operations')document.getElementById('operations')?.scrollIntoView();timer=setInterval(()=>{if(!document.hidden&&navigator.onLine)void load()},30000)})
onScopeDispose(()=>{alive=false;controller.abort();clearInterval(timer)})
</script>
