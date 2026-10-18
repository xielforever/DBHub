import { pageTitles } from '../navigation'
import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router'
import { useUserStore } from '../stores/user'

declare module 'vue-router' {
  interface RouteMeta {
    title?: string
    icon?: string
    public?: boolean
    /** 允许访问的角色编码；不设置表示所有登录用户均可访问 */
    roles?: string[]
  }
}

/** 全部业务页面采用路由懒加载 */
const routes: RouteRecordRaw[] = [
  {
    path: '/login',
    name: 'login',
    component: () => import('../views/auth/LoginView.vue'),
    meta: { title: '登录', public: true },
  },
  { path: '/q/:token', name: 'public-query', component: () => import('../views/public/PublicQueryView.vue'), meta: {title: '查询分享', public: true} },
  {
    path: '/s/:token',
    name: 'public-share',
    component: () => import('../views/public/PublicShareView.vue'),
    meta: { title: '分享', public: true },
  },
  {
    path: '/',
    component: () => import('../layouts/MainLayout.vue'),
    redirect: '/dashboard',
    children: [
      {path:'inbox',name:'inbox',component:()=>import('../views/inbox/InboxView.vue'),meta:{title:pageTitles.inbox}},
      {path:'source-tasks',name:'source-tasks',component:()=>import('../views/connection/SourceTasksView.vue'),meta:{title:pageTitles.sourceTasks}},
      {
        path: 'dashboard',
        name: 'dashboard',
        component: () => import('../views/dashboard/DashboardView.vue'),
        meta: { title: pageTitles.dashboard, icon: 'LayoutDashboard' },
      },
      {
        path: 'assets',
        name: 'assets',
        component: () => import('../views/asset/AssetView.vue'),
        meta: { title: pageTitles.assets, icon: 'Boxes' },
      },
      {
        path: 'connections',
        name: 'connections',
        component: () => import('../views/connection/ConnectionView.vue'),
        meta: { title: pageTitles.connections, icon: 'Database' },
      },
      {
        path: 'query',
        name: 'query',
        component: () => import('../views/query/QueryView.vue'),
        meta: { title: pageTitles.query, icon: 'SquareTerminal' },
      },
      {
        path: 'reports',
        name: 'reports',
        component: () => import('../views/report/ReportsView.vue'),
        meta: { title: pageTitles.reports, icon: 'BarChart3' },
      },
      {
        path: 'dashboards',
        name: 'dashboards',
        component: () => import('../views/dashboard/ManagedDashboardsView.vue'),
        meta: { title: pageTitles.dashboards, icon: 'LayoutDashboard' },
      },
      {
        path: 'proxies',
        name: 'proxies',
        component: () => import('../views/proxy/ProxyView.vue'),
        meta: { title: pageTitles.proxies, icon: 'Waypoints' },
      },
      {
        path: 'tickets',
        name: 'tickets',
        component: () => import('../views/ticket/TicketView.vue'),
        meta: { title: pageTitles.tickets, icon: 'Ticket' },
      },
      {
        path: 'users',
        name: 'users',
        component: () => import('../views/admin/UsersView.vue'),
        meta: { title: pageTitles.users, icon: 'UsersRound', roles: ['admin'] },
      },
      {
        path: 'audit',
        name: 'audit',
        component: () => import('../views/audit/AuditView.vue'),
        meta: { title: pageTitles.audit, icon: 'ShieldCheck', roles: ['admin'] },
      },
      {
        path: 'settings',
        name: 'settings',
        component: () => import('../views/settings/SettingsView.vue'),
        meta: { title: pageTitles.settings, icon: 'Settings' },
      },
    ],
  },
  { path: '/:pathMatch(.*)*', redirect: '/' },
]

const router = createRouter({
  history: createWebHistory(),
  routes,
})

router.beforeEach(async (to) => {
  const userStore = useUserStore()
  if (!to.meta.public && to.name !== 'login' && !userStore.isLoggedIn) {
    return { name: 'login', query: { redirect: to.fullPath } }
  }
  if (to.name === 'login' && userStore.isLoggedIn) {
    return { path: '/' }
  }
  if(!to.meta.public&&userStore.isLoggedIn){try{await userStore.loadWorkspaces()}catch{return false}}
  // 角色守卫：meta.roles 不包含当前角色时回退到仪表盘
  const allowed = to.meta.roles
  if (allowed && userStore.user && userStore.workspaceId && !allowed.includes(userStore.role)) {
    return { path: '/dashboard' }
  }
  if (typeof to.meta.title === 'string') {
    document.title = `${to.meta.title} · DBHub`
  }
  return true
})

export default router
