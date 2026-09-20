<template>
  <div class="space-y-5">
    <header class="flex flex-col sm:flex-row sm:items-center justify-between gap-4">
      <div>
        <h1 class="text-xl font-semibold">数据源管理</h1>
        <p class="text-sm text-white/45 mt-1">统一纳管 MySQL / PostgreSQL / Redis 连接，凭据全程 AES-256-GCM 加密存储</p>
      </div>
      <button v-if="canWrite" class="liquid-button flex items-center justify-center gap-2 shrink-0" @click="openCreate">
        <Plus class="w-4 h-4" />
        {{ activeTab === 'connections' ? '新建连接' : '新建隧道' }}
      </button>
    </header>

    <!-- 二级 Tab -->
    <div class="inline-flex p-1 rounded-2xl bg-white/5 border border-white/10 backdrop-blur-xl">
      <button
        v-for="tab in tabs"
        :key="tab.key"
        class="px-4 py-1.5 text-sm rounded-xl flex items-center gap-2 transition-all"
        :class="activeTab === tab.key ? 'bg-gradient-to-r from-indigo-500/80 to-violet-500/80 text-white shadow-lg shadow-indigo-500/20' : 'text-white/55 hover:text-white'"
        @click="activeTab = tab.key"
      >
        <component :is="tab.icon" class="w-4 h-4" />
        {{ tab.label }}
      </button>
    </div>

    <!-- ============ 数据库连接 ============ -->
    <template v-if="activeTab === 'connections'">
      <div class="flex flex-wrap items-center gap-3">
        <div class="relative w-full sm:w-72 shrink-0">
          <Search class="w-4 h-4 text-white/40 absolute left-3.5 top-1/2 -translate-y-1/2" />
          <input
            v-model.trim="keyword"
            aria-label="搜索数据源名称或主机"
            class="glass-input pl-10"
            placeholder="搜索数据源名称或主机"
            @input="fetchConnections"
          />
        </div>
        <div class="w-full sm:w-44 shrink-0">
          <el-select
            v-model="typeFilter"
            aria-label="按数据库类型筛选"
            class="w-full"
            popper-class="glass-popper"
            @change="fetchConnections"
          >
            <el-option label="全部类型" value="all" />
            <el-option label="MySQL" value="mysql" />
            <el-option label="PostgreSQL" value="postgres" />
            <el-option label="Redis" value="redis" />
          </el-select>
        </div>
      </div>

      <div v-loading="connLoading" class="min-h-[160px]">
        <section v-if="connections.length" class="grid grid-cols-1 sm:grid-cols-2 xl:grid-cols-3 gap-4">
          <article v-for="conn in connections" :key="conn.id" class="glass-card p-5 hover:-translate-y-1 flex flex-col gap-4">
            <div class="flex items-start gap-3.5">
              <div class="w-11 h-11 rounded-xl flex items-center justify-center shrink-0" :style="{ background: typeMeta[conn.type].tint }">
                <component :is="typeMeta[conn.type].icon" class="w-5 h-5" :style="{ color: typeMeta[conn.type].color }" />
              </div>
              <div class="min-w-0 flex-1">
                <h3 class="font-medium truncate">{{ conn.name }}</h3>
                <p class="text-xs text-white/45 mt-0.5 truncate">{{ conn.host }}:{{ conn.port }}</p>
              </div>
            </div>

            <div class="flex flex-wrap items-center gap-2 text-xs">
              <span class="px-2 py-0.5 rounded-full bg-white/8 text-white/60">{{ typeMeta[conn.type].label }}</span>
              <span class="px-2 py-0.5 rounded-full" :class="envMeta[conn.environment || 'dev'].cls">
                {{ envMeta[conn.environment || 'dev'].label }}
              </span>
              <span v-if="conn.database" class="px-2 py-0.5 rounded-full bg-white/8 text-white/50 max-w-[180px] truncate">{{ conn.database }}</span>
              <span v-if="conn.tunnel_name" class="px-2 py-0.5 rounded-full bg-sky-400/15 text-sky-300 flex items-center gap-1">
                <Network class="w-3 h-3" />{{ conn.tunnel_name }}
              </span>
              <span class="px-2 py-0.5 rounded-full" :class="conn.has_password ? 'bg-emerald-400/15 text-emerald-300' : 'bg-amber-400/15 text-amber-300'">
                {{ conn.has_password ? '凭据已保存' : '无口令' }}
              </span>
            </div>

            <div class="flex items-center gap-2 pt-2 border-t border-white/10">
              <button class="ghost-button flex-1 text-xs py-2 flex items-center justify-center gap-1.5" @click="goQuery(conn)">
                <SquareTerminal class="w-3.5 h-3.5" />查询
              </button>
              <button
                class="w-9 h-9 rounded-lg flex items-center justify-center text-white/50 hover:bg-emerald-500/15 hover:text-emerald-300 transition-colors"
                :aria-label="`测试连接 ${conn.name}`"
                title="测试连接"
                @click="testSaved(conn)"
              >
                <Zap class="w-4 h-4" />
              </button>
              <button
                v-if="canWrite"
                class="w-9 h-9 rounded-lg flex items-center justify-center text-white/50 hover:bg-white/10 hover:text-white transition-colors"
                aria-label="编辑连接"
                title="编辑"
                @click="openEdit(conn)"
              >
                <Pencil class="w-4 h-4" />
              </button>
              <button
                v-if="canWrite"
                class="w-9 h-9 rounded-lg flex items-center justify-center text-white/50 hover:bg-rose-500/20 hover:text-rose-300 transition-colors"
                aria-label="删除连接"
                title="删除"
                @click="remove(conn)"
              >
                <Trash2 class="w-4 h-4" />
              </button>
            </div>
          </article>
        </section>
        <div v-else-if="!connLoading" class="glass-card p-12 flex flex-col items-center justify-center gap-3 text-center">
          <Database class="w-10 h-10 text-white/20" />
          <p class="text-white/55 text-sm">还没有数据源，点击右上角「新建连接」接入第一个数据库</p>
        </div>
      </div>
    </template>

    <!-- ============ SSH 隧道 ============ -->
    <template v-else>
      <div v-loading="tunnelLoading" class="glass-card overflow-hidden">
        <div class="overflow-x-auto">
          <table class="w-full text-sm min-w-[640px]">
            <thead>
              <tr class="text-left text-white/40 text-xs border-b border-white/10">
                <th class="font-medium px-5 py-3">名称</th>
                <th class="font-medium px-5 py-3">跳板机</th>
                <th class="font-medium px-5 py-3">用户名</th>
                <th class="font-medium px-5 py-3">认证方式</th>
                <th class="font-medium px-5 py-3 text-right">操作</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="t in tunnels" :key="t.id" class="border-b border-white/5 last:border-0 hover:bg-white/5 transition-colors">
                <td class="px-5 py-3.5">
                  <div class="flex items-center gap-2">
                    <span class="w-8 h-8 rounded-lg bg-sky-400/15 text-sky-300 flex items-center justify-center shrink-0">
                      <Network class="w-4 h-4" />
                    </span>
                    <span class="font-medium">{{ t.name }}</span>
                  </div>
                </td>
                <td class="px-5 py-3.5 text-white/65">{{ t.host }}:{{ t.port }}</td>
                <td class="px-5 py-3.5 text-white/65">{{ t.username }}</td>
                <td class="px-5 py-3.5 text-white/55">{{ t.auth_type === 'private_key' ? '私钥' : '密码' }}</td>
                <td class="px-5 py-3.5">
                  <div class="flex items-center justify-end gap-1">
                    <button
                      v-if="canWrite"
                      class="w-8 h-8 rounded-lg flex items-center justify-center text-white/50 hover:bg-white/10 hover:text-white"
                      aria-label="编辑隧道"
                      @click="openEditTunnel(t)"
                    >
                      <Pencil class="w-3.5 h-3.5" />
                    </button>
                    <button
                      v-if="canWrite"
                      class="w-8 h-8 rounded-lg flex items-center justify-center text-white/50 hover:bg-rose-500/20 hover:text-rose-300"
                      aria-label="删除隧道"
                      @click="removeTunnel(t)"
                    >
                      <Trash2 class="w-3.5 h-3.5" />
                    </button>
                  </div>
                </td>
              </tr>
              <tr v-if="!tunnels.length && !tunnelLoading">
                <td colspan="5" class="px-5 py-12 text-center text-white/40 text-sm">
                  暂无 SSH 隧道；当数据库只能通过跳板机访问时，请先在此创建隧道，再新建连接时选用
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
    </template>

    <!-- ============ 连接弹窗 ============ -->
    <el-dialog
      v-model="connDialog"
      :title="connForm.id ? '编辑连接' : '新建连接'"
      :width="dialogWidth"
      :close-on-click-modal="false"
      class="glass-dialog"
    >
      <el-form label-position="top" class="space-y-1">
        <p class="text-xs text-white/45 mb-3">基本信息</p>
        <el-form-item label="连接名称">
          <el-input v-model="connForm.name" placeholder="例如：生产环境-订单库" />
        </el-form-item>
        <el-form-item label="数据库类型">
          <el-select v-model="connForm.type" class="w-full" popper-class="glass-popper" @change="onTypeChange">
            <el-option label="MySQL" value="mysql" />
            <el-option label="PostgreSQL" value="postgres" />
            <el-option label="Redis" value="redis" />
          </el-select>
        </el-form-item>

        <p class="text-xs text-white/45 mb-3 mt-4">连接信息</p>
        <div class="grid grid-cols-1 sm:grid-cols-3 gap-3">
          <el-form-item label="主机" class="sm:col-span-2">
            <el-input v-model="connForm.host" placeholder="192.168.1.10" />
          </el-form-item>
          <el-form-item label="端口">
            <el-input v-model.number="connForm.port" type="number" :min="1" :max="65535" />
          </el-form-item>
        </div>
        <el-form-item :label="connForm.type === 'redis' ? '数据库编号（0-15，可选）' : '默认数据库（可选）'">
          <el-input v-model="connForm.database" :placeholder="connForm.type === 'redis' ? '0' : '业务库名'" />
        </el-form-item>
        <div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
          <el-form-item :label="connForm.type === 'redis' ? 'ACL 用户名（可选）' : '用户名'">
            <el-input v-model="connForm.username" />
          </el-form-item>
          <el-form-item label="密码">
            <el-input
              v-model="connForm.password"
              type="password"
              show-password
              :placeholder="connForm.id && !connForm.password ? '已保存，留空表示不修改' : 'AES-256-GCM 加密存储'"
            />
          </el-form-item>
        </div>
        <div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
          <el-form-item label="SSL 模式">
            <el-select v-model="connForm.ssl_mode" class="w-full" popper-class="glass-popper">
              <el-option label="禁用（内网明文）" value="disable" />
              <el-option label="要求加密（require）" value="require" />
            </el-select>
          </el-form-item>
          <el-form-item label="连接超时（秒）">
            <el-input v-model.number="connForm.connection_timeout" type="number" :min="1" :max="60" />
          </el-form-item>
        </div>
        <el-form-item label="所属环境（资产徽标与高危操作提示依据）">
          <el-select v-model="connForm.environment" class="w-full" popper-class="glass-popper">
            <el-option label="开发环境（dev）" value="dev" />
            <el-option label="测试环境（test）" value="test" />
            <el-option label="生产环境（prod）" value="prod" />
          </el-select>
        </el-form-item>
        <el-form-item label="SSH 隧道（可选，需先在「SSH 隧道」页签创建）">
          <el-select v-model="connForm.ssh_tunnel_id" class="w-full" popper-class="glass-popper" clearable>
            <el-option label="不使用隧道" :value="null" />
            <el-option v-for="t in tunnels" :key="t.id" :label="`${t.name}（${t.host}）`" :value="t.id" />
          </el-select>
        </el-form-item>
      </el-form>

      <template #footer>
        <div class="flex flex-wrap items-center justify-between gap-2">
          <button class="ghost-button flex items-center gap-2" :disabled="testing" @click="testUnsaved">
            <Zap class="w-4 h-4" /> {{ testing ? '测试中…' : '测试连接' }}
          </button>
          <div class="flex gap-2 ml-auto">
            <button class="ghost-button" @click="connDialog = false">取消</button>
            <button class="liquid-button" :disabled="saving" @click="saveConnection">{{ saving ? '保存中…' : '保存' }}</button>
          </div>
        </div>
      </template>
    </el-dialog>

    <!-- ============ 隧道弹窗 ============ -->
    <el-dialog
      v-model="tunnelDialog"
      :title="tunnelForm.id ? '编辑 SSH 隧道' : '新建 SSH 隧道'"
      :width="dialogWidth"
      :close-on-click-modal="false"
      class="glass-dialog"
    >
      <el-form label-position="top" class="space-y-1">
        <el-form-item label="隧道名称">
          <el-input v-model="tunnelForm.name" placeholder="例如：生产区跳板机" />
        </el-form-item>
        <div class="grid grid-cols-1 sm:grid-cols-3 gap-3">
          <el-form-item label="主机" class="sm:col-span-2">
            <el-input v-model="tunnelForm.host" placeholder="10.0.0.9" />
          </el-form-item>
          <el-form-item label="端口">
            <el-input v-model.number="tunnelForm.port" type="number" :min="1" :max="65535" />
          </el-form-item>
        </div>
        <div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
          <el-form-item label="用户名">
            <el-input v-model="tunnelForm.username" placeholder="跳板机登录用户" />
          </el-form-item>
          <el-form-item label="认证方式">
            <el-select v-model="tunnelForm.auth_type" class="w-full" popper-class="glass-popper">
              <el-option label="密码" value="password" />
              <el-option label="私钥" value="private_key" />
            </el-select>
          </el-form-item>
        </div>
        <el-form-item v-if="tunnelForm.auth_type === 'password'" label="跳板机密码">
          <el-input
            v-model="tunnelForm.password"
            type="password"
            show-password
            :placeholder="tunnelForm.id && !tunnelForm.password ? '已保存，留空表示不修改' : 'AES-256-GCM 加密存储'"
          />
        </el-form-item>
        <template v-else>
          <el-form-item label="私钥内容（PEM，OpenSSH 格式）">
            <el-input
              v-model="tunnelForm.private_key"
              type="textarea"
              :rows="4"
              :placeholder="tunnelForm.id && !tunnelForm.private_key ? '已保存，留空表示不修改' : '-----BEGIN OPENSSH PRIVATE KEY-----'"
            />
          </el-form-item>
          <el-form-item label="私钥口令（若有）">
            <el-input
              v-model="tunnelForm.passphrase"
              type="password"
              show-password
              placeholder="无口令可留空；填写新口令将覆盖旧值"
            />
          </el-form-item>
        </template>
      </el-form>

      <template #footer>
        <div class="flex flex-wrap items-center justify-between gap-2">
          <button class="ghost-button flex items-center gap-2" :disabled="testing" @click="testTunnelCfg">
            <Zap class="w-4 h-4" /> {{ testing ? '测试中…' : '测试连通' }}
          </button>
          <div class="flex gap-2 ml-auto">
            <button class="ghost-button" @click="tunnelDialog = false">取消</button>
            <button class="liquid-button" :disabled="saving" @click="saveTunnel">{{ saving ? '保存中…' : '保存' }}</button>
          </div>
        </div>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  Database,
  Network,
  Pencil,
  Plus,
  Search,
  Server,
  SquareTerminal,
  Trash2,
  Zap,
} from 'lucide-vue-next'
import {
  connectionApi,
  tunnelApi,
  type ConnectionItem,
  type ConnectionPayload,
  type DbType,
  type EnvKind,
  type SSHTunnelItem,
  type SSHTunnelPayload,
} from '../../api/datasource'
import { useUserStore } from '../../stores/user'

