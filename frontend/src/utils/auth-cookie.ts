/**
 * 认证 Cookie 辅助。
 *
 * 标准 Bearer 令牌走 Authorization 头；但部分预览/反向代理网关会剥离
 * Authorization 请求头，导致登录成功后首个鉴权请求被判为「缺少认证令牌」。
 * 因此登录成功后同步写入同源 Cookie 作为兜底通道（后端按
 * Authorization → X-Access-Token → Cookie 顺序取令牌）。
 */

export const ACCESS_COOKIE = 'dbhub_access_token'

/** 写入认证 Cookie（同源会话级，SameSite=Lax）。 */
export function setAuthCookie(token: string): void {
  document.cookie = `${ACCESS_COOKIE}=${encodeURIComponent(token)}; path=/; SameSite=Lax`
}

/** 清除认证 Cookie（含历史 path 变体）。 */
export function clearAuthCookie(): void {
  document.cookie = `${ACCESS_COOKIE}=; path=/; Max-Age=0; SameSite=Lax`
  document.cookie = `${ACCESS_COOKIE}=; Max-Age=0; SameSite=Lax`
}
