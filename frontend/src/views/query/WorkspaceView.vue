<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import { isNavigationFailure, NavigationFailureType, onBeforeRouteUpdate, useRoute, useRouter } from 'vue-router'
import { ChevronDown } from 'lucide-vue-next'
import PageHeader from '../../components/base/PageHeader.vue'
import { connectionApi } from '../../api/datasource'
import { workbenchApi } from '../../api/workbench'
import { useUserStore } from '../../stores/user'
import SqlWorkspace from './SqlWorkspace.vue'
import MetadataWorkspace from './MetadataWorkspace.vue'
import RedisWorkspace from './RedisWorkspace.vue'
import QueryTargetPicker from './components/QueryTargetPicker.vue'
import { environmentLabel, targetKind, useQueryTarget, type QueryTarget } from './useQueryTarget'

const route = useRoute(), router = useRouter(), user = useUserStore()
const sqlWorkspace = ref<InstanceType<typeof SqlWorkspace>>()
const trigger = ref<HTMLButtonElement>()
const blocked = ref(''), discardConfirmed = ref(false), securityGeneration = ref(0)
const routeTargetError = ref('')
const context = computed(() => `${user.user?.id ?? ''}:${user.identityEpoch()}:${user.permissionKey}`)
let ownNavigation = false
async function navigate(target: QueryTarget) {
  ownNavigation = true
  try {
    const failure = await router.replace({ path: '/query', query: { ...route.query, connection: String(target.source.id), database: target.database || undefined } })
    if (isNavigationFailure(failure) && !isNavigationFailure(failure, NavigationFailureType.duplicated)) throw new Error('目标地址切换未完成，请重试')
    routeTargetError.value = ''
  } finally { ownNavigation = false }
}
const picker = useQueryTarget({
  sources: async signal => (await connectionApi.list(undefined, { signal, silentError: true })).items,
  databases: async (id, signal) => (await workbenchApi.databases(id, signal, true)).items.map(item => item.name),
  context: () => context.value,
  blocked: () => blocked.value || (discardRequired.value && !discardConfirmed.value ? '请先确认已保存 SQL 并同意切换工作台类型' : ''),
  navigate,
})
const { current, draft, draftSource, sources, databases, open, sourceLoading, databaseLoading, submitting, sourceError, databaseError, submitError, disabledReason } = picker
const kind = computed(() => current.value ? targetKind(current.value.source) : '')
const discardRequired = computed(() => !!sqlWorkspace.value?.hasDraft && !!draft.value && targetKind(draft.value.source) !== 'sql')
const targetLabel = computed(() => current.value ? `${current.value.source.name} / ${kind.value === 'keys' ? current.value.database || '配置的逻辑库' : current.value.database}` : '选择查询目标')
const policySupported = computed(() => !!sqlWorkspace.value?.policySupported)
watch(() => [draft.value?.source.id, draft.value?.database], () => { discardConfirmed.value = false })
watch(open, async value => { if (!value) { await nextTick(); trigger.value?.focus() } })
watch(() => current.value && targetKind(current.value.source), () => { blocked.value = '' }, { flush: 'sync' })

async function loadRouteTarget() {
  const requestedContext = context.value
  const requestedRoute = route.fullPath
  const raw = route.query.connection
  const id = typeof raw === 'string' && /^[1-9][0-9]*$/.test(raw) && Number.isSafeInteger(Number(raw)) ? Number(raw) : undefined
  routeTargetError.value = ''
  await picker.loadSources()
  if (requestedContext !== context.value || requestedRoute !== route.fullPath || sourceLoading.value || sourceError.value) return
  if (raw !== undefined && (id === undefined || !sources.value.some(source => source.id === id))) {
    routeTargetError.value = '指定数据源不可用，请重新选择。'
    return
  }
  if (id !== undefined) {
    const database = typeof route.query.database === 'string' ? route.query.database : ''
    await picker.show(id, database)
    if (requestedContext !== context.value || requestedRoute !== route.fullPath) return
    // 只有首次完整链接且数据库明确有效时恢复目标；其他情况须显式确认。
    if (!current.value && database && draft.value?.database === database) await picker.confirm()
  }
}
async function closePicker(value: boolean) {
  if (value || submitting.value) return
  picker.close()
  if (current.value) {
    try { await navigate(current.value) } catch { /* 当前正式目标仍保持；下次确认可重试地址更新。 */ }
  }
}
onBeforeRouteUpdate((to, from) => {
  if (!ownNavigation && (to.query.connection !== from.query.connection || to.query.database !== from.query.database) && (blocked.value || submitting.value)) return false
})
watch(() => [route.query.connection, route.query.database], () => {
  if (!ownNavigation && !submitting.value) void loadRouteTarget()
})
watch(context, () => {
  picker.invalidate(); securityGeneration.value++; blocked.value = ''; discardConfirmed.value = false
  routeTargetError.value = ''
  void picker.loadSources()
}, { flush: 'sync' })
onMounted(() => { void loadRouteTarget() })
</script>