const router = useRouter()
const userStore = useUserStore()
const canWrite = computed(() => userStore.role !== 'readonly')

const tabs = [
  { key: 'connections' as const, label: '数据库连接', icon: Database },
  { key: 'tunnels' as const, label: 'SSH 隧道', icon: Network },
]
const activeTab = ref<'connections' | 'tunnels'>('connections')

// ---------- 列表 ----------
const keyword = ref('')
const typeFilter = ref('all')
const connections = ref<ConnectionItem[]>([])
const tunnels = ref<SSHTunnelItem[]>([])
const connLoading = ref(false)
const tunnelLoading = ref(false)

const typeMeta: Record<DbType, { label: string; color: string; tint: string; icon: unknown }> = {
  mysql: { label: 'MySQL', color: '#60a5fa', tint: 'rgba(59,130,246,0.16)', icon: Database },
  postgres: { label: 'PostgreSQL', color: '#a78bfa', tint: 'rgba(167,139,250,0.16)', icon: Database },
  redis: { label: 'Redis', color: '#f472b6', tint: 'rgba(236,72,153,0.14)', icon: Server },
}

/** 环境徽标样式（暗色玻璃规范下的低饱和语义色） */
const envMeta: Record<EnvKind, { label: string; cls: string }> = {
  dev: { label: '开发', cls: 'bg-sky-400/15 text-sky-300' },
  test: { label: '测试', cls: 'bg-amber-400/15 text-amber-300' },
  prod: { label: '生产', cls: 'bg-rose-400/15 text-rose-300' },
}

