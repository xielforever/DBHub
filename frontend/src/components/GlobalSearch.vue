<template>
  <div ref="wrapRef" class="relative w-full">
    <Search class="w-4 h-4 text-white/40 absolute left-3.5 top-1/2 -translate-y-1/2 pointer-events-none" />
    <input
      ref="inputRef"
      v-model.trim="keyword"
      type="search"
      class="glass-input pl-10 pr-8 py-2 text-sm w-full"
      placeholder="搜索数据源、数据表、字段、查询历史…"
      aria-label="全局搜索"
      @focus="onFocus"
      @keydown.down.prevent="move(1)"
      @keydown.up.prevent="move(-1)"
      @keydown.enter.prevent="onEnter"
      @keydown.esc="close"
    />
    <Loader2 v-if="loading" class="w-3.5 h-3.5 text-white/40 absolute right-3 top-1/2 -translate-y-1/2 animate-spin" />

    <!-- 分组结果弹层（暗色玻璃规范，展开态同样为毛玻璃） -->
    <Transition name="search-pop">
      <div
        v-if="open && (loading || hasResult || searched)"
        class="absolute z-50 mt-2 w-full min-w-[340px] max-h-[70vh] overflow-y-auto rounded-2xl
               border border-white/10 bg-slate-900/80 backdrop-blur-2xl shadow-2xl shadow-black/40 p-2"
      >
        <template v-if="!loading && !hasResult">
          <p class="px-3 py-6 text-center text-sm text-white/40">
            未找到与「{{ keyword }}」相关的资产
          </p>
        </template>

        <template v-for="group in groups" :key="group.key">
          <div v-if="group.items.length" :class="groupClass(group.key)">
            <p class="px-3 pt-2 pb-1 text-[11px] uppercase tracking-widest text-white/40 sticky top-0 bg-slate-900/70 backdrop-blur-xl">
              {{ group.label }}
            </p>
            <button
              v-for="(it, idx) in group.items"
              :key="group.key + idx"
              type="button"
              class="w-full text-left px-3 py-2 rounded-xl flex items-center gap-2.5 text-sm transition-colors"
              :class="activeIndex === flatIndex(group.key, idx) ? 'bg-white/10 text-white' : 'text-white/70 hover:bg-white/5'"
              @click="choose(group.key, it)"
              @mousemove="activeIndex = flatIndex(group.key, idx)"
            >
              <component :is="group.icon" class="w-4 h-4 shrink-0 text-white/45" />
              <span class="min-w-0 flex-1 truncate">
                <template v-if="group.key === 'connections'">
                  {{ (it as SearchConnectionHit).name }}
                  <span class="text-white/35 text-xs">· {{ envLabel((it as SearchConnectionHit).environment) }}</span>
                </template>
                <template v-else-if="group.key === 'tables'">
                  {{ (it as SearchTableHit).schema }}.{{ (it as SearchTableHit).name }}
                  <span class="text-white/35 text-xs">· {{ (it as SearchTableHit).connection_name }}</span>
                </template>
                <template v-else-if="group.key === 'columns'">
                  {{ (it as SearchColumnHit).table }}.{{ (it as SearchColumnHit).column }}
                  <span class="text-white/35 text-xs">· {{ (it as SearchColumnHit).data_type }}</span>
                </template>
                <template v-else-if="group.key === 'history'">
                  <span class="font-mono text-xs">{{ (it as SearchHistoryHit).sql_text }}</span>
                </template>
              </span>
              <span v-if="group.key === 'tables'" class="text-[10px] px-1.5 py-0.5 rounded bg-white/8 text-white/45 shrink-0">
                {{ (it as SearchTableHit).type === 'view' ? '视图' : '表' }}
              </span>
            </button>
          </div>
        </template>

        <div class="border-t border-white/10 mt-1 pt-1 px-3 py-1.5 text-[11px] text-white/35 flex items-center justify-between">
          <span>↑↓ 选择 · Enter 跳转</span>
          <span>Enter 查看资产目录</span>
        </div>
      </div>
    </Transition>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { Database, Loader2, Search, Table2, Columns3, History, Box } from 'lucide-vue-next'
import {
  assetApi,
  type SearchColumnHit,
  type SearchConnectionHit,
  type SearchGroups,
  type SearchHistoryHit,
  type SearchTableHit,
} from '../api/asset'
import type { EnvKind } from '../api/asset'

