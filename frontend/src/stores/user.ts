import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { setAuthCookie, clearAuthCookie } from '../utils/auth-cookie'
import request from '../utils/request'
import { changeWorkspaceContext } from '../utils/workspace-context'
export interface Workspace {id:number;name:string;role:string;allow_unknown_freshness:boolean}
import type { LoginResult, UserInfo } from '../types/api'

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

/** 认证状态仓库：令牌、当前用户与登录/登出动作 */
export const useUserStore = defineStore('user', () => {
  const token = ref<string>(localStorage.getItem(TOKEN_KEY) ?? '')
  const refreshToken = ref<string>(localStorage.getItem(REFRESH_KEY) ?? '')
  const user = ref<UserInfo | null>(readUser())

  const isLoggedIn = computed(() => Boolean(token.value))
  const username = computed(() => user.value?.username ?? '')
  const platformAdmin=ref(false)
  const workspaces=ref<Workspace[]>([])
  const workspaceId=ref(Number(localStorage.getItem('dbhub_workspace_id')||0))
  const workspace=computed(()=>workspaces.value.find(w=>w.id===workspaceId.value))
  const role = computed(() => workspace.value?.role ?? '')
  let spacesLoaded=false,spacesLoading:Promise<void>|undefined,identityGeneration=0
  async function loadWorkspaces(force=false){
    if(spacesLoaded&&!force)return
    if(spacesLoading)return spacesLoading
    const identity=identityGeneration
    const work=(async()=>{const [spaces,platform]=await Promise.all([request.get<unknown,Workspace[]>('/workspaces',{silentError:true}),request.get<unknown,{is_platform_admin:boolean}>('/platform/status',{silentError:true})]);if(identity!==identityGeneration)return
      workspaces.value=spaces;platformAdmin.value=platform.is_platform_admin;spacesLoaded=true
      if(!spaces.some(w=>w.id===workspaceId.value)){selectWorkspace(spaces.length===1?spaces[0]!.id:0)}
    })();spacesLoading=work
    try{await work}finally{if(spacesLoading===work)spacesLoading=undefined}
  }
  function selectWorkspace(id:number){if(workspaceId.value===id)return;changeWorkspaceContext();workspaceId.value=id;if(id)localStorage.setItem('dbhub_workspace_id',String(id));else localStorage.removeItem('dbhub_workspace_id')}


  /** 用户名密码登录 */
  async function login(username: string, password: string) {
    const data = await request.post<unknown, LoginResult>('/auth/login', {
      username,
      password,
    })
    identityGeneration++;spacesLoaded=false;spacesLoading=undefined;if(user.value?.id!==data.user.id){platformAdmin.value=false;workspaces.value=[];selectWorkspace(0)}
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
    identityGeneration++;spacesLoading=undefined;platformAdmin.value=false;changeWorkspaceContext();spacesLoaded=false;workspaces.value=[];workspaceId.value=0;localStorage.removeItem('dbhub_workspace_id')
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
    try { await request.post('/auth/logout') } finally { clearAuth() }
  }

  return {
    token,
    refreshToken,
    user,
    isLoggedIn,
    username,
    role,platformAdmin,workspaces,workspaceId,workspace,selectWorkspace,loadWorkspaces,
    login,
    logout,
    clearAuth,
  }
})