async function fetchConnections() {
  connLoading.value = true
  try {
    const res = await connectionApi.list({
      type: typeFilter.value === 'all' ? '' : typeFilter.value,
      keyword: keyword.value,
    })
    connections.value = res.items
  } catch {
    /* 拦截器已统一提示 */
  } finally {
    connLoading.value = false
  }
}
async function fetchTunnels() {
  tunnelLoading.value = true
  try {
    const res = await tunnelApi.list()
    tunnels.value = res.items
  } catch {
    /* 拦截器已统一提示 */
  } finally {
    tunnelLoading.value = false
  }
}
onMounted(() => {
  fetchConnections()
  fetchTunnels()
})

// ---------- 连接表单 ----------
interface ConnForm extends ConnectionPayload {
  id: number | null
}
const emptyConnForm = (): ConnForm => ({
  id: null,
  name: '',
  type: 'mysql',
  host: '',
  port: 3306,
  database: '',
  username: '',
  password: '',
  ssl_mode: 'disable',
  connection_timeout: 10,
  color_label: '',
  environment: 'dev',
  ssh_tunnel_id: null,
})
const connDialog = ref(false)
const connForm = reactive<ConnForm>(emptyConnForm())
const saving = ref(false)
const testing = ref(false)

function onTypeChange(type: DbType) {
  connForm.port = { mysql: 3306, postgres: 5432, redis: 6379 }[type]
}
function openCreate() {
  if (activeTab.value === 'tunnels') {
    openCreateTunnel()
    return
  }
  Object.assign(connForm, emptyConnForm())
  connDialog.value = true
}
function openEdit(c: ConnectionItem) {
  Object.assign(connForm, {
    id: c.id,
    name: c.name,
    type: c.type,
    host: c.host,
    port: c.port,
    database: c.database,
    username: c.username,
    password: '',
    ssl_mode: c.ssl_mode || 'disable',
    connection_timeout: c.connection_timeout || 10,
    color_label: c.color_label || '',
    environment: c.environment || 'dev',
    ssh_tunnel_id: c.ssh_tunnel_id,
  })
  connDialog.value = true
}

