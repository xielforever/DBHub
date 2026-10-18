<template>
  <div class="space-y-5">
    <DashboardViewer
      v-if="viewing"
      :key="`${viewing.id}:${viewing.mode}`"
      :id="viewing.id"
      :mode="viewing.mode"
      @close="viewing = undefined"
    />
    <template v-else>
      <header class="flex flex-wrap justify-between items-start gap-3">
        <div>
          <h2 class="text-2xl font-semibold">仪表盘</h2>
          <p class="text-sm text-white/60 mt-2">
            组合已审批报表，按工作空间管理、审阅和发布。
          </p>
        </div>
        <div class="flex gap-2">
          <button class="ghost-button" :disabled="loading" @click="load">
            刷新列表</button
          ><button
            v-if="canWrite"
            class="liquid-button"
            @click="creating = true"
          >
            新建仪表盘
          </button>
        </div>
      </header>
      <nav
        class="flex gap-2 border-b border-white/10 pb-3"
        aria-label="仪表盘分类"
      >
        <button
          class="ghost-button"
          :aria-pressed="tab === 'published'"
          @click="tab = 'published'"
        >
          已发布</button
        ><button
          v-if="canWrite"
          class="ghost-button"
          :aria-pressed="tab === 'managed'"
          @click="tab = 'managed'"
        >
          开发与审阅</button
        ><button
          class="ghost-button"
          :aria-pressed="tab === 'access'"
          @click="tab = 'access'"
        >
          访问申请
        </button>
      </nav>
      <form
        v-if="tab !== 'access'"
        class="flex flex-wrap gap-2"
        @submit.prevent="
          () => {
            publishedPage = 1
            managedPage = 1
            load()
          }
        "
      >
        <el-select
          v-if="tab === 'managed' && user.role === 'admin'"
          v-model="scope"
          aria-label="仪表盘开发范围"
          class="!w-40"
          @change="
            () => {
              managedPage = 1
              load()
            }
          "
          ><el-option label="全部开发看板" value="all" /><el-option
            label="我的开发"
            value="mine" /><el-option
            label="待审阅"
            value="pending" /></el-select
        ><input
          v-model="keyword"
          class="glass-input flex-1 min-w-0"
          aria-label="搜索仪表盘"
          placeholder="搜索名称或说明"
          maxlength="128"
        /><button class="ghost-button" :disabled="loading">搜索</button>
      </form>
      <p v-if="error" role="alert" class="text-rose-200 text-sm">{{ error }}</p>
      <p v-if="loading" role="status" class="text-white/60">读取中…</p>
      <AccessRequestsPanel v-if="tab === 'access'" />
      <div
        v-else-if="tab === 'published'"
        class="grid md:grid-cols-2 xl:grid-cols-3 gap-4"
      >
        <button
          v-for="d in published"
          :key="d.id"
          class="glass-panel p-5 text-left space-y-3 hover:border-violet-300/40"
          @click="viewing = { id: d.id, mode: 'published' }"
        >
          <h3 class="font-semibold">{{ d.name }}</h3>
          <p class="text-sm text-white/60">{{ d.description || '暂无说明' }}</p>
          <p class="text-xs text-violet-200">
            正式发布 #{{ d.publication_no }} · 打开看板 →
          </p>
        </button>
        <p
          v-if="!published.length && !loading"
          class="glass-panel p-8 text-white/60"
        >
          暂无获授权的正式仪表盘。开发者可组合报表并提交审阅，发布后在此查看。
        </p>
      </div>
      <div v-else class="space-y-3">
        <article
          v-for="d in managed"
          :key="d.id"
          class="glass-panel p-4 space-y-3"
        >
          <div class="flex flex-wrap justify-between gap-3">
            <h3 class="font-semibold">{{ d.draft.name }}</h3>
            <span class="text-xs text-violet-200">{{ stateLabel(d) }}</span>
          </div>
          <p v-if="d.draft.description" class="text-sm text-white/60">
            {{ d.draft.description }}
          </p>
          <p class="text-xs text-white/60">
            责任人 {{ d.owner_name }} · 草稿 {{ d.draft_version
            }}<span v-if="d.latest_version">
              · 内容 v{{ d.latest_version }}</span
            ><span v-if="d.active_release_id">
              · 正式发布 #{{ d.publication_no }}</span
            >
          </p>
          <div class="flex flex-wrap gap-2">
            <button class="liquid-button text-sm" @click="primaryAction(d)">
              {{ primaryLabel(d) }}
            </button>
            <button
              v-if="
                d.lifecycle === 'active' &&
                d.owner_id === user.user?.id &&
                d.review_status !== 'pending'
              "
              class="ghost-button text-sm"
              @click="submit(d)"
            >
              送审检查
            </button>
            <el-dropdown
              trigger="click"
                @command="moreAction($event, d)"
              ><button
                class="ghost-button text-sm"
                :aria-label="`${d.draft.name}更多操作`"
              >
                更多</button
              ><template #dropdown
                ><el-dropdown-menu>
                  <el-dropdown-item command="history" style="min-height: 44px"
                    >版本、审阅与发布</el-dropdown-item
                  >
                  <el-dropdown-item
                    v-if="
                      d.lifecycle === 'active' && d.owner_id === user.user?.id
                    "
                    command="preview"
                    style="min-height: 44px"
                    >预览已保存草稿</el-dropdown-item
                  >
                  <el-dropdown-item
                    v-if="
                      d.lifecycle === 'active' &&
                      d.owner_id === user.user?.id &&
                      primaryLabel(d) !== '编辑草稿'
                    "
                    command="edit"
                    style="min-height: 44px"
                    >编辑草稿</el-dropdown-item
                  >
                  <el-dropdown-item
                    v-if="d.lifecycle === 'active' && user.role === 'admin'"
                    command="transfer"
                    style="min-height: 44px"
                    >移交责任人</el-dropdown-item
                  >
                  <el-dropdown-item
                    v-if="d.lifecycle === 'active'"
                    command="archive"
                    divided
                    style="min-height: 44px"
                    >{{
                      d.latest_version ? '归档' : '删除草稿'
                    }}</el-dropdown-item
                  >
                  <el-dropdown-item
                    v-else
                    command="restore"
                    divided
                    style="min-height: 44px"
                    >恢复为草稿</el-dropdown-item
                  >
                </el-dropdown-menu></template
              ></el-dropdown
            >
          </div>
        </article>
        <p
          v-if="!managed.length && !loading"
          class="glass-panel p-8 text-white/60"
        >
          还没有开发中的看板。先新建草稿，再添加最多8张已发布且获批契约的报表。
        </p>
      </div>
      <div
        v-if="tab !== 'access'"
        class="flex justify-between gap-3 text-xs text-white/60"
      >
        <span
          >共
          {{ tab === 'published' ? publishedTotal : managedTotal }} 个看板</span
        >
        <div class="flex gap-2 items-center">
          <button
            class="ghost-button text-xs"
            :disabled="
              loading ||
              (tab === 'published' ? publishedPage : managedPage) <= 1
            "
            @click="changePage(-1)"
          >
            上一页</button
          ><span
            >第 {{ tab === 'published' ? publishedPage : managedPage }} 页</span
          ><button
            class="ghost-button text-xs"
            :disabled="
              loading ||
              (tab === 'published' ? publishedPage : managedPage) * 12 >=
                (tab === 'published' ? publishedTotal : managedTotal)
            "
            @click="changePage(1)"
          >
            下一页
          </button>
        </div>
      </div>
    </template>
    <DashboardEditor
      v-if="creating || editing"
      :dashboard="editing"
      @close="
        () => {
          creating = false
          editing = undefined
        }
      "
      @saved="
        () => {
          creating = false
          editing = undefined
          tab = 'managed'
          load()
        }
      "
    />
    <DashboardReviewDialog
      v-if="reviewing"
      :dashboard="reviewing"
      @close="reviewing = undefined"
      @changed="load"
    />
    <DashboardSubmitDialog
      v-if="pending"
      :dashboard="pending"
      @close="pending = undefined"
      @submitted="submitted"
      @edit="editFromCheck"
    />
  </div>
