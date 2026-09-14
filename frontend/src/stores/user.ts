import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import request from '../utils/request'
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
  const role = computed(() => user.value?.role ?? '')

  /** 用户名密码登录 */
  async function login(username: string, password: string) {
    const data = await request.post<unknown, LoginResult>('/auth/login', {
      username,
      password,
    })
    token.value = data.access_token
    refreshToken.value = data.refresh_token
    user.value = data.user
    localStorage.setItem(TOKEN_KEY, data.access_token)
    localStorage.setItem(REFRESH_KEY, data.refresh_token)
    localStorage.setItem(USER_KEY, JSON.stringify(data.user))
  }

  function clearAuth() {
    token.value = ''
    refreshToken.value = ''
    user.value = null
    localStorage.removeItem(TOKEN_KEY)
    localStorage.removeItem(REFRESH_KEY)
    localStorage.removeItem(USER_KEY)
  }

  /** 登出并清空本地会话 */
  function logout() {
    // TODO: 调用后端令牌吊销接口（JWT 黑名单写入 Redis）
    clearAuth()
  }

  return {
    token,
    refreshToken,
    user,
    isLoggedIn,
    username,
    role,
    login,
    logout,
    clearAuth,
  }
})
