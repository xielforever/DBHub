<template>
  <div class="space-y-6">
    <header class="flex flex-col sm:flex-row sm:items-end justify-between gap-4">
      <div>
        <h1 class="text-xl font-semibold flex items-center gap-2">
          代理管理
          <span class="px-2 py-0.5 rounded-full text-[10px] bg-amber-400/15 text-amber-300 border border-amber-400/20">占位 M5</span>
        </h1>
        <p class="text-sm text-white/45 mt-1">替代 SSH 隧道的统一代理层，支持 HTTP / HTTPS / SOCKS5 / DB Proxy，当前为功能占位</p>
      </div>
      <div class="flex items-center gap-2">
        <button class="ghost-button text-xs py-1.5 px-3 flex items-center gap-1" @click="reload">
          <RefreshCw class="w-3.5 h-3.5" />刷新
        </button>
        <button v-if="canWrite" class="liquid-button text-sm flex items-center gap-2" @click="openCreate">
          <Plus class="w-4 h-4" />新建代理
        </button>
      </div>
    </header>

    <!-- 演进说明 -->
    <div class="glass-card p-5 border-amber-400/20">
      <div class="flex gap-3">
        <div class="w-9 h-9 rounded-xl bg-amber-400/15 flex items-center justify-center shrink-0">
          <ShieldCheck class="w-5 h-5 text-amber-300" />
        </div>
        <div class="space-y-2">
          <h3 class="font-medium text-sm">为什么从 SSH 隧道演进到 Proxy？</h3>
          <ul class="text-xs text-white/55 leading-relaxed list-disc pl-4 space-y-1">
            <li><b class="text-white/80">SSH 隧道</b> 仅解决跳板机场景，协议单一，无法覆盖企业 HTTP 代理、SOCKS5 全局代理、数据库网关（如 MySQL Proxy、PgBouncer、ProxySQL）等。</li>
            <li><b class="text-white/80">Proxy 代理</b> 更合理：统一抽象 <code class="px-1 py-0.5 rounded bg-white/10 text-white/70">http/https/socks5/db_proxy/custom</code>，支持认证、状态、连接复用，未来可与数据源 <code class="px-1 py-0.5 rounded bg-white/10">proxy_id</code> 绑定，实现“数据源走代理”而非“数据源走隧道”。</li>
            <li>当前 M4 为 <b class="text-amber-300">占位</b>：后端已建表 <code class="px-1 py-0.5 rounded bg-white/10">sys_proxies</code>、API <code class="px-1 py-0.5 rounded bg-white/10">/api/v1/proxies</code>、前端页面，拨号逻辑 M5 实现。</li>
          </ul>
        </div>
      </div>
    </div>

    <!-- 代理类型卡片 -->
    <div class="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-4 gap-4">
      <article v-for="t in proxyTypes" :key="t.type" class="glass-card p-4 hover:-translate-y-0.5 transition-transform">
        <div class="flex items-center gap-2.5">
          <span class="w-9 h-9 rounded-xl flex items-center justify-center" :style="{ background: t.tint }">
            <component :is="t.icon" class="w-4.5 h-4.5" :style="{ color: t.color }" />
          </span>
          <div>
            <p class="font-medium text-sm">{{ t.label }}</p>
            <p class="text-[11px] text-white/40">{{ t.type }}</p>
          </div>
        </div>
        <p class="text-xs text-white/50 mt-3 leading-relaxed">{{ t.desc }}</p>
        <div class="mt-3 flex items-center gap-1.5">
          <span class="px-2 py-0.5 rounded-full text-[10px] bg-white/8 text-white/50">{{ t.status }}</span>
          <span class="flex-1" />
          <span class="text-[10px] text-white/30">M5 实现</span>
        </div>
      </article>
    </div>

    <!-- 列表（占位数据来自后端或 Mock） -->
    <div v-loading="loading" class="glass-card overflow-hidden">
      <div class="overflow-x-auto">
        <table class="w-full text-sm min-w-[760px]">
          <thead>
            <tr class="text-left text-white/40 text-xs border-b border-white/10">
              <th class="font-medium px-4 py-3">名称</th>
              <th class="font-medium px-3 py-3">类型</th>
              <th class="font-medium px-3 py-3">地址</th>
              <th class="font-medium px-3 py-3">认证</th>
              <th class="font-medium px-3 py-3">状态</th>
              <th class="font-medium px-3 py-3">说明</th>
              <th class="font-medium px-3 py-3 text-right">操作</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="p in items" :key="p.id" class="border-b border-white/5 last:border-0 hover:bg-white/5 transition-colors">
              <td class="px-4 py-3 font-medium">{{ p.name }}</td>
              <td class="px-3 py-3"><span class="px-2 py-0.5 rounded-full text-[11px] bg-white/8 text-white/60">{{ p.type }}</span></td>
              <td class="px-3 py-3 font-mono text-xs text-white/60">{{ p.host }}:{{ p.port }}</td>
              <td class="px-3 py-3 text-xs"><span v-if="p.has_password" class="text-emerald-300">已配置</span><span v-else class="text-white/30">无</span></td>
              <td class="px-3 py-3"><span class="px-2 py-0.5 rounded-full text-[11px]" :class="p.status==='active' ? 'bg-emerald-400/15 text-emerald-300' : 'bg-white/8 text-white/40'">{{ p.status==='active' ? '启用' : '停用' }}</span></td>
              <td class="px-3 py-3 text-xs text-white/45 truncate max-w-[200px]">{{ p.description || '—' }}</td>
              <td class="px-3 py-3 text-right">
                <div class="flex justify-end gap-1">
                  <button class="w-7 h-7 rounded-lg flex items-center justify-center text-white/40 hover:bg-white/10" @click="testProxy(p)"><Activity class="w-3.5 h-3.5" /></button>
                  <button v-if="canWrite" class="w-7 h-7 rounded-lg flex items-center justify-center text-white/40 hover:bg-white/10" @click="openEdit(p)"><Pencil class="w-3.5 h-3.5" /></button>
                  <button v-if="canWrite" class="w-7 h-7 rounded-lg flex items-center justify-center text-white/40 hover:bg-rose-500/20 hover:text-rose-300" @click="remove(p)"><Trash2 class="w-3.5 h-3.5" /></button>
                </div>
              </td>
            </tr>
            <tr v-if="!loading && !items.length">
              <td colspan="7" class="px-4 py-12 text-center">
                <Server class="w-8 h-8 text-white/20 mx-auto" />
                <p class="text-white/40 text-sm mt-2">暂无代理，M5 前为占位，可先创建配置</p>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <!-- 创建/编辑 -->
    <el-dialog v-model="editOpen" :title="editingId ? '编辑代理（占位）' : '新建代理（占位）'" width="560px" class="glass-dialog" :close-on-click-modal="false">
      <div class="space-y-4">
        <div class="grid grid-cols-2 gap-3">
          <div>
            <p class="text-xs text-white/50 mb-1">名称</p>
            <el-input v-model="form.name" placeholder="如：公司 HTTP 代理" maxlength="128" />
          </div>
          <div>
            <p class="text-xs text-white/50 mb-1">类型</p>
            <el-select v-model="form.type" class="w-full" popper-class="glass-popper">
              <el-option label="HTTP" value="http" />
              <el-option label="HTTPS" value="https" />
              <el-option label="SOCKS5" value="socks5" />
              <el-option label="DB Proxy" value="db_proxy" />
              <el-option label="Custom" value="custom" />
            </el-select>
          </div>
        </div>
        <div class="grid grid-cols-3 gap-3">
          <div class="col-span-2">
            <p class="text-xs text-white/50 mb-1">Host</p>
            <el-input v-model="form.host" placeholder="proxy.example.com" />
          </div>
          <div>
            <p class="text-xs text-white/50 mb-1">Port</p>
            <el-input v-model.number="form.port" type="number" placeholder="8080" />
          </div>
        </div>
        <div class="grid grid-cols-2 gap-3">
          <div>
            <p class="text-xs text-white/50 mb-1">用户名（可选）</p>
            <el-input v-model="form.username" placeholder="可选" />
          </div>
          <div>
            <p class="text-xs text-white/50 mb-1">密码（可选，加密存储）</p>
            <el-input v-model="form.password" type="password" placeholder="可选" show-password />
          </div>
        </div>
        <div>
          <p class="text-xs text-white/50 mb-1">描述</p>
          <el-input v-model="form.description" type="textarea" :rows="2" placeholder="用途、环境" maxlength="512" />
        </div>
        <div class="rounded-xl bg-white/5 border border-white/10 p-3 text-[11px] text-white/40 leading-relaxed">
          占位说明：M5 将实现真实拨号（HTTP CONNECT / SOCKS5 握手 / DB Proxy 协议），当前仅保存配置与测试占位返回。
        </div>
      </div>
      <template #footer>
        <div class="flex justify-end gap-2">
          <button class="ghost-button" @click="editOpen=false">取消</button>
          <button class="liquid-button" :disabled="saving" @click="save">{{ saving ? '保存中…' : '保存' }}</button>
        </div>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Activity, Globe, Pencil, Plus, RefreshCw, Server, ShieldCheck, Trash2, Waypoints } from 'lucide-vue-next'
