<script setup lang="ts">
import { computed, onScopeDispose, ref, watch } from 'vue'
import LoadingState from '../../components/base/LoadingState.vue'
import PageCategoryTabs from '../../components/base/PageCategoryTabs.vue'
import PageHeader from '../../components/base/PageHeader.vue'
import PageState from '../../components/base/PageState.vue'
import { usePageCategory } from '../../composables/usePageCategory'
import { useUserStore } from '../../stores/user'
import { iamApi } from '../../api/iam'
import type { Proposal } from '../../types/iam'
import { changeLabels, stateLabels } from '../../utils/iam-labels'
import { workspaceGovernanceActions } from '../../utils/permission'
import InboxView from '../inbox/InboxView.vue'
import SourceTasksView from '../connection/SourceTasksView.vue'
const user = useUserStore()
const defaultId = computed(() => {
  const worksOnGovernance = ['space.roles.manage', 'space.assign.manage', 'space.members.exit'].some(user.can)
  const reviewsBusiness = ['report.review', 'dashboard.review', 'operation.review'].some(user.can)
  return worksOnGovernance && !reviewsBusiness ? 'governance' : 'business'
})
const tabs = computed(() => {
  const items = [
    { id: 'business', name: '业务审批与通知' },
    { id: 'governance', name: '治理申请' },
  ]
  if (user.can('connection.task.view')) items.push({ id: 'source-tasks', name: '我的连接任务' })
  return items
})
const { category, selectCategory } = usePageCategory(tabs, { defaultId, aliases: { notifications: 'business' } })

const proposals = ref<Proposal[]>([])
const loading = ref(false)
const error = ref('')
const canGovern = computed(() => workspaceGovernanceActions.some(user.can))
let generation = 0

function canOpenOriginalProposal(proposal: Proposal) {
  return user.can('space.page') && (canGovern.value ||
    (proposal.patch.kind === 'bundle_activate' && user.effective?.isolated_migration?.review === true))
}

async function load() {
  if (loading.value) return
  const request = ++generation
  loading.value = true
  error.value = ''
  proposals.value = []
  try {
    const result = await iamApi.read<Proposal[]>(canGovern.value ? 'proposals' : 'own-proposals')
    if (request === generation) proposals.value = result
  } catch (e) {
    if (request === generation) error.value = e instanceof Error ? e.message : '申请暂不可用'
  } finally {
    if (request === generation) loading.value = false
  }
}

watch(() => category.value?.id, value => {
  if (value === 'governance') {
    void load()
    return
  }
  generation++
  loading.value = false
  error.value = ''
  proposals.value = []
}, { immediate: true })
onScopeDispose(() => { generation++ })
</script>

<template>
  <section class="ui-page-stack">
    <PageHeader title="待办中心" description="集中处理需要审批或关注的事项。标记已读不会完成审批。" />
    <PageCategoryTabs label="待办分类" :categories="tabs" :active-id="category?.id" @select="selectCategory">
      <InboxView v-if="category?.id === 'business'" :key="$route.query.tab === 'notifications' ? 'notifications' : 'business'" embedded />
      <SourceTasksView v-else-if="category?.id === 'source-tasks'" embedded />
      <section v-else-if="category?.id === 'governance'" class="space-y-3">
        <header class="flex flex-wrap items-start justify-between gap-3">
          <h2 class="text-lg font-semibold">治理申请与审批</h2>
          <button type="button" class="ghost-button ui-control-target aria-disabled:opacity-50"
            :aria-disabled="loading || undefined" :aria-busy="loading" @click="load">刷新</button>
        </header>
        <LoadingState v-if="loading" label="正在读取治理申请…" />
        <p v-else-if="error" role="alert" class="text-danger">{{ error }}</p>
        <p v-else-if="!proposals.length" class="glass-card p-8 text-muted">暂无当前可见的治理申请。</p>
        <article v-for="p in proposals" :key="p.id" class="glass-card space-y-3 ui-card-padding">
          <h3>{{ changeLabels[p.patch.kind] }} · {{ stateLabels[p.state] || p.state }}</h3>
          <p class="text-sm text-muted">{{ p.requester_id === user.user?.id ? '我提交的变更' : '与我相关的变更' }} · 原策略与请求编号保留，不自动重发。</p>
          <RouterLink v-if="canOpenOriginalProposal(p)" :to="{ path: '/space', query: { tab: 'governance', workspace_id: user.workspaceId } }"
            class="ghost-button inline-block text-sm">查看原空间治理申请</RouterLink>
        </article>
      </section>
      <template #unavailable>
        <PageState title="此待办分类不可用" description="请选择当前可用的分类。" />
      </template>
    </PageCategoryTabs>
  </section>
</template>
