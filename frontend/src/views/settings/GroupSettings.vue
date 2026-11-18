<script setup lang="ts">
import { computed, nextTick, onMounted, reactive, ref } from 'vue'
import { ElMessageBox } from 'element-plus'
import { isCancel } from 'axios'
import { adminApi, type UserGroup } from '../../api/admin'
import { ApiRequestError } from '../../utils/request'
import UserPicker from '../../components/UserPicker.vue'
import PageState from '../../components/base/PageState.vue'
import LoadingState from '../../components/base/LoadingState.vue'
import { useReportRun } from '../report/useReportRun'
import { useUserManagementPageState, useUserWriteReceipt } from '../admin/useUserManagementPageState'
const list = useReportRun<Awaited<ReturnType<typeof adminApi.groups>>>()
const records = computed(() => list.state.value.status === 'success' ? list.state.value.data : undefined)
const pageSize = 10
const page = useUserManagementPageState('groupPage')
const keyword = useUserManagementPageState('groupKeyword')
const search = useUserManagementPageState('groupSearch')
const open = ref(false), busy = ref(false)
const selectedId = ref<number>(), heading = ref<HTMLElement>(), tableRegion = ref<HTMLElement>()
const selected = computed(() => records.value?.items.find(group => group.id === selectedId.value))
const receipt = useUserWriteReceipt()
const operationError = useUserManagementPageState('groupOperationError')
const saveError = ref('')
const form = reactive({id: 0, name: '', description: '', member_ids: [] as number[], expected_version: 0})
async function load(next = page.value) {
  page.value = next
  const q = search.value
  const loaded = await list.run(`${next}:${q}`, signal => adminApi.groups({ keyword: q || undefined, page: next, page_size: pageSize }, { signal, silentError: true }))
  if (loaded && records.value && next > 1 && !records.value.items.length && records.value.total <= (next - 1) * pageSize) {
    await load(Math.max(1, Math.ceil(records.value.total / pageSize)))
  }
}
function changePage(next: number) { selectedId.value = undefined; void load(next) }
function applySearch() { search.value = keyword.value.trim(); changePage(1) }
async function select(group: UserGroup) { selectedId.value = group.id; await nextTick(); heading.value?.focus() }
async function closeDetails() {
  const id = selectedId.value
  selectedId.value = undefined
  await nextTick()
  tableRegion.value?.querySelector<HTMLElement>(`[data-group-id="${id}"]`)?.focus()
}
function edit(g?: UserGroup) {
  operationError.value = ''
  saveError.value = ''
  Object.assign(form, { id: g?.id || 0, name: g?.name || '', description: g?.description || '', member_ids: [...g?.member_ids || []], expected_version: g?.version || 0 })
  open.value = true
}
async function save() {
  if (busy.value || !form.name.trim()) return
  busy.value = true
  saveError.value = ''
  operationError.value = ''
  receipt.value = ''
  try {
    const result = await adminApi.saveGroup(form.id, { ...form, name: form.name.trim() }, { silentError: true })
    selectedId.value = result.id
    receipt.value = '用户组已保存。'
    open.value = false
    await load()
  } catch (error) {
    if (isCancel(error)) {
      saveError.value = '保存结果需要核对。请刷新用户组，确认当前版本后再编辑。'
      operationError.value = saveError.value
    } else {
      saveError.value = error instanceof Error ? error.message : '暂时无法保存用户组。'
      if (error instanceof ApiRequestError && error.status === 409) operationError.value = saveError.value
    }
  }
  finally { busy.value = false }
}
async function remove(g: UserGroup) {
  if (busy.value) return
  busy.value = true
  operationError.value = ''
  receipt.value = ''
  try {
    try { await ElMessageBox.confirm(`删除“${g.name}”会立即撤销通过该组授予的数据源访问权限。`, '删除用户组', { type: 'warning', confirmButtonText: '确认删除', cancelButtonText: '取消' }) }
    catch { return }
    await adminApi.deleteGroup(g.id, g.version, { silentError: true })
    selectedId.value = undefined
    receipt.value = '用户组已删除。'
    await load()
  } catch (error) { operationError.value = error instanceof Error ? error.message : '暂时无法删除用户组。' }
  finally { busy.value = false }
}
onMounted(() => void load())
</script>

