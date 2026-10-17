<template>
  <section class="space-y-4" aria-label="数据源目录">
    <p class="text-sm text-white/50">目录共享连接目标。添加到我的连接后，使用你自己的数据库账号操作。</p>
    <div class="flex flex-wrap gap-3"><input v-model="keyword" class="glass-input max-w-md" aria-label="搜索数据源目录" placeholder="搜索名称、主机或数据库" /><button class="liquid-button text-xs" @click="openDefinition">新建数据源定义</button><button class="ghost-button text-xs" :disabled="loading" @click="load">刷新</button></div>
    <div v-loading="loading" class="min-h-32 grid grid-cols-1 sm:grid-cols-2 xl:grid-cols-3 gap-4">
      <article v-for="d in filtered" :key="d.id" class="glass-card p-5 space-y-3">
        <div class="flex justify-between items-start gap-2"><h2 class="font-medium break-words">{{ d.name }}</h2><span class="text-xs text-white/50 shrink-0">{{ d.visibility === 'global' ? '全局' : '个人' }} · 修订 {{ d.revision }}</span></div>
        <p class="text-xs text-white/55 break-all">{{ endpoint(d) }}</p>
        <p class="text-xs text-white/45">{{ d.config.type === 'jdbc' ? '自定义 JDBC' : d.config.type }} · {{ d.config.environment || 'dev' }}</p>
        <p v-if="d.description" class="text-sm text-white/60 break-words">{{ d.description }}</p>
        <p v-if="!d.enabled" class="text-xs text-amber-200">此修订已停用</p>
        <p v-else-if="d.my_connection_id && d.my_revision_id !== d.id" class="text-xs text-amber-200">有新修订，现有连接仍使用你已确认的目标。</p>
        <div class="flex flex-wrap gap-2">
          <button v-if="!d.my_connection_id" class="liquid-button text-xs" :disabled="!d.enabled" @click="openIdentity(d)">添加到我的连接</button>
          <button v-else-if="d.my_revision_id !== d.id" class="liquid-button text-xs" :disabled="!d.enabled" @click="openIdentity(d)">查看并切换修订</button>
          <button v-else class="ghost-button text-xs" :disabled="!d.enabled" @click="openConnection(d)">打开我的连接</button>
          <template v-if="canManage(d)"><button class="ghost-button text-xs" @click="openRevision(d)">新修订</button><button class="ghost-button text-xs" :disabled="busy" @click="toggle(d)">{{ d.enabled ? '停用此修订' : '启用此修订' }}</button></template>
        </div>
      </article>
    </div>
    <p v-if="!loading && !filtered.length" class="glass-card p-8 text-center text-sm text-white/45">暂无可见的数据源定义。你可以新建个人数据源，或等待管理员发布。</p>

    <el-dialog v-model="identityOpen" :title="selected?.my_connection_id ? '确认新修订与个人身份' : '添加到我的连接'" width="min(92vw, 560px)" :close-on-click-modal="false" class="glass-dialog" @closed="clearIdentity">
      <div v-if="selected" class="space-y-4">
        <div class="rounded-xl bg-white/5 p-4 space-y-2"><p>{{ selected.name }} · 修订 {{ selected.revision }}</p><p class="text-xs text-white/60 break-all">{{ endpoint(selected) }}</p><p class="text-xs text-white/50">TLS：{{ selected.config.ssl_mode }}。请核实目标后填写自己的数据库账号。</p><p v-if="selected.my_connection_id" class="text-xs text-amber-200">保存后将切换此个人连接的修订，并清理此前采集的结构。请重新输入密码。</p></div>
        <el-form label-position="top">
          <el-form-item label="我的连接名称"><el-input v-model="identity.name" maxlength="100" /></el-form-item>
          <el-form-item label="我的数据库账号"><el-input v-model="identity.username" autocomplete="off" aria-label="我的数据库账号" /></el-form-item>
          <el-form-item label="我的数据库密码"><el-input v-model="identity.password" type="password" show-password autocomplete="new-password" aria-label="我的数据库密码" /></el-form-item>
        </el-form>
        <el-checkbox v-model="identity.confirmed" class="identity-confirm" aria-label="确认连接目标">我已确认地址、数据库和修订，使用自己的数据库账号</el-checkbox>
        <p v-if="testText" role="status" class="text-sm break-words" :class="testOK ? 'text-emerald-300' : 'text-rose-300'">{{ testText }}</p>
      </div>
      <template #footer><div class="flex flex-wrap justify-between gap-2"><button class="ghost-button" :disabled="busy || !identityReady" @click="testIdentity">{{ busy ? '处理中…' : '测试我的连接' }}</button><button class="liquid-button" :disabled="busy || !identityReady" @click="saveIdentity">{{ selected?.my_connection_id ? '确认并切换' : '保存我的连接' }}</button></div></template>
    </el-dialog>

    <el-dialog v-model="revisionOpen" :title="revisionId ? '发布数据源新修订' : '新建数据源定义'" width="min(92vw, 620px)" :close-on-click-modal="false" class="glass-dialog">
      <p class="text-xs text-white/50 mb-4">只发布连接目标。现有使用者确认新修订后才会切换，个人账号密码不会随定义发布。</p>
      <el-form label-position="top">
        <el-form-item label="数据源名称"><el-input v-model="revision.name" maxlength="100" /></el-form-item>
        <el-form-item label="说明"><el-input v-model="revision.description" type="textarea" maxlength="1000" /></el-form-item>
        <el-form-item label="可见范围"><el-select v-model="revision.visibility" class="w-full"><el-option label="仅自己" value="private" /><el-option v-if="isAdmin" label="全局发布" value="global" /></el-select></el-form-item>
        <el-form-item label="类型"><el-select v-model="revision.config.type" class="w-full" @change="resetType"><el-option label="PostgreSQL" value="postgres" /><el-option label="MySQL" value="mysql" /><el-option label="Redis" value="redis" /><el-option label="自定义 JDBC" value="jdbc" /></el-select></el-form-item>
        <template v-if="revision.config.type === 'jdbc'">
          <el-form-item label="驱动连接规则"><el-select v-model="revision.config.template_revision_id" class="w-full" @change="resetFields"><el-option v-for="t in eligibleTemplates" :key="t.id" :value="t.id" :label="`${t.name} · 修订 ${t.revision}`" /></el-select></el-form-item>
          <el-form-item v-for="f in revisionTemplate?.fields || []" :key="f.name" :label="f.label"><el-input v-model="revisionParameters[f.name]" :type="f.type === 'integer' ? 'number' : 'text'" /></el-form-item>
        </template>
        <template v-else>
          <el-form-item label="主机"><el-input v-model="revision.config.host" /></el-form-item>
          <el-form-item label="端口"><el-input v-model.number="revision.config.port" type="number" /></el-form-item>
          <el-form-item label="数据库"><el-input v-model="revision.config.database" /></el-form-item>
        </template>
        <el-form-item label="TLS"><el-select v-model="revision.config.ssl_mode" class="w-full"><el-option label="验证服务器证书" value="verify-full" /><el-option label="禁用" value="disable" /></el-select></el-form-item>
        <el-form-item label="环境"><el-select v-model="revision.config.environment" class="w-full"><el-option label="开发" value="dev" /><el-option label="测试" value="test" /><el-option label="生产" value="prod" /></el-select></el-form-item>
      </el-form>
      <template #footer><button class="liquid-button" :disabled="busy || !revision.name.trim()" @click="saveRevision">{{ revisionId ? '发布新修订' : '保存数据源定义' }}</button></template>
    </el-dialog>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { sourceCatalogApi, type DefinitionPayload, type PersonalIdentity, type SourceDefinition } from '../../api/source-catalog'
