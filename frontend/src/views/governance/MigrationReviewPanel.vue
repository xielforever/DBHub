<script setup lang="ts">
import { computed, nextTick, onMounted, onScopeDispose, ref, useId, watch } from 'vue'
import { ElMessageBox } from 'element-plus'
import LoadingState from '../../components/base/LoadingState.vue'
import PageState from '../../components/base/PageState.vue'
import {iamApi} from '../../api/iam'
import {useUserStore} from '../../stores/user'
import type {Baseline,Delegation,Member} from '../../types/iam'
import {actionLabel} from '../../utils/iam-labels'
import {newRequestId} from '../../utils/request-id'
import {useReportRun} from '../report/useReportRun'
interface Migration {state:string;config_version:number;authorization_epoch:number;minimum_bridge?:string;active_bundle_id?:string;inventory_unknown_count:number;principals:{user_id:number;mode:string;legacy_data_role:string;confirmed:boolean;valid_until?:string}[]}
interface Difference {action:string;legacy:boolean;target:boolean;reason?:string}
interface Shadow {baseline:Baseline;fingerprint:string;read_only:boolean;target_connections_opened:number;principals:{user_id:number;username:string;legacy_data_role:string;actions:string[];unknown:number;differences:Difference[] | null}[]}
interface Bundle {id:string;created_at:string;manifest:{valid_until:string;shadow:Shadow}}
interface Bridge {isolated_activation_enabled:boolean;unbridged_runtime_connections:number;live_agents:{id:string;kind:string;version:string;current:boolean}[]}
const user=useUserStore(),selected=ref<number[]>([]),shadow=ref<Shadow>(),delegationID=ref(''),rollbackReason=ref(''),error=ref(''),message=ref(''),prepared=ref<{id:string;input:string}>()
const pendingCommand = ref<'compare' | 'prepare' | 'rollback'>()
const busy = computed(() => pendingCommand.value !== undefined)
const refreshButton = ref<HTMLButtonElement>()
const migrationHeading = ref<HTMLHeadingElement>()
const compareButton = ref<HTMLButtonElement>()
const compareReceipt = ref<HTMLParagraphElement>()
const compareError = ref('')
const compareErrorId = useId()
const snapshot = useReportRun<{ state: Migration; bridge: Bridge; members: Member[]; delegations: Delegation[]; bundles: Bundle[] }>()
const data = computed(() => snapshot.state.value.status === 'success' ? snapshot.state.value.data : undefined)
const state = computed(() => data.value?.state), bridge = computed(() => data.value?.bridge)
const members = computed(() => data.value?.members ?? []), delegations = computed(() => data.value?.delegations ?? []), bundles = computed(() => data.value?.bundles ?? [])
const readPending = computed(() => snapshot.state.value.status === 'loading')
const readError = computed(() => snapshot.state.value.status === 'error' ? snapshot.state.value.message || '迁移状态不可用。' : '')
const modeLabels:Record<string,string>={LEGACY_GUARDED:'旧权限兼容',SHADOW:'只读对照',COMPAT_ACTIVE:'按主体采用角色权限'},agentLabels:Record<string,string>={api:'接口服务',worker:'任务执行器',database:'数据库授权'},date=(v:string)=>v?new Date(v).toLocaleString():'未设期限',memberName=(id:number)=>members.value.find(m=>m.id===id)?.username||`成员 #${id}`
const now = ref(Date.now())
const ownDelegations=computed(()=>delegations.value.filter(d=>d.delegate_id===user.user?.id&&!d.revoked_at&&Date.parse(d.valid_until)>now.value&&d.governance_actions.some(a=>['bundle.prepare','bundle.rollback','bundle.activate'].includes(a))))
const chosenDelegation = computed(() => ownDelegations.value.find(item => item.id === delegationID.value))
const selectedKey = computed(() => JSON.stringify([...selected.value].sort((a, b) => a - b)))
const eligibleMembers = computed(() => members.value.filter(member => member.active && !member.frozen && (!member.expires_at || Date.parse(member.expires_at) > now.value)))
const mayPrepare = computed(() => user.can('space.assign.manage') || user.effective?.isolated_migration?.prepare === true)
const mayRollback = computed(() => user.can('space.assign.manage') || user.effective?.isolated_migration?.rollback === true)
const isolated = computed(() => bridge.value?.isolated_activation_enabled === true && (mayPrepare.value || mayRollback.value))
const validShadow = computed(() => !!shadow.value && shadow.value.read_only === true && shadow.value.target_connections_opened === 0 && !!state.value &&
  shadow.value.baseline.config_version === state.value.config_version && shadow.value.baseline.authorization_epoch === state.value.authorization_epoch &&
  JSON.stringify(shadow.value.principals.map(item => item.user_id).sort((a, b) => a - b)) === selectedKey.value)
