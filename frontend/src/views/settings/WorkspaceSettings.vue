<template>
  <section class="glass-card p-5 space-y-4" aria-label="工作空间管理">
    <h2 class="font-semibold">{{ user.workspace?.name }} · 工作空间成员</h2>
    <p class="text-xs text-white/65">
      角色只在当前工作空间生效。移出成员前须移交活动报表和仪表盘；共享数据源、驱动和历史记录继续属于该空间。
    </p>
    <p v-if="error" role="alert" class="text-sm text-rose-200">{{ error }}</p>
    <form class="flex flex-wrap gap-2" @submit.prevent="add">
      <el-input
        v-model="username"
        aria-label="添加现有用户到工作空间"
        placeholder="现有平台用户名"
        class="!w-52"
        :disabled="busy"
      /><el-select
        v-model="role"
        aria-label="新成员空间角色"
        class="!w-32"
        :disabled="busy"
        ><el-option label="管理员" value="admin" /><el-option
          label="开发者"
          value="developer" /><el-option
          label="只读"
          value="readonly" /></el-select
      ><button
        class="ghost-button text-xs"
        :disabled="busy || !username.trim()"
      >
        添加现有用户
      </button>
    </form>
    <div
      v-for="member in members"
      :key="member.id"
      class="flex flex-wrap gap-2 items-center border-t border-white/10 pt-3"
    >
      <span class="text-sm flex-1"
        >{{ member.username }}
        <span class="text-xs text-white/50"
          >#{{ member.id }} · {{ member.is_active ? '有效' : '已停用' }}</span
        ></span
      ><el-select
        :model-value="member.role"
        :aria-label="`${member.username}的空间角色`"
        class="!w-32"
        :disabled="busy"
        @change="update(member, { role: $event })"
        ><el-option label="管理员" value="admin" /><el-option
          label="开发者"
          value="developer" /><el-option
          label="只读"
          value="readonly" /></el-select
      ><button
        class="ghost-button text-xs"
        :disabled="busy"
        @click="update(member, { active: !member.is_active })"
      >
        {{ member.is_active ? '停用成员' : '恢复成员' }}
      </button>
    </div>
    <label class="text-xs flex items-start gap-2 pt-3 border-t border-white/10"
      ><input
        :checked="user.workspace?.allow_unknown_freshness"
        type="checkbox"
        :disabled="busy"
        @change="policy(($event.target as HTMLInputElement).checked)"
      />允许看板申请“未知数据时效”策略（仍需业务理由与独立审阅；关闭后立即阻断相关刷新）</label
    >
    <button
      v-if="user.platformAdmin"
      class="ghost-button text-xs"
      :disabled="busy"
      @click="create"
    >
      创建新工作空间（仅平台管理员）
    </button>
  </section>
</template>
<script setup lang="ts">
import { onMounted, onScopeDispose, ref } from 'vue'
import { ElMessageBox } from 'element-plus'
import { useUserStore } from '../../stores/user'
import request from '../../utils/request'
import { reportCommandKeys } from '../report/reportCommands'
const keys = reportCommandKeys(),
  user = useUserStore(),
  members = ref<Member[]>([]),
  username = ref(''),
  role = ref('readonly'),
  busy = ref(false),
  error = ref('')
let alive = true
interface Member {
  id: number
  username: string
  role: string
  is_active: boolean
  membership_version: number
}
const message = (e: unknown) =>
  e instanceof Error ? e.message : '成员操作失败，请刷新状态'
onMounted(load)
onScopeDispose(() => {
  alive = false
})
async function load() {
  try {
    const out = await request.get<unknown, { items: Member[] }>(
      `/workspaces/${user.workspaceId}/members`,
      { silentError: true },
    )
    if (alive) members.value = out.items
  } catch (e) {
    if (alive) error.value = message(e)
  }
}
async function add() {
  await mutate(() =>
    request.post(
      `/workspaces/${user.workspaceId}/members`,
      { username: username.value.trim(), role: role.value },
      { silentError: true },
    ),
  )
  username.value = ''
}
async function update(member: Member, change: Record<string, unknown>) {
  if (change.active === false) {
    try {
      await ElMessageBox.confirm(
        `停用「${member.username}」在本工作空间的成员资格？现有查询会失效；活动报表和看板需先移交。`,
        '确认停用成员',
      )
    } catch {
      return
    }
  }
  await mutate(() =>
    request.patch(
      `/workspaces/${user.workspaceId}/members/${member.id}`,
      { ...change, expected_version: member.membership_version },
      { silentError: true },
    ),
  )
}
async function mutate(fn: () => Promise<unknown>) {
  busy.value = true
  error.value = ''
  try {
    await fn()
    if (alive) {
      await user.loadWorkspaces(true)
      await load()
    }
  } catch (e) {
    if (alive) error.value = message(e)
  } finally {
    if (alive) busy.value = false
  }
}
async function create() {
  try {
    const result = await ElMessageBox.prompt(
      '新空间不会复制当前数据源、凭据、报表或成员。请输入组织名称。',
      '创建工作空间',
      { inputValidator: (v) => !!v?.trim() || '请填写名称' },
    )
    await mutate(() =>
      request.post(
        '/workspaces',
        {
          name: result.value,
          request_id: keys.get('workspace-create', { name: result.value }),
        },
        { silentError: true },
      ),
    )
  } catch {}
}
async function policy(allow_unknown_freshness: boolean) {
  await mutate(() =>
    request.patch(
      `/workspaces/${user.workspaceId}/policy`,
      { allow_unknown_freshness },
      { silentError: true },
    ),
  )
}
</script>
