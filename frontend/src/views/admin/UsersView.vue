<template>
  <div class="space-y-5">
    <header class="flex flex-col sm:flex-row sm:items-center justify-between gap-4">
      <div>
        <h1 class="text-xl font-semibold">用户与权限</h1>
        <p class="text-sm text-white/45 mt-1">基于角色的访问控制：管理员 / 开发者 / 只读用户</p>
      </div>
      <button class="liquid-button flex items-center justify-center gap-2 shrink-0" @click="openCreate">
        <UserPlus class="w-4 h-4" /> 新建用户
      </button>
    </header>

    <!-- 角色说明 -->
    <div class="grid grid-cols-1 sm:grid-cols-3 gap-3">
      <div
        v-for="r in roleCards"
        :key="r.code"
        class="glass-card p-4 flex items-start gap-3"
      >
        <span class="w-9 h-9 rounded-xl flex items-center justify-center shrink-0" :style="{ background: r.tint }">
          <component :is="r.icon" class="w-4.5 h-4.5" :style="{ color: r.color }" />
        </span>
        <div class="min-w-0">
          <p class="text-sm font-medium">{{ r.name }}</p>
          <p class="text-xs text-white/45 mt-0.5 leading-relaxed">{{ r.desc }}</p>
        </div>
      </div>
    </div>

    <!-- 搜索 -->
    <div class="flex flex-wrap items-center gap-3">
      <div class="relative w-full sm:w-72 shrink-0">
        <Search class="w-4 h-4 text-white/40 absolute left-3.5 top-1/2 -translate-y-1/2" />
        <input
          v-model.trim="keyword"
          aria-label="搜索用户名或邮箱"
          class="glass-input pl-10"
          placeholder="搜索用户名 / 邮箱"
          @input="reload"
        />
      </div>
      <button class="ghost-button flex items-center gap-2 text-sm" @click="reload">
        <RefreshCw class="w-4 h-4" :class="{ 'animate-spin': loading }" /> 刷新
      </button>
    </div>

    <!-- 用户表格 -->
    <div class="glass-panel overflow-hidden" v-loading="loading">
      <div class="overflow-x-auto">
        <table class="w-full text-sm min-w-[720px]">
          <thead>
            <tr class="text-left text-white/40 text-xs border-b border-white/10">
              <th class="font-medium px-5 py-3">用户</th>
              <th class="font-medium px-5 py-3">角色</th>
              <th class="font-medium px-5 py-3">状态</th>
              <th class="font-medium px-5 py-3 hidden md:table-cell">最近登录</th>
              <th class="font-medium px-5 py-3 hidden lg:table-cell">创建时间</th>
              <th class="font-medium px-5 py-3 text-right">操作</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="u in users" :key="u.id" class="border-b border-white/5 last:border-0 hover:bg-white/5">
              <td class="px-5 py-3.5">
                <div class="flex items-center gap-3">
                  <span
                    class="w-8 h-8 rounded-full flex items-center justify-center text-xs font-semibold shrink-0"
                    :style="{ background: avatarTint(u.username) }"
                  >{{ u.username.slice(0, 1).toUpperCase() }}</span>
                  <div class="min-w-0">
                    <p class="font-medium flex items-center gap-1.5">
                      {{ u.username }}
                      <ShieldCheck v-if="u.role === 'admin'" class="w-3.5 h-3.5 text-indigo-300" />
                    </p>
                    <p class="text-xs text-white/40 truncate">{{ u.email || '—' }}</p>
                  </div>
                </div>
              </td>
              <td class="px-5 py-3.5">
                <span class="px-2 py-0.5 rounded-full text-xs" :class="roleClass(u.role)">{{ u.role_name }}</span>
              </td>
              <td class="px-5 py-3.5">
                <span class="flex items-center gap-1.5 text-xs" :class="u.is_active ? 'text-emerald-300' : 'text-white/35'">
                  <span class="w-1.5 h-1.5 rounded-full" :class="u.is_active ? 'bg-emerald-400' : 'bg-white/25'" />
                  {{ u.is_active ? '启用' : '已停用' }}
                </span>
              </td>
              <td class="px-5 py-3.5 text-white/55 text-xs hidden md:table-cell">{{ u.last_login_at ? fmtTime(u.last_login_at) : '从未登录' }}</td>
              <td class="px-5 py-3.5 text-white/55 text-xs hidden lg:table-cell">{{ fmtDate(u.created_at) }}</td>
              <td class="px-5 py-3.5">
                <div class="flex items-center justify-end gap-1">
                  <button class="icon-btn" aria-label="编辑角色" title="编辑角色与邮箱" @click="openEdit(u)">
                    <Pencil class="w-4 h-4" />
                  </button>
                  <button class="icon-btn" aria-label="重置密码" title="重置密码" @click="openReset(u)">
                    <KeyRound class="w-4 h-4" />
                  </button>
                  <button
                    class="w-8 h-8 rounded-lg flex items-center justify-center transition-colors"
                    :class="u.is_active
                      ? 'text-amber-300/80 hover:bg-amber-400/15'
                      : 'text-emerald-300/80 hover:bg-emerald-400/15'"
                    :aria-label="u.is_active ? '停用' : '启用'"
                    @click="toggleActive(u)"
                  >
                    <component :is="u.is_active ? Lock : LockOpen" class="w-4 h-4" />
                  </button>
                  <button
                    class="w-8 h-8 rounded-lg flex items-center justify-center text-white/50 hover:bg-rose-500/20 hover:text-rose-300 transition-colors"
                    aria-label="删除用户"
                    @click="remove(u)"
                  >
                    <Trash2 class="w-4 h-4" />
                  </button>
                </div>
              </td>
            </tr>
            <tr v-if="!users.length && !loading">
              <td colspan="6" class="px-5 py-12 text-center text-white/40 text-sm">暂无用户</td>
            </tr>
          </tbody>
        </table>
      </div>

      <!-- 分页 -->
      <div class="flex items-center justify-between px-5 py-3 border-t border-white/10 text-xs text-white/50">
        <span>共 {{ total }} 个用户</span>
        <div class="flex items-center gap-2">
          <button class="ghost-button !py-1 !px-3" :disabled="page <= 1" @click="page--; reload()">上一页</button>
          <span>第 {{ page }} 页</span>
          <button class="ghost-button !py-1 !px-3" :disabled="page * pageSize >= total" @click="page++; reload()">下一页</button>
        </div>
      </div>
    </div>

    <!-- 新建/编辑弹窗 -->
    <el-dialog
      v-model="dialog"
      :title="form.id ? '编辑用户' : '新建用户'"
      :width="dialogWidth"
      :close-on-click-modal="false"
      class="glass-dialog"
    >
      <el-form label-position="top">
        <el-form-item label="用户名">
          <el-input v-model="form.username" :disabled="Boolean(form.id)" :placeholder="form.id ? '' : '至少 3 个字符'" />
        </el-form-item>
        <el-form-item v-if="!form.id" label="初始密码">
          <el-input v-model="form.password" type="password" show-password placeholder="至少 8 个字符" />
        </el-form-item>
        <el-form-item label="角色">
          <el-select v-model="form.role" class="w-full" popper-class="glass-popper">
            <el-option v-for="r in roles" :key="r.code" :label="`${r.name}（${r.description}）`" :value="r.code" />
          </el-select>
        </el-form-item>
        <el-form-item label="邮箱（可选）">
          <el-input v-model="form.email" placeholder="name@example.com" />
        </el-form-item>
      </el-form>
      <template #footer>
        <div class="flex justify-end gap-2">
          <button class="ghost-button" @click="dialog = false">取消</button>
          <button class="liquid-button" :disabled="saving" @click="save">{{ saving ? '保存中…' : '保存' }}</button>
        </div>
      </template>
    </el-dialog>

    <!-- 重置密码弹窗 -->
    <el-dialog v-model="resetDialog" title="重置密码" width="440px" :close-on-click-modal="false" class="glass-dialog">
      <el-form label-position="top">
        <el-form-item label="新密码">
          <el-input v-model="resetPwd" type="password" show-password placeholder="至少 8 个字符" />
        </el-form-item>
      </el-form>
      <template #footer>
        <div class="flex justify-end gap-2">
          <button class="ghost-button" @click="resetDialog = false">取消</button>
          <button class="liquid-button" @click="confirmReset">确认重置</button>
        </div>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  Eye,
  KeyRound,
  Lock,
  LockOpen,
  Pencil,
  RefreshCw,
  Search,
  ShieldCheck,
  TerminalSquare,
  Trash2,
  UserPlus,
  Users,
} from 'lucide-vue-next'
import { adminApi, type Role, type UserItem } from '../../api/admin'

