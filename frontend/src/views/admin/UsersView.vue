<script setup lang="ts">
import { computed, nextTick, onMounted, reactive, ref, useId, watch } from 'vue'
import { ElMessageBox } from 'element-plus'
import { adminApi, type Role, type UserItem } from '../../api/admin'
import PageHeader from '../../components/base/PageHeader.vue'
import PageState from '../../components/base/PageState.vue'
import LoadingState from '../../components/base/LoadingState.vue'
import { useReportRun } from '../report/useReportRun'
import MemberCapabilitiesDialog from './MemberCapabilitiesDialog.vue'
import { useUserWriteReceipt } from './useUserManagementPageState'
defineProps<{ embedded?: boolean }>()
const capabilityTarget=ref<UserItem>()

const directory = useReportRun<Awaited<ReturnType<typeof adminApi.users>>>()
const roleDirectory = useReportRun<{ items: Role[] }>()
const records = computed(() => directory.state.value.status === 'success' ? directory.state.value.data : undefined)
const roles = computed(() => roleDirectory.state.value.status === 'success' ? roleDirectory.state.value.data.items : [])
const roleLoading = computed(() => roleDirectory.state.value.status === 'loading')
const page = ref(1)
const pageSize = 20
const keyword = ref(''), search = ref('')
const loading = computed(() => directory.state.value.status === 'loading')
const saving = ref(false)
const selectedId = ref<number>(), details = ref<HTMLElement>(), tableRegion = ref<HTMLElement>()
const selected = computed(() => records.value?.items.find(item => item.id === selectedId.value))
const operationError = ref(''), formError = ref(''), resetError = ref('')
const receipt = useUserWriteReceipt()
const emailHelpId = useId()
const dialogWidth = 'min(520px, calc(100vw - 24px))'
const quiet = { silentError: true }

async function reload(next = page.value) {
  page.value = next
  const q = search.value
  const loaded = await directory.run(`${next}:${q}`, signal => adminApi.users({ keyword: q || undefined, page: next, page_size: pageSize }, { signal, silentError: true }))
  if (!loaded || !records.value) return
  if (next > 1 && !records.value.items.length && records.value.total <= (next - 1) * pageSize) {
    await reload(Math.max(1, Math.ceil(records.value.total / pageSize)))
  }
}
function searchUsers() { search.value = keyword.value.trim(); selectedId.value = undefined; void reload(1) }
function changePage(next: number) { selectedId.value = undefined; void reload(next) }
async function openDetails(item: UserItem) { selectedId.value = item.id; await nextTick(); details.value?.focus() }
async function closeDetails() {
  const id = selectedId.value
  selectedId.value = undefined
  await nextTick()
  tableRegion.value?.querySelector<HTMLElement>(`[data-user-id="${id}"]`)?.focus()
}
onMounted(() => { void reload() })

