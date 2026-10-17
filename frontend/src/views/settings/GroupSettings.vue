<template>
  <section class="glass-card p-6 space-y-4" aria-label="用户组管理">
    <div class="flex flex-wrap justify-between gap-3"><div><h2 class="font-medium">用户组</h2><p class="text-xs text-white/50 mt-1">按团队维护数据源发布范围。移出组后，相关授权随即失效。</p></div><button class="ghost-button text-sm" @click="edit()">新建用户组</button></div>
    <input v-model="keyword" class="glass-input" aria-label="搜索用户组" placeholder="搜索用户组名称" @input="page = 1; load()" />
    <div v-loading="loading" class="space-y-2 min-h-12">
      <article v-for="g in groups" :key="g.id" class="rounded-xl bg-white/5 p-3 flex flex-wrap items-center justify-between gap-3">
        <div><p>{{ g.name }} <span class="text-xs text-white/45">{{ g.member_ids.length }} 人</span></p><p class="text-xs text-white/50 break-words">{{ g.description }}</p></div>
        <div class="flex gap-2"><button class="ghost-button text-xs" @click="edit(g)">编辑成员</button><button class="ghost-button text-xs" @click="remove(g)">删除</button></div>
      </article>
      <p v-if="!groups.length && !loading" class="text-sm text-white/45">暂无用户组</p>
    </div>
    <div class="flex flex-wrap justify-between gap-2 text-xs text-white/50"><span>共 {{ total }} 组</span><div class="flex gap-3"><button :disabled="page <= 1" @click="page--; load()">上一页</button><span>第 {{ page }} 页</span><button :disabled="page * 10 >= total" @click="page++; load()">下一页</button></div></div>
    <el-dialog v-model="open" :title="form.id ? '编辑用户组' : '新建用户组'" width="min(92vw, 560px)" class="glass-dialog" :close-on-click-modal="false">
      <el-form label-position="top"><el-form-item label="组名称"><el-input v-model="form.name" maxlength="100" aria-label="组名称" /></el-form-item><el-form-item label="说明"><el-input v-model="form.description" maxlength="500" type="textarea" /></el-form-item><el-form-item label="成员"><UserPicker v-model="form.member_ids" label="搜索用户组成员" /></el-form-item></el-form>
      <p class="text-xs text-amber-200/80">修改成员会影响所有授权给此组的数据源；已发送的数据库操作可能仍在执行。</p>
      <template #footer><button class="liquid-button" :disabled="busy || !form.name.trim()" @click="save">保存用户组</button></template>
    </el-dialog>
  </section>
</template>
<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { adminApi, type UserGroup } from '../../api/admin'
import UserPicker from '../../components/UserPicker.vue'
const groups = ref<UserGroup[]>([]), total = ref(0), page = ref(1), keyword = ref(''), loading = ref(false), open = ref(false), busy = ref(false)
const form = reactive({id: 0, name: '', description: '', member_ids: [] as number[], expected_version: 0})
let generation = 0
async function load() {const current = ++generation; loading.value = true; try {const r = await adminApi.groups({keyword: keyword.value, page: page.value, page_size: 10}); if (current === generation) {groups.value = r.items; total.value = r.total}} catch { /* API displays failure */ } finally {if (current === generation) loading.value = false}}
function edit(g?: UserGroup) {Object.assign(form, {id: g?.id || 0, name: g?.name || '', description: g?.description || '', member_ids: [...g?.member_ids || []], expected_version: g?.version || 0}); open.value = true}
async function save() {if (busy.value) return; busy.value = true; try {await adminApi.saveGroup(form.id, {...form}); open.value = false; await load(); ElMessage.success('用户组已保存')} catch { /* Keep edits on conflict */ } finally {busy.value = false}}
async function remove(g: UserGroup) {try {await ElMessageBox.confirm(`删除“${g.name}”会立即撤销通过该组授予的数据源访问权限。`, '删除用户组', {type: 'warning', confirmButtonText: '确认删除', cancelButtonText: '取消'}); await adminApi.deleteGroup(g.id, g.version); await load()} catch { /* Cancel or displayed API failure */ }}
onMounted(load)
</script>
