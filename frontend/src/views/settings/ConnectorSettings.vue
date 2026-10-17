<template>
  <section class="glass-card p-4 sm:p-6 space-y-4" aria-labelledby="driver-settings-title">
    <header class="flex flex-wrap items-start justify-between gap-3">
      <div><h2 id="driver-settings-title" class="font-medium">驱动管理</h2><p class="text-xs text-white/45 mt-1">登记部署中已安装并校验的 JDBC 驱动版本。</p></div>
      <button class="ghost-button text-xs" :disabled="loading" @click="load">刷新</button>
    </header>
    <p v-if="serviceError" class="text-sm text-amber-200 break-words" role="alert">{{ serviceError }}</p>
    <div class="grid sm:grid-cols-2 gap-3">
      <label class="text-xs text-white/60">可用制品
        <el-select v-model="artifactId" class="w-full mt-1" placeholder="选择已安装的驱动" aria-label="可用驱动制品" @change="chooseArtifact">
          <el-option v-for="a in artifacts" :key="a.id" :value="a.id" :label="`${a.name} ${a.version}`" />
        </el-select>
      </label>
      <label class="text-xs text-white/60">驱动名称<input v-model="driverName" class="glass-input mt-1" maxlength="100" placeholder="例如 PostgreSQL JDBC" /></label>
    </div>
    <p v-if="selectedArtifact" class="text-xs text-white/45 break-all">{{ selectedArtifact.class_name }} · SHA-256 {{ selectedArtifact.sha256 }}</p>
    <button class="liquid-button text-xs" :disabled="busy || !artifactId || !driverName.trim()" @click="register">{{ busy ? '处理中…' : '登记驱动' }}</button>
    <ul class="divide-y divide-white/10">
      <li v-for="d in drivers" :key="d.id" class="py-3 space-y-2">
        <div class="flex flex-wrap items-center justify-between gap-2"><p class="text-sm break-all">{{ d.name }} <span class="text-white/45">{{ d.version }} · {{ d.enabled ? '已启用' : '已停用' }}</span></p>
          <div class="flex gap-2"><button class="ghost-button text-xs" :disabled="busy" @click="setStatus('driver',d.id,!d.enabled)">{{ d.enabled ? '停用' : '启用' }}</button><button class="ghost-button text-xs text-rose-300" :disabled="busy" @click="remove('driver',d.id,d.name)">删除</button></div>
        </div><p class="text-xs text-white/40 break-all">{{ d.class_name }}</p>
      </li>
      <li v-if="!drivers.length && !loading" class="py-3 text-sm text-white/45">尚未登记 JDBC 驱动。</li>
    </ul>
  </section>

  <section class="glass-card p-4 sm:p-6 space-y-4" aria-labelledby="template-settings-title">
    <header class="flex flex-wrap items-start justify-between gap-3"><div><h2 id="template-settings-title" class="font-medium">数据源模板</h2><p class="text-xs text-white/45 mt-1">模板固定驱动与连接规则；发布新修订不会改变现有实例。</p></div><button class="ghost-button text-xs" :disabled="busy" @click="reset">新建模板</button></header>
    <div class="grid sm:grid-cols-2 gap-3">
      <label class="text-xs text-white/60">模板标识<input v-model="form.template_key" class="glass-input mt-1" :disabled="!!editingId" placeholder="例如 analytics-pg" maxlength="80" /></label>
      <label class="text-xs text-white/60">模板名称<input v-model="form.name" class="glass-input mt-1" placeholder="例如分析库连接" maxlength="100" /></label>
      <label class="text-xs text-white/60">产品名称<input v-model="form.product" class="glass-input mt-1" placeholder="例如 PostgreSQL" maxlength="80" /></label>
      <label class="text-xs text-white/60">驱动版本<el-select v-model="form.driver_id" aria-label="模板驱动版本" class="w-full mt-1" placeholder="选择驱动版本" @change="chooseDriver"><el-option v-for="d in drivers.filter(d=>d.enabled)" :key="d.id" :value="d.id" :label="`${d.name} ${d.version}`" /></el-select></label>
    </div>
    <label class="block text-xs text-white/60">JDBC URL 模板<input v-model="form.url_template" aria-label="JDBC URL 模板" aria-describedby="jdbc-url-hint" class="glass-input mt-1 font-mono text-xs" spellcheck="false" /><span id="jdbc-url-hint" class="block mt-1 text-white/40">使用所选驱动的已支持 URL 规则；用户名和密码由实例独立填写。</span></label>
    <label class="block text-xs text-white/60">SQL 能力<el-select v-model="form.dialect" aria-label="模板 SQL 能力" class="w-full mt-1"><el-option label="仅结构浏览（未启用 SQL）" value="" /><el-option v-if="driverDialect" :label="`${driverDialect==='postgres'?'PostgreSQL':'MySQL'} 方言与基础执行`" :value="driverDialect" /></el-select></label>
    <div v-if="form.fields.length" class="space-y-2"><h3 class="text-xs text-white/60">连接字段</h3><div v-for="(field, index) in form.fields" :key="index" class="grid grid-cols-1 sm:grid-cols-3 gap-2 items-center"><input v-model="field.name" class="glass-input text-xs" :aria-label="`字段 ${index + 1} 标识`" placeholder="字段标识" /><input v-model="field.label" class="glass-input text-xs" :aria-label="`${field.name} 显示名称`" /><div class="flex gap-2"><input v-model="field.default" class="glass-input min-w-0 text-xs" :aria-label="`${field.name} 默认值`" placeholder="默认值（可选）" /><button class="ghost-button text-xs" @click="form.fields.splice(index, 1)" :aria-label="`移除字段 ${index + 1}`">移除</button></div></div></div>
    <button class="ghost-button text-xs" :disabled="form.fields.length >= 20" @click="form.fields.push({name:'',label:'',type:'string',required:true})">添加连接字段</button>
    <button class="liquid-button text-xs" :disabled="busy || !form.driver_id || !form.template_key || !form.name" @click="publish">{{ busy ? '处理中…' : editingId ? '发布新修订' : '发布模板' }}</button>
    <ul class="divide-y divide-white/10"><li v-for="t in templates" :key="t.id" class="py-3 space-y-2"><div class="flex flex-wrap items-center justify-between gap-2"><p class="text-sm break-all">{{ t.name }} · 修订 {{ t.revision }} <span class="text-xs text-white/45">{{ t.enabled && t.driver.enabled ? '已启用' : '不可用' }}</span></p><div class="flex flex-wrap gap-2"><button class="ghost-button text-xs" :disabled="busy" @click="edit(t)">新修订</button><button class="ghost-button text-xs" :disabled="busy" @click="setStatus('template',t.id,!t.enabled)">{{ t.enabled ? '停用' : '启用' }}</button><button class="ghost-button text-xs text-rose-300" :disabled="busy" @click="remove('template',t.id,t.name)">删除</button></div></div><p class="text-xs text-white/40 break-all">{{ t.product }} · {{ t.driver.name }} {{ t.driver.version }} · {{ t.dialect ? 'SQL 与结构浏览' : '仅结构浏览' }}</p></li><li v-if="!templates.length && !loading" class="py-3 text-sm text-white/45">发布模板后，即可在“新建数据源 → 自定义 JDBC”中选择。</li></ul>
  </section>
