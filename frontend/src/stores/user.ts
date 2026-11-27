import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { iamApi } from '../api/iam'
import { setAuthCookie, clearAuthCookie } from '../utils/auth-cookie'
import request from '../utils/request'
import { changeWorkspaceContext } from '../utils/workspace-context'
import { workspaceGeneration } from '../utils/workspace-context'
import { iamUiEnabled } from '../utils/iam-mode'
import { requiresActionChecks } from '../utils/permission'
import type { Effective } from '../types/iam'
export interface Workspace {id:number;name:string;role:string;capabilities?:string[];allow_unknown_freshness:boolean}
import type { LoginResult, LoginOutcome, UserInfo } from '../types/api'

const TOKEN_KEY = 'dbhub_access_token'
const REFRESH_KEY = 'dbhub_refresh_token'
const USER_KEY = 'dbhub_user'

function readUser(): UserInfo | null {
  const raw = localStorage.getItem(USER_KEY)
  if (!raw) return null
  try {
    return JSON.parse(raw) as UserInfo
  } catch {
    return null
  }
}

// Time-bound grants/releases can expire without incrementing a stored epoch.
// Changes to the effective boundary must still discard cached pages and late
// responses; ordering of an otherwise identical permission set is immaterial.
function permissionBoundary(value:Effective|null):string{
 if(!value)return ''
 const s=value.snapshot
 return JSON.stringify([s.target,!!s.rollback_guard,!!s.governance_only,s.mode,s.legacy_data_role,[...value.actions].sort(),[...(s.denied_actions||[])].sort()])
}

