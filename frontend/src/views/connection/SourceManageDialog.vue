<template>
  <el-dialog :model-value="modelValue" title="管理数据源" width="min(92vw, 640px)" class="glass-dialog source-manage-dialog" :close-on-click-modal="false" @update:model-value="emit('update:modelValue', $event)">
    <p class="text-sm text-white/60 mb-3">{{ source?.name }} · 资料和发布范围适用于此数据源的所有修订。</p>
    <el-tabs v-model="tab">
      <el-tab-pane label="目录资料" name="profile">
        <el-form label-position="top"><el-form-item label="分组"><el-input v-model="profile.category" maxlength="64" aria-label="目录分组" /></el-form-item><el-form-item label="标签（最多 10 个）"><el-select v-model="profile.tags" multiple filterable allow-create default-first-option class="w-full" :multiple-limit="10" aria-label="数据源标签" /></el-form-item><el-form-item label="负责人"><el-input v-model="profile.maintainer" maxlength="100" aria-label="数据源负责人" /></el-form-item><el-form-item label="联系方式"><el-input v-model="profile.contact" maxlength="200" aria-label="负责人联系方式" /></el-form-item></el-form>
      </el-tab-pane>
      <el-tab-pane v-if="source?.visibility === 'global'" label="发布范围" name="access">
        <div v-loading="accessLoading" class="space-y-4">
          <el-select v-model="access.mode" class="w-full" aria-label="发布范围"><el-option label="全体用户" value="global" /><el-option label="指定用户或用户组" value="restricted" /></el-select>
          <template v-if="access.mode === 'restricted'"><UserPicker v-model="access.user_ids" label="授权用户" /><el-select v-model="access.group_ids" multiple filterable remote :remote-method="searchGroups" class="w-full" aria-label="授权用户组" placeholder="搜索并选择用户组"><el-option v-for="g in groupOptions" :key="g.id" :label="g.name" :value="g.id" /></el-select><p class="text-xs text-white/50">用户直接授权或任一用户组授权均可访问。留空时仅管理员可见。</p></template>
          <p class="text-xs text-amber-200/80">缩小范围后，相关用户已有连接的后续操作也会被拦截。已发送的操作可能仍在执行；数据库账号权限由数据库管理。</p>
        </div>
      </el-tab-pane>
    </el-tabs>
    <template #footer><button class="liquid-button" :disabled="busy || accessLoading" @click="save">{{ busy ? '保存中…' : '保存设置' }}</button></template>
  </el-dialog>
</template>
<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import UserPicker from '../../components/UserPicker.vue'
import { adminApi, type UserGroup } from '../../api/admin'
import { sourceCatalogApi, type SourceDefinition } from '../../api/source-catalog'
const props = defineProps<{modelValue: boolean; source?: SourceDefinition}>()
const emit = defineEmits<{'update:modelValue': [value: boolean]; changed: []}>()
const tab = ref('profile'), busy = ref(false), accessLoading = ref(false), groups = ref<UserGroup[]>([])
const profile = reactive({category: '', tags: [] as string[], maintainer: '', contact: '', expected_version: 0})
const access = reactive({mode: 'global' as 'global' | 'restricted', user_ids: [] as number[], group_ids: [] as number[], expected_version: 0})
const names = new Map<number, string>()
const groupOptions = computed(() => [...access.group_ids.map(id => ({id, name: names.get(id) || `用户组 #${id}`})), ...groups.value.filter(g => !access.group_ids.includes(g.id))])
let groupGeneration = 0, loadGeneration = 0
async function searchGroups(keyword: string) {const current = ++groupGeneration; try {const r = await adminApi.groups({keyword, page: 1, page_size: 50}); if (current === groupGeneration) {groups.value = r.items; for (const g of r.items) names.set(g.id, g.name)}} catch { /* API displays failure */ }}
watch(() => [props.modelValue, props.source?.id], async () => {
  const current = ++loadGeneration, d = props.source; if (!props.modelValue || !d) return
  tab.value = 'profile'; Object.assign(profile, {category: d.category || '', tags: [...d.tags || []], maintainer: d.maintainer || '', contact: d.contact || '', expected_version: d.profile_version})
  if (d.visibility !== 'global') {accessLoading.value = false; return}
  accessLoading.value = true
  try {const a = await sourceCatalogApi.access(d.id); if (current === loadGeneration) Object.assign(access, {mode: a.mode, user_ids: a.user_ids, group_ids: a.group_ids, expected_version: a.version}); await searchGroups('')}
  catch {if (current === loadGeneration) emit('update:modelValue', false)} finally {if (current === loadGeneration) accessLoading.value = false}
})
async function save() {
  const d = props.source; if (!d || busy.value || accessLoading.value) return
  busy.value = true
  try {
    if (tab.value === 'profile') await sourceCatalogApi.profile(d.id, {...profile})
    else {await ElMessageBox.confirm('此发布范围会立即用于所有已发布修订和已有个人连接。', '确认发布范围', {confirmButtonText: '确认保存', cancelButtonText: '取消'}); await sourceCatalogApi.setAccess(d.id, {...access, user_ids: access.mode === 'global' ? [] : [...access.user_ids], group_ids: access.mode === 'global' ? [] : [...access.group_ids]})}
    emit('changed'); emit('update:modelValue', false); ElMessage.success('设置已保存')
  } catch { /* Keep data for refresh/review after a conflict */ } finally {busy.value = false}
}
</script>
<style scoped>
:global(.source-manage-dialog) {max-height: calc(100dvh - 32px); display: flex; flex-direction: column; margin-top: 16px !important;}
:global(.source-manage-dialog .el-dialog__body) {overflow-y: auto; min-height: 0;}
</style>