import { connectorApi, type DatasourceTemplate } from '../../api/connector'
import { useUserStore } from '../../stores/user'

const emit = defineEmits<{ added: [] }>()
const user = useUserStore(), router = useRouter()
const isAdmin = computed(() => user.role === 'admin')
const canManage = (d: SourceDefinition) => d.visibility === 'private' && d.owner_id === user.user?.id || d.visibility === 'global' && isAdmin.value
const items = ref<SourceDefinition[]>([]), keyword = ref(''), loading = ref(false), busy = ref(false)
const templates = ref<DatasourceTemplate[]>([])
const filtered = computed(() => items.value.filter(d => `${d.name} ${endpoint(d)}`.toLowerCase().includes(keyword.value.toLowerCase())))
const endpoint = (d: SourceDefinition) => `${d.config.host}${d.config.port ? ':' + d.config.port : ''} / ${d.config.database || d.config.parameters?.database || ''}`
async function load() { loading.value = true; try { items.value = (await sourceCatalogApi.list()).items } finally { loading.value = false } }
onMounted(() => { void load().catch(() => {}); void connectorApi.templates().then(r => { templates.value = r.items }).catch(() => {}) })
const selected = ref<SourceDefinition>(), identityOpen = ref(false), testText = ref(''), testOK = ref(false)
const emptyIdentity = (): PersonalIdentity => ({ name: '', username: '', password: '', confirmed: false })
const identity = reactive(emptyIdentity())
const identityReady = computed(() => identity.confirmed && !!identity.name.trim() && !!selected.value && (selected.value.config.type === 'redis' || !!identity.username.trim() && !!identity.password))
watch(() => JSON.stringify(identity), () => { testText.value = '' })
function clearIdentity() { Object.assign(identity, emptyIdentity()); selected.value = undefined; testText.value = '' }
function openIdentity(d: SourceDefinition) { clearIdentity(); selected.value = d; identity.name = d.name; identityOpen.value = true }
function openConnection(d: SourceDefinition) { if (d.my_connection_id) void router.push({ path: '/query', query: { connection: String(d.my_connection_id) } }) }
async function testIdentity() {
  if (!selected.value || busy.value) return
  busy.value = true; const snapshot = JSON.stringify(identity), id = selected.value.id
  try { const result = await sourceCatalogApi.test(id, { ...identity }); if (snapshot === JSON.stringify(identity) && selected.value?.id === id) { testOK.value = result.ok; testText.value = result.ok ? '个人连接测试成功' : result.error || '测试失败' } }
  catch { if (snapshot === JSON.stringify(identity) && selected.value?.id === id) { testOK.value = false; testText.value = '个人连接测试失败，请检查账号、密码和目标地址' } }
  finally { busy.value = false }
}
async function saveIdentity() {
  if (!selected.value || busy.value) return
  busy.value = true
  try { const d = selected.value; if (d.my_connection_id) await sourceCatalogApi.upgrade(d.my_connection_id, d.id, { ...identity }); else await sourceCatalogApi.add(d.id, { ...identity }); identityOpen.value = false; await load(); ElMessage.success('我的连接已保存'); emit('added') }
  catch { /* API displays the failure */ } finally { busy.value = false }
}
const revisionOpen = ref(false), revisionId = ref(0)
const revision = reactive<DefinitionPayload>({ name: '', description: '', visibility: 'private', config: { type: 'postgres', host: '', port: 5432, database: '', ssl_mode: 'verify-full', connection_timeout: 10, environment: 'dev' } })
const revisionParameters = reactive<Record<string, string>>({})
const eligibleTemplates = computed(() => templates.value.filter(t => t.enabled && t.driver.enabled && (revision.visibility !== 'global' || t.visibility === 'global' && t.driver.visibility === 'global')))
const revisionTemplate = computed(() => templates.value.find(t => t.id === revision.config.template_revision_id))
function resetFields() { for (const k of Object.keys(revisionParameters)) delete revisionParameters[k]; for (const f of revisionTemplate.value?.fields || []) revisionParameters[f.name] = f.default || ''; if (revisionTemplate.value && !['org.postgresql.Driver', 'com.mysql.cj.jdbc.Driver'].includes(revisionTemplate.value.driver.class_name)) revision.config.ssl_mode = 'disable' }
function resetType() { revision.config.port = { postgres: 5432, mysql: 3306, redis: 6379, jdbc: 0 }[revision.config.type]; revision.config.template_revision_id = null; delete revision.config.parameters; resetFields() }
function openRevision(d: SourceDefinition) { revisionId.value = d.id; Object.assign(revision, { name: d.name, description: d.description, visibility: d.visibility, config: JSON.parse(JSON.stringify(d.config)) }); for (const k of Object.keys(revisionParameters)) delete revisionParameters[k]; Object.assign(revisionParameters, d.config.parameters || {}); revisionOpen.value = true }
function openDefinition() { revisionId.value = 0; Object.assign(revision, { name: '', description: '', visibility: isAdmin.value ? 'global' : 'private', config: { type: 'postgres', host: '', port: 5432, database: '', ssl_mode: 'verify-full', connection_timeout: 10, environment: 'dev' } }); for (const k of Object.keys(revisionParameters)) delete revisionParameters[k]; revisionOpen.value = true }
async function saveRevision() {
  if (busy.value) return
  busy.value = true
  try { const config = { ...revision.config }; if (config.type === 'jdbc') config.parameters = { ...revisionParameters }; else { delete config.template_revision_id; delete config.parameters }; if (revisionId.value) await sourceCatalogApi.revise(revisionId.value, { ...revision, config }); else await sourceCatalogApi.create({ ...revision, config }); revisionOpen.value = false; await load(); ElMessage.success('定义已保存，使用者需配置自己的数据库账号') }
  catch { /* API displays the failure */ } finally { busy.value = false }
}
async function toggle(d: SourceDefinition) {
  if (busy.value) return
  if (d.enabled) { try { await ElMessageBox.confirm('停用此修订后，绑定它的连接无法发起新操作。已经发送的操作可能继续完成。', '停用修订', { type: 'warning' }) } catch { return } }
  busy.value = true; try { await sourceCatalogApi.status(d.id, !d.enabled); await load() } catch { /* API displays the failure */ } finally { busy.value = false }
}
</script>

<style scoped>
.identity-confirm { height: auto; align-items: flex-start; }
.identity-confirm :deep(.el-checkbox__label) { white-space: normal; line-height: 1.5; overflow-wrap: anywhere; }
</style>
