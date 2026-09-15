<template>
  <div class="space-y-5">
    <header class="flex flex-col sm:flex-row sm:items-center justify-between gap-4">
      <div>
        <h1 class="text-xl font-semibold">数据源管理</h1>
        <p class="text-sm text-white/45 mt-1">统一纳管 MySQL / PostgreSQL / Redis 连接，凭据全程加密存储</p>
      </div>
      <button class="liquid-button flex items-center justify-center gap-2 shrink-0" @click="dialogVisible = true">
        <Plus class="w-4 h-4" />
        新建连接
      </button>
    </header>

    <!-- 搜索筛选 -->
    <div class="flex flex-wrap items-center gap-3">
      <div class="relative w-full sm:w-72 shrink-0">
        <Search class="w-4 h-4 text-white/40 absolute left-3.5 top-1/2 -translate-y-1/2" />
        <input
          v-model.trim="keyword"
          aria-label="搜索数据源名称或主机"
          class="glass-input pl-10"
          placeholder="搜索数据源名称或主机"
        />
      </div>
      <select
        v-model="typeFilter"
        aria-label="按数据库类型筛选"
        class="glass-input w-full sm:w-44 appearance-none cursor-pointer shrink-0"
      >
        <option value="">全部类型</option>
        <option value="mysql">MySQL</option>
        <option value="postgres">PostgreSQL</option>
        <option value="redis">Redis</option>
      </select>
    </div>

    <!-- 连接卡片网格 -->
    <section class="grid grid-cols-1 sm:grid-cols-2 xl:grid-cols-3 gap-4">
      <article
        v-for="conn in filteredConnections"
        :key="conn.id"
        class="glass-card p-5 hover:-translate-y-1 flex flex-col gap-4"
      >
        <div class="flex items-start gap-3.5">
          <div
            class="w-11 h-11 rounded-xl flex items-center justify-center shrink-0"
            :style="{ background: typeMeta[conn.type].tint }"
          >
            <component :is="typeMeta[conn.type].icon" class="w-5 h-5" :style="{ color: typeMeta[conn.type].color }" />
          </div>
          <div class="min-w-0 flex-1">
            <h3 class="font-medium truncate">{{ conn.name }}</h3>
            <p class="text-xs text-white/45 mt-0.5 truncate">{{ conn.host }}:{{ conn.port }}</p>
          </div>
        </div>

        <div class="flex items-center gap-2 text-xs">
          <span
            class="px-2 py-0.5 rounded-full"
            :class="conn.status === 'online' ? 'bg-emerald-400/15 text-emerald-300' : 'bg-amber-400/15 text-amber-300'"
          >
            {{ conn.status === 'online' ? '连接正常' : '连接异常' }}
          </span>
          <span class="text-white/35">{{ typeMeta[conn.type].label }}</span>
        </div>

        <div class="flex items-center gap-2 pt-2 border-t border-white/10">
          <button
            class="ghost-button flex-1 text-xs py-2 flex items-center justify-center gap-1.5"
            @click="goQuery(conn)"
          >
            <SquareTerminal class="w-3.5 h-3.5" />
            查询
          </button>
          <button
            class="w-9 h-9 rounded-lg flex items-center justify-center text-white/50 hover:bg-white/10 hover:text-white transition-colors"
            aria-label="编辑连接"
            @click="todo"
          >
            <Pencil class="w-4 h-4" />
          </button>
          <button
            class="w-9 h-9 rounded-lg flex items-center justify-center text-white/50 hover:bg-rose-500/20 hover:text-rose-300 transition-colors"
            aria-label="删除连接"
            @click="todo"
          >
            <Trash2 class="w-4 h-4" />
          </button>
        </div>
      </article>
    </section>

    <!-- 新建连接弹窗（静态表单，接口对接后启用提交） -->
    <el-dialog
      v-model="dialogVisible"
      title="新建连接"
      :width="dialogWidth"
      :close-on-click-modal="false"
      class="glass-dialog"
    >
      <el-form label-position="top" class="space-y-1">
        <p class="text-xs text-white/45 mb-3">基本信息</p>
        <el-form-item label="连接名称">
          <el-input v-model="form.name" placeholder="例如：生产环境-订单库" />
        </el-form-item>
        <el-form-item label="数据库类型">
          <el-select v-model="form.type" class="w-full">
            <el-option label="MySQL" value="mysql" />
            <el-option label="PostgreSQL" value="postgres" />
            <el-option label="Redis" value="redis" />
          </el-select>
        </el-form-item>

        <p class="text-xs text-white/45 mb-3 mt-4">连接信息</p>
        <div class="grid grid-cols-1 sm:grid-cols-3 gap-3">
          <el-form-item label="主机" class="sm:col-span-2">
            <el-input v-model="form.host" placeholder="192.168.1.10" />
          </el-form-item>
          <el-form-item label="端口">
            <el-input v-model.number="form.port" type="number" />
          </el-form-item>
        </div>
        <el-form-item label="默认数据库">
          <el-input v-model="form.database" placeholder="可选" />
        </el-form-item>
        <div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
          <el-form-item label="用户名">
            <el-input v-model="form.username" />
          </el-form-item>
          <el-form-item label="密码">
            <el-input v-model="form.password" type="password" show-password placeholder="AES-256-GCM 加密存储" />
          </el-form-item>
        </div>
        <el-form-item label="SSL 模式">
          <el-select v-model="form.sslMode" class="w-full">
            <el-option label="require（推荐）" value="require" />
            <el-option label="disable" value="disable" />
          </el-select>
        </el-form-item>

        <div class="rounded-xl border border-white/10 p-3 mt-2">
          <button
            type="button"
            class="w-full flex items-center justify-between text-sm text-white/70"
            @click="sshExpanded = !sshExpanded"
          >
            <span class="flex items-center gap-2">
              <Network class="w-4 h-4" /> SSH 隧道（跳板机）
            </span>
            <ChevronDown class="w-4 h-4 transition-transform" :class="{ 'rotate-180': sshExpanded }" />
          </button>
          <div v-if="sshExpanded" class="grid grid-cols-1 sm:grid-cols-2 gap-3 mt-3">
            <el-form-item label="跳板机主机">
              <el-input v-model="form.sshHost" />
            </el-form-item>
            <el-form-item label="端口">
              <el-input v-model.number="form.sshPort" type="number" />
            </el-form-item>
            <el-form-item label="用户名">
              <el-input v-model="form.sshUsername" />
            </el-form-item>
            <el-form-item label="认证方式">
              <el-select v-model="form.sshAuthType" class="w-full">
                <el-option label="私钥" value="private_key" />
                <el-option label="密码" value="password" />
              </el-select>
            </el-form-item>
          </div>
        </div>
      </el-form>

      <template #footer>
        <div class="flex flex-wrap items-center justify-between gap-2">
          <button class="ghost-button flex items-center gap-2" @click="todo">
            <Zap class="w-4 h-4" /> 测试连接
          </button>
          <div class="flex gap-2 ml-auto">
            <button class="ghost-button" @click="dialogVisible = false">取消</button>
            <button class="liquid-button" @click="todo">保存</button>
          </div>
        </div>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import {
  ChevronDown,
  Database,
  Network,
  Pencil,
  Plus,
  Search,
  SquareTerminal,
  Trash2,
  Zap,
} from 'lucide-vue-next'