const dialog = ref(false)
const form = reactive({ id: null as number | null, username: '', password: '', role: '', email: '', canEditEmail: true })
let preferredRole = ''
let originalEmail = ''
async function loadRoles() {
  const role = preferredRole
  const loaded = await roleDirectory.run('roles', signal => adminApi.roles({ signal, silentError: true }))
  if (loaded) form.role = roles.value.some(item => item.code === role) ? role : ''
}
watch(dialog, open => {
  if (open) {
    void loadRoles()
    return
  }
  roleDirectory.reset()
  form.role = ''
  form.password = ''
}, { flush: 'sync' })
function openCreate() {
  formError.value = ''
  preferredRole = 'developer'
  Object.assign(form, { id: null, username: '', password: '', role: '', email: '', canEditEmail: true })
  dialog.value = true
}
function openEdit(u: UserItem) {
  formError.value = ''
  preferredRole = u.role
  originalEmail = u.email
  Object.assign(form, { id: u.id, username: u.username, password: '', role: '', email: u.email, canEditEmail: u.can_edit_email === true })
  dialog.value = true
}
async function save() {
  if (saving.value || !roles.value.some(item => item.code === form.role)) return
  if (!form.username.trim()) { formError.value = '请填写用户名。'; return }
  if (!form.id && form.username.trim().length < 3) { formError.value = '用户名至少 3 个字符。'; return }
  if (!form.id && form.password.length < 8) { formError.value = '初始密码至少 8 个字符。'; return }
  saving.value = true
  formError.value = ''
  receipt.value = ''
  try {
    if (form.id) {
      const email = form.email.trim()
      await adminApi.updateUser(form.id, {
        role: form.role,
        ...(form.canEditEmail && email !== originalEmail ? { email } : {}),
      }, quiet)
      receipt.value = '用户已更新。'
    } else {
      await adminApi.createUser({
        username: form.username.trim(),
        password: form.password,
        role: form.role,
        email: form.email.trim(),
      }, quiet)
      receipt.value = '用户已创建。'
    }
    dialog.value = false
    await reload()
  } catch (error) {
    formError.value = error instanceof Error ? error.message : '暂时无法保存用户。'
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
  resetError.value = ''
  resetDialog.value = true
}
async function confirmReset() {
  if (saving.value) return
  if (resetPwd.value.length < 8) { resetError.value = '新密码至少 8 个字符。'; return }
  if (!resetTarget.value) return
  saving.value = true
  resetError.value = ''
  try {
    await adminApi.resetPassword(resetTarget.value.id, resetPwd.value, quiet)
    receipt.value = '密码已重置。'
    resetDialog.value = false
  } catch (error) { resetError.value = error instanceof Error ? error.message : '暂时无法重置密码。' }
  finally { resetPwd.value = ''; saving.value = false }
}

async function toggleActive(u: UserItem) {
  if (saving.value) return
  const next = !u.is_active
  saving.value = true
  operationError.value = ''
  receipt.value = ''
  try {
    if (!next) {
      try {
        await ElMessageBox.confirm(`确认停用用户「${u.username}」？停用后该用户将无法登录。`, '停用确认', {
          type: 'warning', confirmButtonText: '停用', cancelButtonText: '取消',
        })
      } catch { return }
    }
    await adminApi.setUserStatus(u.id, next, quiet)
    receipt.value = next ? '用户已启用。' : '用户已停用。'
    await reload()
  } catch (error) { operationError.value = error instanceof Error ? error.message : '暂时无法更新用户状态。' }
  finally { saving.value = false }
}

async function remove(u: UserItem) {
  if (saving.value) return
  saving.value = true
  operationError.value = ''
  receipt.value = ''
  try {
    try { await ElMessageBox.confirm(`确认删除用户「${u.username}」？其数据源与查询历史将一并删除。`, '删除确认', {
      type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消',
    }) } catch { return }
    await adminApi.deleteUser(u.id, quiet)
    selectedId.value = undefined
    receipt.value = '用户已删除。'
    await reload()
  } catch (error) { operationError.value = error instanceof Error ? error.message : '暂时无法删除用户。' }
  finally { saving.value = false }
}

function fmtTime(s: string) {
  return new Date(s).toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false })
}
function fmtDate(s: string) {
  return new Date(s).toLocaleDateString('zh-CN')
}

</script>

