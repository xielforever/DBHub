<script setup lang="ts">
import { computed, onMounted, onScopeDispose, ref, shallowRef } from 'vue'
import { platformManagementApi, type PlatformRoleTemplate } from '../../api/platform-management'
import LoadingState from '../../components/base/LoadingState.vue'
import PageState from '../../components/base/PageState.vue'
import { actionLabel } from '../../utils/iam-labels'

const roles = shallowRef<PlatformRoleTemplate[]>([])
const actions = computed(() => [...new Set(roles.value.flatMap(role => role.actions))])
const loading = ref(true)
const error = ref('')
let request: AbortController | undefined

async function load() {
  request?.abort()
  const read = new AbortController()
  request = read
  loading.value = true
  error.value = ''
  roles.value = []
  try {
    const result = await platformManagementApi.roleTemplates(read.signal)
    if (!read.signal.aborted) roles.value = result.items
  } catch (failure) {
    if (!read.signal.aborted) error.value = failure instanceof Error ? failure.message : '角色权限暂不可用。'
  } finally {
    if (!read.signal.aborted) loading.value = false
  }
}

onMounted(() => void load())
onScopeDispose(() => request?.abort())
</script>

<template>
  <section class="glass-panel ui-card-padding min-w-0 space-y-4" aria-label="全局角色权限">
    <h2 class="editorial-object-title">全局角色权限</h2>
    <LoadingState v-if="loading" label="正在读取共享角色…" />
    <PageState v-else-if="error" title="读取角色权限失败" :description="error" tone="danger">
      <template #actions><button type="button" class="ghost-button ui-control-target" @click="load">重试读取</button></template>
    </PageState>
    <PageState v-else-if="!roles.length" title="暂无共享角色" description="请刷新后核对平台角色目录。" />
    <template v-else>
      <p class="text-sm text-muted">此处查看矩阵空间共用的内置角色。有效角色的操作权限取并集，具体资源访问继续按对象授权核对。</p>
      <div class="editorial-table-wrap" role="region" tabindex="0" aria-label="共享角色权限矩阵">
        <table class="editorial-table min-w-[460px]">
          <thead><tr><th scope="col">页面与操作</th><th v-for="role in roles" :key="role.id" scope="col">{{ role.name }}</th></tr></thead>
          <tbody><tr v-for="action in actions" :key="action">
            <th scope="row">{{ actionLabel(action) }}</th>
            <td v-for="role in roles" :key="role.id">
              <span v-if="role.actions.includes(action)">允许</span><span v-else class="text-muted" aria-label="无许可">—</span>
            </td>
          </tr></tbody>
        </table>
      </div>
    </template>
  </section>
</template>
