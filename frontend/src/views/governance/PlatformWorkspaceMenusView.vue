<script setup lang="ts">
import { computed, onMounted, onScopeDispose, ref, shallowRef, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { iamApi } from '../../api/iam'
import { platformDirectoryPageSize, platformManagementApi, type PlatformWorkspace } from '../../api/platform-management'
import LoadingState from '../../components/base/LoadingState.vue'
import PageCategoryTabs from '../../components/base/PageCategoryTabs.vue'
import PageState from '../../components/base/PageState.vue'
import { useUserStore } from '../../stores/user'
import type { Effective, MenuConfiguration, MenuItem, Role } from '../../types/iam'
import { configuredEditorialGroup, editorialGroupNames, editorialMenuGroup } from '../../utils/editorial-navigation'
import { newRequestId } from '../../utils/request-id'
import { ApiRequestError } from '../../utils/request'
import RoleMenuMatrix from './RoleMenuMatrix.vue'

const route = useRoute()
const router = useRouter()
const user = useUserStore()
const targets = shallowRef<PlatformWorkspace[]>([])
const directoryLoading = ref(true)
const directoryError = ref('')
const targetId = computed(() => {
  const raw = route.query.workspace_id
  return typeof raw === 'string' && /^[1-9]\d*$/.test(raw) && Number.isSafeInteger(Number(raw)) ? Number(raw) : 0
})
const target = computed(() => targets.value.find(item => item.id === targetId.value))
const effective = shallowRef<Effective>()
const configuration = ref<MenuConfiguration>()
const roles = shallowRef<Role[]>([])
const roleId = ref('')
const selectedRoles = computed(() => roles.value.filter(role => role.id === roleId.value))
const loading = ref(false)
const saving = ref(false)
const error = ref('')
const roleError = ref('')
const denied = ref(false)
const receipt = ref('')
const categories = [{ id: 'default', name: '默认显示' }, { id: 'role', name: '按角色显示' }]
const category = ref('default')
const canManage = computed(() => !!effective.value?.is_super_admin && effective.value.actions.includes('space.menus.manage'))
const dirty = computed(() => !!configuration.value && JSON.stringify(configuration.value) !== JSON.stringify(effective.value?.menus))
const items = computed(() => [...(configuration.value?.items ?? [])].sort((a, b) => a.id.localeCompare(b.id, undefined, { numeric: true })))
const validation = computed(() => {
  if (!configuration.value || !effective.value) return ''
  const rows = configuration.value.items
  if (rows.length !== effective.value.catalog.menus.length) return '请重新核对完整菜单目录。'
  if (rows.some(row => !row.label.trim() || !row.group.trim())) return '菜单名称和分组均不能为空。'
  if (rows.some(row => !Number.isSafeInteger(row.order) || row.order < 0)) return '顺序必须是非负整数。'
  return new Set(rows.map(row => row.order)).size !== rows.length ? '菜单顺序不能重复。' : ''
})
let directoryRead: AbortController | undefined
let menuRead: AbortController | undefined
let menuWrite: AbortController | undefined
let retry: { payload: string; id: string } | undefined

async function loadTargets() {
  directoryRead?.abort()
  const read = new AbortController()
  directoryRead = read
  directoryLoading.value = true
  directoryError.value = ''
  try {
    const ordered = new Map<number, PlatformWorkspace>()
    for (let page = 1; !read.signal.aborted; page++) {
      const result = await platformManagementApi.workspaces({ keyword: '', page }, read.signal)
      result.items.forEach(item => ordered.set(item.id, item))
      if (!result.items.length || page * platformDirectoryPageSize >= result.total) break
    }
    if (!read.signal.aborted) targets.value = [...ordered.values()]
  } catch (failure) {
    if (!read.signal.aborted) directoryError.value = failure instanceof Error ? failure.message : '暂时无法读取工作空间。'
  } finally {
    if (!read.signal.aborted) directoryLoading.value = false
  }
}

function selectTarget(event: Event) {
  const id = (event.target as HTMLSelectElement).value
  void router.replace({ query: { ...route.query, workspace_id: id || undefined } })
}

async function loadMenus(keepReceipt = false) {
  menuRead?.abort()
  const id = target.value?.id
  const read = new AbortController()
  menuRead = read
  effective.value = undefined
  configuration.value = undefined
  roles.value = []
  roleId.value = ''
  error.value = ''
  roleError.value = ''
  denied.value = false
  if (!keepReceipt) {
    receipt.value = ''
    category.value = 'default'
    retry = undefined
  }
  loading.value = !!id
  if (!id || !target.value?.enabled) {
    denied.value = !!id
    loading.value = false
    return
  }
  const actor = user.user?.id
  const current = () => !read.signal.aborted && id === targetId.value && user.user?.id === actor
  try {
    const out = await iamApi.effective({ id, signal: read.signal })
    if (!current()) return
    if (out.snapshot.workspace_id !== id || out.snapshot.subject !== actor) throw new Error('菜单回执与配置目标不一致。')
    if (!out.actions.includes('space.menus.view') && !out.actions.includes('space.menus.manage')) {
      denied.value = true
      return
    }
    effective.value = out
    configuration.value = JSON.parse(JSON.stringify(out.menus)) as MenuConfiguration
    if (out.matrix_enabled && canManage.value) {
      try {
        const [readRoles, templates] = await Promise.all([
          platformManagementApi.workspaceRoles(id, read.signal), platformManagementApi.roleTemplates(read.signal),
        ])
        if (!current()) return
        const withTemplates = new Map<string, Role>(readRoles.map(role => [role.id, role]))
        for (const template of templates.items) {
          if (!withTemplates.has(template.id)) withTemplates.set(template.id, { ...template, enabled: true, version: 1, revision: 1, assigned_count: 0 })
        }
        roles.value = [...withTemplates.values()]
        roleId.value = roles.value[0]?.id ?? ''
      } catch (failure) {
        if (current()) roleError.value = failure instanceof Error ? failure.message : '暂时无法读取可配置的角色。'
      }
    }
  } catch (failure) {
    if (current()) {
      denied.value = failure instanceof ApiRequestError && (failure.status === 403 || failure.status === 404)
      if (!denied.value) error.value = failure instanceof Error ? failure.message : '暂时无法读取菜单。'
    }
  } finally {
    if (current()) loading.value = false
  }
}

function menuName(id: string) {
  return effective.value?.catalog.menus.find(item => item.id === id)?.label ?? id
}

function menuGroup(item: MenuItem) {
  const entry = effective.value?.catalog.menus.find(menu => menu.id === item.id)
  return editorialMenuGroup(entry?.path ?? '', item.group, entry?.group)
}

function changeGroup(item: MenuItem, event: Event) {
  const entry = effective.value?.catalog.menus.find(menu => menu.id === item.id)
  if (entry) item.group = configuredEditorialGroup((event.target as HTMLSelectElement).value, entry.path, entry.group)
}

function changeVisibility(item: MenuItem) {
  if (configuration.value?.preferred_menu === item.id && !item.visible) configuration.value.preferred_menu = ''
}

function changeRoleVisibility(id: string, menuId: string, choice: 'show' | 'hide' | '') {
  if (!configuration.value || !canManage.value || saving.value) return
  const next = { ...configuration.value.role_visibility }
  const byRole = { ...next[id] }
  if (choice) byRole[menuId] = choice
  else delete byRole[menuId]
  if (Object.keys(byRole).length) next[id] = byRole
  else delete next[id]
  configuration.value.role_visibility = next
}

async function save() {
  const selected = target.value
  const snapshot = effective.value?.snapshot
  const menu = configuration.value
  if (saving.value || loading.value || !canManage.value || !selected || !snapshot || !menu || !dirty.value || validation.value) return
  const input = {
    baseline: { config_version: snapshot.config_version, authorization_epoch: snapshot.authorization_epoch },
    configuration: JSON.parse(JSON.stringify(menu)) as MenuConfiguration,
  }
  const payload = JSON.stringify(input)
  if (retry?.payload !== payload) retry = { payload, id: newRequestId() }
  const write = new AbortController()
  menuWrite = write
  saving.value = true
  error.value = ''
  receipt.value = ''
  const current = () => !write.signal.aborted && targetId.value === selected.id && user.user?.id === snapshot.subject
  try {
    const result = await platformManagementApi.saveWorkspaceMenus(selected.id, { ...input, request_id: retry.id }, write.signal)
    if (!current()) return
    retry = undefined
    receipt.value = `${selected.name}的菜单配置已保存，版本 ${result.version}。`
    await loadMenus(true)
    if (current() && user.workspaceId === selected.id) void user.loadPermissions(true).catch(() => {})
  } catch (failure) {
    if (current()) {
      if (failure instanceof ApiRequestError && (failure.status === 403 || failure.status === 404)) {
        await loadMenus()
      } else error.value = failure instanceof Error ? failure.message : '菜单保存结果待确认，请重试。'
    }
  } finally {
    if (menuWrite === write) { menuWrite = undefined; saving.value = false }
  }
}

onMounted(() => void loadTargets())
watch([targetId, target], () => {
  menuWrite?.abort()
  void loadMenus()
})
onScopeDispose(() => { directoryRead?.abort(); menuRead?.abort(); menuWrite?.abort() })
</script>

<template>
  <section class="glass-panel ui-card-padding min-w-0 space-y-4" aria-label="工作空间菜单配置">
    <LoadingState v-if="directoryLoading" label="正在读取工作空间…" />
    <PageState v-else-if="directoryError" title="读取空间目录失败" :description="directoryError" tone="danger">
      <template #actions><button type="button" class="ghost-button ui-control-target" @click="loadTargets">重试读取</button></template>
    </PageState>
    <template v-else>
      <div class="flex flex-wrap items-end gap-3">
        <label class="grid gap-1 text-sm min-w-0 max-w-full">
          <span>配置目标工作空间</span>
          <select class="glass-input ui-control-target w-full min-w-0" :value="targetId || ''" :disabled="saving" @change="selectTarget">
            <option value="">选择工作空间</option>
            <option v-for="item in targets" :key="item.id" :value="item.id">{{ item.name }}</option>
          </select>
        </label>
        <span v-if="effective" class="text-sm text-muted">配置版本 {{ effective.menus.version }}</span>
      </div>
      <h2 v-if="target" class="editorial-object-title">{{ target.name }}</h2>
      <p v-if="receipt" role="status" class="text-sm text-success">{{ receipt }}</p>
      <LoadingState v-if="loading" label="正在核对目标空间权限…" />
      <PageState v-else-if="!target" title="选择配置目标工作空间" :description="targetId ? '该工作空间当前不可用。' : ''" />
      <PageState v-else-if="denied" title="当前身份没有该空间的菜单配置许可" description="目标空间继续按现有权限管理菜单。" />
      <PageState v-else-if="error && !configuration" title="读取菜单失败" :description="error" tone="danger">
        <template #actions><button type="button" class="ghost-button ui-control-target" @click="loadMenus(true)">重试读取</button></template>
      </PageState>
      <template v-else-if="configuration && effective">
        <p class="text-sm text-muted">菜单配置控制导航显示。页面访问和操作权限仍按已授予的角色核对。</p>
        <p v-if="error || (dirty && validation)" role="alert" class="text-sm text-danger">{{ error || validation }}</p>
        <PageCategoryTabs label="工作空间菜单分类" :categories="effective.matrix_enabled && canManage ? categories : [categories[0]!]"
          :active-id="category" :hide-tabs="!effective.matrix_enabled || !canManage" @select="category = $event">
          <div v-if="category === 'role'" class="space-y-3">
            <PageState v-if="roleError" title="读取菜单角色失败" :description="roleError" tone="danger">
              <template #actions><button type="button" class="ghost-button ui-control-target" @click="loadMenus(true)">重新读取</button></template>
            </PageState>
            <template v-else>
              <label class="flex flex-wrap items-center gap-2 text-sm"><span>配置角色</span>
                <select v-model="roleId" class="glass-input ui-control-target" :disabled="saving"><option v-for="role in roles" :key="role.id" :value="role.id">{{ role.name }}</option></select>
              </label>
              <RoleMenuMatrix :items="items" :catalog="effective.catalog.menus" :roles="selectedRoles"
                :visibility="configuration.role_visibility ?? {}" :reserved-actions="effective.reserved_actions ?? []"
                :disabled="saving" @change="changeRoleVisibility" />
            </template>
          </div>
          <template v-else>
            <div class="editorial-table-wrap" role="region" tabindex="0" aria-label="工作空间默认菜单">
              <table class="editorial-table min-w-[660px]">
                <thead><tr><th scope="col">入口</th><th scope="col">显示名称</th><th scope="col">分组</th><th scope="col">顺序</th><th scope="col">显示</th><th scope="col">推荐首页</th></tr></thead>
                <tbody><tr v-for="item in items" :key="item.id">
                  <th scope="row" class="whitespace-nowrap">{{ menuName(item.id) }}</th>
                  <td><input v-model="item.label" class="glass-input" :aria-label="`${menuName(item.id)}显示名称`" :disabled="saving || !canManage" maxlength="40" /></td>
                  <td><select :value="menuGroup(item)" class="glass-input" :aria-label="`${menuName(item.id)}分类`" :disabled="saving || !canManage" @change="changeGroup(item, $event)">
                    <option v-for="group in editorialGroupNames" :key="group" :value="group">{{ group }}</option>
                  </select></td>
                  <td><input v-model.number="item.order" type="number" min="0" step="1" class="glass-input !w-20" :aria-label="`${menuName(item.id)}顺序`" :disabled="saving || !canManage" /></td>
                  <td><input v-model="item.visible" type="checkbox" :aria-label="`显示${menuName(item.id)}`" :disabled="saving || !canManage" @change="changeVisibility(item)" /></td>
                  <td><input v-model="configuration.preferred_menu" name="platform-workspace-preferred-menu" :value="item.id" type="radio" :aria-label="`推荐${menuName(item.id)}为首页`" :disabled="saving || !item.visible || !canManage" /></td>
                </tr></tbody>
              </table>
            </div>
            <label class="flex items-center gap-2 pt-3 text-sm"><input v-model="configuration.preferred_menu" name="platform-workspace-preferred-menu" type="radio" value="" :disabled="saving || !canManage" />不设置推荐首页</label>
          </template>
        </PageCategoryTabs>
        <div class="flex flex-wrap justify-end gap-2">
          <button v-if="error" type="button" class="ghost-button ui-control-target" :disabled="saving" @click="loadMenus()">读取最新配置</button>
          <button v-if="canManage" type="button" class="liquid-button ui-control-target" :disabled="saving || !dirty || !!validation" @click="save">{{ saving ? '正在保存…' : '保存菜单配置' }}</button>
        </div>
      </template>
    </template>
  </section>
</template>
