<script setup lang="ts">
import { computed, nextTick, onMounted, onScopeDispose, ref } from 'vue'
import request, { ApiRequestError } from '../../utils/request'
import { newRequestId } from '../../utils/request-id'
import { useUserStore } from '../../stores/user'
import { stateLabels } from '../../utils/iam-labels'
import PageHeader from '../../components/base/PageHeader.vue'
import PageState from '../../components/base/PageState.vue'
import LoadingState from '../../components/base/LoadingState.vue'

interface Recovery {
  id: string
  requester_id: number
  target_id: number
  reason: string
  state: string
  expires_at: string
  verified_count: number
  self_verified: boolean
}
interface Space { workspace_id: number; name: string; requests: Recovery[] }
interface Application { request_id: string; workspace_id: number; target_id: number; reason: string }

const user = useUserStore()
const spaces = ref<Space[]>([]), workspaceId = ref<number>(), targetId = ref<number>(), reason = ref('')
const loading = ref(true), busy = ref(false), readError = ref(''), commandError = ref(''), message = ref('')
const pending = ref<Application>(), reconciled = ref(false)
const selectedId = ref<string>(), verificationOpen = ref(false), password = ref(''), code = ref('')
const verificationError = ref(''), passwordInput = ref<HTMLInputElement>()
const now = ref(Date.now())
const space = computed(() => spaces.value.find(item => item.workspace_id === workspaceId.value))
const selected = computed(() => space.value?.requests.find(item => item.id === selectedId.value))
const canSubmit = computed(() => !loading.value && !readError.value && !busy.value && !pending.value && !!space.value &&
  typeof targetId.value === 'number' && Number.isSafeInteger(targetId.value) && targetId.value > 0 && !!reason.value.trim())
const formatTime = (value: string) => new Date(value).toLocaleString('zh-CN')
const errorText = (error: unknown, fallback: string) => error instanceof Error ? error.message : fallback
const statuses: Record<string, string> = { ...stateLabels, pending: '待核验', applied: '已完成双人核验' }
let active = true
let readController: AbortController | undefined
let clock: ReturnType<typeof setInterval> | undefined

function state(item: Recovery) {
  return item.state === 'pending' && Date.parse(item.expires_at) <= now.value ? 'expired' : item.state
}
function canVerify(item: Recovery) {
  const id = user.user?.id
  return !!id && state(item) === 'pending' && Date.parse(item.expires_at) > Date.now() && item.verified_count < 2 &&
    !item.self_verified && item.requester_id !== id && item.target_id !== id
}
function verificationReady() {
  return !loading.value && !readError.value && !busy.value && !!space.value && !!selected.value && canVerify(selected.value)
}
async function load() {
  readController?.abort()
  const controller = new AbortController()
  readController = controller
  loading.value = true
  readError.value = ''
  try {
    const data = await request.get<unknown, Space[]>('/iam-recovery', { silentError: true, signal: controller.signal })
    if (!active || readController !== controller) return
    spaces.value = data ?? []
    if (!spaces.value.some(item => item.workspace_id === workspaceId.value)) workspaceId.value = spaces.value[0]?.workspace_id
    now.value = Date.now()
    if (pending.value) {
      const command = pending.value
      const found = spaces.value.find(item => item.workspace_id === command.workspace_id)?.requests.some(item => item.id === command.request_id)
      if (found) finishApplication(command)
      else reconciled.value = true
    }
  } catch (error) {
    if (active && readController === controller) {
      spaces.value = []
      readError.value = errorText(error, '恢复服务暂不可用。')
    }
  } finally {
    if (active && readController === controller) loading.value = false
  }
}
function finishApplication(command: Application) {
  pending.value = undefined
  reconciled.value = false
  commandError.value = ''
  targetId.value = undefined
  reason.value = ''
  selectedId.value = command.request_id
  message.value = '恢复申请已登记，等待两名不同的预登记身份核验。'
}
async function sendApplication(command: Application) {
  if (busy.value) return
  busy.value = true
  commandError.value = ''
  message.value = ''
  try {
    await request.post('/iam-recovery/requests', command, { silentError: true })
    if (active) { finishApplication(command); await load() }
  } catch (error) {
    if (!active) return
    if (!(error instanceof ApiRequestError) || !error.status || error.status === 408 || error.status >= 500) {
      pending.value = command
      reconciled.value = false
      commandError.value = '申请结果待确认，请先核对原申请编号。'
    } else {
      pending.value = undefined
      commandError.value = errorText(error, '提交恢复申请失败。')
    }
  } finally { if (active) busy.value = false }
}
function requestRecovery() {
  if (!canSubmit.value || !space.value || targetId.value === undefined) return
  void sendApplication({ request_id: newRequestId(), workspace_id: space.value.workspace_id, target_id: targetId.value, reason: reason.value.trim() })
}
function retryApplication() {
  if (reconciled.value && !loading.value && !readError.value && pending.value && spaces.value.some(item => item.workspace_id === pending.value?.workspace_id)) {
    void sendApplication(pending.value)
  }
}
function openVerification(item: Recovery) {
  if (!verificationReady()) return
  selectedId.value = item.id
  password.value = ''
  code.value = ''
  verificationError.value = ''
  verificationOpen.value = true
}
function clearCredentials() { password.value = ''; code.value = ''; verificationError.value = '' }
async function verify() {
  if (!verificationReady() || !space.value || !selected.value || !password.value) return
  busy.value = true
  verificationError.value = ''
  message.value = ''
  const payload = { workspace_id: space.value.workspace_id, request_id: selected.value.id, password: password.value, code: code.value }
  try {
    const result = await request.post<unknown, { state: string }>('/iam-recovery/verifications', payload, { silentError: true })
    if (!active) return
    verificationOpen.value = false
    message.value = result.state === 'applied'
      ? '两人核验通过。接收人请重新登录，在原工作空间使用有限治理权限。'
      : '本次核验已登记，等待另一名独立身份。'
    await load()
  } catch (error) {
    if (active) {
      verificationError.value = errorText(error, '核验结果待确认。')
      password.value = ''
      code.value = ''
      await load()
    }
  } finally {
    password.value = ''
    code.value = ''
    if (active) {
      busy.value = false
      await nextTick()
      if (active && verificationOpen.value && verificationReady()) passwordInput.value?.focus()
    }
  }
}
onMounted(() => {
  void load()
  clock = setInterval(() => { now.value = Date.now() }, 1000)
})
onScopeDispose(() => { active = false; readController?.abort(); clearInterval(clock); clearCredentials() })
</script>

