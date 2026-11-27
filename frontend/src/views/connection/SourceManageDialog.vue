<script setup lang="ts">
import { computed, onScopeDispose, reactive, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import UserPicker from '../../components/UserPicker.vue'
import LoadingState from '../../components/base/LoadingState.vue'
import PageState from '../../components/base/PageState.vue'
import { adminApi, type UserGroup } from '../../api/admin'
import { sourceCatalogApi, type SourceDefinition } from '../../api/source-catalog'
import SourceRevisionDialog from './SourceRevisionDialog.vue'

const props = defineProps<{ modelValue: boolean; source?: SourceDefinition; initialTab?: 'profile' | 'access' }>()
const emit = defineEmits<{
  'update:modelValue': [value: boolean]
  changed: [revision?: { id: number; enabled: boolean }]
  revise: [source: SourceDefinition]
  closed: []
}>()
const tab = ref('profile')
const busy = ref(false)
const accessLoading = ref(false)
const accessReady = ref(false)
const accessError = ref('')
const groups = ref<UserGroup[]>([])
const profile = reactive({ category: '', tags: [] as string[], maintainer: '', contact: '', expected_version: 0 })
const access = reactive({ mode: 'global' as 'global' | 'restricted', user_ids: [] as number[], group_ids: [] as number[], expected_version: 0 })
const names = new Map<number, string>()
const groupOptions = computed(() => [
  ...access.group_ids.map(id => ({ id, name: names.get(id) || `用户组 #${id}` })),
  ...groups.value.filter(group => !access.group_ids.includes(group.id)),
])
let groupGeneration = 0
let accessGeneration = 0

async function searchGroups(keyword: string) {
  const current = ++groupGeneration
  try {
    const result = await adminApi.groups({ keyword, page: 1, page_size: 50 })
    if (current !== groupGeneration || !props.modelValue) return
    groups.value = result.items
    for (const group of result.items) names.set(group.id, group.name)
  } catch { /* 接口提示搜索失败。 */ }
}

async function loadAccess() {
  const source = props.source
  if (!props.modelValue || !source) return
  const current = ++accessGeneration
  accessLoading.value = true
  accessReady.value = false
  accessError.value = ''
  try {
    const result = await sourceCatalogApi.access(source.id)
    if (current !== accessGeneration) return
    Object.assign(access, { mode: result.mode, user_ids: result.user_ids,
      group_ids: result.group_ids, expected_version: result.version })
    accessReady.value = true
    if (result.mode === 'restricted') void searchGroups('')
  } catch (error) {
    if (current === accessGeneration) accessError.value = error instanceof Error ? error.message : '请稍后重试。'
  } finally {
    if (current === accessGeneration) accessLoading.value = false
  }
}

watch([() => props.modelValue, () => props.source?.family_id], () => {
  accessGeneration++
  groupGeneration++
  accessReady.value = false
  accessLoading.value = false
  accessError.value = ''
  groups.value = []
  names.clear()
  if (!props.modelValue) return
  tab.value = props.initialTab || 'profile'
}, { immediate: true })
watch([() => props.modelValue, () => props.source?.family_id, () => props.source?.profile_version], () => {
  const source = props.source
  if (!props.modelValue || !source) return
  Object.assign(profile, { category: source.category || '', tags: [...source.tags || []],
    maintainer: source.maintainer || '', contact: source.contact || '', expected_version: source.profile_version })
}, { immediate: true })
watch([() => props.modelValue, () => props.source?.family_id, tab], () => {
  if (props.modelValue && tab.value === 'access' && !accessReady.value && !accessLoading.value) void loadAccess()
}, { immediate: true })
onScopeDispose(() => { accessGeneration++; groupGeneration++ })

async function save() {
  const source = props.source
  const section = tab.value
  if (!props.modelValue || !source || busy.value || section === 'access' && !accessReady.value) return
  busy.value = true
  try {
    if (section === 'profile') {
      await sourceCatalogApi.profile(source.id, { ...profile })
      profile.expected_version++
    } else if (section === 'access') {
      await ElMessageBox.confirm('保存后，新的发布范围会立即用于此数据源的所有已发布修订和已有个人连接。',
        '确认发布范围', { confirmButtonText: '确认保存', cancelButtonText: '取消' })
      if (!props.modelValue || props.source?.family_id !== source.family_id) return
      await sourceCatalogApi.setAccess(source.id, {
        ...access,
        user_ids: access.mode === 'global' ? [] : [...access.user_ids],
        group_ids: access.mode === 'global' ? [] : [...access.group_ids],
      })
      await loadAccess()
    }
    emit('changed')
    ElMessage.success('设置已保存')
  } catch { /* 取消或写入失败时保留表单。 */ }
  finally { busy.value = false }
}
</script>

<template>
  <el-dialog :model-value="modelValue" :title="source ? '管理 · ' + source.name : '管理数据源'" width="min(94vw, 760px)"
    append-to-body align-center class="glass-dialog source-manage-dialog" :close-on-click-modal="false"
    @update:model-value="emit('update:modelValue', $event)" @closed="emit('closed')">
    <template v-if="modelValue && source">
      <p class="flex flex-wrap gap-3 text-sm text-muted">
        <span>最新修订 {{ source.revision }}</span>
        <span :class="source.enabled ? 'text-success' : 'text-muted'">{{ source.enabled ? '已启用' : '已停用' }}</span>
      </p>
      <el-tabs v-model="tab" class="source-manage-tabs">
        <el-tab-pane label="基础配置" name="profile">
          <dl class="source-config-facts mb-4 text-sm">
            <dt>连接目标</dt><dd>{{ source.config.host }}{{ source.config.port ? ':' + source.config.port : '' }}</dd>
            <dt>默认数据库</dt><dd>{{ source.config.database || source.config.parameters?.database || '未设置' }}</dd>
            <dt>TLS</dt><dd>{{ source.config.ssl_mode === 'verify-full' ? '验证服务器证书' : '禁用' }}</dd>
            <dt>连接超时</dt><dd>{{ source.config.connection_timeout }} 秒</dd>
            <dt v-if="source.description">说明</dt><dd v-if="source.description">{{ source.description }}</dd>
          </dl>
          <el-form label-position="top" class="grid grid-cols-1 sm:grid-cols-2 gap-x-3">
            <el-form-item label="目录分组"><el-input v-model="profile.category" maxlength="64" /></el-form-item>
            <el-form-item label="标签（最多 10 个）"><el-select v-model="profile.tags" multiple filterable allow-create default-first-option class="w-full" :multiple-limit="10" aria-label="数据源标签" /></el-form-item>
            <el-form-item label="负责人"><el-input v-model="profile.maintainer" maxlength="100" /></el-form-item>
            <el-form-item label="联系方式"><el-input v-model="profile.contact" maxlength="200" /></el-form-item>
          </el-form>
        </el-tab-pane>
        <el-tab-pane label="发布范围" name="access">
          <LoadingState v-if="accessLoading" label="正在读取发布范围…" />
          <PageState v-else-if="accessError" title="发布范围读取失败" :description="accessError" tone="danger">
            <template #actions><button type="button" class="ghost-button ui-control-target" @click="loadAccess">重试</button></template>
          </PageState>
          <div v-else-if="accessReady" class="space-y-4">
            <el-select v-model="access.mode" class="w-full" aria-label="发布范围">
              <el-option label="空间内成员" value="global" />
              <el-option label="指定用户或用户组" value="restricted" />
            </el-select>
            <template v-if="access.mode === 'restricted'">
              <UserPicker v-model="access.user_ids" label="授权用户" />
              <el-select v-model="access.group_ids" multiple filterable remote :remote-method="searchGroups" class="w-full" aria-label="授权用户组" placeholder="搜索并选择用户组">
                <el-option v-for="group in groupOptions" :key="group.id" :label="group.name" :value="group.id" />
              </el-select>
              <p class="text-xs text-muted">用户直接授权或任一用户组授权均可访问。留空时仅管理员可见。</p>
            </template>
            <p class="text-sm text-muted">发布范围适用于此数据源的所有已发布修订和已有个人连接。数据库中的账号权限仍由数据库管理。</p>
          </div>
        </el-tab-pane>
        <el-tab-pane label="修订记录" name="revisions">
          <SourceRevisionDialog v-if="tab === 'revisions'" :source="source" @changed="emit('changed', $event)" />
        </el-tab-pane>
      </el-tabs>
    </template>
    <template #footer>
      <div class="flex flex-wrap justify-end gap-2">
        <button type="button" class="ghost-button ui-control-target" :disabled="busy" @click="emit('update:modelValue', false)">关闭</button>
        <button v-if="tab !== 'revisions'" type="button" class="ghost-button ui-control-target"
          :disabled="busy || tab === 'access' && (!accessReady || accessLoading)" @click="save">
          {{ busy ? '保存中…' : tab === 'access' ? '保存发布范围' : '保存目录资料' }}
        </button>
        <button v-if="source && tab !== 'access'" type="button" class="liquid-button ui-control-target" :disabled="busy"
          @click="emit('revise', source)">发布新修订</button>
      </div>
    </template>
  </el-dialog>
</template>

<style scoped>
:global(.source-manage-dialog) {
  height: fit-content;
  max-height: calc(100dvh - 32px);
  align-self: center;
  display: flex;
  flex-direction: column;
}
:global(.source-manage-dialog .el-dialog__body) { overflow-y: auto; min-height: 0; }
:global(.source-manage-dialog .el-dialog__header), :global(.source-manage-dialog .el-dialog__footer) { flex-shrink: 0; }
.source-config-facts { display: grid; grid-template-columns: max-content minmax(0, 1fr); gap: 8px 14px; }
.source-config-facts dt { color: var(--db-text-secondary); }
.source-config-facts dd { overflow-wrap: anywhere; }
.source-manage-tabs :deep(.el-tabs__item) { padding-inline: 10px; }
</style>
