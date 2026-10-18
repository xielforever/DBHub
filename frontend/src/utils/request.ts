import axios, { AxiosError, type AxiosInstance, type InternalAxiosRequestConfig } from 'axios'
import { ElMessage } from 'element-plus'
import type { ApiResponse, LoginResult } from '../types/api'
import { useUserStore } from '../stores/user'
import { setAuthCookie } from './auth-cookie'
import router from '../router'
import {workspaceGeneration,workspaceSignal} from './workspace-context'

declare module 'axios' {
  interface AxiosRequestConfig {
    /** 调用方自行展示错误和恢复操作时，避免重复弹窗。 */
    workspaceGeneration?: number
    silentError?: boolean
  }
}

export class ApiRequestError extends Error {
  status: number | undefined
  code: number | undefined
  data: unknown

  constructor(message: string, status?: number, code?: number, data?: unknown) {
    super(message)
    this.status = status
    this.code = code
    this.data = data
  }
}

/**
 * 全局 Axios 实例
 * - baseURL 走 Vite 代理（/api → 后端服务）
 * - 请求拦截器自动附加 Bearer Token（同时附 X-Access-Token 头与 Cookie 兜底，
 *   兼容会剥离 Authorization 头的预览/反向代理网关）
 * - 响应拦截器统一解包 { code, message, data }、401 静默刷新后重放、鉴权失效跳登录
 */
const request: AxiosInstance = axios.create({
  baseURL: '/api/v1',
  timeout: 15_000,
})

request.interceptors.request.use((config) => {
  const userStore = useUserStore()
  if (userStore.token) {
    // 标准通道 + 自定义头兜底（部分网关会剥离 Authorization）
    config.headers.Authorization = `Bearer ${userStore.token}`
    config.headers['X-Access-Token'] = userStore.token
    config.headers['X-DBHub-CSRF'] = '1'
    // Cookie 同源自动携带，是最不易被网关剥离的通道；每次请求确保存在
    if (!document.cookie.includes('dbhub_access_token=')) {
      setAuthCookie(userStore.token)
    }
  }
  const global=/^\/(?:auth|platform)\//.test(config.url||'') || /^\/workspaces(?:$|\/\d+\/(?:members|policy)(?:$|\/))/.test(config.url||'')
  if(!global){
    if(config.workspaceGeneration!==undefined&&config.workspaceGeneration!==workspaceGeneration())throw new axios.CanceledError('工作空间已切换')
    if(!userStore.workspaceId)throw new axios.CanceledError('请选择工作空间')
    config.workspaceGeneration=workspaceGeneration()
    config.signal=AbortSignal.any([...(config.signal?[config.signal as AbortSignal]:[]),workspaceSignal()])
    if(!config.url?.startsWith('/workspaces/'))config.url=`/workspaces/${userStore.workspaceId}${config.url}`
  }
  return config
})

// ---- 静默刷新（单飞，避免多个并发请求同时刷新）----
let refreshing: Promise<string | null> | null = null

async function refreshAccessToken(): Promise<string | null> {
  const userStore = useUserStore()
  if (!userStore.refreshToken) return null
  const refreshAtStart=userStore.refreshToken
  try {
    // 用裸 axios 发刷新请求，绕过本实例拦截器，避免 401 递归
    const resp = await axios.post<ApiResponse<LoginResult>>(
      '/api/v1/auth/refresh',
      { refresh_token: userStore.refreshToken },
      { timeout: 10_000 },
    )
    const data = resp.data?.data
    if (!data?.access_token||userStore.refreshToken!==refreshAtStart) return null
    userStore.token = data.access_token
    userStore.refreshToken = data.refresh_token
    localStorage.setItem('dbhub_access_token', data.access_token)
    localStorage.setItem('dbhub_refresh_token', data.refresh_token)
    if (data.user) {
      userStore.user = data.user
      localStorage.setItem('dbhub_user', JSON.stringify(data.user))
    }
    setAuthCookie(data.access_token)
    return data.access_token
  } catch (error) {
    if (axios.isAxiosError(error) && [401, 403].includes(error.response?.status ?? 0))
      return null
    const failure = axios.isAxiosError<ApiResponse>(error) ? error : undefined
    throw new ApiRequestError(
      failure?.response?.data?.message || '暂时无法刷新会话，请稍后重试',
      failure?.response?.status,
      failure?.response?.data?.code,
    )
  }
}

function redirectToLogin() {
  const userStore = useUserStore()
  userStore.clearAuth()
  if (router.currentRoute.value.name !== 'login') {
    ElMessage.error('登录状态已过期，请重新登录')
    router.replace({
      name: 'login',
      query: { redirect: router.currentRoute.value.fullPath },
    })
  }
}

request.interceptors.response.use(
  (response) => {
    if(response.config.workspaceGeneration!==undefined&&response.config.workspaceGeneration!==workspaceGeneration())return Promise.reject(new axios.CanceledError('工作空间已切换'))
    const body = response.data as ApiResponse
    if (body.code === 0) {
      return body.data as never
    }
    if (!response.config.silentError) ElMessage.error(body.message || '请求失败')
    return Promise.reject(new ApiRequestError(body.message || '请求失败', response.status, body.code, body.data))
  },
  async (error: AxiosError<ApiResponse>) => {
    if (axios.isCancel(error)||error.config?.workspaceGeneration!==undefined&&error.config.workspaceGeneration!==workspaceGeneration()) return Promise.reject(new axios.CanceledError('请求已取消或工作空间已切换'))
    const status = error.response?.status
    const original = error.config as (InternalAxiosRequestConfig & { _retried?: boolean }) | undefined

    // 401：先尝试静默刷新令牌并重放原请求（登录/刷新接口自身除外，只重试一次）
    const url = error.config?.url || ''
    const isAuthEndpoint = url.includes('/auth/login') || url.includes('/auth/refresh')
    if (status === 401 && original && !original._retried && !isAuthEndpoint) {
      original._retried = true
      if (!refreshing) refreshing = refreshAccessToken().finally(() => { refreshing = null })
      let newToken: string | null
      try {
        newToken = await refreshing
      } catch (refreshError) {
        // An expired access token plus a temporary refresh outage is not a
        // revoked session. Keep both tokens and allow an explicit later retry.
        if (!original.silentError)
          ElMessage.error(refreshError instanceof Error ? refreshError.message : '会话服务暂不可用')
        return Promise.reject(refreshError)
      }
      if (newToken) {
        original.headers.Authorization = `Bearer ${newToken}`
        original.headers['X-Access-Token'] = newToken
        return request(original)
      }
      redirectToLogin()
      return Promise.reject(error)
    }

    if (status === 401) {
      redirectToLogin()
    } else {
      const message = error.response?.data?.message
      if (original?.silentError) {
        // 数据工作台自行解释执行结果，通信异常不能提示直接重试写操作。
      } else if (message) {
        ElMessage.error(message)
      } else if (error.code === 'ECONNABORTED') {
        ElMessage.error('请求超时，请稍后重试')
      } else {
        ElMessage.error('网络异常，请检查后端服务状态')
      }
      // 统一以服务端中文消息（或网络兜底文案）拒绝，避免调用方拿到
      // axios 原始英文 "Request failed with status code xxx"
      const cn = error.response?.data?.message
        || (error.code === 'ECONNABORTED' ? '请求超时，请稍后重试' : '网络异常，请检查后端服务状态')
      return Promise.reject(new ApiRequestError(cn, status, error.response?.data?.code, error.response?.data?.data))
    }
    return Promise.reject(error)
  },
)

export default request
