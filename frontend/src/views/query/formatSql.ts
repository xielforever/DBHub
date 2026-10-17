/** 使用当前连接对应的 SQL 方言，格式化失败由调用方保留原文。 */
export async function formatSql(sql: string, dialect: 'mysql' | 'postgres'): Promise<string> {
  const { format } = await import('sql-formatter')
  return format(sql, {
    language: dialect === 'mysql' ? 'mysql' : 'postgresql',
    tabWidth: 2,
    keywordCase: 'upper',
  })
}
