<script setup lang="ts">
import { useReadActivity } from '../../composables/useReadActivity'
const { busy: readPending, run: trackRead } = useReadActivity()
import {onMounted,onScopeDispose,ref,watch} from 'vue'
import {operationApi,type OperationAccount,type OperationTemplate} from '../../api/business-operations'
import request from '../../utils/request'
const props=defineProps<{sourceManager:boolean;admin:boolean}>(),emit=defineEmits<{changed:[]}>(),accounts=ref<OperationAccount[]>([]),templates=ref<OperationTemplate[]>([]),sources=ref<{id:number;name:string}[]>([]),members=ref<{id:number;username:string}[]>([]),account=ref<OperationAccount>(),template=ref<Omit<OperationTemplate,'spec'>>(),password=ref(''),error=ref(''),message=ref(''),busy=ref(false),key=ref(crypto.randomUUID()),controller=new AbortController();let alive=true
const names:Record<string,string>={member_points_adjust:'会员积分调整',member_coupon_issue:'会员发券',warehouse_stock_adjust:'仓库库存调整',transport_status_advance:'运输状态推进'}
watch([account,template,password],()=>{if(!busy.value)key.value=crypto.randomUUID()},{deep:true})
async function load(){
return trackRead(async () => {try{if(props.sourceManager){const [a,s]=await Promise.all([operationApi.accounts(controller.signal),request.get<unknown,{items:{id:number;name:string;enabled:boolean;visibility:string}[]}>('/datasource-definitions',{params:{page_size:100},signal:controller.signal,silentError:true})]);if(!alive)return;accounts.value=a.items;sources.value=s.items.filter(item=>item.enabled&&item.visibility==='global')};if(props.admin){const [t,u]=await Promise.all([operationApi.templates(true,controller.signal),request.get<unknown,{items:{id:number;username:string}[]}>('/users',{params:{page_size:100},signal:controller.signal,silentError:true})]);if(!alive)return;templates.value=t.items;members.value=u.items}}catch(e){if(alive)error.value=e instanceof Error?e.message:'读取操作配置失败'}
})
}
function editAccount(item?:OperationAccount){account.value=item?{...item}:{id:0,name:'',definition_revision_id:0,username:'',installation_id:'',enabled:true,version:0};password.value='';message.value=''}
function editTemplate(item?:OperationTemplate){template.value=item?{id:item.id,account_id:item.account_id,kind:item.kind,version:item.version,enabled:item.enabled,user_ids:[...(item.user_ids||[])],group_ids:[...(item.group_ids||[])]}:{id:0,account_id:0,kind:'member_points_adjust',version:0,enabled:true,user_ids:[],group_ids:[]};message.value=''}
async function save(action:()=>Promise<unknown>){if(busy.value)return;busy.value=true;error.value='';message.value='';try{await action();if(alive){account.value=undefined;template.value=undefined;password.value='';key.value=crypto.randomUUID();message.value='配置已保存，旧预览和审批需要重新核对。';await load();emit('changed')}}catch(e){if(alive)error.value=e instanceof Error?e.message:'保存配置失败'}finally{if(alive)busy.value=false}}
async function saveAccount(){if(account.value)await save(()=>operationApi.saveAccount(account.value!,password.value,key.value))}
async function saveTemplate(){if(template.value)await save(()=>operationApi.saveTemplate(template.value!,key.value))}
onMounted(load);onScopeDispose(()=>{alive=false;controller.abort()})
</script>

<template>
  <section class="ui-page-stack" aria-label="业务操作配置">
    <p class="text-sm text-muted">由数据库负责人安装操作协议并提供 EXECUTE 专用账号，再由工作空间管理员分配模板受众。账号启停、密码轮换和受众变更会使旧预览及审批失效。</p>
    <p v-if="error" role="alert" class="text-danger text-sm">{{error}}</p><p v-if="message" role="status" class="text-success text-sm">{{message}}</p>
    <div v-data-loading="readPending" data-loading-label="正在读取业务操作配置…" class="space-y-4">
