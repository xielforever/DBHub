<template>
  <el-dialog
    :model-value="true"
    :title="dashboard ? '编辑仪表盘草稿' : '新建仪表盘'"
    width="min(980px,calc(100vw - 24px))"
    :close-on-click-modal="false"
    :show-close="!busy"
    :close-on-press-escape="!busy"
    @close="emit('close')"
  >
    <form class="space-y-4" @submit.prevent="save">
      <label class="block text-xs"
        >名称<el-input
          v-model="form.name"
          aria-label="仪表盘名称"
          maxlength="128"
          :disabled="busy"
      /></label>
      <label class="block text-xs"
        >说明<el-input
          v-model="form.description"
          aria-label="仪表盘说明"
          maxlength="512"
          :disabled="busy"
      /></label>
      <p class="text-xs text-white/65">
        卡片使用已发布报表。看板受众仅影响看板入口；每张必需报表还须明确覆盖相同受众。提交后由另一位有全部报表读取权限的管理员审阅。
      </p>
      <div class="grid sm:grid-cols-2 gap-3">
        <label class="text-xs"
          >个人受众<el-select
            v-model="form.user_ids"
            multiple
            filterable
            aria-label="仪表盘个人受众"
            class="w-full"
            :disabled="busy"
            ><el-option
              v-for="u in users"
              :key="u.id"
              :value="u.id"
              :label="u.username" /></el-select></label
        ><label class="text-xs"
          >用户组受众<el-select
            v-model="form.group_ids"
            multiple
            filterable
            aria-label="仪表盘用户组受众"
            class="w-full"
            :disabled="busy"
            ><el-option
              v-for="g in groups"
              :key="g.id"
              :value="g.id"
              :label="g.name" /></el-select
        ></label>
      </div>
      <div class="flex gap-2">
        <el-select
          v-model="selected"
          filterable
          placeholder="选择拥有普通查看权且已批准契约的报表"
          aria-label="添加报表卡片"
          class="flex-1"
          :disabled="busy || form.tiles.length >= 8"
          ><el-option
            v-for="report in available"
            :key="report.id"
            :label="report.name"
            :value="report.id" /></el-select
        ><button
          type="button"
          class="ghost-button"
          :disabled="!selected || busy || form.tiles.length >= 8"
          @click="add"
        >
          添加卡片
        </button>
      </div>
      <p v-if="!reports.length" class="text-sm text-amber-200">
        暂无可用报表。请先到报表中补充数据契约，完成审批发布，并授予作者和审阅人普通查看权。
      </p>
      <div
        v-for="(tile, index) in form.tiles"
        :key="tile.key"
        class="border border-white/15 rounded-xl p-3 space-y-2"
      >
        <div class="flex flex-wrap items-center gap-2">
          <strong class="text-sm flex-1"
            >{{ index + 1 }}. {{ reportName(tile.report_id) }}</strong
          ><button
            type="button"
            class="ghost-button text-xs"
            :disabled="busy || index === 0"
            :aria-label="`上移卡片${index + 1}`"
            @click="move(index, -1)"
          >
            上移</button
          ><button
            type="button"
            class="ghost-button text-xs"
            :disabled="busy || index === form.tiles.length - 1"
            :aria-label="`下移卡片${index + 1}`"
            @click="move(index, 1)"
          >
            下移</button
          ><button
            type="button"
            class="ghost-button text-xs"
            :disabled="busy"
            @click="form.tiles.splice(index, 1)"
          >
            移除
          </button>
        </div>
        <div class="grid sm:grid-cols-3 gap-3 items-center text-xs">
          <label class="flex gap-2"
            ><input
              v-model="tile.required"
              type="checkbox"
              :disabled="busy"
            />必需卡片</label
          ><label
            >宽度<el-select
              v-model="tile.width"
              :aria-label="`卡片${index + 1}宽度`"
              :disabled="busy"
              ><el-option label="整行" value="full" /><el-option
                label="半行"
                value="half" /><el-option
                label="三分之二"
                value="wide" /><el-option
                label="三分之一"
                value="narrow" /></el-select></label
          ><label
            >比较组（可选）<el-input
              v-model="tile.comparison_group"
              :aria-label="`卡片${index + 1}比较组`"
              :disabled="busy"
              maxlength="64"
          /></label>
        </div>
        <button
          v-if="dashboard"
          type="button"
          class="ghost-button text-xs"
          :disabled="busy || dirty"
          @click="accessTile = tile.key"
        >
          为已保存的拟受众申请报表权限
        </button>
        <p class="text-xs text-white/55">
          自动跟随口径兼容的当前正式发布 · 契约
          {{ tile.contract_hash.slice(0, 12) || '保存时由服务端选定' }}
        </p>
        <div
          v-if="contractChanged(tile)"
          class="text-xs text-amber-200 space-y-2"
        >
          <p>
            当前正式报表契约已变化。保存布局不会自动采纳新口径；请核对后明确采纳，再重新送审。
          </p>
          <button
            class="ghost-button text-xs"
            type="button"
            :disabled="busy || dirty"
            @click="adopt(tile)"
          >
            明确采纳当前口径</button
          ><span v-if="dirty"> 请先保存布局修改</span>
        </div>
      </div>
      <div class="space-y-2 text-xs">
        <label class="flex gap-2"
          ><input
            v-model="form.quality.allow_unknown_freshness"
            type="checkbox"
            :disabled="busy || !user.workspace?.allow_unknown_freshness"
          />允许未知时效（需空间策略许可及单独审阅）</label
        ><label v-if="form.quality.allow_unknown_freshness" class="block"
          >业务理由<el-input
            v-model="form.quality.reason"
            aria-label="允许未知时效的理由"
            :disabled="busy"
            maxlength="1000" /></label
        ><label
          >比较组最大水位差（秒）<el-input-number
            v-model="form.quality.max_watermark_skew_seconds"
            :min="0"
            :max="3600"
            aria-label="比较组最大水位差"
            :disabled="busy"
        /></label>
      </div>
      <p v-if="error" role="alert" class="text-sm text-rose-200">{{ error }}</p>
      <div class="flex justify-end gap-2">
        <button
          type="button"
          class="ghost-button"
          :disabled="busy"
          @click="emit('close')"
        >
          取消</button
        ><button class="liquid-button" :disabled="busy || !form.name.trim()">
          {{ busy ? '保存中…' : '保存草稿' }}
        </button>
      </div>
    </form>
    <DashboardAccessDialog
      v-if="accessTile && dashboard"
      :dashboard="{ ...dashboard, draft: form, draft_version: version }"
      :tile="accessTile"
      @close="accessTile = undefined"
      @submitted="
        () => {
          accessTile = undefined
          error = '申请已提交，请在仪表盘的访问申请队列查看进度'
        }
      "
    />
  </el-dialog>
