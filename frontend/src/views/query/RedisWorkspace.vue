<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { ElMessageBox } from 'element-plus'
import LoadingState from '../../components/base/LoadingState.vue'
import PageCategoryTabs from '../../components/base/PageCategoryTabs.vue'
import PageState from '../../components/base/PageState.vue'
import type { ConnectionItem } from '../../api/datasource'
import { workbenchApi, type RedisOverview, type RedisValue } from '../../api/workbench'
import { useUserStore } from '../../stores/user'
import { featureAllowed } from '../../utils/permission'
import { ApiRequestError } from '../../utils/request'
import { useReportRun } from '../report/useReportRun'

const props = defineProps<{ source: ConnectionItem }>()
const emit = defineEmits<{ blocked: [reason: string] }>()
const user = useUserStore()
const canWrite = computed(() => featureAllowed(user, 'query.write', ['admin', 'developer'].includes(user.role)))
const overview = useReportRun<RedisOverview>()
const directory = useReportRun<Awaited<ReturnType<typeof workbenchApi.redisKeys>>>()
const reading = useReportRun<RedisValue | null>()
const scanLimit = 200
const pattern = ref('*')
const appliedPattern = ref('*')
const typeFilter = ref('')
const selected = ref('')
const category = ref('value')
const tabs = [{ id: 'value', name: '值' }, { id: 'metadata', name: '元数据' }]
const busy = ref(false)
const refreshBlocked = computed(() => busy.value || reading.state.value.status === 'loading')
const confirming = ref(false)
const ttl = ref<number | string>(-1)
const createOpen = ref(false)
const newKey = ref('')
const newValue = ref('')
const writeError = ref('')
const receipt = ref('')
const keys = computed(() => directory.state.value.status === 'success' ? directory.state.value.data.items ?? [] : [])
const types = computed(() => [...new Set(keys.value.map(item => item.type))].sort())
const visibleKeys = computed(() => typeFilter.value ? keys.value.filter(item => item.type === typeFilter.value) : keys.value)
const detail = computed(() => reading.state.value.status === 'success' ? reading.state.value.data : undefined)
const keyMissing = computed(() => detail.value === null)
const valueText = computed(() => typeof detail.value?.value === 'string' ? detail.value.value : JSON.stringify(detail.value?.value, null, 2))
let active = true

watch([busy, createOpen, confirming], () => {
  emit('blocked', busy.value || createOpen.value || confirming.value ? '请先完成或关闭键值操作' : '')
}, { flush: 'sync' })

function ttlLabel(value: number) {
  if (value === -1) return '永久'
  if (value === -2) return '键已不存在'
  return value + ' 秒'
}

function loadOverview() {
  return overview.run('overview', signal => workbenchApi.redisOverview(props.source.id, { signal, silentError: true }))
}

function clearSelection() {
  selected.value = ''
  reading.reset()
  category.value = 'value'
}

function loadKeys() {
  if (directory.state.value.status === 'loading') return
  clearSelection()
  typeFilter.value = ''
  return directory.run(appliedPattern.value, signal => workbenchApi.redisKeys(props.source.id, appliedPattern.value, scanLimit, 'all', { signal, silentError: true }))
}

function search() {
  if (busy.value) return
  appliedPattern.value = pattern.value || '*'
  typeFilter.value = ''
  writeError.value = ''
  receipt.value = ''
  void loadKeys()
}

function changeType() {
  if (!visibleKeys.value.some(item => item.key === selected.value)) clearSelection()
}

async function inspect(key: string) {
  if (key !== selected.value) category.value = 'value'
  selected.value = key
  const loaded = await reading.run(key, async signal => {
    try {
      return await workbenchApi.redisValue(props.source.id, key, { signal, silentError: true })
    } catch (error) {
      // 来源不存在会有自己的错误码，只有键明确不存在时才从目录撤下。
      if (error instanceof ApiRequestError && error.code === 40401) return null
      throw error
    }
  })
  if (!loaded || selected.value !== key) return
  const current = detail.value
  if (current === undefined) return
  if (current) ttl.value = current.ttl
  if (directory.state.value.status !== 'success') return
  const data = directory.state.value.data
  const items = current
    ? keys.value.map(item => item.key === key ? { ...item, ttl: current.ttl, type: current.type } : item)
    : keys.value.filter(item => item.key !== key)
  directory.state.value = { status: 'success', data: { ...data, items } }
  if (typeFilter.value && (current ? typeFilter.value !== current.type : !types.value.includes(typeFilter.value))) {
    typeFilter.value = ''
  }
}

function selectKey(key: string) {
  if (busy.value) return
  writeError.value = ''
  receipt.value = ''
  void inspect(key)
}

function refreshKey() {
  if (refreshBlocked.value || !selected.value) return
  void inspect(selected.value)
}

