<script setup lang="ts">
import BrandLogo from '../components/base/BrandLogo.vue'
import { computed, nextTick, onMounted, onScopeDispose, ref, watch } from 'vue'
import { useRoute, useRouter, type LocationQueryRaw } from 'vue-router'
import { ChevronDown } from 'lucide-vue-next'
import PageState from '../components/base/PageState.vue'
import EditorialNavigation from '../components/base/EditorialNavigation.vue'
import ThemeSwitcher from '../components/base/ThemeSwitcher.vue'
import WorkspaceSwitcher from '../components/base/WorkspaceSwitcher.vue'
import InboxButton from '../components/InboxButton.vue'
import WorkspaceCatalog from '../components/catalog/WorkspaceCatalog.vue'
import { contentDevelopmentEntries, contentNavigationMenus, editorialGroups, managementNavigationMenus, managementNavigationPath } from '../utils/editorial-navigation'
import { featureAllowed } from '../utils/permission'
import { useUserStore } from '../stores/user'

const user = useUserStore()
const route = useRoute()
const router = useRouter()
const switchingSpace = ref(false)
const navigationOpen = ref(false)
const development = computed(() => contentDevelopmentEntries(user.visibleMenus,
  kind => featureAllowed(user, kind + '.design', ['admin', 'developer'].includes(user.role))))
const groups = computed(() => editorialGroups(contentNavigationMenus(managementNavigationMenus(user.visibleMenus, user.platformAdmin), development.value)))
const activeNavigationPath = computed(() => {
  const managed = route.path === '/reports' ? route.query.view === 'managed' : route.query.tab === 'managed'
  return managed && development.value.some(entry => entry.path === route.path) && !route.query.review
    ? development.value[0]?.target ?? route.path
    : managementNavigationPath(route.path)
})
const roleNames = computed(() => [...new Set(user.effective?.snapshot.sources?.map(source => source.role_name).filter(Boolean))].join('、'))
const workspaceContext = computed(() => user.workspaceId ? roleNames.value || '当前空间' : '本人入口')
const identityScopedRoutes = new Set(['workspace-link', 'workspace-directory', 'personal-profile', 'platform-management', 'governance-recovery'])
const accessStatusRoutes = new Set(['no-menu', 'access-denied'])
const viewKey = computed(() => {
  const name = String(route.name)
  if (identityScopedRoutes.has(name)) return `${user.user?.id}:${name}`
  const scope = accessStatusRoutes.has(name) ? user.workspaceId : user.permissionKey
  return `${user.user?.id}:${scope}:${name}`
})

watch([() => route.path, viewKey], () => {
  void nextTick(() => document.getElementById('main-content')?.focus())
})
function navigate(path: string) {
  const target = router.resolve(path)
  const query: LocationQueryRaw = { ...target.query, workspace_id: target.path === '/spaces' ? undefined : user.workspaceId || undefined }
  if (path === '/reports' && featureAllowed(user, 'report.view', true)) query.view = 'published'
  if (path === '/dashboards' && featureAllowed(user, 'dashboard.view', true)) query.tab = 'published'
  void router.push({ path: target.path, query })
}
async function switchSpace(id: number) {
  if (switchingSpace.value || id === user.workspaceId) return
  switchingSpace.value = true
  user.selectWorkspace(id)
  try { await user.loadPermissions(); await router.replace(user.safeHome) }
  catch { /* Store保留当前空间的安全错误与重试入口。 */ }
  finally { switchingSpace.value = false }
}
async function retry() {
  try { await user.loadWorkspaces(true); await user.loadPermissions(true) } catch { /* Store显示错误。 */ }
}
async function accountCommand(command: string) {
  if (command === 'logout') {
    try { await user.logout() } finally { await router.replace('/login') }
  } else { await router.push(command) }
}
let timer: ReturnType<typeof setInterval> | undefined
onMounted(() => {
  timer = setInterval(() => {
    if (user.workspaceId && !document.hidden) void user.loadPermissions(true).catch(() => {})
  }, 15000)
})
onScopeDispose(() => clearInterval(timer))
</script>

<template>
  <div class="iam-shell editorial-shell">
    <a href="#main-content" class="skip-link">跳到主要内容</a>
    <header class="editorial-masthead">
      <RouterLink :to="user.safeHome" class="editorial-wordmark" aria-label="知数，返回获准首页"><BrandLogo aria-hidden="true" /></RouterLink>
      <EditorialNavigation :groups="groups" :active-path="activeNavigationPath" :context-key="user.permissionKey" @navigate="navigate" @open-change="navigationOpen = $event" />
      <div class="editorial-masthead__actions" :inert="navigationOpen" aria-label="工作空间与账户操作">
        <WorkspaceSwitcher :options="user.workspaces" :model-value="user.workspaceId || undefined"
          :context-label="workspaceContext" :loading="switchingSpace" @change="switchSpace" />
        <InboxButton />
        <ThemeSwitcher compact />
        <el-dropdown trigger="click" @command="accountCommand">
          <button type="button" class="editorial-account" :aria-label="`账号：${user.username || '本人入口'}`">
            <span>{{ user.username || '账号' }}</span><ChevronDown class="w-3 h-3 shrink-0" aria-hidden="true" />
          </button>
          <template #dropdown>
            <el-dropdown-menu>
              <el-dropdown-item command="/profile">个人设置与安全</el-dropdown-item>
              <el-dropdown-item v-if="user.platformAdmin" command="/platform">平台管理</el-dropdown-item>
              <el-dropdown-item v-if="user.canRecover || user.effective?.snapshot.governance_only" command="/governance-recovery">治理恢复</el-dropdown-item>
              <el-dropdown-item command="logout" divided>退出登录</el-dropdown-item>
            </el-dropdown-menu>
          </template>
        </el-dropdown>
      </div>
    </header>
    <main id="main-content" class="app-content editorial-content" tabindex="-1" :inert="navigationOpen">
      <PageState v-if="!route.meta.independent && !user.workspaceId" title="请选择工作空间" description="在页头选择已有成员资格的工作空间后继续。" heading="h1">
        <template #actions><RouterLink to="/profile" class="ghost-button">个人安全设置</RouterLink></template>
      </PageState>
      <PageState v-else-if="!route.meta.independent && user.permissionStatus === 'error'" title="暂时无法确认当前权限" :description="user.permissionError" tone="danger" heading="h1">
        <template #actions><button type="button" class="liquid-button" @click="retry">重新加载</button><RouterLink to="/profile" class="ghost-button">个人安全设置</RouterLink></template>
      </PageState>
      <PageState v-else-if="!route.meta.independent && user.permissionStatus !== 'ready'" title="正在确认工作空间权限" description="请稍候，当前操作将在权限确认后显示。" heading="h1" loading />
      <RouterView v-else v-slot="{ Component }"><component :is="Component" :key="viewKey" /></RouterView>
    </main>
    <WorkspaceCatalog v-if="user.workspaceId && !route.meta.independent && !accessStatusRoutes.has(String(route.name)) && user.can('catalog.metadata')"
      :key="`${user.user?.id}:${user.permissionKey}`" :inert="navigationOpen" />
  </div>
</template>