</template>
<script setup lang="ts">
import { computed, onMounted, onScopeDispose, ref } from 'vue'
import { ElMessageBox } from 'element-plus'
import {useRoute} from 'vue-router'
import { useUserStore } from '../../stores/user'
import {
  managedDashboardApi as api,
  type ManagedDashboard,
  type DashboardView,
} from '../../api/managed-dashboard'
import { reportCommandKeys } from '../report/reportCommands'
import DashboardViewer from './DashboardViewer.vue'
import DashboardEditor from './DashboardEditor.vue'
import DashboardReviewDialog from './DashboardReviewDialog.vue'
import AccessRequestsPanel from './AccessRequestsPanel.vue'
import DashboardSubmitDialog from './DashboardSubmitDialog.vue'
const user = useUserStore(),
  canWrite = computed(() => ['admin', 'developer'].includes(user.role)),
  keys = reportCommandKeys()
const keyword = ref(''),
  scope = ref('all'),
  publishedPage = ref(1),
  managedPage = ref(1),
  publishedTotal = ref(0),
  managedTotal = ref(0)
const route=useRoute()
const tab = ref(['managed','access'].includes(String(route.query.tab))?String(route.query.tab):'published'),
  published = ref<DashboardView[]>([]),
  managed = ref<ManagedDashboard[]>([]),
  creating = ref(false),
  editing = ref<ManagedDashboard>(),
  reviewing = ref<ManagedDashboard>(),
  viewing = ref<{ id: number; mode: 'published' | 'draft' }>(),
  loading = ref(false),
  error = ref(''),
  pending = ref<ManagedDashboard>()