function payloadOf(): ConnectionPayload {
  return {
    name: connForm.name.trim(),
    type: connForm.type,
    host: connForm.host.trim(),
    port: Number(connForm.port),
    database: connForm.database?.trim() || '',
    username: connForm.username?.trim() || '',
    password: connForm.password ?? '',
    ssl_mode: connForm.ssl_mode,
    connection_timeout: Number(connForm.connection_timeout) || 10,
    color_label: connForm.color_label || '',
    environment: connForm.environment || 'dev',
    ssh_tunnel_id: connForm.ssh_tunnel_id ?? null,
  }
}

async function saveConnection() {
  if (!connForm.name.trim() || !connForm.host.trim()) {
    ElMessage.warning('请填写连接名称与主机')
    return
  }
  saving.value = true
  try {
    if (connForm.id) {
      await connectionApi.update(connForm.id, payloadOf())
      ElMessage.success('连接已更新')
    } else {
      await connectionApi.create(payloadOf())
      ElMessage.success('连接已创建')
    }
    connDialog.value = false
    await fetchConnections()
  } catch {
    /* 拦截器已提示 */
  } finally {
    saving.value = false
  }
}

async function testUnsaved() {
  if (!connForm.host.trim()) {
    ElMessage.warning('请先填写主机与端口')
    return
  }
  testing.value = true
  try {
    const res = await connectionApi.test(payloadOf())
    ElMessage.success(`连接成功：${res.version || res.type}（${res.elapsed_ms} ms）`)
  } catch {
    /* 拦截器已提示 */
  } finally {
    testing.value = false
  }
}

