<script setup lang="ts">
import { ref } from 'vue'
import request from '../../utils/request'
import { useUserStore } from '../../stores/user'
import type { UserItem } from '../../api/admin'
const props=defineProps<{user:UserItem}>(),emit=defineEmits<{close:[];saved:[]}>(),store=useUserStore(),selected=ref(props.user.role==='admin'?['report_review','datasource_manage','audit_view','operation_review']:[...(props.user.capabilities||[])]),saving=ref(false),error=ref('')
const options=[{code:'report_review',label:'报表审批与发布',description:'审阅正式报表、仪表盘及导出。不能审批自己的申请，不包含数据源、成员或审计管理。'},{code:'datasource_manage',label:'数据源管理',description:'管理空间共享数据源、授权范围、执行账号和访问策略。不授予报表批准权或其他人的个人数据库凭据。'},{code:'audit_view',label:'操作审计查看',description:'只读查询当前空间审计。不包含平台身份审计或变更审批。'},{code:'operation_review',label:'业务操作审批',description:'独立审批受控业务操作；不授予业务数据写入账号或其他管理职责。'}]
async function save(){if(saving.value||!props.user.membership_version)return;saving.value=true;error.value='';try{await request.patch(`/workspaces/${store.workspaceId}/members/${props.user.id}`,{expected_version:props.user.membership_version,capabilities:[...selected.value]});await store.loadWorkspaces(true);emit('saved');emit('close')}catch(e){error.value=e instanceof Error?e.message:'保存失败，请刷新成员版本后重试'}finally{saving.value=false}}
</script>

<template>
 <el-dialog :model-value="true" title="工作空间职责" width="min(620px, 94vw)" :close-on-click-modal="false" :show-close="!saving" :close-on-press-escape="!saving" @close="emit('close')">
  <form class="space-y-4" @submit.prevent="save">
   <p class="text-sm">{{user.username}} · {{store.workspace?.name}}</p>
   <p class="text-xs leading-6 text-muted">职责只在当前空间生效，不改变基础角色或数据库账号。管理员拥有全部职责；其他用户仅获得下列明确授予的职责。撤回职责后，新请求及旧的执行授权失效。</p>
   <p v-if="error" role="alert" class="text-sm text-danger">{{error}}</p>
   <fieldset class="space-y-3" :disabled="saving||user.role==='admin'">
    <label v-for="option in options" :key="option.code" class="glass-card p-3 flex gap-3 text-xs"><input v-model="selected" type="checkbox" :value="option.code"><span><span class="block text-sm">{{option.label}}</span><span class="block mt-1 text-muted leading-6">{{option.description}}</span></span></label>
   </fieldset>
   <button class="liquid-button text-xs ui-control-target" :disabled="saving||user.role==='admin'||!user.membership_version">{{saving?'保存中…':'保存职责权限'}}</button>
  </form>
 </el-dialog>
</template>