<template>
  <div class="space-y-4">
    <PageHeader title="数据工作台" eyebrow="开发与连接" description="选择查询目标，完成查询与数据检查。" />
    <section class="query-target-bar" aria-label="查询目标">
      <span class="text-sm shrink-0">查询目标</span>
      <button ref="trigger" type="button" class="ghost-button query-target-trigger ui-control-target" :title="targetLabel" :aria-label="`选择查询目标，当前${targetLabel}`" aria-haspopup="dialog" :aria-expanded="open" :disabled="!!blocked || submitting" @click="picker.show()"><span class="truncate">{{ targetLabel }}</span><ChevronDown :size="16" class="shrink-0" aria-hidden="true" /></button>
      <span v-if="current" class="text-xs shrink-0" :class="current.source.environment === 'prod' ? 'text-danger' : 'text-muted'">{{ environmentLabel(current.source) }}</span>
      <div v-if="kind === 'sql'" class="query-target-actions">
        <button class="ghost-button text-xs ui-control-target" :disabled="!!blocked || open" @click="sqlWorkspace?.openSettings()">查询限制</button>
        <button v-if="policySupported" class="ghost-button text-xs ui-control-target" :disabled="!!blocked || open" @click="sqlWorkspace?.openPolicies()">{{ user.hasCapability('datasource_manage') ? '行列访问策略' : '查看访问策略' }}</button>
        <span v-if="sqlWorkspace?.settings" class="text-xs text-muted">{{ sqlWorkspace.settings.timeout_ms / 1000 }} 秒 · 最多 {{ sqlWorkspace.settings.max_rows }} 行</span>
      </div>
      <p v-if="blocked" role="status" class="query-target-note text-xs text-muted">{{ blocked }}</p>
      <p v-if="sqlWorkspace?.settingsError" role="alert" class="query-target-note text-xs text-warning">{{ sqlWorkspace.settingsError }}；查询将使用服务端保存的限制。</p>
    </section>
    <section v-if="!current" class="glass-panel p-6 space-y-3" aria-live="polite">
      <p v-if="sourceLoading">正在加载获准数据源…</p>
      <template v-else-if="sourceError"><p role="alert" class="text-danger">{{ sourceError }}</p><button class="ghost-button" @click="loadRouteTarget">重新加载数据源</button></template>
      <p v-else-if="routeTargetError" role="alert" class="text-danger">{{ routeTargetError }}</p>
      <p v-else>{{ sources.length ? '选择数据源和数据库，确认后进入工作台。' : '当前范围没有可用数据源。' }}</p>
    </section>
    <SqlWorkspace v-if="current && kind === 'sql'" :key="`sql-${securityGeneration}`" ref="sqlWorkspace" :source="current.source" :database="current.database" :locked="open || submitting" @blocked="blocked = $event" />
    <RedisWorkspace v-else-if="current && kind === 'keys'" :key="`${securityGeneration}-${current.source.id}`" :source="current.source" @blocked="blocked = $event" />
    <MetadataWorkspace v-else-if="current && kind === 'metadata'" :key="`${securityGeneration}-${current.source.id}-${current.database}`" :source="current.source" :database="current.database" @blocked="blocked = $event" />
    <QueryTargetPicker :open="open" :current-target="current" :draft-target="draft" :sources="sources" :databases="databases" :source-loading="sourceLoading" :database-loading="databaseLoading" :source-error="sourceError" :database-error="databaseError" :submit-error="submitError" :submitting="submitting" :disabled-reason="disabledReason" :discard-required="discardRequired" :discard-confirmed="discardConfirmed" @update:open="closePicker" @select-source="picker.chooseSource($event)" @select-database="picker.chooseDatabase" @retry-sources="picker.loadSources" @retry-databases="draftSource && picker.chooseSource(draftSource.id)" @confirm="picker.confirm" @update:discard-confirmed="discardConfirmed = $event" />
  </div>
</template>