import { proxyApi, type ProxyItem } from '../../api/proxy'
import { useUserStore } from '../../stores/user'

const userStore = useUserStore()
const canWrite = computed(() => userStore.role !== 'readonly')

const items = ref<ProxyItem[]>([])
const loading = ref(false)

const proxyTypes = [
  { type: 'http', label: 'HTTP 代理', desc: '企业常见，支持 CONNECT 隧道，适合数据库走 HTTP 代理出网', icon: Globe, color: '#60a5fa', tint: 'rgba(96,165,250,0.15)', status: '占位' },
  { type: 'https', label: 'HTTPS 代理', desc: 'TLS 加密代理，安全性更高', icon: ShieldCheck, color: '#34d399', tint: 'rgba(52,211,153,0.15)', status: '占位' },
  { type: 'socks5', label: 'SOCKS5', desc: '通用代理，支持 TCP 全转发，适合任意数据库协议', icon: Waypoints, color: '#a78bfa', tint: 'rgba(167,139,250,0.15)', status: '占位' },
  { type: 'db_proxy', label: 'DB Proxy', desc: 'PgBouncer / ProxySQL / MySQL Router 等数据库网关', icon: Server, color: '#fbbf24', tint: 'rgba(251,191,36,0.15)', status: '占位' },
]