const router = useRouter()
const keyword = ref('')
const typeFilter = ref('')
const dialogVisible = ref(false)
const sshExpanded = ref(false)

interface Connection {
  id: number
  name: string
  type: 'mysql' | 'postgres' | 'redis'
  host: string
  port: number
  status: 'online' | 'offline'
}

const connections = ref<Connection[]>([
  { id: 1, name: 'sales-prod-mysql', type: 'mysql', host: '10.20.1.11', port: 3306, status: 'online' },
  { id: 2, name: 'user-center-pg', type: 'postgres', host: '10.20.1.12', port: 5432, status: 'online' },
  { id: 3, name: 'cache-redis-01', type: 'redis', host: '10.20.1.21', port: 6379, status: 'online' },
  { id: 4, name: 'analytics-mysql', type: 'mysql', host: '10.20.2.31', port: 3306, status: 'offline' },
  { id: 5, name: 'billing-postgres', type: 'postgres', host: '10.20.2.32', port: 5432, status: 'online' },
  { id: 6, name: 'session-redis', type: 'redis', host: '10.20.2.41', port: 6379, status: 'online' },
])

const typeMeta: Record<Connection['type'], { label: string; color: string; tint: string; icon: unknown }> = {
  mysql: { label: 'MySQL', color: '#60a5fa', tint: 'rgba(59,130,246,0.16)', icon: Database },
  postgres: { label: 'PostgreSQL', color: '#a78bfa', tint: 'rgba(118,75,162,0.2)', icon: Database },
  redis: { label: 'Redis', color: '#f472b6', tint: 'rgba(236,72,153,0.14)', icon: Database },
}

const filteredConnections = computed(() =>
  connections.value.filter((c) => {
    const matchType = !typeFilter.value || c.type === typeFilter.value
    const kw = keyword.value.toLowerCase()
    const matchKw = !kw || c.name.toLowerCase().includes(kw) || c.host.includes(kw)
    return matchType && matchKw
  }),
)

const form = reactive({
  name: '', type: 'mysql', host: '', port: 3306, database: '',
  username: '', password: '', sslMode: 'require',
  sshHost: '', sshPort: 22, sshUsername: '', sshAuthType: 'private_key',
})

/** 弹窗宽度随视口自适应 */
const viewportWidth = ref(window.innerWidth)
const dialogWidth = computed(() => (viewportWidth.value < 640 ? '92vw' : '600px'))
function onResize() {
  viewportWidth.value = window.innerWidth
}
onMounted(() => window.addEventListener('resize', onResize))
onBeforeUnmount(() => window.removeEventListener('resize', onResize))

function todo() {
  ElMessage.info('连接管理后端接口开发中，敬请期待')
}

function goQuery(conn: Connection) {
  router.push({ path: '/query', query: { connection: String(conn.id) } })
}
</script>