const users = ref<UserItem[]>([])
const roles = ref<Role[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = 20
const keyword = ref('')
const loading = ref(false)
const saving = ref(false)

const roleCards = computed(() => [
  { code: 'admin', name: '管理员', desc: '全部权限：用户、数据源、SQL、审计', icon: ShieldCheck, color: '#818cf8', tint: 'rgba(129,140,248,.16)' },
  { code: 'developer', name: '开发者', desc: '管理数据源与隧道、执行读写 SQL', icon: TerminalSquare, color: '#34d399', tint: 'rgba(52,211,153,.14)' },
  { code: 'readonly', name: '只读用户', desc: '仅浏览数据源与执行只读查询', icon: Eye, color: '#fbbf24', tint: 'rgba(251,191,36,.14)' },
])
void Users

async function reload() {
  loading.value = true
  try {
    const [u, r] = await Promise.all([
      adminApi.users({ keyword: keyword.value, page: page.value, page_size: pageSize }),
      roles.value.length ? Promise.resolve(null) : adminApi.roles(),
    ])
    users.value = u.items
    total.value = u.total
    if (r) roles.value = r.items
  } finally {
    loading.value = false
  }
}
onMounted(reload)

const dialog = ref(false)
const form = reactive<{ id: number | null; username: string; password: string; role: string; email: string }>({
  id: null, username: '', password: '', role: 'developer', email: '',
})
function openCreate() {
  Object.assign(form, { id: null, username: '', password: '', role: 'developer', email: '' })
  dialog.value = true
}
function openEdit(u: UserItem) {
  Object.assign(form, { id: u.id, username: u.username, password: '', role: u.role, email: u.email })
  dialog.value = true
}
async function save() {
  if (!form.username.trim()) return ElMessage.warning('请填写用户名')
  if (!form.id && form.username.trim().length < 3) return ElMessage.warning('用户名至少 3 个字符')
  if (!form.id && form.password.length < 8) return ElMessage.warning('初始密码至少 8 个字符')
  saving.value = true
  try {
    if (form.id) {
      await adminApi.updateUser(form.id, { role: form.role, email: form.email.trim() })
      ElMessage.success('用户已更新')
    } else {
      await adminApi.createUser({
        username: form.username.trim(),
        password: form.password,
        role: form.role,
        email: form.email.trim(),
      })
      ElMessage.success('用户已创建')
    }
    dialog.value = false
    await reload()
  } catch {
    /* 拦截器已提示 */
  } finally {
    saving.value = false
  }
}

const resetDialog = ref(false)
const resetTarget = ref<UserItem | null>(null)
const resetPwd = ref('')
function openReset(u: UserItem) {
  resetTarget.value = u
  resetPwd.value = ''
  resetDialog.value = true
}
async function confirmReset() {
  if (resetPwd.value.length < 8) return ElMessage.warning('新密码至少 8 个字符')
  if (!resetTarget.value) return
  await adminApi.resetPassword(resetTarget.value.id, resetPwd.value)
  ElMessage.success('密码已重置')
  resetDialog.value = false
}

async function toggleActive(u: UserItem) {
  const next = !u.is_active
  if (!next) {
    try {
      await ElMessageBox.confirm(`确认停用用户「${u.username}」？停用后该用户将无法登录。`, '停用确认', {
        type: 'warning', confirmButtonText: '停用', cancelButtonText: '取消',
      })
    } catch {
      return
    }
  }
  await adminApi.setUserStatus(u.id, next)
  ElMessage.success(next ? '已启用' : '已停用')
  reload()
}

async function remove(u: UserItem) {
  try {
    await ElMessageBox.confirm(`确认删除用户「${u.username}」？其数据源与查询历史将一并删除。`, '删除确认', {
      type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消',
    })
  } catch {
    return
  }
  await adminApi.deleteUser(u.id)
  ElMessage.success('已删除')
  reload()
}

function roleClass(role: string) {
  return {
    admin: 'bg-indigo-400/15 text-indigo-300',
    developer: 'bg-emerald-400/15 text-emerald-300',
    readonly: 'bg-amber-400/15 text-amber-300',
  }[role] ?? 'bg-white/10 text-white/60'
}
function avatarTint(username: string) {
  const colors = ['rgba(129,140,248,.25)', 'rgba(52,211,153,.22)', 'rgba(244,114,182,.22)', 'rgba(251,191,36,.22)', 'rgba(96,165,250,.22)']
  let h = 0
  for (const ch of username) h = (h * 31 + ch.charCodeAt(0)) >>> 0
  return colors[h % colors.length]
}
function fmtTime(s: string) {
  return new Date(s).toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false })
}
function fmtDate(s: string) {
  return new Date(s).toLocaleDateString('zh-CN')
}

const viewportWidth = ref(window.innerWidth)
const dialogWidth = computed(() => (viewportWidth.value < 640 ? '92vw' : '520px'))
function onResize() { viewportWidth.value = window.innerWidth }
onMounted(() => window.addEventListener('resize', onResize))
onBeforeUnmount(() => window.removeEventListener('resize', onResize))
</script>

<style scoped>
.icon-btn {
  width: 2rem;
  height: 2rem;
  border-radius: 0.5rem;
  display: flex;
  align-items: center;
  justify-content: center;
  color: rgba(255, 255, 255, 0.5);
  transition: all 0.15s;
}
.icon-btn:hover {
  background: rgba(255, 255, 255, 0.1);
  color: #fff;
}
</style>
