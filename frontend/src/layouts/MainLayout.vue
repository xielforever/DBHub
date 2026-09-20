<template>
  <div class="h-screen flex overflow-hidden">
    <!-- 移动端遮罩 -->
    <Transition name="fade">
      <div
        v-if="mobileOpen"
        class="fixed inset-0 z-30 bg-black/50 backdrop-blur-sm lg:hidden"
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
      <div class="h-16 shrink-0 flex items-center gap-3 px-4 border-b border-white/10">
        <span
          class="w-9 h-9 rounded-xl flex items-center justify-center shrink-0 shadow-lg"
          style="background-image: var(--image-liquid-gradient)"
        >
          <DatabaseZap class="w-5 h-5 text-white" />
        </span>
        <div v-if="!collapsed" class="leading-tight overflow-hidden">
          <p class="font-bold tracking-wide whitespace-nowrap">DBHub</p>
          <p class="text-[10px] text-white/40 whitespace-nowrap">数据管理平台</p>
        </div>
        <button
          class="ml-auto w-8 h-8 flex items-center justify-center rounded-lg text-white/50 hover:bg-white/10 hover:text-white lg:hidden"
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
            class="px-2 pb-1.5 text-[11px] uppercase tracking-widest text-white/40"
          >
            {{ group.title }}
          </p>
          <p v-else class="px-2 pb-1.5 flex justify-center">
            <span class="w-6 h-px bg-white/15" />
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
      <div class="p-3 border-t border-white/10">
        <div
          class="flex items-center gap-2 px-2 py-2 rounded-xl text-xs text-white/55"
          :class="collapsed ? 'lg:justify-center lg:px-0' : ''"
        >
          <span
            class="w-1.5 h-1.5 rounded-full shrink-0"
            :class="healthOk ? 'bg-emerald-400 animate-pulse' : 'bg-amber-400'"
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
        class="h-16 shrink-0 flex items-center gap-2 sm:gap-4 px-4 sm:px-6 border-b border-white/10 bg-white/5 backdrop-blur-xl z-10"
      >
        <!-- 移动端汉堡按钮 -->
        <button
          class="w-9 h-9 flex items-center justify-center rounded-xl text-white/60 hover:bg-white/10 hover:text-white transition-colors lg:hidden"
          aria-label="打开导航"
          @click="mobileOpen = true"
        >
          <Menu class="w-5 h-5" />
        </button>
        <!-- 桌面端折叠按钮 -->
        <button
          class="hidden lg:flex w-9 h-9 items-center justify-center rounded-xl text-white/60 hover:bg-white/10 hover:text-white transition-colors"
          :aria-label="collapsed ? '展开侧边栏' : '折叠侧边栏'"
          @click="toggleCollapsed"
        >
          <PanelLeftClose v-if="!collapsed" class="w-5 h-5" />
          <PanelLeftOpen v-else class="w-5 h-5" />
        </button>

        <!-- 当前页面标题 -->
        <h1 class="text-sm sm:text-base font-medium whitespace-nowrap">{{ currentTitle }}</h1>

        <!-- 全局搜索（中等屏幕以上；移动端通过搜索图标进入资产目录） -->
        <div class="flex-1 max-w-md mx-auto hidden md:block">
          <GlobalSearch />
        </div>
        <div class="flex-1 md:hidden" />

        <button
          class="md:hidden w-9 h-9 rounded-xl flex items-center justify-center text-white/55 hover:bg-white/10 hover:text-white transition-colors shrink-0"
          aria-label="搜索数据资产"
          @click="router.push('/assets')"
        >
          <Search class="w-4.5 h-4.5" />
        </button>

        <!-- 通知中心上线前隐藏铃铛，避免无响应假入口 -->
        <el-dropdown trigger="click" @command="onUserCommand">
          <button class="flex items-center gap-2.5 pl-1 pr-2 sm:pr-3 py-1.5 rounded-xl hover:bg-white/10 transition-colors">
            <span
              class="w-8 h-8 rounded-full flex items-center justify-center text-xs font-bold shrink-0"
              style="background-image: var(--image-liquid-gradient)"
            >
              {{ avatarText }}
            </span>
            <span class="text-sm text-white/85 hidden sm:inline">{{ userStore.username || '未登录' }}</span>
            <ChevronDown class="w-4 h-4 text-white/40" />
          </button>
          <template #dropdown>
            <el-dropdown-menu>
              <el-dropdown-item command="settings" :icon="Settings">系统设置</el-dropdown-item>
              <el-dropdown-item command="logout" :icon="LogOut" divided>退出登录</el-dropdown-item>
            </el-dropdown-menu>
          </template>
        </el-dropdown>
      </header>

      <main
        class="flex-1 overflow-y-auto p-4 sm:p-6 outline-none"
        tabindex="0"
        aria-label="主内容区（可滚动）"
      >
        <RouterView v-slot="{ Component }">
          <Transition name="page-fade" mode="out-in">
            <component :is="Component" />
          </Transition>
        </RouterView>
      </main>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  BarChart3,
  Boxes,
  ChevronDown,
  Database,
  DatabaseZap,
  LayoutDashboard,
  LogOut,
  Menu,
  PanelLeftClose,
  PanelLeftOpen,
  Search,
  Settings,
  ShieldCheck,
  SquareTerminal,
  UsersRound,
  X,
} from 'lucide-vue-next'
import { useUserStore } from '../stores/user'
import type { HealthInfo } from '../types/api'
import GlobalSearch from '../components/GlobalSearch.vue'

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
}
const menuGroups: { title: string; items: MenuItem[] }[] = [
  {
    title: '概览',
    items: [{ name: 'dashboard', label: '仪表盘', to: '/dashboard', icon: LayoutDashboard }],
  },
  {
    title: '数据',
    items: [
      { name: 'assets', label: '数据资产', to: '/assets', icon: Boxes },
      { name: 'connections', label: '数据源管理', to: '/connections', icon: Database },
      { name: 'query', label: 'SQL 工作台', to: '/query', icon: SquareTerminal },
      { name: 'reports', label: '报表中心', to: '/reports', icon: BarChart3 },
    ],
  },
  {
    title: '安全',
    items: [
      { name: 'users', label: '用户与权限', to: '/users', icon: UsersRound, roles: ['admin'] },
      { name: 'audit', label: '操作审计', to: '/audit', icon: ShieldCheck, roles: ['admin'] },
    ],
  },
]
const visibleMenu = computed(() => {
  const role = userStore.user?.role ?? ''
  return menuGroups
    .map((g) => ({
      ...g,
      items: g.items.filter((i) => !i.roles || i.roles.includes(role)),
    }))
    .filter((g) => g.items.length > 0)
})

const COLLAPSE_KEY = 'dbhub_sidebar_collapsed'
const collapsed = ref(localStorage.getItem(COLLAPSE_KEY) === '1')
const mobileOpen = ref(false)

const currentTitle = computed(() => (route.meta.title as string) ?? 'DBHub')
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
    userStore.logout()
    ElMessage.success('已安全退出')
    router.replace({ name: 'login' })
  } else if (command === 'settings') {
    router.push('/settings')
  }
}
</script>

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