let alive = true,
  loadGeneration = 0
onMounted(async()=>{await load();const id=Number(route.query.dashboard);if(alive&&Number.isSafeInteger(id)&&id>0){if(tab.value==='managed'&&canWrite.value){try{const d=await api.get(id);if(alive)reviewing.value=d}catch(e){if(alive)error.value=message(e)}}else if(tab.value==='published')viewing.value={id,mode:'published'}}})
onScopeDispose(() => {
  alive = false
  loadGeneration++
})
const message = (e: unknown) =>
  e instanceof Error ? e.message : '操作失败，请刷新后核对'
function changePage(change: number) {
  if (tab.value === 'published') publishedPage.value += change
  else managedPage.value += change
  void load()
}
async function load() {
  const generation = ++loadGeneration
  loading.value = true
  error.value = ''
  try {
    const [p, m] = await Promise.all([
      api.published({
        q: keyword.value,
        page: publishedPage.value,
        page_size: 12,
      }),
      canWrite.value
        ? api.list({
            q: keyword.value,
            scope: scope.value,
            page: managedPage.value,
            page_size: 12,
          })
        : Promise.resolve({ items: [], total: 0 }),
    ])
    if (alive && generation === loadGeneration) {
      published.value = p.items
      managed.value = m.items
      publishedTotal.value = p.total
      managedTotal.value = m.total
    }
  } catch (e) {
    if (alive && generation === loadGeneration) error.value = message(e)
  } finally {
    if (alive && generation === loadGeneration) loading.value = false
  }
}
function submit(d: ManagedDashboard) {
  pending.value = d
}
function submitted() {
  pending.value = undefined
  tab.value = 'managed'
  void load()
}
function editFromCheck(d: ManagedDashboard) {
  pending.value = undefined
  editing.value = d
}
function stateLabel(d: ManagedDashboard) {
  if (d.lifecycle === 'archived') return '已归档'
  if (d.review_status === 'pending') return '待审阅'
  if (d.review_status === 'approved' && !d.review_was_published)
    return '已批准 · 待发布'
  if (d.active_release_id) return '已发布'
  return (
    (
      {
        rejected: '已退回',
        withdrawn: '已撤回',
        expired: '审阅已过期',
      } as Record<string, string>
    )[d.review_status] || '草稿'
  )
}
function primaryLabel(d: ManagedDashboard) {
  if (d.lifecycle === 'archived') return '查看历史版本'
  if (d.review_status === 'pending')
    return user.role === 'admin' && d.reviewer_id === user.user?.id
      ? '审阅'
      : '查看审阅'
  if (d.review_status === 'approved' && !d.review_was_published)
    return user.role === 'admin' ? '审阅与发布' : '查看发布进度'
  return d.owner_id === user.user?.id ? '编辑草稿' : '查看版本'
}
function primaryAction(d: ManagedDashboard) {
  if (primaryLabel(d) === '编辑草稿') editing.value = d
  else reviewing.value = d
}
function moreAction(action: string, d: ManagedDashboard) {
  if (action === 'history') reviewing.value = d
  else if (action === 'preview') viewing.value = { id: d.id, mode: 'draft' }
  else if (action === 'edit') editing.value = d
  else if (action === 'transfer') void transfer(d)
  else if (action === 'archive')
    void lifecycle(d, d.latest_version ? 'archive' : 'delete')
  else if (action === 'restore') void lifecycle(d, 'restore')
}
async function lifecycle(d: ManagedDashboard, action: string) {
  try {
    await ElMessageBox.confirm(
      action === 'archive'
        ? '归档会立即下线、取消待审和访问申请，并使所有刷新上下文失效。'
        : action === 'restore'
          ? '恢复后为草稿，旧批准不能重新发布，需重新送审。'
          : '删除这个从未送审的草稿？',
      '确认操作',
    )
    const payload = { version: d.draft_version }
    await api.command(
      d.id,
      action,
      payload,
      keys.get(`${action}:${d.id}`, payload),
    )
    if (alive) await load()
  } catch (e) {
    if (e instanceof Error && alive) error.value = message(e)
  }
}
async function transfer(d: ManagedDashboard) {
  try {
    const input = await ElMessageBox.prompt(
      '输入本工作空间有效开发者或管理员的用户编号。归属移交不改变当前正式发布。',
      '移交看板责任人',
      { inputPattern: /^[1-9]\d*$/, inputErrorMessage: '请输入用户编号' },
    )
    const payload = { version: d.draft_version, owner_id: Number(input.value) }
    await api.command(
      d.id,
      'transfer',
      payload,
      keys.get(`transfer:${d.id}`, payload),
    )
    if (alive) await load()
  } catch (e) {
    if (e instanceof Error && alive) error.value = message(e)
  }
}
</script>

<style scoped>
button {
  min-height: 44px;
}
</style>
