/** 后端统一响应信封 */
export interface ApiResponse<T = unknown> {
  /** 业务状态码，0 表示成功 */
  code: number
  message: string
  data: T
}

/** 登录用户信息 */
export interface UserInfo {
  id: number
  username: string
  role: string
}

/** 登录接口返回数据 */
export interface LoginResult {
  token_type: string
  access_token: string
  refresh_token: string
  expires_at: string
  user: UserInfo
}

/** 健康检查数据 */
export interface HealthInfo {
  status: string
  service: string
  version: string
  env: string
  uptime_seconds: number
  time: string
}