</template>

<script setup lang="ts">
import {computed,onMounted,reactive,ref} from 'vue'
import {ElMessage,ElMessageBox} from 'element-plus'
import {connectorApi,type DatasourceTemplate,type DriverArtifact,type JDBCDriver,type TemplatePayload} from '../../api/connector'
const drivers=ref<JDBCDriver[]>([]),artifacts=ref<DriverArtifact[]>([]),templates=ref<DatasourceTemplate[]>([])
const loading=ref(false),busy=ref(false),serviceError=ref(''),artifactId=ref(''),driverName=ref(''),editingId=ref<number>()
const empty=():TemplatePayload=>({template_key:'',name:'',product:'',driver_id:0,dialect:'',url_template:'',fields:[]})
const form=reactive<TemplatePayload>(empty())
const selectedArtifact=computed(()=>artifacts.value.find(a=>a.id===artifactId.value))
const driverDialect=computed(()=>{const c=drivers.value.find(d=>d.id===form.driver_id)?.class_name;return c==='org.postgresql.Driver'?'postgres':c==='com.mysql.cj.jdbc.Driver'?'mysql':''})
async function load(){loading.value=true;serviceError.value='';try{const [d,t]=await Promise.all([connectorApi.drivers(),connectorApi.templates()]);drivers.value=d.items;templates.value=t.items;try{artifacts.value=(await connectorApi.artifacts()).items}catch(e){serviceError.value=e instanceof Error?e.message:'JDBC 服务暂不可用'}}finally{loading.value=false}}
function chooseArtifact(){if(selectedArtifact.value)driverName.value=`${selectedArtifact.value.name} JDBC`}
function chooseDriver(){const d=drivers.value.find(d=>d.id===form.driver_id);if(!d)return;const h2=d.class_name==='org.h2.Driver';const pg=d.class_name==='org.postgresql.Driver';const mysql=d.class_name==='com.mysql.cj.jdbc.Driver';form.product=h2?'H2':pg?'PostgreSQL':mysql?'MySQL':d.name;form.dialect='';form.url_template=h2?'jdbc:h2:mem:{database};DB_CLOSE_DELAY=-1':`jdbc:${pg?'postgresql':mysql?'mysql':'vendor'}://{host}:{port}/{database}`;form.fields=h2?[]:[{name:'host',label:'主机',type:'string',required:true},{name:'port',label:'端口',type:'integer',required:true,default:pg?'5432':mysql?'3306':''}];form.fields.push({name:'database',label:'数据库',type:'string',required:true})}
function reset(){editingId.value=undefined;Object.assign(form,empty())}
function edit(t:DatasourceTemplate){editingId.value=t.id;Object.assign(form,{template_key:t.template_key,name:t.name,product:t.product,driver_id:t.driver_id,dialect:t.dialect,url_template:t.url_template,fields:t.fields.map(f=>({...f}))})}
async function action(fn:()=>Promise<unknown>){if(busy.value)return;busy.value=true;try{await fn();await load()}catch{/* API displays failure */}finally{busy.value=false}}
async function register(){await action(async()=>{await connectorApi.register(artifactId.value,driverName.value.trim());artifactId.value='';driverName.value='';ElMessage.success('驱动已登记')})}
async function publish(){await action(async()=>{await connectorApi.publish({...form,fields:form.fields.map(f=>({...f}))},editingId.value);reset();ElMessage.success('模板修订已发布')})}
async function setStatus(kind:'driver'|'template',id:number,enabled:boolean){if(!enabled){try{await ElMessageBox.confirm('停用后将阻止使用此版本建立新会话，已有操作不会重派。','停用确认',{type:'warning'})}catch{return}}await action(()=>connectorApi.status(kind,id,enabled))}
async function remove(kind:'driver'|'template',id:number,name:string){try{await ElMessageBox.confirm(`删除「${name}」？引用中的版本不能删除。`,'删除确认',{type:'warning'})}catch{return}await action(()=>connectorApi.remove(kind,id))}
onMounted(()=>void load().catch(()=>{}))
</script>
