import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router'
import { useUserStore } from '../stores/user'

/** 全部业务页面采用路由懒加载 */
const routes: RouteRecordRaw[] = [
  {
    path: '/login',
    name: 'login',
    component: () => import('../views/auth/LoginView.vue'),
    meta: { title: '登录', public: true },
  },
  {
    path: '/',
    component: () => import('../layouts/MainLayout.vue'),
    redirect: '/dashboard',
    children: [
      {
        path: 'dashboard',
        name: 'dashboard',
        component: () => import('../views/dashboard/DashboardView.vue'),
        meta: { title: '仪表盘', icon: 'LayoutDashboard' },
      },
      {
        path: 'connections',
        name: 'connections',
        component: () => import('../views/connection/ConnectionView.vue'),
        meta: { title: '数据源管理', icon: 'Database' },
      },
      {
        path: 'query',
        name: 'query',
        component: () => import('../views/query/QueryView.vue'),
        meta: { title: 'SQL 工作台', icon: 'SquareTerminal' },
      },
      {
        path: 'audit',
        name: 'audit',
        component: () => import('../views/audit/AuditView.vue'),
        meta: { title: '操作审计', icon: 'ShieldCheck' },
      },
      {
        path: 'settings',
        name: 'settings',
        component: () => import('../views/settings/SettingsView.vue'),
        meta: { title: '系统设置', icon: 'Settings' },
      },
    ],
  },
  { path: '/:pathMatch(.*)*', redirect: '/' },
]

const router = createRouter({
  history: createWebHistory(),
  routes,
})

router.beforeEach((to) => {
  const userStore = useUserStore()
  if (to.name !== 'login' && !userStore.isLoggedIn) {
    return { name: 'login', query: { redirect: to.fullPath } }
  }
  if (to.name === 'login' && userStore.isLoggedIn) {
    return { path: '/' }
  }
  if (typeof to.meta.title === 'string') {
    document.title = `${to.meta.title} · DBHub`
  }
  return true
})

export default router