async function testSaved(c: ConnectionItem) {
  try {
    ElMessage.info({ message: `正在测试 ${c.name} …`, duration: 1200 })
    const res = await connectionApi.testSaved(c.id)
    ElMessage.success(`连接成功：${res.version || res.type}（${res.elapsed_ms} ms）`)
  } catch {
    /* 拦截器已提示 */
  }
}

async function remove(c: ConnectionItem) {
  try {
    await ElMessageBox.confirm(`确认删除数据源「${c.name}」？其查询历史将一并删除。`, '删除确认', {
      type: 'warning',
      confirmButtonText: '删除',
      cancelButtonText: '取消',
    })
  } catch {
    return
  }
  await connectionApi.remove(c.id)
  ElMessage.success('已删除')
  fetchConnections()
}

function goQuery(c: ConnectionItem) {
  router.push({ path: '/query', query: { connection: String(c.id) } })
}

// ---------- 隧道表单 ----------
interface TunnelForm extends SSHTunnelPayload {
  id: number | null
}
const emptyTunnelForm = (): TunnelForm => ({
  id: null,
  name: '',
  host: '',
  port: 22,
  username: '',
  auth_type: 'private_key',
  private_key: '',
  passphrase: '',
  password: '',
})
const tunnelDialog = ref(false)
const tunnelForm = reactive<TunnelForm>(emptyTunnelForm())