const canPrepare = computed(() => {
  const delegation = chosenDelegation.value
  return isolated.value && mayPrepare.value && validShadow.value && delegation?.governance_actions.includes('bundle.prepare') &&
    delegation.governance_actions.includes('bundle.activate') && shadow.value?.principals.every(item =>
      item.unknown === 0 && delegation.target_ids.includes(item.user_id) && item.actions.every(action => delegation.grantable_actions.includes(action)) &&
      (item.differences ?? []).every(difference => Array.from(difference.reason?.trim() ?? '').length >= 4))
})
const canRollback = computed(() => isolated.value && mayRollback.value && !!state.value?.active_bundle_id &&
  chosenDelegation.value?.governance_actions.includes('bundle.rollback') && !!rollbackReason.value.trim())
const commandIDs=new Map<string,string>()
function commandID(kind:string,input:unknown){const key=kind+JSON.stringify(input);if(!commandIDs.has(key))commandIDs.set(key,newRequestId());return commandIDs.get(key)!}
const until=ref(new Date(Date.now()+3600000-new Date().getTimezoneOffset()*60000).toISOString().slice(0,16))
function clearShadow(){shadow.value=undefined;prepared.value=undefined;compareError.value=''}
let active = true
async function load() {
  error.value = ''
  const loaded = await snapshot.run('migration', async signal => {
    const [state, bridge, members, delegations, bundles] = await Promise.all([
      iamApi.read<Migration>('migration', signal), iamApi.read<Bridge>('migration/bridge', signal),
      iamApi.read<Member[]>(user.can('space.members.view') ? 'members' : 'assignment-targets', signal),
      mayPrepare.value || mayRollback.value ? iamApi.read<Delegation[]>('delegations', signal) : Promise.resolve([]),
      iamApi.read<Bundle[]>('bundles', signal),
    ])
    return { state, bridge, members, delegations, bundles }
  })
  if (!loaded) { if (readError.value) clearShadow(); return }
  if (!validShadow.value) clearShadow()
  const valid = new Set(eligibleMembers.value.map(item => item.id))
  selected.value = selected.value.filter(id => valid.has(id))
  if (!chosenDelegation.value) delegationID.value = ownDelegations.value[0]?.id ?? ''
}
async function refreshFromControl() {
  if (busy.value || readPending.value) return
  await load()
  await nextTick()
  if (active && document.activeElement === refreshButton.value) migrationHeading.value?.focus()
}
watch(selectedKey, clearShadow, { flush: 'sync' })
async function compare() {
  if (busy.value || readPending.value || !state.value || !selected.value.length) return
  pendingCommand.value = 'compare'
  error.value = ''
  const ids = [...selected.value]
  const key = selectedKey.value
  clearShadow()
  try {
    const result = await iamApi.shadow<Shadow>(ids)
    if (!active || key !== selectedKey.value) return
    if (state.value?.config_version !== result.baseline.config_version || state.value.authorization_epoch !== result.baseline.authorization_epoch) {
      compareError.value = '当前配置基线已变化，请刷新后重新核对。'
      return
    }
    shadow.value = result
    if (!validShadow.value) {
      compareError.value = '服务端回执未确认所选主体的只读对照，请重新核对。'
      return
    }
    await nextTick()
    if (active && document.activeElement === compareButton.value) compareReceipt.value?.focus()
  } catch (e) { if (active) compareError.value = e instanceof Error ? e.message : '对照失败。' }
  finally { pendingCommand.value = undefined }
}
async function prepare() {
  const d = chosenDelegation.value, s = shadow.value
  if (busy.value || !canPrepare.value || !d || !s) return
  const untilTime = new Date(until.value)
  const expiryTime = untilTime.getTime(), submittedAt = Date.now()
  if (!Number.isFinite(expiryTime) || expiryTime <= submittedAt || expiryTime > submittedAt + 30 * 86400000 || expiryTime > Date.parse(d.valid_until)) {
    error.value = '请选择未来 30 天内且在委派范围内的有效期。'
    return
  }
  const expiry = untilTime.toISOString()
  const input = { baseline: s.baseline, delegation_id: d.id, delegation_version: d.version, user_ids: [...selected.value], shadow_fingerprint: s.fingerprint, differences: Object.fromEntries(s.principals.map(p => [p.user_id, p.differences ?? []])), valid_until: expiry }
  const fingerprint = JSON.stringify(input)
  pendingCommand.value = 'prepare'
  error.value = ''
  try {
    if (!prepared.value || prepared.value.input !== fingerprint) {
      const result = await iamApi.bundle({ ...input, request_id: commandID('prepare', input) })
      if (!active) return
      prepared.value = { id: result.id, input: fingerprint }
    }
    const proposal = { baseline: s.baseline, delegation_id: d.id, delegation_version: d.version, valid_until: expiry, patch: { kind: 'bundle_activate' as const, bundle_id: prepared.value.id, valid_until: expiry } }
    await iamApi.propose({ ...proposal, request_id: commandID('propose', proposal) })
    if (active) { message.value = '配置包已提交独立复核，有效权限保持原状。'; await load() }
  } catch (e) { if (active) error.value = e instanceof Error ? e.message : '提交状态待核实。' }
  finally { pendingCommand.value = undefined }
}
async function rollback() {
  const d = chosenDelegation.value, current = state.value
  if (busy.value || !canRollback.value || !d || !current?.active_bundle_id) return
  const identity = user.identityEpoch(), workspace = user.workspaceId
  pendingCommand.value = 'rollback'
  error.value = ''
  try {
    try { await ElMessageBox.confirm('将回退当前隔离配置，并保留已有撤权、冻结和原有效期。', '确认隔离配置回退', { type: 'warning', confirmButtonText: '确认回退', cancelButtonText: '取消' }) }
    catch { return }
    if (!active || !canRollback.value || state.value !== current || Date.parse(d.valid_until) <= Date.now()) return
    const input = { baseline: { config_version: current.config_version, authorization_epoch: current.authorization_epoch }, bundle_id: current.active_bundle_id, delegation_id: d.id, delegation_version: d.version, reason: rollbackReason.value.trim() }
    const receipt = await iamApi.rollback({ ...input, request_id: commandID('rollback', input) })
    if (identity !== user.identityEpoch() || workspace !== user.workspaceId) return
    if (receipt?.state !== 'LEGACY_GUARDED' || receipt.prior_bundle_id !== input.bundle_id) {
      if (active) error.value = '回退回执未确认原配置包，请刷新检查。'
      return
    }
    user.migrationRollbackBundleId = receipt.prior_bundle_id
    if (!active) return
    message.value = ''
    clearShadow()
    try { await user.loadPermissions(true) }
    catch { if (active) error.value = '回退回执已收到，权限刷新待确认。'; return }
    await nextTick()
    if (active) await load()
  } catch (e) { if (active) error.value = e instanceof Error ? e.message : '回退状态待核实。' }
  finally { pendingCommand.value = undefined }
}
onMounted(()=>void load())
const clock = setInterval(() => { now.value = Date.now() }, 15000)
onScopeDispose(() => { active = false; clearInterval(clock) })
</script>