<template>
  <div class="ui-page-stack min-w-0">
    <PageHeader title="治理恢复" description="空间没有有效治理人时，由两名预登记身份独立核验。">
      <template #actions><button class="ghost-button ui-control-target" :disabled="busy || loading" @click="load">刷新恢复记录</button></template>
    </PageHeader>
    <p v-if="message" role="status" class="text-success text-sm">{{ message }}</p>
    <div v-if="commandError" class="glass-panel p-3 space-y-2">
      <p role="alert" class="text-danger text-sm">{{ commandError }}</p>
      <p v-if="pending" class="text-xs break-all">原申请编号 {{ pending.request_id }}</p>
      <div v-if="pending" class="flex flex-wrap gap-2">
        <button class="ghost-button ui-control-target" :disabled="busy || loading" @click="load">核对原申请</button>
        <button v-if="reconciled && !readError && !loading" class="ghost-button ui-control-target" :disabled="busy" @click="retryApplication">以原编号重试</button>
      </div>
    </div>
    <LoadingState v-if="loading" label="正在读取治理恢复状态…" />
    <PageState v-else-if="readError" title="读取治理恢复状态失败" :description="readError" tone="danger">
      <template #actions><button class="ghost-button ui-control-target" :disabled="busy" @click="load">重试读取恢复记录</button></template>
    </PageState>
    <PageState v-else-if="!spaces.length" title="暂无可核对的恢复空间" description="仅显示当前身份可查看的治理恢复记录。" />
    <template v-else>
      <div class="grid gap-4 lg:grid-cols-2">
        <section class="glass-panel p-4 min-w-0 space-y-4" aria-label="发起恢复申请">
          <h2 class="editorial-object-title">发起恢复申请</h2>
          <label class="block text-sm">待恢复空间<el-select v-model="workspaceId" aria-label="待恢复空间" class="w-full mt-1" :disabled="busy" @change="selectedId = undefined"><el-option v-for="item in spaces" :key="item.workspace_id" :value="item.workspace_id" :label="item.name" /></el-select></label>
          <form class="space-y-3" @submit.prevent="requestRecovery">
            <label class="block text-sm">治理接收成员编号<input v-model.number="targetId" type="number" min="1" step="1" class="glass-input block w-full mt-1" aria-label="治理接收成员编号" :disabled="busy || !!pending" required /></label>
            <label class="block text-sm">恢复原因<el-input v-model="reason" type="textarea" maxlength="1000" class="mt-1" aria-label="恢复原因" :disabled="busy || !!pending" /></label>
            <button class="liquid-button ui-control-target" :disabled="!canSubmit">提交恢复申请</button>
          </form>
        </section>
        <section class="glass-panel p-4 min-w-0 space-y-3" aria-label="恢复规则">
          <h2 class="editorial-object-title">恢复规则</h2>
          <ol class="list-decimal space-y-2 pl-5 text-sm text-muted">
            <li>提交申请后，须在 10 分钟内由两名不同的预登记身份重新核验。申请人和接收人须回避。</li>
            <li>接收人须重新登录，有限治理权限最多有效 15 分钟。</li>
            <li>业务查看、原角色组、个人凭据、旧会话和订阅保持原来的限制。</li>
          </ol>
        </section>
      </div>
      <section v-if="space" class="space-y-4 min-w-0" aria-label="恢复申请记录">
        <h2 class="editorial-object-title">{{ space.name }} · 申请记录</h2>
        <div v-if="space.requests.length" class="editorial-table-wrap" role="region" aria-label="恢复申请表格" tabindex="0">
          <table class="editorial-table" aria-label="恢复申请">
            <thead><tr><th scope="col">序号</th><th scope="col">申请编号</th><th scope="col">申请人</th><th scope="col">接收人</th><th scope="col">状态</th><th scope="col">独立核验</th><th scope="col">截止时间</th><th scope="col">操作</th></tr></thead>
            <tbody><tr v-for="(item, index) in space.requests" :key="item.id">
              <td class="editorial-table__number">{{ String(index + 1).padStart(2, '0') }}</td>
              <td><code class="text-xs whitespace-nowrap">{{ item.id }}</code></td><td>#{{ item.requester_id }}</td><td>#{{ item.target_id }}</td>
              <td><span class="status-badge">{{ statuses[state(item)] || state(item) }}</span></td>
              <td class="whitespace-nowrap">{{ item.verified_count }} / 2</td><td class="whitespace-nowrap">{{ formatTime(item.expires_at) }}</td>
              <td><button type="button" class="editorial-text-action whitespace-nowrap" :aria-expanded="selectedId === item.id" :disabled="busy" @click="selectedId = selectedId === item.id ? undefined : item.id">查看申请</button></td>
            </tr></tbody>
          </table>
        </div>
        <PageState v-else title="暂无恢复申请" description="当前空间还没有此身份可见的申请。" />
        <section v-if="selected" class="glass-panel p-4 space-y-3 min-w-0" aria-label="恢复申请详情">
          <h3 class="editorial-object-title">恢复申请详情</h3>
          <p class="text-sm break-words">原因：{{ selected.reason }}</p>
          <ol class="grid grid-cols-1 gap-2 text-sm sm:grid-cols-4" aria-label="核验流程">
            <li class="border-l-2 border-accent pl-2">申请已登记</li>
            <li class="border-l-2 border-stroke pl-2">第一位核验 · {{ selected.verified_count >= 1 ? '已完成' : '未完成' }}</li>
            <li class="border-l-2 border-stroke pl-2">第二位核验 · {{ selected.verified_count >= 2 ? '已完成' : '未完成' }}</li>
            <li class="border-l-2 border-stroke pl-2">恢复状态 · {{ statuses[state(selected)] || state(selected) }}</li>
          </ol>
          <button v-if="canVerify(selected)" class="ghost-button ui-control-target" :disabled="busy" @click="openVerification(selected)">以本人身份重新核验</button>
          <p v-else-if="selected.state === 'applied'" class="text-sm text-success">接收人请重新登录，使用原工作空间的有限治理权限。</p>
          <p v-else-if="state(selected) === 'pending' && selected.self_verified" class="text-sm text-muted">你已完成核验，等待另一名独立身份。</p>
        </section>
      </section>
    </template>
    <el-dialog v-model="verificationOpen" title="独立身份核验" width="min(520px, calc(100vw - 24px))" destroy-on-close
      :show-close="!busy" :close-on-click-modal="false" :close-on-press-escape="!busy" @opened="passwordInput?.focus()" @closed="clearCredentials">
      <form class="space-y-4" @submit.prevent="verify">
        <p class="text-sm text-muted">请使用当前本人账号 {{ user.username }} 的凭据重新核验。申请人及接收人须回避。</p>
        <label class="block text-sm">当前密码<input ref="passwordInput" v-model="password" type="password" autocomplete="current-password" class="glass-input block w-full mt-1" :disabled="busy" required /></label>
        <label class="block text-sm">动态验证码（已启用时必填）<input v-model="code" inputmode="numeric" autocomplete="one-time-code" maxlength="6" class="glass-input block w-full mt-1" :disabled="busy" /></label>
        <p v-if="verificationError" role="alert" class="text-danger text-sm">{{ verificationError }}</p>
        <LoadingState v-if="loading" label="正在核对申请状态…" />
        <button v-else-if="readError" type="button" class="ghost-button ui-control-target" @click="load">重新核对申请状态</button>
        <p v-else-if="!selected || !canVerify(selected)" class="text-sm text-muted">当前申请已无需或无法继续由此账号核验。</p>
        <button class="liquid-button ui-control-target" :disabled="!verificationReady() || !password">核验本次恢复申请</button>
      </form>
    </el-dialog>
  </div>
</template>