<template>
  <section class="space-y-4 min-w-0" aria-label="用户组管理">
    <div class="flex flex-wrap justify-between gap-3"><div><h2 class="editorial-object-title">用户组</h2><p class="text-sm text-muted mt-1">按团队维护数据源发布范围。移出组后，相关授权随即失效。</p></div><button class="liquid-button ui-control-target" :disabled="busy" @click="edit()">新建用户组</button></div>
    <form class="ui-filter-bar" @submit.prevent="applySearch"><label class="text-sm min-w-0 sm:max-w-sm">用户组名称<input v-model.trim="keyword" class="glass-input block w-full mt-1" aria-label="搜索用户组" placeholder="搜索用户组名称" /></label><button class="ghost-button ui-control-target">搜索</button><button type="button" class="ghost-button ui-control-target" :disabled="busy || list.state.value.status === 'loading'" @click="load()">刷新用户组</button></form>
    <p v-if="receipt" role="status" class="text-sm text-success">{{ receipt }}</p>
    <p v-if="operationError && !open" role="alert" class="text-sm text-danger">{{ operationError }}</p>
    <LoadingState v-if="list.state.value.status === 'loading'" label="正在读取用户组…" />
    <PageState v-else-if="list.state.value.status === 'error'" title="读取用户组失败" :description="list.state.value.message || '请重试读取。'" tone="danger" heading="h3"><template #actions><button class="ghost-button ui-control-target" @click="load()">重试读取用户组</button></template></PageState>
    <template v-else-if="records">
      <div v-if="records.items.length" ref="tableRegion" class="editorial-table-wrap" role="region" tabindex="0" aria-label="用户组表格">
        <table class="editorial-table" aria-label="用户组列表"><thead><tr><th scope="col">序号</th><th scope="col">用户组名称</th><th scope="col">成员数</th><th scope="col">更新时间</th><th scope="col">操作</th></tr></thead>
          <tbody><tr v-for="(group, index) in records.items" :key="group.id" :aria-label="group.name"><td class="editorial-table__number">{{ String((page - 1) * pageSize + index + 1).padStart(2, '0') }}</td><td class="editorial-table__name"><p class="editorial-object-title">{{ group.name }}</p><p v-if="group.description" class="editorial-table__meta">{{ group.description }}</p></td><td>{{ group.member_ids.length }}</td><td class="whitespace-nowrap">{{ new Date(group.updated_at).toLocaleString('zh-CN') }}</td><td><button class="editorial-text-action whitespace-nowrap" :data-group-id="group.id" :aria-expanded="selectedId === group.id" :disabled="busy" @click="select(group)">查看用户组</button></td></tr></tbody>
        </table>
      </div>
      <PageState v-else title="暂无匹配用户组" description="可调整搜索条件或创建用户组。" heading="h3" />
      <div class="flex flex-wrap justify-between gap-2 text-xs text-muted"><span>共 {{ records.total }} 组</span><div class="flex items-center gap-2"><button class="ghost-button ui-control-target" :disabled="busy || page <= 1" @click="changePage(page - 1)">上一页</button><span>第 {{ page }} 页</span><button class="ghost-button ui-control-target" :disabled="busy || page * pageSize >= records.total" @click="changePage(page + 1)">下一页</button></div></div>
    </template>
    <section v-if="selected" class="glass-panel p-4 space-y-3 min-w-0" aria-label="用户组详情">
      <div class="flex flex-wrap justify-between gap-2"><h3 ref="heading" tabindex="-1" class="editorial-object-title scroll-mt-32">{{ selected.name }} · 用户组详情</h3><button class="ghost-button ui-control-target" :disabled="busy" @click="closeDetails">关闭用户组详情</button></div>
      <p v-if="selected.description" class="text-sm break-words">{{ selected.description }}</p>
      <dl class="space-y-2 text-sm"><div class="flex gap-2"><dt class="text-muted">版本</dt><dd>{{ selected.version }}</dd></div><div class="flex flex-wrap gap-2"><dt class="text-muted">成员编号（{{ selected.member_ids.length }}）</dt><dd class="min-w-0 break-all">{{ selected.member_ids.join('、') || '暂无成员' }}</dd></div></dl>
      <div class="flex flex-wrap gap-2"><button class="ghost-button ui-control-target" :disabled="busy" @click="edit(selected)">编辑成员</button><button class="ghost-button text-danger ui-control-target" :disabled="busy" @click="remove(selected)">删除用户组</button></div>
    </section>
    <el-dialog v-model="open" :title="form.id ? '编辑用户组' : '新建用户组'" width="min(92vw, 560px)" class="glass-dialog" :close-on-click-modal="false" :show-close="!busy" :close-on-press-escape="!busy">
      <el-form label-position="top"><el-form-item label="组名称"><el-input v-model="form.name" maxlength="100" aria-label="组名称" /></el-form-item><el-form-item label="说明"><el-input v-model="form.description" maxlength="500" type="textarea" /></el-form-item><el-form-item label="成员"><UserPicker v-model="form.member_ids" label="搜索用户组成员" /></el-form-item></el-form>
      <p class="text-xs text-warning">修改成员会影响所有授权给此组的数据源；已发送的数据库操作可能仍在执行。</p>
      <p v-if="saveError" role="alert" class="mt-2 text-sm text-danger">{{ saveError }}</p>
      <template #footer><button type="button" class="liquid-button ui-control-target aria-disabled:opacity-50"
        :disabled="!form.name.trim()" :aria-disabled="busy || undefined" @click="save">保存用户组</button></template>
    </el-dialog>
  </section>
</template>
