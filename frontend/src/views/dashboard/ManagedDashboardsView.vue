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
            <span class="text-xs text-white/60"
              >{{ d.lifecycle === 'archived' ? '已归档' : '开发中' }} · 责任人
              {{ d.owner_name }} · 草稿 {{ d.draft_version }} · 内容 v{{
                d.latest_version
              }}
              · 发布 #{{ d.publication_no }}</span
            >
          </div>
          <p class="text-sm text-white/60">{{ d.draft.description }}</p>
          <div class="flex flex-wrap gap-2">
            <template v-if="d.lifecycle === 'active'"
              ><button
                v-if="d.owner_id === user.user?.id"
                class="ghost-button text-xs"
                @click="editing = d"
              >
                编辑草稿</button
              ><button
                v-if="d.owner_id === user.user?.id"
                class="ghost-button text-xs"
                @click="viewing = { id: d.id, mode: 'draft' }"
              >
                预览已保存草稿</button
              ><button
                v-if="d.owner_id === user.user?.id"
                class="liquid-button text-xs"
                @click="submit(d)"
              >
                提交审阅</button
              ><button class="ghost-button text-xs" @click="reviewing = d">
                版本、审阅与发布</button
              ><button
                v-if="user.role === 'admin'"
                class="ghost-button text-xs"
                @click="transfer(d)"
              >
                移交责任人</button
              ><button
                class="ghost-button text-xs"
                @click="lifecycle(d, d.latest_version ? 'archive' : 'delete')"
              >
                {{ d.latest_version ? '归档' : '删除草稿' }}
              </button></template
            ><template v-else
              ><button class="ghost-button text-xs" @click="reviewing = d">
                查看历史版本</button
              ><button
                class="ghost-button text-xs"
                @click="lifecycle(d, 'restore')"
              >
                恢复为草稿
              </button></template
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
    <el-dialog
      v-model="submitting"
      title="选择独立审阅人"
      width="min(480px,calc(100vw - 24px))"
      ><div class="space-y-4">
        <p class="text-xs text-white/65">
          需为另一位有效管理员，且具有所有引用报表的普通查看权。没有合格人选时，请先完成报表访问申请。
        </p>
        <el-select
          v-model="reviewer"
          aria-label="仪表盘提交审阅人"
          placeholder="选择管理员"
          class="w-full"
          ><el-option
            v-for="u in admins"
            :key="u.id"
            :value="u.id"
            :label="u.username"
        /></el-select>
        <p v-if="error" role="alert" class="text-rose-200 text-sm">
          {{ error }}
        </p>
        <button
          class="liquid-button"
          :disabled="!reviewer || loading"
          @click="confirmSubmit"
        >
          提交冻结版本
        </button>
      </div></el-dialog
    >
  </div>
</template>
<script setup lang="ts">
import { computed, onMounted, onScopeDispose, ref } from 'vue'
import { ElMessageBox } from 'element-plus'
import request from '../../utils/request'
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
const user = useUserStore(),
  canWrite = computed(() => ['admin', 'developer'].includes(user.role)),
  keys = reportCommandKeys()
const keyword = ref(''),
  scope = ref('all'),
  publishedPage = ref(1),
  managedPage = ref(1),
  publishedTotal = ref(0),
  managedTotal = ref(0)
const tab = ref('published'),
  published = ref<DashboardView[]>([]),
  managed = ref<ManagedDashboard[]>([]),
  creating = ref(false),
  editing = ref<ManagedDashboard>(),
  reviewing = ref<ManagedDashboard>(),
  viewing = ref<{ id: number; mode: 'published' | 'draft' }>(),
  loading = ref(false),
  error = ref(''),
  submitting = ref(false),
  pending = ref<ManagedDashboard>(),
  reviewer = ref<number>(),
  admins = ref<{ id: number; username: string }[]>([])
let alive = true,
  loadGeneration = 0
onMounted(load)
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
async function submit(d: ManagedDashboard) {
  pending.value = d
  reviewer.value = undefined
  submitting.value = true
  error.value = ''
  try {
    const u = await request.get<
      unknown,
      { items: { id: number; username: string; role: string }[] }
    >('/users/brief')
    if (alive)
      admins.value = u.items.filter(
        (x) => x.role === 'admin' && x.id !== user.user?.id,
      )
  } catch (e) {
    if (alive) error.value = message(e)
  }
}
async function confirmSubmit() {
  if (!pending.value || !reviewer.value) return
  loading.value = true
  try {
    const payload = {
      version: pending.value.draft_version,
      reviewer_id: reviewer.value,
    }
    await api.command(
      pending.value.id,
      'submit',
      payload,
      keys.get(`submit:${pending.value.id}`, payload),
    )
    if (alive) {
      submitting.value = false
      await load()
      tab.value = 'managed'
    }
  } catch (e) {
    if (alive) error.value = message(e)
  } finally {
    if (alive) loading.value = false
  }
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