function openCreateTunnel() {
  Object.assign(tunnelForm, emptyTunnelForm())
  tunnelDialog.value = true
}
function openEditTunnel(t: SSHTunnelItem) {
  Object.assign(tunnelForm, {
    id: t.id,
    name: t.name,
    host: t.host,
    port: t.port,
    username: t.username,
    auth_type: t.auth_type,
    private_key: '',
    passphrase: '',
    password: '',
  })
  tunnelDialog.value = true
}

function tunnelPayload(): SSHTunnelPayload {
  return {
    name: tunnelForm.name.trim(),
    host: tunnelForm.host.trim(),
    port: Number(tunnelForm.port) || 22,
    username: tunnelForm.username.trim(),
    auth_type: tunnelForm.auth_type,
    private_key: tunnelForm.private_key ?? '',
    passphrase: tunnelForm.passphrase ?? '',
    password: tunnelForm.password ?? '',
  }
}

async function saveTunnel() {
  if (!tunnelForm.name.trim() || !tunnelForm.host.trim() || !tunnelForm.username.trim()) {
    ElMessage.warning('名称、主机、用户名必填')
    return
  }
  const p = tunnelPayload()
  if (!tunnelForm.id) {
    if (p.auth_type === 'private_key' && !(p.private_key ?? '').trim()) {
      ElMessage.warning('请填写私钥内容')
      return
    }
    if (p.auth_type === 'password' && !p.password) {
      ElMessage.warning('请填写跳板机密码')
      return
    }
  }
  saving.value = true
  try {
    if (tunnelForm.id) {
      await tunnelApi.update(tunnelForm.id, p)
      ElMessage.success('隧道已更新')
    } else {
      await tunnelApi.create(p)
      ElMessage.success('隧道已创建')
    }
    tunnelDialog.value = false
    await fetchTunnels()
  } catch {
    /* 拦截器已提示 */
  } finally {
    saving.value = false
  }
}

async function testTunnelCfg() {
  if (!tunnelForm.host.trim() || !tunnelForm.username.trim()) {
    ElMessage.warning('请先填写主机与用户名')
    return
  }
  testing.value = true
  try {
    await tunnelApi.test(tunnelPayload())
    ElMessage.success('SSH 跳板连接成功')
  } catch {
    /* 拦截器已提示 */
  } finally {
    testing.value = false
  }
}

async function removeTunnel(t: SSHTunnelItem) {
  try {
    await ElMessageBox.confirm(`确认删除 SSH 隧道「${t.name}」？引用它的连接将改为直连。`, '删除确认', {
      type: 'warning',
      confirmButtonText: '删除',
      cancelButtonText: '取消',
    })
  } catch {
    return
  }
  await tunnelApi.remove(t.id)
  ElMessage.success('已删除')
  await Promise.all([fetchTunnels(), fetchConnections()])
}

// ---------- 弹窗宽度自适应 ----------
const viewportWidth = ref(window.innerWidth)
const dialogWidth = computed(() => (viewportWidth.value < 640 ? '92vw' : '600px'))
function onResize() {
  viewportWidth.value = window.innerWidth
}
onMounted(() => window.addEventListener('resize', onResize))
onBeforeUnmount(() => window.removeEventListener('resize', onResize))
</script>