/** 认证状态仓库：令牌、当前用户与登录/登出动作 */
export const useUserStore = defineStore('user', () => {
  const token = ref<string>(localStorage.getItem(TOKEN_KEY) ?? '')
  const refreshToken = ref<string>(localStorage.getItem(REFRESH_KEY) ?? '')
  const user = ref<UserInfo | null>(readUser())

  const isLoggedIn = computed(() => Boolean(token.value))
  const username = computed(() => user.value?.username ?? '')
  const platformAdmin=ref(false)
  const canRecover=ref(false)
  const workspaces=ref<Workspace[]>([])
  const workspaceId=ref(Number(localStorage.getItem('dbhub_workspace_id')||0))
  const migrationRollbackBundleId = ref('')
  const workspace=computed(()=>workspaces.value.find(w=>w.id===workspaceId.value))
  const effective=ref<Effective|null>(null)
  const role = computed(() => iamUiEnabled&&effective.value?effective.value.snapshot.legacy_data_role:workspace.value?.role ?? '')
  const actionControlled=computed(()=>iamUiEnabled&&requiresActionChecks(effective.value?.snapshot))
  const permissionStatus=ref<'idle'|'loading'|'ready'|'error'>('idle')
  const permissionError=ref('')
  const permissionKey=computed(()=>`${workspaceId.value}:${effective.value?.snapshot.config_version??0}:${effective.value?.snapshot.authorization_epoch??0}:${effective.value?.snapshot.membership_version??0}:${permissionBoundary(effective.value)}`)
  let permissionLoading:Promise<void>|undefined,lastPermissionLoad=0
  const can=(action:string)=>permissionStatus.value==='ready'&&!!effective.value?.actions.includes(action)
  const legacyNames=['report_review','datasource_manage','audit_view','operation_review']
  const hasCapability=(name:string)=>{
    if(!iamUiEnabled)return role.value==='admin'||!!workspace.value?.capabilities?.includes(name)
    if(!legacyNames.includes(name))return false
    if(actionControlled.value){const actions:Record<string,string[]>={report_review:['report.review','dashboard.review','report.publish','dashboard.publish'],datasource_manage:['source.definition.manage','source.account.manage','source.driver.manage'],audit_view:['audit.page'],operation_review:['operation.review']};return actions[name]?.some(can)??false}
    return role.value==='admin'||!!workspace.value?.capabilities?.includes(name)
  }
  function clearPermissions(){effective.value=null;permissionStatus.value='idle';permissionError.value='';permissionLoading=undefined;lastPermissionLoad=0}
  function applyPermissions(data: Effective) {
    const previous = effective.value
    if (previous && (
      previous.snapshot.config_version !== data.snapshot.config_version ||
      previous.snapshot.authorization_epoch !== data.snapshot.authorization_epoch ||
      previous.snapshot.membership_version !== data.snapshot.membership_version ||
      permissionBoundary(previous) !== permissionBoundary(data)
    )) changeWorkspaceContext()
    effective.value = data
    permissionStatus.value = 'ready'
    permissionError.value = ''
    lastPermissionLoad = Date.now()
  }
  async function loadPermissions(force=false){
    if(!iamUiEnabled||!workspaceId.value)return
    if(permissionLoading)return permissionLoading
    if(!force&&permissionStatus.value==='ready'&&Date.now()-lastPermissionLoad<10000)return
    const wid=workspaceId.value,identity=identityGeneration,generation=workspaceGeneration()
    if(!effective.value)permissionStatus.value='loading'
    const work=(async()=>{try{const data=await iamApi.effective();if(wid!==workspaceId.value||identity!==identityGeneration||generation!==workspaceGeneration())return
      applyPermissions(data)
    }catch(error){if(wid!==workspaceId.value||identity!==identityGeneration||generation!==workspaceGeneration())return;effective.value=null;permissionStatus.value='error';permissionError.value=error instanceof Error?error.message:'权限服务暂不可用';throw error}})()
    permissionLoading=work;try{await work}finally{if(permissionLoading===work)permissionLoading=undefined}
  }
  const allowedMenuEntries = computed(() => {
    if (!effective.value || permissionStatus.value !== 'ready') return []
    const catalog = effective.value.catalog.menus
    return [...effective.value.menus.items].sort((a, b) => a.order - b.order).flatMap(item => {
      const menu = catalog.find(entry => entry.id === item.id)
      if (!menu || !can(menu.capability)) return []
      return [{ ...menu, label: item.label, group: item.group, catalogGroup: menu.group, visible: item.visible }]
    })
  })
  const visibleMenus = computed(() => {
    const snapshot = effective.value
    const allowedIds = snapshot?.matrix_enabled ? new Set(snapshot.visible_menu_ids ?? []) : undefined
    return allowedMenuEntries.value.filter(item => item.visible && (!allowedIds || allowedIds.has(item.id)))
  })
  const handlingMenus = computed(() => effective.value?.matrix_enabled ? allowedMenuEntries.value : visibleMenus.value)
  const safeHome=computed(()=>{
    const personal=visibleMenus.value.find(m=>m.id===effective.value?.personal_home)
    const preferred=visibleMenus.value.find(m=>m.id===effective.value?.menus.preferred_menu)
    const path=(personal??preferred??visibleMenus.value[0])?.path??'/no-menu'
    return path === '/space' ? '/spaces' : path
  })
  let spacesLoaded=false,spacesLoading:Promise<void>|undefined,identityGeneration=0
  // Requests keep this generation across refresh/retry, including global APIs.
  const identityEpoch=()=>identityGeneration
  async function loadWorkspaces(force=false){
    if(spacesLoaded&&!force)return
    if(spacesLoading)return spacesLoading
    const identity=identityGeneration
    const work=(async()=>{const [spaces,platform]=await Promise.all([request.get<unknown,Workspace[]>('/workspaces',{silentError:true}),request.get<unknown,{is_platform_admin:boolean;can_governance_recover?:boolean}>('/platform/status',{silentError:true})]);if(identity!==identityGeneration)return
      workspaces.value=spaces;platformAdmin.value=platform.is_platform_admin;canRecover.value=platform.is_platform_admin||!!platform.can_governance_recover;spacesLoaded=true
      if(!spaces.some(w=>w.id===workspaceId.value)){selectWorkspace(iamUiEnabled&&workspaceId.value?0:spaces.length===1?spaces[0]!.id:0)}
    })();spacesLoading=work
    try{await work}finally{if(spacesLoading===work)spacesLoading=undefined}
  }
  function selectWorkspace(id:number){if(workspaceId.value===id)return;migrationRollbackBundleId.value='';changeWorkspaceContext();clearPermissions();workspaceId.value=id;if(id)localStorage.setItem('dbhub_workspace_id',String(id));else localStorage.removeItem('dbhub_workspace_id')}
  async function confirmWorkspace(id: number, signal: AbortSignal, requiredAction?: string) {
    if (signal.aborted || !workspaces.value.some(space => space.id === id)) return false
    const identity = identityGeneration
    const generation = workspaceGeneration()
    const data = await iamApi.effective({ id, signal })
    if (signal.aborted || identity !== identityGeneration || generation !== workspaceGeneration() ||
        !workspaces.value.some(space => space.id === id)) return false
    if (data.snapshot.workspace_id !== id || data.snapshot.subject !== user.value?.id) {
      throw new Error('工作空间权限回执与当前身份不一致，请重试。')
    }
    if (requiredAction && !data.actions.includes(requiredAction)) return false
    selectWorkspace(id)
    applyPermissions(data)
    return true
  }


  /** 用户名密码登录 */
  async function login(username: string, password: string, silentError = false) {
    const data = await request.post<unknown, LoginOutcome>('/auth/login', {
      username,
      password,
    }, { silentError })
    if (!('mfa_required' in data)) acceptSession(data)
    else clearAuth()
    return data
  }

  async function completeSSO(){
    const data=await request.post<unknown,LoginOutcome>('/auth/sso/complete',{}, {headers:{'X-DBHub-CSRF':'1'},silentError:true})
    if('mfa_required' in data)clearAuth();else acceptSession(data)
    return data
  }

  async function verifyMFA(challenge: string, code: string, recovery = false, silentError = false) {
    const data = await request.post<unknown, LoginResult>('/auth/mfa/verify', {
      challenge_token: challenge, ...(recovery ? { recovery_code: code } : { code }),
    }, { silentError })
    acceptSession(data)
  }

  /** Store only a fully authenticated session. Challenges and factors stay in memory. */
  function acceptSession(data: LoginResult) {
    migrationRollbackBundleId.value = ''
    identityGeneration++;clearPermissions();spacesLoaded=false;spacesLoading=undefined;if(user.value?.id!==data.user.id){platformAdmin.value=false;canRecover.value=false;workspaces.value=[];selectWorkspace(0)}
    token.value = data.access_token
    refreshToken.value = data.refresh_token
    user.value = data.user
    localStorage.setItem(TOKEN_KEY, data.access_token)
    localStorage.setItem(REFRESH_KEY, data.refresh_token)
    localStorage.setItem(USER_KEY, JSON.stringify(data.user))
    // Cookie 兜底通道，防止代理网关剥离 Authorization 头
    setAuthCookie(data.access_token)
  }

  function clearAuth() {
    migrationRollbackBundleId.value = ''
    identityGeneration++;clearPermissions();spacesLoading=undefined;platformAdmin.value=false;changeWorkspaceContext();spacesLoaded=false;workspaces.value=[];workspaceId.value=0;localStorage.removeItem('dbhub_workspace_id')
    token.value = ''
    refreshToken.value = ''
    user.value = null
    localStorage.removeItem(TOKEN_KEY)
    localStorage.removeItem(REFRESH_KEY)
    localStorage.removeItem(USER_KEY)
    clearAuthCookie()
  }

  /** 登出并清空本地会话 */
  async function logout() {
    const identity=identityGeneration
    try { await request.post('/auth/logout') } finally { if(identity===identityGeneration)clearAuth() }
  }

  return {
    token,
    refreshToken,
    user,
    isLoggedIn,
    username,
    role,hasCapability,platformAdmin,workspaces,workspaceId,workspace,selectWorkspace,loadWorkspaces,confirmWorkspace,
    effective,can,permissionStatus,permissionError,permissionKey,loadPermissions,clearPermissions,visibleMenus,handlingMenus,safeHome,canRecover,actionControlled,
    login,completeSSO,verifyMFA,acceptSession,migrationRollbackBundleId,
    logout,identityEpoch,
    clearAuth,
  }
})