<template>
  <section class="ui-page-stack min-w-0" aria-label="迁移与回退">
    <header class="flex flex-wrap items-start justify-between gap-3">
      <h2 class="editorial-object-title">迁移与回退</h2>
      <button ref="refreshButton" class="ghost-button ui-control-target" :aria-disabled="busy || readPending || undefined" @click="refreshFromControl">
        {{ readError ? '重试迁移检查' : '刷新检查' }}
      </button>
    </header>
    <p v-if="error" role="alert" class="text-danger text-sm">{{ error }}</p>
    <p v-if="message" role="status" class="text-success text-sm">{{ message }}</p>
    <p v-if="user.migrationRollbackBundleId" role="status" class="text-success text-sm">
      隔离配置已回退。配置包 <span class="break-all font-mono">{{ user.migrationRollbackBundleId }}</span>。
      现有撤权、冻结和原期限保留。
    </p>
    <LoadingState v-if="readPending" label="正在读取迁移与治理状态…" />
    <PageState v-else-if="readError" title="读取迁移状态失败" :description="readError" tone="danger" heading="h3" />
    <template v-else-if="state && bridge">
      <section class="space-y-3 min-w-0">
        <h3 ref="migrationHeading" tabindex="-1" class="editorial-object-title ui-focus-target">当前迁移状态</h3>
        <dl class="grid grid-cols-2 gap-3 text-sm sm:grid-cols-4">
          <div><dt>当前方案</dt><dd>{{ modeLabels[state.state] || state.state }}</dd></div>
          <div><dt>配置版本</dt><dd>{{ state.config_version }}</dd></div>
          <div><dt>授权代次</dt><dd>{{ state.authorization_epoch }}</dd></div>
          <div><dt>未确认约束</dt><dd>{{ state.inventory_unknown_count }} 项</dd></div>
        </dl>
        <p class="text-sm text-muted">{{ bridge.isolated_activation_enabled ? '当前为已登记的隔离验收部署。配置包须经独立复核。' : '当前部署可运行只读对照。真实空间切换未开放。' }}</p>
        <div v-if="state.principals.length" class="editorial-table-wrap" role="region" tabindex="0" aria-label="迁移主体表格">
          <table class="editorial-table" aria-label="迁移主体">
            <thead><tr><th scope="col">序号</th><th scope="col">主体</th><th scope="col">采用方案</th><th scope="col">原数据身份</th><th scope="col">确认状态</th><th scope="col">有效期</th></tr></thead>
            <tbody><tr v-for="(principal, index) in state.principals" :key="principal.user_id">
              <td>{{ index + 1 }}</td><th scope="row">{{ memberName(principal.user_id) }}</th><td>{{ principal.mode === 'TARGET' ? '目标角色权限' : '完整旧权限' }}</td><td>{{ principal.legacy_data_role }}</td><td>{{ principal.confirmed ? '已确认' : '保持旧模式' }}</td><td>{{ date(principal.valid_until ?? '') }}</td>
            </tr></tbody>
          </table>
        </div>
        <p v-else class="text-sm text-muted">暂无迁移主体记录。</p>
      </section>

      <section class="glass-panel ui-card-padding space-y-3 min-w-0">
        <h3 class="editorial-object-title">只读权限对照</h3>
        <p class="text-sm text-muted">对照当前空间的功能许可和原数据约束。未确认的约束需在启用前核实。</p>
        <label class="block text-sm">
          核对成员
          <el-select v-model="selected" multiple filterable class="w-full mt-2" aria-label="对照成员" :disabled="busy || !eligibleMembers.length">
            <el-option v-for="member in eligibleMembers" :key="member.id" :value="member.id" :label="member.username" />
          </el-select>
        </label>
        <p v-if="!eligibleMembers.length" class="text-sm text-muted">当前无可核对的有效成员。</p>
        <button ref="compareButton" class="ghost-button ui-control-target" :disabled="!selected.length"
          :aria-disabled="busy || undefined" :aria-describedby="compareError ? compareErrorId : undefined" @click="compare">运行只读对照</button>
        <LoadingState v-if="pendingCommand === 'compare'" variant="inline" label="正在运行只读对照…" />
        <p v-if="compareError" :id="compareErrorId" role="alert" class="text-danger text-sm">{{ compareError }}</p>
        <template v-if="validShadow && shadow">
          <p ref="compareReceipt" tabindex="-1" class="text-sm text-muted break-words ui-focus-target">只读回执 {{ shadow.fingerprint.slice(0, 12) }} · 打开目标数据库连接 {{ shadow.target_connections_opened }} 个</p>
          <article v-for="principal in shadow.principals" :key="principal.user_id" class="border-t border-stroke pt-3 space-y-3">
            <h4>{{ principal.username }} · 原数据身份 {{ principal.legacy_data_role }}</h4>
            <p v-if="principal.unknown" class="text-warning text-sm">{{ principal.unknown }} 项约束未确认，启用条件未满足。</p>
            <p v-if="!principal.differences?.length" class="text-sm text-muted">功能许可一致。原连接、受众与行列策略继续约束执行。</p>
            <div v-else class="editorial-table-wrap" role="region" tabindex="0" :aria-label="principal.username + '的功能差异'">
              <table class="editorial-table">
                <thead><tr><th scope="col">功能许可</th><th scope="col">原方案</th><th scope="col">目标方案</th><th v-if="isolated && mayPrepare" scope="col">差异处理理由</th></tr></thead>
                <tbody><tr v-for="difference in principal.differences" :key="difference.action">
                  <th scope="row">{{ actionLabel(difference.action) }}</th><td>{{ difference.legacy ? '允许' : '未开放' }}</td><td>{{ difference.target ? '允许' : '拒绝' }}</td>
                  <td v-if="isolated && mayPrepare"><input v-model="difference.reason" class="glass-input" maxlength="500" :aria-label="principal.username + actionLabel(difference.action) + '差异理由'" :disabled="busy" /></td>
                </tr></tbody>
              </table>
            </div>
          </article>
        </template>
      </section>

      <section class="space-y-3 min-w-0">
        <h3 class="editorial-object-title">运行版本检查</h3>
        <p class="text-sm text-muted">未桥接的运行连接 {{ bridge.unbridged_runtime_connections }} 个<span v-if="state.minimum_bridge"> · 最低桥接版本 {{ state.minimum_bridge }}</span>。同版本运行证明有效期为两分钟。</p>
        <div v-if="bridge.live_agents?.length" class="editorial-table-wrap" role="region" tabindex="0" aria-label="运行证明表格">
          <table class="editorial-table" aria-label="运行证明">
            <thead><tr><th scope="col">序号</th><th scope="col">服务</th><th scope="col">版本</th><th scope="col">证明状态</th></tr></thead>
            <tbody><tr v-for="(agent, index) in bridge.live_agents" :key="agent.id">
              <td>{{ index + 1 }}</td><th scope="row">{{ agentLabels[agent.kind] || agent.kind }}</th><td>{{ agent.version }}</td><td>{{ agent.current ? '有效' : '已过期' }}</td>
            </tr></tbody>
          </table>
        </div>
        <p v-else class="text-sm text-muted">暂无运行证明。</p>
      </section>

      <section v-if="isolated" class="glass-panel ui-card-padding space-y-3">
        <h3 class="editorial-object-title">{{ mayPrepare ? '隔离配置包申请' : '隔离配置回退委派' }}</h3>
        <p v-if="mayPrepare" class="text-sm text-muted">配置包包含目标主体、原数据身份、约束指纹及全部差异。独立复核通过前沿用原权限。</p>
        <label class="block text-sm">
          治理委派
          <el-select v-model="delegationID" class="w-full mt-2" aria-label="迁移使用的委派" :disabled="busy">
            <el-option v-for="delegation in ownDelegations" :key="delegation.id" :value="delegation.id" :label="'委派 ' + delegation.id.slice(0, 8) + ' · 截止 ' + date(delegation.valid_until)" />
          </el-select>
        </label>
        <p v-if="!ownDelegations.length" class="text-sm text-muted">当前无适用的迁移治理委派。</p>
        <template v-if="mayPrepare">
          <p v-if="ownDelegations.length && validShadow && !canPrepare" class="text-sm text-muted">请核对委派允许的主体和许可、未确认的约束及差异理由。每项理由至少填写四个字符。</p>
          <label class="block text-sm">
            验收配置有效期至
            <input v-model="until" class="glass-input block max-w-full mt-2" type="datetime-local" :disabled="busy" />
          </label>
          <button class="liquid-button ui-control-target" :disabled="busy || !canPrepare" @click="prepare">保存配置包并提交独立复核</button>
        </template>
      </section>

      <section class="space-y-3 min-w-0">
        <h3 class="editorial-object-title">配置包记录</h3>
        <div v-if="bundles.length" class="editorial-table-wrap" role="region" tabindex="0" aria-label="配置包表格">
          <table class="editorial-table" aria-label="配置包记录">
            <thead><tr><th scope="col">配置包</th><th scope="col">主体</th><th scope="col">创建时间</th><th scope="col">有效期</th><th scope="col">采用状态</th></tr></thead>
            <tbody><tr v-for="bundle in bundles" :key="bundle.id">
              <th scope="row">{{ bundle.id.slice(0, 8) }}</th><td>{{ bundle.manifest.shadow.principals.map(item => item.username).join('、') }}</td><td>{{ date(bundle.created_at) }}</td><td>{{ date(bundle.manifest.valid_until) }}</td><td>{{ bundle.id === state.active_bundle_id ? '当前采用' : '保留记录' }}</td>
            </tr></tbody>
          </table>
        </div>
        <p v-else class="text-sm text-muted">暂无配置包记录。</p>
      </section>

      <section class="glass-panel ui-card-padding space-y-3">
        <h3 class="editorial-object-title">回退约束</h3>
        <p class="text-sm text-muted">回退更新配置指针与授权代次，保留已有撤权、冻结、期限、业务签名、发布号、文件有效期和待核实回执。新模式任务转为暂停。</p>
        <template v-if="isolated && mayRollback && state.active_bundle_id">
          <label class="block text-sm">回退原因<el-input v-model="rollbackReason" type="textarea" aria-label="回退原因" maxlength="500" class="mt-2" :disabled="busy" /></label>
          <button class="ghost-button ui-control-target" :disabled="!canRollback" :aria-disabled="busy || undefined" @click="rollback">回退此隔离配置</button>
        </template>
      </section>
    </template>
  </section>
</template>