</template>
<script setup lang="ts">
import { computed, onMounted, onScopeDispose, ref } from 'vue'
import { ElMessageBox } from 'element-plus'
import request from '../../utils/request'
import { useUserStore } from '../../stores/user'
import {
  managedDashboardApi as api,
  type DashboardDefinition,
  type ManagedDashboard,
  type DashboardReport,
  type DashboardTile,
} from '../../api/managed-dashboard'
import { newRequestId } from '../../utils/request-id'
import DashboardAccessDialog from './DashboardAccessDialog.vue'
import { reportCommandKeys } from '../report/reportCommands'
const props = defineProps<{ dashboard?: ManagedDashboard }>(),
  emit = defineEmits<{ close: []; saved: [] }>(),
  user = useUserStore(),
  keys = reportCommandKeys()
const form = ref<DashboardDefinition>(
  props.dashboard
    ? JSON.parse(JSON.stringify(props.dashboard.draft))
    : {
        name: '',
        description: '',
        tiles: [],
        user_ids: [],
        group_ids: [],
        quality: {
          allow_unknown_freshness: false,
          reason: '',
          max_watermark_skew_seconds: 300,
        },
      },
)
const accessTile = ref<string>()
const original = ref(JSON.stringify(form.value)),
  dirty = computed(() => JSON.stringify(form.value) !== original.value)
const version = ref(props.dashboard?.draft_version || 0),
  reports = ref<DashboardReport[]>([]),
  users = ref<{ id: number; username: string }[]>([]),
  groups = ref<{ id: number; name: string }[]>([]),
  selected = ref<number>(),
  busy = ref(false),
  error = ref('')
let alive = true
const available = computed(() =>
  reports.value.filter(
    (r) => !form.value.tiles.some((t) => t.report_id === r.id),
  ),
)
onMounted(async () => {
  try {
    const [r, u, g] = await Promise.all([
      api.catalog(),
      request.get<unknown, { items: { id: number; username: string }[] }>(
        '/users/brief',
      ),
      request.get<unknown, { items: { id: number; name: string }[] }>(
        '/audience-groups',
      ),
    ])
    if (alive) {
      reports.value = r.items
      users.value = u.items
      groups.value = g.items
    }
  } catch (e) {
    if (alive) error.value = message(e)
  }
})
onScopeDispose(() => {
  alive = false
})
const message = (e: unknown) =>
  e instanceof Error ? e.message : '操作失败，请刷新后核对'
const reportName = (id: number) =>
  reports.value.find((r) => r.id === id)?.name || `报表 #${id}（暂不可用）`
const contractChanged = (tile: DashboardTile) =>
  !!reports.value.find(
    (r) =>
      r.id === tile.report_id && r.data_contract.hash !== tile.contract_hash,
  )
function add() {
  const p = reports.value.find((r) => r.id === selected.value)
  if (!p) return
  form.value.tiles.push({
    key: newRequestId(),
    report_id: p.id,
    required: true,
    width: 'half',
    comparison_group: '',
    contract_hash: p.data_contract.hash,
    binding_policy: 'follow_published',
  })
  selected.value = undefined
}
function move(index: number, direction: number) {
  const tiles = form.value.tiles
  ;[tiles[index], tiles[index + direction]] = [
    tiles[index + direction]!,
    tiles[index]!,
  ]
}
async function save() {
  busy.value = true
  error.value = ''
  const payload = { version: version.value, definition: form.value }
  try {
    await api.save(
      props.dashboard?.id || 0,
      version.value,
      form.value,
      keys.get('save', payload),
    )
    if (alive) emit('saved')
  } catch (e) {
    if (alive) error.value = message(e)
  } finally {
    if (alive) busy.value = false
  }
}
async function adopt(tile: DashboardTile) {
  if (!props.dashboard) return
  const report = reports.value.find((r) => r.id === tile.report_id)
  if (!report) return
  try {
    await ElMessageBox.confirm(
      `采纳「${report.name}」当前正式发布 #${report.publication_no} 的新口径？此操作修改草稿，必须重新审批发布。`,
      '确认口径变化',
    )
    busy.value = true
    const payload = {
      version: version.value,
      report_id: report.id,
      publication_no: report.publication_no,
    }
    await api.command(
      props.dashboard.id,
      'adopt-contract',
      payload,
      keys.get('adopt', payload),
    )
    const next = await api.get(props.dashboard.id)
    if (alive) {
      form.value = next.draft
      version.value = next.draft_version
      original.value = JSON.stringify(next.draft)
    }
  } catch (e) {
    if (e instanceof Error && alive) error.value = message(e)
  } finally {
    if (alive) busy.value = false
  }
}
</script>
