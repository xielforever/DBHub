<script setup lang="ts">
import { computed, nextTick, onScopeDispose, ref, watch } from 'vue'
import { useRoute, useRouter, type RouteLocationRaw } from 'vue-router'
import LoadingState from '../../components/base/LoadingState.vue'
import PageCategoryTabs from '../../components/base/PageCategoryTabs.vue'
import PageHeader from '../../components/base/PageHeader.vue'
import PageState from '../../components/base/PageState.vue'
import { handlingReturnState, useRestoredHandlingList } from '../../composables/useHandlingReturn'
import { usePageCategory } from '../../composables/usePageCategory'
import { useUserStore } from '../../stores/user'
import { iamApi } from '../../api/iam'
import type { Proposal } from '../../types/iam'
import { changeLabels, stateLabels } from '../../utils/iam-labels'
import { workspaceGovernanceActions } from '../../utils/permission'
import InboxView from '../inbox/InboxView.vue'
import SourceTasksView from '../connection/SourceTasksView.vue'
const props = defineProps<{ embedded?: boolean }>()
const user = useUserStore()
const route = useRoute()
const router = useRouter()
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
const sourceOptions = computed(() => [
  { id: 'business', name: '待我处理的业务审批' },
  { id: 'notifications', name: '通知' },
  ...tabs.value.filter(item => item.id !== 'business'),
])
const selectedSource = computed(() => {
  if (category.value?.id === 'business' && route.query.tab === 'notifications') return 'notifications'
  return category.value?.id
})

async function selectSource(event: Event) {
  const id = (event.currentTarget as HTMLSelectElement).value
  if (id === selectedSource.value || !sourceOptions.value.some(item => item.id === id)) return
  await router.push({ path: route.path, query: { ...route.query, tab: id }, hash: route.hash })
}

const proposals = ref<Proposal[]>([])
const loading = ref(false)
const error = ref('')
const canGovern = computed(() => workspaceGovernanceActions.some(user.can))
const returned = useRestoredHandlingList()
const governanceRegion = ref<HTMLElement>()
let generation = 0
let restoredProposalId: string | undefined

function canOpenOriginalProposal(proposal: Proposal) {
  return user.can('space.page') && (canGovern.value ||
    (proposal.patch.kind === 'bundle_activate' && user.effective?.isolated_migration?.review === true))
}

function proposalLocation(proposal: Proposal): RouteLocationRaw {
  const userId = user.user?.id
  const workspaceId = user.workspaceId
  return {
    path: '/space', query: { tab: 'governance', workspace_id: String(workspaceId), proposal_id: proposal.id },
    ...(props.embedded && userId ? {
      state: handlingReturnState({ kind: 'governance', userId, workspaceId, proposalId: proposal.id }),
    } : {}),
  }
}

async function restoreProposalFocus(request: number) {
  const origin = props.embedded && returned.value
  if (!origin || origin.kind !== 'governance' || origin.proposalId === restoredProposalId) return
  await nextTick()
  if (request !== generation || category.value?.id !== 'governance') return
  restoredProposalId = origin.proposalId
  const originalLink = governanceRegion.value?.querySelector<HTMLElement>(`[data-proposal-id="${origin.proposalId}"]`)
  const target = originalLink ?? governanceRegion.value
  target?.focus()
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
    if (request === generation) {
      loading.value = false
      void restoreProposalFocus(request)
    }
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
    <PageHeader v-if="!embedded" title="待办中心" description="集中处理需要审批或关注的事项。标记已读不会完成审批。" />
    <label v-else class="ui-filter-field max-w-xs">
      <span>事项来源</span>
      <select class="glass-input ui-control-target" :value="selectedSource ?? ''" @change="selectSource">
        <option v-if="!selectedSource" value="" disabled>请选择事项来源</option>
        <option v-for="source in sourceOptions" :key="source.id" :value="source.id">{{ source.name }}</option>
      </select>
    </label>
    <PageCategoryTabs label="待办分类" :categories="tabs" :active-id="category?.id" :hide-tabs="embedded" @select="selectCategory">
      <InboxView v-if="category?.id === 'business'" :key="selectedSource" embedded :hide-category-tabs="embedded" />
      <SourceTasksView v-else-if="category?.id === 'source-tasks'" embedded />
      <section v-else-if="category?.id === 'governance'" ref="governanceRegion"
        class="space-y-3 ui-focus-target" aria-label="治理申请与审批" tabindex="-1">
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
          <RouterLink v-if="canOpenOriginalProposal(p)" :to="proposalLocation(p)" :data-proposal-id="p.id"
            class="ghost-button inline-block text-sm">查看原空间治理申请</RouterLink>
        </article>
      </section>
      <template #unavailable>
        <PageState title="此待办分类不可用" description="请选择当前可用的分类。" />
      </template>
    </PageCategoryTabs>
  </section>
</template>
