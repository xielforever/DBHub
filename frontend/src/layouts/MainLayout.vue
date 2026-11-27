<script setup lang="ts">
import BrandLogo from '../components/base/BrandLogo.vue'
import { pageTitles } from '../navigation'
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  Activity,
  BarChart3,
  Boxes,
  ChevronDown,
  Database,
  LogOut,
  Menu,
  PanelLeftClose,
  PanelLeftOpen,
  Search,
  Settings,
  ShieldCheck,
  SquareTerminal,
  Ticket,
  UsersRound,
  X,
} from 'lucide-vue-next'
import { useUserStore } from '../stores/user'
import type { HealthInfo } from '../types/api'
import GlobalSearch from '../components/GlobalSearch.vue'
import InboxButton from '../components/InboxButton.vue'
import WorkspaceSwitcher from '../components/base/WorkspaceSwitcher.vue'
import ThemeSwitcher from '../components/base/ThemeSwitcher.vue'

const route = useRoute()
const router = useRouter()
const userStore = useUserStore()

/** 全站一级菜单：分组、与路由一一对应、无重复无假链接；安全组仅 admin 可见 */
interface MenuItem {
  name: string
  label: string
  to: string
  icon: unknown
  roles?: string[]
  capability?:string
}
const menuGroups: { title: string; items: MenuItem[] }[] = [
  {
    title: '概览',
    items: [{ name: 'business-home', label: '业务工作台', to: '/home', icon: BarChart3 }, {name:'business-operations',label:'受控业务操作',to:'/business-operations',icon:Ticket}, { name: 'dashboard', label: pageTitles.dashboard, to: '/dashboard', icon: Activity }],
  },
  {
    title: '数据',
    items: [
      { name: 'assets', label: pageTitles.assets, to: '/assets', icon: Boxes },
      { name: 'connections', label: pageTitles.connections, to: '/connections', icon: Database },
      { name: 'query', label: pageTitles.query, to: '/query', icon: SquareTerminal },
      { name: 'reports', label: pageTitles.reports, to: '/reports', icon: BarChart3 },
      { name: 'metrics', label: '指标口径', to: '/metrics', icon: BarChart3 },
      { name: 'dashboards', label: pageTitles.dashboards, to: '/dashboards', icon: BarChart3 },
      { name: 'tickets', label: pageTitles.tickets, to: '/tickets', icon: Ticket },
    ],
  },
  {
    title: '安全',
    items: [
      { name: 'users', label: pageTitles.users, to: '/users', icon: UsersRound, roles: ['admin'] },
      { name: 'audit', label: pageTitles.audit, to: '/audit', icon: ShieldCheck, capability:'audit_view' },
    ],
  },
]
const visibleMenu = computed(() => {
  const role = userStore.role
  return menuGroups
    .map((g) => ({
      ...g,
      items: g.items.filter((i) => (!i.roles || i.roles.includes(role))&&(!i.capability||userStore.hasCapability(i.capability))),
    }))
    .filter((g) => g.items.length > 0)
})

function switchWorkspace(id:number){userStore.selectWorkspace(id);if(route.meta.roles&&!route.meta.roles.includes(userStore.role)||route.meta.capability&&!userStore.hasCapability(route.meta.capability))router.replace('/home')}
const COLLAPSE_KEY = 'dbhub_sidebar_collapsed'
const collapsed = ref(localStorage.getItem(COLLAPSE_KEY) === '1')
const mobileOpen = ref(false)

const currentTitle = computed(() => (route.meta.title as string) ?? '知数')
const avatarText = computed(() => (userStore.username || '?').slice(0, 1).toUpperCase())

function toggleCollapsed() {
  collapsed.value = !collapsed.value
  localStorage.setItem(COLLAPSE_KEY, collapsed.value ? '1' : '0')
}

// 路由切换后自动收起移动端抽屉
watch(
  () => route.fullPath,
  () => {
    mobileOpen.value = false
  },
)

