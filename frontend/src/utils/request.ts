import axios, { AxiosError, type AxiosInstance } from 'axios'
import { ElMessage } from 'element-plus'
import type { ApiResponse } from '../types/api'
import { useUserStore } from '../stores/user'
import router from '../router'

/**
 * 全局 Axios 实例
 * - baseURL 走 Vite 代理（/api → 后端服务）
 * - 请求拦截器自动附加 Bearer Token
 * - 响应拦截器统一解包 { code, message, data } 并处理鉴权失效
 */
const request: AxiosInstance = axios.create({
  baseURL: '/api/v1',
  timeout: 15_000,
})

request.interceptors.request.use((config) => {
  const userStore = useUserStore()
  if (userStore.token) {
    config.headers.Authorization = `Bearer ${userStore.token}`
  }
  return config
})

request.interceptors.response.use(
  (response) => {
    const body = response.data as ApiResponse
    if (body.code === 0) {
      return body.data as never
    }
    ElMessage.error(body.message || '请求失败')
    return Promise.reject(new Error(body.message || '请求失败'))
  },
  async (error: AxiosError<ApiResponse>) => {
    const status = error.response?.status
    const message = error.response?.data?.message

    if (status === 401) {
      const userStore = useUserStore()
      userStore.clearAuth()
      if (router.currentRoute.value.name !== 'login') {
        ElMessage.error('登录状态已过期，请重新登录')
        router.replace({
          name: 'login',
          query: { redirect: router.currentRoute.value.fullPath },
        })
      }
    } else if (message) {
      ElMessage.error(message)
    } else if (error.code === 'ECONNABORTED') {
      ElMessage.error('请求超时，请稍后重试')
    } else {
      ElMessage.error('网络异常，请检查后端服务状态')
    }
    return Promise.reject(error)
  },
)

export default request
