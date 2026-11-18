/** 所有连接测试入口共用等待预算；不改变数据库握手或写请求时限。 */
export function resolveConnectionTestTimeout(value: unknown): number {
  const timeout = Number(value)
  return Number.isFinite(timeout) && timeout >= 25000 && timeout <= 300000 ? timeout : 25000
}

export const connectionTestTimeout = resolveConnectionTestTimeout(import.meta.env.VITE_CONNECTION_TEST_TIMEOUT_MS)