const healthOk = ref(false)
onMounted(async () => {
  try {
    const resp = await fetch('/api/health')
    const body = await resp.json()
    healthOk.value = resp.ok && (body.data as HealthInfo)?.status === 'ok'
  } catch {
    healthOk.value = false
  }
})

async function onUserCommand(command: string) {
  if (command === 'logout') {
    try {
      await ElMessageBox.confirm('确定要退出当前登录吗？', '提示', {
        confirmButtonText: '退出',
        cancelButtonText: '取消',
        type: 'warning',
      })
    } catch {
      return
    }
    try {await userStore.logout();ElMessage.success('已退出，会话已撤销')} finally {router.replace({name:'login'})}
  } else if (command === 'settings') {
    router.push('/settings')
  }
}
</script>

<template>
  <div class="h-screen flex overflow-hidden">
    <!-- 移动端遮罩 -->
    <Transition name="fade">
      <div
        v-if="mobileOpen"
        class="fixed inset-0 z-30 bg-overlay backdrop-blur-sm lg:hidden"
        aria-hidden="true"
        @click="mobileOpen = false"
      />
    </Transition>

    <!-- 侧边栏：移动端抽屉，桌面端常驻且可折叠 -->
    <aside
      class="fixed lg:static inset-y-0 left-0 z-40 w-64 flex flex-col glass-rail
             transition-all duration-300 ease-out"
      :class="[
        collapsed ? 'lg:w-[4.5rem]' : 'lg:w-60',
        mobileOpen ? 'translate-x-0' : '-translate-x-full lg:translate-x-0',
      ]"
      aria-label="主导航"
    >
      <!-- 品牌区 -->
      <div class="h-16 shrink-0 flex items-center gap-3 px-4 border-b border-stroke">
        <BrandLogo :compact="collapsed" />
        <button
          class="ml-auto flex items-center justify-center rounded-lg text-muted hover:bg-interactive hover:text-content lg:hidden ui-control-target ui-icon-button"
          aria-label="关闭导航"
          @click="mobileOpen = false"
        >
          <X class="w-4.5 h-4.5" />
        </button>
      </div>

      <!-- 分组一级菜单（每项与真实路由一一对应，按角色显隐） -->
      <nav class="flex-1 overflow-y-auto px-3 py-4 space-y-4">
        <div v-for="group in visibleMenu" :key="group.title">
          <p
            v-if="!collapsed"
            class="px-2 pb-1.5 text-xs uppercase tracking-widest text-subtle"
          >
            {{ group.title }}
          </p>
          <p v-else class="px-2 pb-1.5 flex justify-center">
            <span class="w-6 h-px bg-surface-muted" />
          </p>
          <div class="space-y-1">
            <RouterLink
              v-for="item in group.items"
              :key="item.name"
              :to="item.to"
              class="nav-item"
              :class="collapsed ? 'lg:justify-center lg:px-2' : ''"
              :title="item.label"
            >
              <component :is="item.icon" class="w-5 h-5 shrink-0" />
              <span class="whitespace-nowrap" :class="collapsed ? 'lg:hidden' : ''">{{ item.label }}</span>
            </RouterLink>
          </div>
        </div>
      </nav>

      <!-- 底部：健康状态（系统设置已收归头像下拉，避免与侧栏入口重复） -->
      <div class="p-3 border-t border-stroke">
        <div
          class="flex items-center gap-2 px-2 py-2 rounded-xl text-xs text-muted"
          :class="collapsed ? 'lg:justify-center lg:px-0' : ''"
        >
          <span
            class="w-1.5 h-1.5 rounded-full shrink-0"
            :class="healthOk ? 'bg-success animate-pulse' : 'bg-warning'"
          />
          <span class="whitespace-nowrap" :class="collapsed ? 'lg:hidden' : ''">
            后端{{ healthOk ? '在线' : '检测中' }}
          </span>
        </div>
      </div>
    </aside>

    <!-- 主区域 -->
    <div class="flex-1 flex flex-col min-w-0">
      <header
        class="app-topbar shrink-0"
      >
        <!-- 移动端汉堡按钮 -->
        <button
          class="flex items-center justify-center rounded-xl text-muted hover:bg-interactive hover:text-content transition-colors lg:hidden ui-control-target ui-icon-button"
          aria-label="打开导航"
          @click="mobileOpen = true"
        >
          <Menu class="w-5 h-5" />
        </button>
        <!-- 桌面端折叠按钮 -->
        <button
          class="hidden lg:flex items-center justify-center rounded-xl text-muted hover:bg-interactive hover:text-content transition-colors ui-control-target ui-icon-button"
          :aria-label="collapsed ? '展开侧边栏' : '折叠侧边栏'"
          @click="toggleCollapsed"
        >
          <PanelLeftClose v-if="!collapsed" class="w-5 h-5" />
          <PanelLeftOpen v-else class="w-5 h-5" />
        </button>

        <!-- 当前页面标题 -->
        <span class="app-topbar__location"><span class="app-topbar__title">{{ currentTitle }}</span></span>

        <!-- 全局搜索（中等屏幕以上；移动端通过搜索图标进入资产目录） -->
        <div class="flex-1 min-w-0 max-w-md mx-auto hidden md:block">
          <GlobalSearch :key="userStore.workspaceId" />
        </div>

        <button
          class="hidden sm:flex md:hidden rounded-xl flex items-center justify-center text-muted hover:bg-interactive hover:text-content transition-colors shrink-0 ui-control-target ui-icon-button"
          aria-label="搜索数据目录"
          @click="router.push('/assets')"
        >
          <Search class="w-4.5 h-4.5" />
        </button>

        <div class="app-topbar__actions" aria-label="工作空间与账户操作">
        <WorkspaceSwitcher :options="userStore.workspaces" :model-value="userStore.workspaceId || undefined" @change="switchWorkspace" />
        <InboxButton />
        <ThemeSwitcher />
        <el-dropdown trigger="click" @command="onUserCommand">
          <button class="flex items-center gap-2.5 pl-1 pr-2 sm:pr-3 py-1.5 rounded-xl hover:bg-interactive transition-colors ui-control-target" :aria-label="`账号：${userStore.username || '未登录'}`">
            <span
              class="w-8 h-8 rounded-full flex items-center justify-center text-xs font-bold shrink-0 text-on-primary"
              style="background-image: var(--image-liquid-gradient)"
            >
              {{ avatarText }}
            </span>
            <span class="app-topbar__user">{{ userStore.username || '未登录' }}</span>
            <ChevronDown class="w-4 h-4 text-subtle" />
          </button>
          <template #dropdown>
            <el-dropdown-menu>
              <el-dropdown-item command="settings" :icon="Settings">系统设置</el-dropdown-item>
              <el-dropdown-item command="logout" :icon="LogOut" divided>退出登录</el-dropdown-item>
            </el-dropdown-menu>
          </template>
        </el-dropdown>
        </div>
      </header>

      <main
        class="flex-1 overflow-y-auto p-4 sm:p-6 outline-none"
        tabindex="0"
        aria-label="主内容区（可滚动）"
      >
        <RouterView v-slot="{ Component }">
          <Transition name="page-fade" mode="out-in">
            <component :is="Component" :key="`${userStore.workspaceId}:${String(route.name)}`" v-if="userStore.workspaceId" />
            <section v-else class="glass-panel p-8"><h1 class="text-xl">选择工作空间</h1><p class="text-muted mt-3">请在页头选择你要进入的组织工作空间。</p></section>
          </Transition>
        </RouterView>
      </main>
    </div>
  </div>
</template>



<style scoped>
.page-fade-enter-active,
.page-fade-leave-active {
  transition: opacity 0.18s ease;
}
.page-fade-enter-from,
.page-fade-leave-to {
  opacity: 0;
}
.fade-enter-active,
.fade-leave-active {
  transition: opacity 0.25s ease;
}
.fade-enter-from,
.fade-leave-to {
  opacity: 0;
}
</style>
