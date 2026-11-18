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

/** Password/SSO success can require an additional factor; it is not a session. */
export interface MFAChallenge { mfa_required: true; challenge_token: string; expires_in: number }
export type LoginOutcome = LoginResult | MFAChallenge

/** 健康检查数据 */
export interface HealthInfo {
  status: string
  service: string
  version: string
  env: string
  uptime_seconds: number
  time: string
}
