import { pageTitles } from '../navigation'
import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router'
import { useUserStore } from '../stores/user'
import { iamUiEnabled } from '../utils/iam-mode'
import { handlingCenterCategories } from '../utils/editorial-navigation'

declare module 'vue-router' {
  interface RouteMeta {
    title?: string
    icon?: string
    public?: boolean
    /** 允许访问的角色编码；不设置表示所有登录用户均可访问 */
    roles?: string[]
    capability?:string
    action?: string
    anyAction?: string[]
    independent?: boolean
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
  { path: '/account/setup', name: 'account-setup', component: () => import('../views/auth/AccountCredentialView.vue'), meta: { title: '设置账号密码', public: true } },
  { path: '/q/:token', name: 'public-query', component: () => import('../views/public/PublicShareView.vue'), meta: {title: '当前分享不可用', public: true} },
  {
    path: '/s/:token',
    name: 'public-share',
    component: () => import('../views/public/PublicShareView.vue'),
    meta: { title: '当前分享不可用', public: true },
  },
  {
    path: '/',
    component: iamUiEnabled ? () => import('../layouts/AdaptiveIAMLayout.vue') : () => import('../layouts/MainLayout.vue'),
    redirect: '/home',
    children: [
      {path:'space',name:'space-management',component:()=>import('../views/governance/SpaceView.vue'),meta:{title:'空间管理',action:'space.page'}},
      {path:'spaces',name:'workspace-directory',component:()=>import('../views/governance/WorkspaceDirectoryView.vue'),meta:{title:'空间管理',independent:true}},
      {path:'handling',name:'handling-center',component:()=>import('../views/governance/HandlingCenterView.vue'),meta:{title:'办理中心'}},
      {path:'sources',name:'source-management',component:()=>import('../views/governance/SourcesView.vue'),meta:{title:'数据管理',action:'source.page'}},
      {path:'profile',name:'personal-profile',component:()=>import('../views/governance/PersonalView.vue'),meta:{title:'个人设置与安全',independent:true}},
      {path:'security',redirect:'/profile'},
      {path:'notifications',name:'personal-notifications',component:()=>import('../views/governance/NotificationsView.vue'),meta:{title:'通知',independent:true}},
      {path:'platform',name:'platform-management',component:()=>import('../views/governance/PlatformView.vue'),meta:{title:'平台管理',independent:true}},
      {path:'governance-recovery',name:'governance-recovery',component:()=>import('../views/governance/RecoveryView.vue'),meta:{title:'治理恢复',independent:true}},
      {path:'no-menu',name:'no-menu',component:()=>import('../views/governance/EmptyAccessView.vue'),meta:{title:'暂无可用菜单',independent:true}},
      {path:'access-denied',name:'access-denied',component:()=>import('../views/governance/EmptyAccessView.vue'),meta:{title:'操作权限不足',independent:true}},
      {path:'workspace-link',name:'workspace-link',component:()=>import('../views/governance/WorkspaceLinkView.vue'),meta:{title:'确认原工作空间',independent:true}},
      {path:'home',name:'business-home',component:()=>import('../views/business/BusinessHome.vue'),meta:{title:'业务工作台'}},
      {path:'business-operations',name:'business-operations',component:()=>import('../views/business/BusinessOperations.vue'),meta:{title:'受控业务操作'}},
      {path:'metrics',name:'metrics',component:()=>import('../views/business/MetricCatalog.vue'),meta:{title:'指标与数据集目录'}},
      {path:'inbox',name:'inbox',component:iamUiEnabled?()=>import('../views/governance/TasksView.vue'):()=>import('../views/inbox/InboxView.vue'),meta:{title:iamUiEnabled?'待办中心':pageTitles.inbox}},
      {path:'source-tasks',name:'source-tasks',component:()=>import('../views/connection/SourceTasksView.vue'),meta:{title:pageTitles.sourceTasks,anyAction:['connection.task.view','source.task.view']}},
      {
        path: 'dashboard',
        name: 'dashboard',
        component: iamUiEnabled ? () => import('../views/governance/RuntimeView.vue') : () => import('../views/dashboard/DashboardView.vue'),
        meta: { title: iamUiEnabled ? '空间运行统计' : pageTitles.dashboard, icon: 'LayoutDashboard', action: 'source.runtime.view' },
      },
      {
        path: 'assets',
        name: 'assets',
        component: iamUiEnabled ? () => import('../views/governance/CatalogEntryView.vue') : () => import('../views/asset/AssetView.vue'),
        meta: { title: pageTitles.assets, icon: 'Boxes' },
      },
      {
        path: 'connections',
        name: 'connections',
        component: iamUiEnabled ? () => import('../views/governance/PermissionEntryView.vue') : () => import('../views/connection/ConnectionView.vue'),
        meta: { title: pageTitles.connections, icon: 'Database' },
      },
      {
        path: 'query',
        name: 'query',
        component: iamUiEnabled ? () => import('../views/governance/PermissionEntryView.vue') : () => import('../views/query/QueryView.vue'),
        meta: { title: pageTitles.query, icon: 'SquareTerminal' },
      },
      {
        path: 'reports',
        name: 'reports',
        component: iamUiEnabled ? () => import('../views/governance/PermissionEntryView.vue') : () => import('../views/report/ReportsView.vue'),
        meta: { title: pageTitles.reports, icon: 'BarChart3' },
      },
      {
        path: 'dashboards',
        name: 'dashboards',
        component: iamUiEnabled ? () => import('../views/governance/PermissionEntryView.vue') : () => import('../views/dashboard/ManagedDashboardsView.vue'),
        meta: { title: pageTitles.dashboards, icon: 'LayoutDashboard' },
      },
      {
        path: 'proxies',
        name: 'proxies',
        component: () => import('../views/proxy/ProxyView.vue'),
        meta: { title: pageTitles.proxies, icon: 'Waypoints', action: 'connection.proxy.manage' },
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
        meta: { title: pageTitles.audit, icon: 'ShieldCheck', capability:'audit_view' },
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

router.beforeEach(async (to,from) => {
  const userStore = useUserStore()
  if (!to.meta.public && to.name !== 'login' && !userStore.isLoggedIn) {
    return { name: 'login', query: { redirect: to.fullPath } }
  }
  if (to.name === 'login' && userStore.isLoggedIn && to.query.sso!=='complete') {
    return { path: '/' }
  }
  if(typeof to.meta.title==='string')document.title=`${to.meta.title} · 知数`
  if (to.path === '/connections' && (to.query.tab === 'proxies' || to.query.tab === 'tasks')) {
    const { tab, ...query } = to.query
    return { path: tab === 'proxies' ? '/proxies' : '/source-tasks', query, hash: to.hash, replace: true }
  }
  if(iamUiEnabled&&to.path==='/settings'&&to.query.section==='operations'){
    const { section: _section, ...query } = to.query
    return { path: '/platform', query: { ...query, tab: 'operations' }, hash: to.hash, replace: true }
  }
  if(iamUiEnabled&&!to.meta.public&&userStore.isLoggedIn){
    if (to.name === 'platform-management' && to.query.tab === 'spaces') {
      return { name: 'workspace-directory', query: { q: to.query.q, page: to.query.page }, replace: true }
    }
    if (to.name === 'space-management' && !Object.keys(to.query).length && !to.hash) {
      return { name: 'workspace-directory', replace: true }
    }
    if(to.meta.independent){void userStore.loadWorkspaces().catch(()=>{});return true}
    try{await userStore.loadWorkspaces();await userStore.loadPermissions()}catch{return true}
    const desired=Number(to.query.workspace_id||0)
    const originWorkspace=Number(from.query.workspace_id||0)
    const assetDirectory = to.path === '/assets' || to.path === '/assets/'
    const hasObject=Object.keys(to.query).some(key=>['report','report_id','dashboard','dashboard_id','connection','connection_id','task','task_id','ticket','request','export','delivery','team_delivery','incident','subscription'].includes(key)
      || (assetDirectory && ['asset_connection', 'table'].includes(key))
      || (to.name === 'space-management' && ['member_id', 'proposal_id', 'return_proposal_id'].includes(key)))
    if(to.query.workspace_id!==undefined&&(!Number.isSafeInteger(desired)||desired<=0))return{path:'/workspace-link',query:{from:to.fullPath,reason:'invalid_workspace'},replace:true}
    if(desired&&desired!==userStore.workspaceId||hasObject&&!desired&&!from.query.workspace_id)return{path:'/workspace-link',query:{from:to.fullPath,...(desired?{workspace_id:String(desired)}:{})},replace:true}
    if(hasObject&&!desired&&from.query.workspace_id!==undefined&&(!Number.isSafeInteger(originWorkspace)||originWorkspace<=0))return{path:'/workspace-link',query:{from:to.fullPath,reason:'invalid_workspace'},replace:true}
    if(hasObject&&!desired&&originWorkspace&&originWorkspace!==userStore.workspaceId)return{path:'/workspace-link',query:{from:to.fullPath,workspace_id:String(originWorkspace)},replace:true}
    // Canonicalize entry pages too, including the first page after login. This
    // gives their later object links an explicit origin before any ID is used.
    if(!desired&&userStore.workspaceId)return{path:to.path,query:{...to.query,workspace_id:String(userStore.workspaceId)},replace:true}
    if (to.path === '/home' && userStore.workspaceId) {
      const businessView = to.query.view === 'business'
      if (businessView && userStore.effective && !userStore.actionControlled) return true
      const query = { ...to.query }
      if (businessView) delete query.view
      return { path: userStore.safeHome, query, replace: true }
    }
    if (to.name === 'source-management' && (to.query.tab === 'tasks' || to.query.tab === 'runtime')) {
      const { tab, ...query } = to.query
      return { path: tab === 'tasks' ? '/source-tasks' : '/dashboard', query, hash: to.hash, replace: true }
    }
    if (userStore.workspaceId && userStore.permissionStatus === 'ready' && (
      to.name === 'handling-center' && !handlingCenterCategories(userStore.handlingMenus ?? userStore.visibleMenus).length ||
      to.name === 'space-management' && !userStore.can('space.page')
    )) {
      return { path: '/access-denied', query: { from: to.fullPath }, replace: true }
    }
    // Unconfirmed users retain their complete legacy tools and role rules, but
    // workspace origin validation applies before either permission mode.
    if(userStore.effective&&!userStore.actionControlled){
      if(to.meta.capability&&!userStore.hasCapability(to.meta.capability))return{path:'/home'}
      if(to.meta.roles&&userStore.workspaceId&&!to.meta.roles.includes(userStore.role))return{path:'/dashboard'}
      return true
    }
    if(!userStore.workspaceId)return true
    if(to.path==='/reports'&&to.query.view==='accounts')return{path:'/sources',query:{workspace_id:String(userStore.workspaceId),tab:'accounts'},replace:true}
    const oldRedirects:Record<string,{path:string;query?:Record<string,string>}>={
      '/users':{path:'/space',query:{tab:'members'}},'/settings':{path:'/profile'},'/metrics':{path:'/assets',query:{tab:'metrics'}}}
    const redirect=oldRedirects[to.path];if(redirect)return{path:redirect.path,query:{...to.query,...redirect.query},replace:true}
    const actions:Record<string,string>={'/reports':'report.page','/dashboards':'dashboard.page','/business-operations':'operation.page','/query':'query.page','/assets':'catalog.page','/connections':'connection.page','/tickets':'change.page','/inbox':'task.page','/space':'space.page','/sources':'source.page','/audit':'audit.page'}
    const action=to.meta.action??actions[assetDirectory ? '/assets' : to.path]
    if (action && !userStore.can(action) || to.meta.anyAction && !to.meta.anyAction.some(userStore.can)) {
      return { path: '/access-denied', query: { from: to.fullPath }, replace: true }
    }
    return true
  }
  if(!to.meta.public&&userStore.isLoggedIn){try{await userStore.loadWorkspaces()}catch{return false}}
  // 角色守卫：meta.roles 不包含当前角色时回退到仪表盘
  const allowed = to.meta.roles
  if(to.meta.capability&&!userStore.hasCapability(to.meta.capability))return{path:'/home'}
  if (allowed && userStore.user && userStore.workspaceId && !allowed.includes(userStore.role)) {
    return { path: '/dashboard' }
  }
  if (typeof to.meta.title === 'string') {
    document.title = `${to.meta.title} · 知数`
  }
  return true
})

export default router
