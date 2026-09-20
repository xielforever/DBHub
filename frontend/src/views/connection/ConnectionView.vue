<template>
  <div class="space-y-5">
    <header class="flex flex-col sm:flex-row sm:items-center justify-between gap-4">
      <div>
        <h1 class="text-xl font-semibold">数据源管理</h1>
        <p class="text-sm text-white/45 mt-1">统一纳管 MySQL / PostgreSQL / Redis 连接，凭据全程 AES-256-GCM 加密存储，支持代理出网（占位）</p>
      </div>
      <button v-if="canWrite" class="liquid-button flex items-center justify-center gap-2 shrink-0" @click="openCreate">
        <Plus class="w-4 h-4" />新建连接
      </button>
    </header>

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
      <button class="ghost-button text-xs py-1.5 px-3 flex items-center gap-1" @click="goProxies">
        <Waypoints class="w-3.5 h-3.5" />代理管理
      </button>
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
            <span v-if="conn.proxy_name" class="px-2 py-0.5 rounded-full bg-violet-400/15 text-violet-300 flex items-center gap-1">
              <Waypoints class="w-3 h-3" />{{ conn.proxy_name }}
            </span>
            <span v-if="conn.tunnel_name" class="px-2 py-0.5 rounded-full bg-sky-400/15 text-sky-300 flex items-center gap-1 opacity-60">
              <Network class="w-3 h-3" />{{ conn.tunnel_name }}(废弃)
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

    <!-- 连接弹窗 -->
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
        <el-form-item label="代理（可选，替代原 SSH 隧道，占位 M5）">
          <el-select v-model="connForm.proxy_id" class="w-full" popper-class="glass-popper" clearable placeholder="不使用代理">
            <el-option label="不使用代理" :value="null" />
            <el-option v-for="p in proxies" :key="p.id" :label="`${p.name}（${p.type} ${p.host}:${p.port}）`" :value="p.id" />
          </el-select>
          <p class="text-[11px] text-white/30 mt-1">原 SSH 隧道已废弃，请在代理管理创建代理后在此选用。M5 实现真实代理拨号。</p>
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
  Waypoints,
  Zap,
} from 'lucide-vue-next'
import {
  connectionApi,
  type ConnectionItem,
  type ConnectionPayload,
  type DbType,
  type EnvKind,
} from '../../api/datasource'
import { proxyApi, type ProxyItem } from '../../api/proxy'
import { useUserStore } from '../../stores/user'

const router = useRouter()
const userStore = useUserStore()
const canWrite = computed(() => userStore.role !== 'readonly')

const keyword = ref('')
const typeFilter = ref('all')
const connections = ref<ConnectionItem[]>([])
const proxies = ref<ProxyItem[]>([])
const connLoading = ref(false)

const typeMeta: Record<DbType, { label: string; color: string; tint: string; icon: unknown }> = {
  mysql: { label: 'MySQL', color: '#60a5fa', tint: 'rgba(59,130,246,0.16)', icon: Database },
  postgres: { label: 'PostgreSQL', color: '#a78bfa', tint: 'rgba(167,139,250,0.16)', icon: Database },
  redis: { label: 'Redis', color: '#f472b6', tint: 'rgba(236,72,153,0.14)', icon: Server },
}

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
async function fetchProxies() {
  try {
    const res = await proxyApi.list()
    proxies.value = res.items
  } catch {
    /* 忽略 */
  }
}
onMounted(() => {
  fetchConnections()
  fetchProxies()
})

function goProxies() {
  router.push('/proxies')
}

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
  proxy_id: null,
})
const connDialog = ref(false)
const connForm = reactive<ConnForm>(emptyConnForm())
const saving = ref(false)
const testing = ref(false)

function onTypeChange(type: DbType) {
  connForm.port = { mysql: 3306, postgres: 5432, redis: 6379 }[type]
}
function openCreate() {
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
    proxy_id: (c as any).proxy_id ?? null,
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
    proxy_id: (connForm as any).proxy_id ?? null,
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

// ---------- 弹窗宽度自适应 ----------
const viewportWidth = ref(window.innerWidth)
const dialogWidth = computed(() => (viewportWidth.value < 640 ? '92vw' : '600px'))
function onResize() {
  viewportWidth.value = window.innerWidth
}
onMounted(() => window.addEventListener('resize', onResize))
onBeforeUnmount(() => window.removeEventListener('resize', onResize))
</script>
