function endOfBlockComment(sql: string, start: number, allowNested: boolean): number {
  const delimiter = /\/\*|\*\//g
  delimiter.lastIndex = start + 2
  let depth = 1
  for (let match = delimiter.exec(sql); match; match = delimiter.exec(sql)) {
    if (match[0] === '/*') {
      if (!allowNested) return -1
      depth++
    } else if (--depth === 0) {
      return delimiter.lastIndex
    }
  }
  return -1
}

/** Detect the submitted command for presentation only. Execution policy stays on the server. */
export function isExplainStatement(sql: string, dialect?: 'postgres' | 'mysql'): boolean {
  const postgres = dialect === 'postgres'
  let offset = 0
  while (offset < sql.length) {
    const whitespace = /^\s+/.exec(sql.slice(offset))
    const mysqlHashComment = dialect === 'mysql' && sql[offset] === '#'
    if (whitespace) {
      offset += whitespace[0].length
    } else if (mysqlHashComment || (sql.startsWith('--', offset) && (postgres || /\s/.test(sql[offset + 2] ?? '')))) {
      const prefixLength = mysqlHashComment ? 1 : 2
      const comment = sql.slice(offset + prefixLength)
      const end = postgres ? comment.search(/[\r\n]/) : comment.indexOf('\n')
      if (end < 0) return false
      offset += prefixLength + end + 1
    } else if (sql.startsWith('/*', offset)) {
      if (!postgres && /^\/\*(?:m)?!/i.test(sql.slice(offset, offset + 4))) return false
      const end = endOfBlockComment(sql, offset, postgres)
      if (end < 0) return false
      offset = end
    } else {
      return /^explain\b/i.test(sql.slice(offset))
    }
  }
  return false
}

const estimate = /\s+\(cost=([\d.]+\.\.[\d.]+) rows=(\d+) width=\d+\)/
const observed = /\(actual (?:time=([\d.]+\.\.[\d.]+) )?rows=(\d+(?:\.\d+)?) loops=(\d+)\)/

/** Parse PostgreSQL text nodes. Keep unparsed annotations available in the original plan. */
export function postgresPlanNodes(columns: readonly string[], rows: readonly unknown[][]) {
  if (columns.length !== 1 || columns[0]?.trim().toUpperCase() !== 'QUERY PLAN') return []
  const indents: number[] = []
  return rows.flatMap((row, index) => {
    const line = row[0]
    if (typeof line !== 'string') return []
    const indent = line.length - line.trimStart().length
    if (indent > 0 && !line.trimStart().startsWith('->')) return []
    const match = estimate.exec(line)
    if (!match) return []
    const prefix = line.slice(0, match.index).trimStart().replace(/^->\s*/, '')
    if (!prefix) return []
    while (indents.length && (indents[indents.length - 1] ?? -1) >= indent) indents.pop()
    const depth = indents.length
    indents.push(indent)
    const actual = observed.exec(line.slice(match.index + match[0].length))
    return [{
      index,
      depth,
      label: prefix,
      cost: match[1],
      estimatedRows: match[2],
      actualRows: actual?.[2],
      actualTime: actual?.[1],
      loops: actual?.[3],
    }]
  })
}
