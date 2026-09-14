<template>
  <div class="h-screen flex overflow-hidden">
    <!-- 一级：图标导航栏 -->
    <nav
      class="glass-rail w-16 shrink-0 flex flex-col items-center py-4 gap-2 z-20"
      aria-label="主导航"
    >
      <RouterLink to="/dashboard" class="mb-3" aria-label="DBHub 首页">
        <span
          class="w-10 h-10 rounded-xl flex items-center justify-center shadow-lg"
          style="background-image: var(--image-liquid-gradient)"
        >
          <DatabaseZap class="w-5 h-5 text-white" />
        </span>
      </RouterLink>

      <RouterLink
        v-for="item in railItems"
        :key="item.name"
        :to="item.to"
        class="rail-icon"
        :aria-label="item.title"
      >
        <component :is="item.icon" class="w-5 h-5" />
      </RouterLink>

      <div class="flex-1" />
      <RouterLink to="/settings" class="rail-icon" aria-label="系统设置">
        <Settings class="w-5 h-5" />
      </RouterLink>
    </nav>

    <!-- 二级：功能侧边栏 -->
    <aside
      class="w-60 shrink-0 flex flex-col z-10 bg-white/[0.03] border-r border-white/10"
      aria-label="模块导航"
    >
      <div class="px-5 pt-6 pb-4">
        <h2 class="text-base font-semibold tracking-wide">{{ currentTitle }}</h2>
        <p class="text-xs text-white/40 mt-1">{{ currentSubtitle }}</p>
      </div>

      <nav class="flex-1 px-3 space-y-1 overflow-y-auto">
        <template v-for="group in menuGroups" :key="group.label">
          <p class="px-3.5 pt-4 pb-2 text-[11px] uppercase tracking-widest text-white/30">
            {{ group.label }}
          </p>
          <RouterLink
            v-for="item in group.items"
            :key="item.name"
            :to="item.to"
            class="nav-item"
          >
            <component :is="item.icon" class="w-4 h-4 shrink-0" />
            <span>{{ item.title }}</span>
          </RouterLink>
        </template>
      </nav>

      <!-- 后端健康状态 -->
      <div class="m-3 p-3 rounded-xl bg-white/5 border border-white/10">
        <div class="flex items-center gap-2 text-xs text-white/55">
          <span
            class="w-1.5 h-1.5 rounded-full"
            :class="healthOk ? 'bg-emerald-400 animate-pulse' : 'bg-amber-400'"
          />
          后端 {{ healthOk ? '在线' : '检测中' }}
        </div>
      </div>
    </aside>

    <!-- 主区域 -->
    <div class="flex-1 flex flex-col min-w-0">
      <header
        class="h-16 shrink-0 flex items-center gap-4 px-6 border-b border-white/10 bg-white/5 backdrop-blur-xl z-10"
      >
        <div class="flex-1 max-w-md relative">
          <Search class="w-4 h-4 text-white/35 absolute left-3.5 top-1/2 -translate-y-1/2" />
          <input
            class="glass-input pl-10 py-2 text-sm"
            placeholder="搜索数据源、数据表、查询历史..."
            type="text"
          />
        </div>
        <div class="flex-1" />

        <button class="w-9 h-9 rounded-xl flex items-center justify-center text-white/55 hover:bg-white/10 hover:text-white transition-colors" aria-label="通知">
          <Bell class="w-5 h-5" />
        </button>

        <el-dropdown trigger="click" @command="onUserCommand">
          <button class="flex items-center gap-2.5 pl-2 pr-3 py-1.5 rounded-xl hover:bg-white/10 transition-colors">
            <span
              class="w-8 h-8 rounded-full flex items-center justify-center text-xs font-bold"
              style="background-image: var(--image-liquid-gradient)"
            >
              {{ avatarText }}
            </span>
            <span class="text-sm text-white/85">{{ userStore.username || '未登录' }}</span>
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

      <main class="flex-1 overflow-y-auto p-6">
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
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  Bell,
  ChevronDown,
  Clock3,
  Database,
  DatabaseZap,
  LayoutDashboard,
  LogOut,
  Search,
  Settings,
  ShieldCheck,
  SquareTerminal,
  Table2,
} from 'lucide-vue-next'
import { useUserStore } from '../stores/user'
import type { HealthInfo } from '../types/api'

const route = useRoute()
const router = useRouter()
const userStore = useUserStore()

const railItems = [
  { name: 'dashboard', title: '仪表盘', to: '/dashboard', icon: LayoutDashboard },
  { name: 'connections', title: '数据源管理', to: '/connections', icon: Database },
  { name: 'query', title: 'SQL 工作台', to: '/query', icon: SquareTerminal },
  { name: 'audit', title: '操作审计', to: '/audit', icon: ShieldCheck },
]

const menuGroups = [
  {
    label: '概览',
    items: [{ name: 'dashboard', title: '仪表盘', to: '/dashboard', icon: LayoutDashboard }],
  },
  {
    label: '数据',
    items: [
      { name: 'connections', title: '数据源管理', to: '/connections', icon: Database },
      { name: 'query', title: 'SQL 工作台', to: '/query', icon: SquareTerminal },
      { name: 'tables', title: '表设计器', to: '/query', icon: Table2 },
    ],
  },
  {
    label: '安全',
    items: [
      { name: 'audit', title: '操作审计', to: '/audit', icon: ShieldCheck },
      { name: 'history', title: '查询历史', to: '/audit', icon: Clock3 },
    ],
  },
]

const subtitles: Record<string, string> = {
  dashboard: '平台运行状态一览',
  connections: '统一管理多源数据库连接',
  query: '编写、运行与分析 SQL',
  audit: '全链路操作可追溯',
  settings: '偏好与安全配置',
}

const currentTitle = computed(() => (route.meta.title as string) ?? 'DBHub')
const currentSubtitle = computed(() => subtitles[String(route.name)] ?? '')
const avatarText = computed(() => (userStore.username || '?').slice(0, 1).toUpperCase())

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
</style>