async function updateTTL() {
  if (!canWrite.value || busy.value || !selected.value || !detail.value) return
  if (typeof ttl.value !== 'number' || !Number.isInteger(ttl.value) || ttl.value < -1) {
    writeError.value = 'TTL 必须为 -1 或非负整数。'
    return
  }
  const key = selected.value
  const nextTTL = ttl.value
  busy.value = true
  writeError.value = ''
  receipt.value = ''
  try {
    await workbenchApi.redisUpdateTTL(props.source.id, key, nextTTL)
    if (!active) return
    receipt.value = 'TTL 更新已确认。'
    await inspect(key)
  } catch (error) {
    if (active) writeError.value = error instanceof Error ? error.message : 'TTL 更新结果待核对。'
  } finally {
    busy.value = false
  }
}

async function remove() {
  if (!canWrite.value || busy.value || !selected.value || !detail.value) return
  const key = selected.value
  confirming.value = true
  try {
    await ElMessageBox.confirm('将永久删除键「' + key + '」。', '删除确认', { type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消' })
  } catch {
    return
  } finally {
    confirming.value = false
  }
  if (!active || busy.value || !canWrite.value || selected.value !== key) return
  busy.value = true
  writeError.value = ''
  receipt.value = ''
  try {
    const result = await workbenchApi.redisDeleteKey(props.source.id, key)
    if (!active) return
    receipt.value = result.deleted ? '键已删除。' : '服务端未删除任何键。'
    await loadKeys()
  } catch (error) {
    if (active) writeError.value = error instanceof Error ? error.message : '删除结果待核对。'
  } finally {
    busy.value = false
  }
}

async function create() {
  if (!canWrite.value || busy.value || !newKey.value) return
  const key = newKey.value
  busy.value = true
  writeError.value = ''
  receipt.value = ''
  try {
    await workbenchApi.redisCreateKey(props.source.id, { key, type: 'string', value: newValue.value, ttl: -1 })
    if (!active) return
    receipt.value = '键已创建。'
    createOpen.value = false
    newKey.value = ''
    newValue.value = ''
    await loadKeys()
    if (active && keys.value.some(item => item.key === key)) {
      typeFilter.value = ''
      await inspect(key)
    }
  } catch (error) {
    if (active) writeError.value = error instanceof Error ? error.message : '创建结果待核对。'
  } finally {
    busy.value = false
  }
}

onMounted(() => { void loadKeys(); void loadOverview() })
onBeforeUnmount(() => {
  active = false
  if (confirming.value) ElMessageBox.close()
})
</script>

<template>
  <section class="space-y-4 min-w-0" aria-label="Redis 工作台">
    <header class="flex flex-wrap items-start justify-between gap-3 border-b border-stroke pb-3">
      <div class="space-y-2 min-w-0">
        <h2 class="editorial-object-title">键值浏览</h2>
        <LoadingState v-if="overview.state.value.status === 'loading'" variant="inline" label="正在读取 Redis 服务信息…" />
        <p v-else-if="overview.state.value.status === 'success'" class="text-sm text-muted">
          Redis {{ overview.state.value.data.version }} · {{ overview.state.value.data.used_memory_mb }} MB · 实例共 {{ overview.state.value.data.total_keys }} 个键
        </p>
        <div v-else-if="overview.state.value.status === 'error'" class="flex flex-wrap items-center gap-2">
          <p class="text-sm text-danger" role="alert">{{ overview.state.value.message || '读取服务信息失败。' }}</p>
          <button class="ghost-button ui-control-target" @click="loadOverview">重试服务信息</button>
        </div>
      </div>
      <button v-if="canWrite" class="liquid-button ui-control-target" :disabled="busy" @click="writeError = ''; createOpen = true">新建字符串键</button>
    </header>

    <p v-if="receipt" class="text-sm text-success" role="status">{{ receipt }}</p>
    <p v-if="writeError && !createOpen" class="text-sm text-danger break-words" role="alert">{{ writeError }}</p>
    <div class="grid grid-cols-1 items-start gap-4 lg:grid-cols-[minmax(260px,1fr)_minmax(0,2fr)]">
      <section class="glass-panel ui-card-padding space-y-3 min-w-0" aria-labelledby="redis-directory-title">
        <h3 id="redis-directory-title" class="editorial-object-title">键目录</h3>
        <form class="flex flex-wrap items-end gap-2" @submit.prevent="search">
          <label class="flex-1 basis-32 min-w-0 text-sm">键名模式<input v-model="pattern" class="glass-input block w-full mt-1" placeholder="例如 orders:*" :disabled="busy" /></label>
          <button class="ghost-button ui-control-target" :disabled="busy || directory.state.value.status === 'loading'">搜索键</button>
        </form>
        <LoadingState v-if="directory.state.value.status === 'loading'" label="正在读取键目录…" />
        <PageState v-else-if="directory.state.value.status === 'error'" heading="h3" title="读取键目录失败" :description="directory.state.value.message || '请重新读取当前键名模式。'" tone="danger">
          <template #actions><button class="ghost-button ui-control-target" @click="loadKeys">重试键目录</button></template>
        </PageState>
        <template v-else-if="directory.state.value.status === 'success'">
          <label class="block text-sm">本次结果的类型<select v-model="typeFilter" class="glass-input block w-full mt-1" :disabled="busy" @change="changeType"><option value="">全部类型</option><option v-for="type in types" :key="type" :value="type">{{ type }}</option></select></label>
          <p class="text-xs text-muted">
            本次返回 {{ directory.state.value.data.returned }} 个键，上限 {{ scanLimit }} 个<span v-if="directory.state.value.data.returned !== keys.length">，当前目录 {{ keys.length }} 个</span><span v-if="typeFilter">，其中 {{ visibleKeys.length }} 个匹配当前类型</span>。
          </p>
          <ul v-if="visibleKeys.length" class="max-h-96 overflow-y-auto divide-y divide-divider">
            <li v-for="item in visibleKeys" :key="item.key">
              <button type="button" class="query-target-option" :aria-pressed="selected === item.key" :data-current="selected === item.key || undefined" :disabled="busy" @click="selectKey(item.key)">
                <span class="min-w-0 font-mono break-all">{{ item.key }}</span><span class="shrink-0 text-xs text-muted">{{ item.type }} · {{ ttlLabel(item.ttl) }}</span>
              </button>
            </li>
          </ul>
          <p v-else class="text-sm text-muted">{{ keys.length ? '本次返回的键中没有匹配类型。' : '当前键名模式没有返回可见键。' }}</p>
        </template>
      </section>

      <section class="glass-panel ui-card-padding space-y-3 min-w-0" aria-labelledby="redis-detail-title">
        <div class="flex flex-wrap items-start justify-between gap-2">
          <h3 id="redis-detail-title" class="editorial-object-title min-w-0 break-all">{{ selected || '键详情' }}</h3>
          <button v-if="selected" class="ghost-button ui-control-target aria-disabled:opacity-50" :aria-disabled="refreshBlocked || undefined" @click="refreshKey">刷新键详情</button>
        </div>
        <LoadingState v-if="reading.state.value.status === 'loading'" variant="detail" label="正在读取键值与 TTL…" />
        <PageState v-else-if="reading.state.value.status === 'error'" heading="h3" title="读取键详情失败" :description="reading.state.value.message || '请重新核对所选键。'" tone="danger" />
        <PageState v-else-if="keyMissing" heading="h3" title="键已不存在" description="此键已从当前目录撤下。" role="status" />
        <template v-else-if="detail">
          <p v-if="detail.truncated" class="text-sm text-muted" role="status">当前仅展示前 10,000 项，键值还有更多项。</p>
          <PageCategoryTabs label="键详情分类" :categories="tabs" :active-id="category" @select="category = $event">
            <pre v-if="category === 'value'" class="max-h-96 overflow-auto whitespace-pre-wrap break-all text-xs leading-6 p-3 bg-surface-muted" data-testid="redis-value">{{ valueText }}</pre>
            <dl v-else class="grid grid-cols-2 gap-3 p-3 text-sm sm:grid-cols-3">
              <div><dt class="text-muted">类型</dt><dd>{{ detail.type }}</dd></div>
              <div><dt class="text-muted">TTL</dt><dd>{{ ttlLabel(detail.ttl) }}</dd></div>
              <div><dt class="text-muted">内存占用</dt><dd>{{ detail.size }} 字节</dd></div>
            </dl>
          </PageCategoryTabs>
          <div v-if="canWrite" class="flex flex-wrap items-end gap-2 border-t border-stroke pt-3">
            <form class="flex flex-wrap items-end gap-2 min-w-0" @submit.prevent="updateTTL">
              <label class="text-sm min-w-0">TTL 秒数（-1 为永久）<input v-model.number="ttl" type="number" min="-1" class="glass-input block w-full mt-1" :disabled="busy" /></label>
              <button class="ghost-button ui-control-target" :disabled="busy">更新 TTL</button>
            </form>
            <button class="ghost-button text-danger ui-control-target" :disabled="busy" @click="remove">删除键</button>
          </div>
        </template>
        <p v-else class="text-sm text-muted">从键目录选择一个键。</p>
      </section>
    </div>

    <el-dialog v-model="createOpen" title="新建字符串键" width="min(92vw, 520px)" class="glass-dialog" :close-on-click-modal="false" :close-on-press-escape="!busy" :show-close="!busy">
      <div class="space-y-3">
        <label class="block text-sm">键名<input v-model="newKey" class="glass-input block w-full mt-1" :disabled="busy" /></label>
        <label class="block text-sm">值<textarea v-model="newValue" class="glass-input block w-full mt-1 min-h-24" :disabled="busy" /></label>
        <p class="text-xs text-muted">新键默认永久保存；已存在的键不会覆盖。</p>
        <p v-if="writeError" class="text-sm text-danger" role="alert">{{ writeError }}</p>
      </div>
      <template #footer><button class="liquid-button ui-control-target" :disabled="busy || !newKey" @click="create">{{ busy ? '创建中…' : '创建' }}</button></template>
    </el-dialog>
  </section>
</template>

<style scoped>
button[data-current] {
  background: var(--db-selected);
  border-left-color: var(--db-accent);
}
</style>
