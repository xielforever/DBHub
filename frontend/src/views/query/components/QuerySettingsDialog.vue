<script setup lang="ts">
import { computed, onMounted, onScopeDispose, reactive, ref } from 'vue'
import { advancedApi, type QuerySettings } from '../../../api/advanced'
import { useUserStore } from '../../../stores/user'
const emit=defineEmits<{close:[];saved:[settings:QuerySettings]}>(),user=useUserStore(),isAdmin=computed(()=>user.role==='admin')
const form=reactive<QuerySettings>({timeout_ms:30000,max_rows:1000,block_unsafe_write:true}),loading=ref(true),saving=ref(false),loadFailed=ref(false),error=ref('')
let alive=true
async function load(){loading.value=true;loadFailed.value=false;error.value='';try{const settings=await advancedApi.settings();if(alive)Object.assign(form,settings)}catch(e){if(alive){loadFailed.value=true;error.value=e instanceof Error?e.message:'读取查询限制失败'}}finally{if(alive)loading.value=false}}
function defaults(){Object.assign(form,{timeout_ms:30000,max_rows:1000,block_unsafe_write:true})}
function close(){if(!saving.value)emit('close')}
async function save(){if(loading.value||saving.value||loadFailed.value)return;saving.value=true;error.value='';try{const settings=await advancedApi.saveSettings({...form});if(alive){emit('saved',settings);emit('close')}}catch(e){if(alive)error.value=e instanceof Error?e.message:'保存查询限制失败'}finally{if(alive)saving.value=false}}
onMounted(()=>void load());onScopeDispose(()=>{alive=false})
</script>

<template>
  <el-dialog :model-value="true" title="查询限制" width="min(560px, 94vw)" :close-on-click-modal="false" :before-close="close" @closed="emit('close')">
    <form class="space-y-4" :aria-busy="saving" @submit.prevent="save">
      <p class="text-xs leading-6 text-muted">设置保存到当前工作空间，适用于自己的 SQL 查询。服务端仍会限制并发和执行权限。</p>
      <p v-if="error" role="alert" class="text-sm text-danger">{{ error }}</p>
      <button v-if="loadFailed" type="button" class="ghost-button text-xs ui-control-target" :disabled="loading" @click="load">重新加载</button>
      <fieldset v-data-loading="loading" data-loading-label="正在读取查询限制…" data-loading-variant="detail" :disabled="loading || saving || loadFailed" class="space-y-4 disabled:opacity-60">
        <label class="block text-xs text-muted space-y-2">执行超时（毫秒）<el-input-number v-model="form.timeout_ms" aria-label="执行超时（毫秒）" :min="1000" :max="120000" :step="1000" :precision="0" class="!w-full" /></label>
        <label class="block text-xs text-muted space-y-2">最大返回行数<el-input-number v-model="form.max_rows" aria-label="最大返回行数" :min="1" :max="10000" :precision="0" class="!w-full" /></label>
        <label class="flex items-center gap-2 text-xs text-muted"><input v-model="form.block_unsafe_write" type="checkbox" :disabled="!isAdmin">禁止无 WHERE 的 UPDATE / DELETE</label>
        <p class="text-xs text-muted">超过行数时截断并提示。非管理员不能关闭写入保护；生产环境写操作始终需要确认。</p>
        <div class="flex gap-2"><button type="submit" class="liquid-button text-xs ui-control-target">{{ saving ? '保存中…' : '保存查询限制' }}</button><button type="button" class="ghost-button text-xs ui-control-target" @click="defaults">恢复默认值</button></div>
      </fieldset>
    </form>
  </el-dialog>
</template>