const router = useRouter()
const keyword = ref('')
const loading = ref(false)
const open = ref(false)
const searched = ref(false)
const result = ref<SearchGroups | null>(null)
const activeIndex = ref(0)
const wrapRef = ref<HTMLElement | null>(null)
const inputRef = ref<HTMLInputElement | null>(null)
let timer: ReturnType<typeof setTimeout> | null = null
let reqSeq = 0

const groups = computed(() => [
  { key: 'connections' as const, label: '数据源', icon: Database, items: result.value?.connections ?? [] },
  { key: 'tables' as const, label: '表 / 视图', icon: Table2, items: result.value?.tables ?? [] },
  { key: 'columns' as const, label: '字段', icon: Columns3, items: result.value?.columns ?? [] },
  { key: 'reports' as const, label: '报表', icon: Box, items: result.value?.reports ?? [] },
  { key: 'history' as const, label: '查询历史', icon: History, items: result.value?.history ?? [] },
])
const hasResult = computed(() => groups.value.some((g) => g.items.length > 0))

function flatIndex(key: string, idx: number): number {
  let n = 0
  for (const g of groups.value) {
    if (g.key === key) return n + idx
    n += g.items.length
  }
  return -1
}
const flatCount = computed(() => groups.value.reduce((n, g) => n + g.items.length, 0))

function envLabel(env: EnvKind): string {
  return { dev: '开发', test: '测试', prod: '生产' }[env]
}

function groupClass(key: string): string {
  return key === 'reports' ? 'hidden' : ''
}

watch(keyword, (val) => {
  if (timer) clearTimeout(timer)
  if (val.length < 2) {
    result.value = null
    searched.value = false
    open.value = val.length > 0
    return
  }
  open.value = true
  loading.value = true
  const seq = ++reqSeq
  timer = setTimeout(async () => {
    try {
      const data = await assetApi.search(val)
      if (seq === reqSeq) {
        result.value = data
        searched.value = true
        activeIndex.value = 0
      }
    } catch {
      if (seq === reqSeq) result.value = null
    } finally {
      if (seq === reqSeq) loading.value = false
    }
  }, 250)
})

function onFocus() {
  if (keyword.value.length >= 2) open.value = true
}
function close() {
  open.value = false
}
function move(delta: number) {
  if (!flatCount.value) return
  activeIndex.value = (activeIndex.value + delta + flatCount.value) % flatCount.value
}

function locate(): { key: string; item: unknown } | null {
  let n = 0
  for (const g of groups.value) {
    if (activeIndex.value < n + g.items.length) {
      return { key: g.key, item: g.items[activeIndex.value - n] }
    }
    n += g.items.length
  }
  return null
}

function choose(key: string, item: unknown) {
  jump(key, item)
}

function onEnter() {
  const cur = locate()
  if (cur) {
    jump(cur.key, cur.item)
  } else {
    // 无高亮项：进入资产目录并带上关键字
    router.push({ path: '/assets', query: { q: keyword.value } })
    close()
  }
}

function jump(key: string, item: unknown) {
  const q = keyword.value
  close()
  if (key === 'connections') {
    const it = item as SearchConnectionHit
    router.push({ path: '/assets', query: { q, connection_id: String(it.id) } })
  } else if (key === 'tables') {
    const it = item as SearchTableHit
    router.push({
      path: '/assets',
      query: {
        q,
        connection_id: String(it.connection_id),
        database: it.database,
        schema: it.schema,
        table: it.name,
      },
    })
  } else if (key === 'columns') {
    const it = item as SearchColumnHit
    router.push({
      path: '/assets',
      query: {
        q,
        connection_id: String(it.connection_id),
        database: it.database,
        schema: it.schema,
        table: it.table,
      },
    })
  } else if (key === 'history') {
    const it = item as SearchHistoryHit
    router.push({ path: '/query', query: { connection: String(it.connection_id) } })
  }
  nextTick(() => (keyword.value = q))
}

function onDocClick(e: MouseEvent) {
  if (wrapRef.value && !wrapRef.value.contains(e.target as Node)) close()
}
onMounted(() => document.addEventListener('click', onDocClick))
onBeforeUnmount(() => {
  document.removeEventListener('click', onDocClick)
  if (timer) clearTimeout(timer)
})

defineExpose({
  focus: () => inputRef.value?.focus(),
})
</script>

<style scoped>
.search-pop-enter-active,
.search-pop-leave-active {
  transition: opacity 0.15s ease, transform 0.15s ease;
}
.search-pop-enter-from,
.search-pop-leave-to {
  opacity: 0;
  transform: translateY(-4px);
}
</style>