async function reload() {
  loading.value = true
  try {
    const res = await proxyApi.list()
    items.value = res.items
    if (res.note) {
      // 占位提示不打扰
      console.log('[proxy]', res.note)
    }
  } catch {
    /* 拦截器提示 */
  } finally {
    loading.value = false
  }
}

const editOpen = ref(false)
const editingId = ref<number | null>(null)
const saving = ref(false)
const form = reactive({ name: '', type: 'http' as ProxyItem['type'], host: '', port: 8080 as number, username: '', password: '', description: '' })

function openCreate() {
  editingId.value = null
  Object.assign(form, { name: '', type: 'http' as const, host: '', port: 8080, username: '', password: '', description: '' })
  editOpen.value = true
}
function openEdit(p: ProxyItem) {
  editingId.value = p.id
  Object.assign(form, { name: p.name, type: p.type, host: p.host, port: p.port, username: p.username || '', password: '', description: p.description })
  editOpen.value = true
}
async function save() {
  if (!form.name.trim() || !form.host.trim() || !form.port) {
    ElMessage.warning('名称/Host/Port 必填')
    return
  }
  saving.value = true
  try {
    if (editingId.value) {
      await proxyApi.update(editingId.value, { name: form.name.trim(), type: form.type, host: form.host.trim(), port: Number(form.port), username: form.username.trim(), password: form.password || undefined, description: form.description.trim() })
      ElMessage.success('已更新（占位）')
    } else {
      await proxyApi.create({ name: form.name.trim(), type: form.type, host: form.host.trim(), port: Number(form.port), username: form.username.trim(), password: form.password || undefined, description: form.description.trim() })
      ElMessage.success('已创建（占位）')
    }
    editOpen.value = false
    reload()
  } finally {
    saving.value = false
  }
}
async function remove(p: ProxyItem) {
  try {
    await ElMessageBox.confirm(`确认删除代理「${p.name}」？`, '删除确认', { type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消' })
  } catch { return }
  await proxyApi.remove(p.id)
  ElMessage.success('已删除')
  reload()
}
async function testProxy(p: ProxyItem) {
  try {
    const res = await proxyApi.test({ type: p.type, host: p.host, port: p.port, username: p.username, password: undefined })
    ElMessage.success(res.ok ? `连通占位成功：${res.note || ''}` : '测试占位')
  } catch {
    /* 拦截器提示 */
  }
}

onMounted(() => reload())
</script>