<template v-if="sourceManager">
      <div class="flex flex-wrap gap-2"><button class="ghost-button ui-control-target" :disabled="busy" @click="editAccount()">新增专用账号</button><button v-for="item in accounts" :key="item.id" class="glass-card p-3 text-left ui-control-target" :disabled="busy" @click="editAccount(item)">{{item.name}} · {{item.enabled?'启用':'停用'}} · v{{item.version}}</button></div>
      <form v-if="account" class="glass-panel p-4 space-y-3" aria-label="业务操作专用账号" @submit.prevent="saveAccount">
        <h3 class="font-medium">{{account.id?'编辑':'新增'}}专用执行账号</h3>
        <div class="grid sm:grid-cols-2 gap-3">
          <label class="text-sm">名称<input v-model.trim="account.name" aria-label="专用账号名称" required maxlength="100" class="glass-input block mt-1 w-full" :disabled="busy" /></label>
          <label class="text-sm">已发布数据源<select v-model.number="account.definition_revision_id" aria-label="操作账号数据源" required class="glass-input block mt-1 w-full" :disabled="busy||!!account.id"><option :value="0">请选择原生 PostgreSQL 数据源</option><option v-for="source in sources" :key="source.id" :value="source.id">{{source.name}} #{{source.id}}</option></select></label>
          <label class="text-sm">数据库用户名<input v-model.trim="account.username" aria-label="操作数据库用户名" required maxlength="100" class="glass-input block mt-1 w-full" :disabled="busy||!!account.id" /></label>
          <label class="text-sm">协议安装编号<input v-model.trim="account.installation_id" aria-label="操作协议安装编号" required class="glass-input block mt-1 w-full" :disabled="busy||!!account.id" /></label>
          <label class="text-sm">{{account.id?'新密码（留空保留）':'数据库密码'}}<input v-model="password" aria-label="操作账号数据库密码" type="password" autocomplete="new-password" :required="!account.id" class="glass-input block mt-1 w-full" :disabled="busy" /></label>
        </div>
        <label class="flex gap-2 text-sm"><input v-model="account.enabled" type="checkbox" :disabled="busy" />启用操作账号</label><p class="text-xs text-muted">启用时验证安装签名及账号权限。用户名、数据源和安装编号在创建后固定，迁移请创建新账号与模板。</p><button class="liquid-button ui-control-target" :disabled="busy">验证并保存专用账号</button>
      </form>
    </template>
    <template v-if="admin">
      <div class="flex flex-wrap gap-2"><button class="ghost-button ui-control-target" :disabled="busy||!accounts.length" @click="editTemplate()">新增受众模板</button><button v-for="item in templates" :key="item.id" class="glass-card p-3 ui-control-target" :disabled="busy" @click="editTemplate(item)">{{names[item.kind]}} #{{item.id}} · {{item.enabled?'启用':'停用'}} · v{{item.version}}</button></div>
      <form v-if="template" class="glass-panel p-4 space-y-3" aria-label="业务操作模板受众" @submit.prevent="saveTemplate">
        <h3 class="font-medium">模板与受众</h3><div class="grid sm:grid-cols-2 gap-3"><label class="text-sm">业务操作<select v-model="template.kind" aria-label="受众模板业务类型" class="glass-input block mt-1 w-full" :disabled="busy||!!template.id"><option v-for="(name,kind) in names" :key="kind" :value="kind">{{name}}</option></select></label><label class="text-sm">专用执行账号<select v-model.number="template.account_id" aria-label="受众模板执行账号" class="glass-input block mt-1 w-full" :disabled="busy||!!template.id"><option :value="0">请选择</option><option v-for="item in accounts.filter(a=>a.enabled)" :key="item.id" :value="item.id">{{item.name}}</option></select></label></div>
        <fieldset class="border border-stroke rounded-xl p-3"><legend class="text-sm px-2">允许申请的成员</legend><div class="grid sm:grid-cols-2 lg:grid-cols-3 gap-2 max-h-60 overflow-y-auto"><label v-for="member in members" :key="member.id" class="text-xs flex gap-2 min-h-9 items-center"><input v-model="template.user_ids" type="checkbox" :value="member.id" :disabled="busy" />{{member.username}} #{{member.id}}</label></div></fieldset>
        <p v-if="template.group_ids?.length" class="text-xs text-muted">保留原分配的用户组：{{template.group_ids.join('、')}}。组成员由用户组目录维护。</p><label class="flex gap-2 text-sm"><input v-model="template.enabled" type="checkbox" :disabled="busy" />启用此模板</label><p class="text-xs text-muted">受众可以发起申请，目标库仍只接受申请人负责的对象。修改分配后旧审批无法继续执行。</p><button class="liquid-button ui-control-target" :disabled="busy||!template.account_id||!(template.user_ids?.length||template.group_ids?.length)">保存模板与受众</button>
      </form>
    </template>
  
</div>
</section>
</template>