<template>
  <div class="ui-page-stack">
    <PageHeader :title="embedded ? '兼容用户与职责' : '用户管理（兼容入口）'" :heading="embedded ? 'h2' : 'h1'"
      description="管理现有基础角色、账号状态和当前工作空间职责。">
      <template #actions><button type="button" class="liquid-button ui-control-target" :disabled="saving" @click="openCreate">新建用户</button></template>
    </PageHeader>
    <form class="ui-filter-bar" @submit.prevent="searchUsers">
      <label class="text-sm min-w-0 sm:max-w-sm">用户名或邮箱<input v-model.trim="keyword" aria-label="搜索用户名或邮箱" class="glass-input block w-full mt-1" placeholder="搜索用户名或邮箱" /></label>
      <button class="ghost-button ui-control-target">搜索</button>
      <button type="button" class="ghost-button ui-control-target" :disabled="loading || saving" @click="reload()">刷新</button>
    </form>
    <p v-if="receipt" role="status" class="text-success text-sm">{{ receipt }}</p>
    <p v-if="operationError" role="alert" class="text-danger text-sm">{{ operationError }}</p>
    <LoadingState v-if="loading" label="正在读取用户列表…" />
    <PageState v-else-if="directory.state.value.status === 'error'" title="读取用户列表失败" :description="directory.state.value.message || '请重试读取。'" tone="danger" :heading="embedded ? 'h3' : 'h2'">
      <template #actions><button type="button" class="ghost-button ui-control-target" @click="reload()">重试读取用户</button></template>
    </PageState>
    <template v-else-if="records">
      <div v-if="records.items.length" ref="tableRegion" class="editorial-table-wrap" role="region" tabindex="0" aria-label="用户列表表格">
        <table class="editorial-table" aria-label="用户列表">
          <thead><tr><th scope="col">序号</th><th scope="col">用户</th><th scope="col">角色</th><th scope="col">状态</th><th scope="col">最近登录</th><th scope="col">操作</th></tr></thead>
          <tbody><tr v-for="(item, index) in records.items" :key="item.id" :aria-label="item.username">
            <td class="editorial-table__number">{{ String((page - 1) * pageSize + index + 1).padStart(2, '0') }}</td>
            <td class="editorial-table__name"><p class="editorial-object-title">{{ item.username }}</p><p v-if="item.email" class="editorial-table__meta">{{ item.email }}</p></td>
            <td>{{ item.role_name }}</td>
            <td><span class="status-badge" :class="{ 'status-badge--success': item.is_active }">{{ item.is_active ? '已启用' : '已停用' }}</span></td>
            <td class="whitespace-nowrap">{{ item.last_login_at ? fmtTime(item.last_login_at) : '从未登录' }}</td>
            <td><button type="button" class="editorial-text-action whitespace-nowrap" :data-user-id="item.id" :aria-expanded="item.id === selectedId" :disabled="saving" @click="openDetails(item)">查看用户</button></td>
          </tr></tbody>
        </table>
      </div>
      <PageState v-else title="暂无匹配用户" description="可调整用户名或邮箱搜索条件。" :heading="embedded ? 'h3' : 'h2'" />
      <div class="flex flex-wrap items-center justify-between gap-2 text-xs text-muted">
        <span>共 {{ records.total }} 个用户</span>
        <div class="flex items-center gap-2"><button class="ghost-button ui-control-target" :disabled="page <= 1 || saving" @click="changePage(page - 1)">上一页</button><span>第 {{ page }} 页</span><button class="ghost-button ui-control-target" :disabled="page * pageSize >= records.total || saving" @click="changePage(page + 1)">下一页</button></div>
      </div>
    </template>
    <section v-if="selected" class="glass-panel p-4 space-y-4 min-w-0" aria-label="用户详情">
      <header class="flex flex-wrap items-center justify-between gap-2">
        <component :is="embedded ? 'h3' : 'h2'" ref="details" tabindex="-1" class="editorial-object-title scroll-mt-32">{{ selected.username }} · 用户详情</component>
        <button type="button" class="ghost-button ui-control-target" :disabled="saving" @click="closeDetails">关闭用户详情</button>
      </header>
      <dl class="grid gap-3 text-sm sm:grid-cols-3">
        <div><dt class="text-muted">邮箱</dt><dd class="break-all">{{ selected.email || '未填写' }}</dd></div>
        <div><dt class="text-muted">基础角色</dt><dd>{{ selected.role_name }}</dd></div>
        <div><dt class="text-muted">账号状态</dt><dd>{{ selected.is_active ? '已启用' : '已停用' }}</dd></div>
        <div><dt class="text-muted">创建时间</dt><dd>{{ fmtDate(selected.created_at) }}</dd></div>
        <div><dt class="text-muted">最近登录</dt><dd>{{ selected.last_login_at ? fmtTime(selected.last_login_at) : '从未登录' }}</dd></div>
      </dl>
      <div class="flex flex-wrap gap-2">
        <button class="ghost-button ui-control-target" :disabled="saving" @click="capabilityTarget = selected">职责权限</button>
        <button class="ghost-button ui-control-target" :disabled="saving" @click="openEdit(selected)">编辑用户</button>
        <button class="ghost-button ui-control-target" :disabled="saving" @click="openReset(selected)">重置密码</button>
        <button class="ghost-button ui-control-target" :disabled="saving" @click="toggleActive(selected)">{{ selected.is_active ? '停用' : '启用' }}</button>
        <button class="ghost-button text-danger ui-control-target" :disabled="saving" @click="remove(selected)">删除用户</button>
      </div>
    </section>
    <!-- 新建/编辑弹窗 -->
    <el-dialog
      v-model="dialog"
      :title="form.id ? '编辑用户' : '新建用户'"
      :width="dialogWidth"
      :close-on-click-modal="false"
      :show-close="!saving"
      :close-on-press-escape="!saving"
      class="glass-dialog"
    >
      <el-form label-position="top" @submit.prevent>
        <el-form-item label="用户名">
          <el-input v-model="form.username" :disabled="Boolean(form.id)" :placeholder="form.id ? '' : '至少 3 个字符'" />
        </el-form-item>
        <el-form-item v-if="!form.id" label="初始密码">
          <el-input v-model="form.password" type="password" show-password placeholder="至少 8 个字符" />
        </el-form-item>
        <el-form-item label="角色">
          <div class="w-full" :aria-busy="roleLoading">
            <el-select
              v-model="form.role"
              aria-label="基础角色"
              class="w-full"
              placeholder="请选择基础角色"
              :disabled="saving || !roles.length"
              placement="top-start"
              fit-input-width
              popper-class="glass-popper user-role-popper"
            >
              <el-option v-for="r in roles" :key="r.code" :label="`${r.name}（${r.description}）`" :value="r.code" />
            </el-select>
          </div>
          <LoadingState v-if="roleLoading" variant="inline" label="正在读取角色选项…" />
          <div v-else-if="roleDirectory.state.value.status === 'error'" class="flex flex-wrap items-center gap-2">
            <p role="alert" class="text-sm text-danger">读取角色选项失败。{{ roleDirectory.state.value.message }}</p>
            <button type="button" class="ghost-button ui-control-target" @click="loadRoles">重试读取角色</button>
          </div>
          <p v-else-if="roleDirectory.state.value.status === 'success' && !roles.length" role="status" class="text-sm text-muted">暂无可用角色。</p>
        </el-form-item>
        <el-form-item label="邮箱（可选）">
          <el-input v-model="form.email" placeholder="name@example.com" :readonly="!form.canEditEmail"
            :aria-describedby="form.canEditEmail ? undefined : emailHelpId" />
          <p v-if="!form.canEditEmail" :id="emailHelpId" class="text-sm text-muted">当前只能修改该用户在此空间的角色。</p>
        </el-form-item>
      </el-form>
      <p v-if="formError" role="alert" class="text-sm text-danger">{{ formError }}</p>
      <template #footer>
        <div class="flex justify-end gap-2">
          <button type="button" class="ghost-button ui-control-target" :disabled="saving" @click="dialog = false">取消</button>
          <button type="button" class="liquid-button ui-control-target user-save-button" :disabled="saving || !roles.some(item => item.code === form.role)" @click="save">
            <span :class="{ invisible: saving }">保存</span>
            <span :class="{ invisible: !saving }">保存中…</span>
          </button>
        </div>
      </template>
    </el-dialog>

    <!-- 重置密码弹窗 -->
    <el-dialog v-model="resetDialog" title="重置密码" :width="dialogWidth" :close-on-click-modal="false" :show-close="!saving" :close-on-press-escape="!saving" class="glass-dialog" @closed="resetPwd = ''">
      <el-form label-position="top">
        <el-form-item label="新密码">
          <el-input v-model="resetPwd" type="password" show-password placeholder="至少 8 个字符" />
        </el-form-item>
      </el-form>
      <p v-if="resetError" role="alert" class="text-sm text-danger">{{ resetError }}</p>
      <template #footer>
        <div class="flex justify-end gap-2">
          <button class="ghost-button ui-control-target" :disabled="saving" @click="resetDialog = false">取消</button>
          <button class="liquid-button ui-control-target" :disabled="saving" @click="confirmReset">确认重置</button>
        </div>
      </template>
    </el-dialog>
    <MemberCapabilitiesDialog v-if="capabilityTarget" :user="capabilityTarget" @close="capabilityTarget=undefined" @saved="reload" />
  </div>
</template>

<style scoped>
:global(.user-role-popper .el-select-dropdown__item) {
  line-height: 1.5;
  white-space: normal;
  overflow-wrap: anywhere;
}

:global(.user-role-popper.el-zoom-in-top-leave-active) {
  pointer-events: none;
}

.liquid-button.user-save-button {
  display: inline-grid;
  place-items: center;
}

.user-save-button > span {
  grid-area: 1 / 1;
}
</style>
