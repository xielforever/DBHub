import { Columns3, Database, Eye, KeyRound, Layers3, ListTree, Table2 } from 'lucide-vue-next'
import type { TechnicalElement, TechnicalElementKind, TechnicalElementLevel } from '../../api/asset'

export const elementKinds = {
  database: { label: '数据库', icon: Database },
  schema: { label: 'Schema', icon: Layers3 },
  table: { label: '表', icon: Table2 },
  view: { label: '视图', icon: Eye },
  field: { label: '字段', icon: Columns3 },
  index: { label: '索引', icon: ListTree },
  constraint: { label: '约束', icon: KeyRound },
} satisfies Record<TechnicalElementKind, { label: string; icon: typeof Database }>

export const elementLevels = {
  normal: '普通',
  sensitive: '敏感',
  confidential: '机密',
  unclassified: '未标注',
} satisfies Record<TechnicalElementLevel, string>

export function elementKey(element: TechnicalElement) {
  return JSON.stringify([
    element.connection_id, element.database, element.schema, element.table, element.kind, element.name,
  ])
}

export function elementPath(element: TechnicalElement) {
  const path = [element.connection_name]
  if (element.kind !== 'database') path.push(element.database)
  if (element.kind !== 'database' && element.kind !== 'schema') path.push(element.schema)
  if (!['database', 'schema', 'table', 'view'].includes(element.kind)) path.push(element.table)
  return path.filter(Boolean).join(' / ')
}

export function elementAttribute(element: TechnicalElement) {
  const properties = element.properties
  switch (element.kind) {
    case 'database':
      return ({ postgres: 'PostgreSQL', mysql: 'MySQL', redis: 'Redis', mongo: 'MongoDB', jdbc: 'JDBC' } as Record<string, string>)[element.connection_type] || element.connection_type
    case 'schema':
      return `${properties.object_count ?? 0} 个表或视图`
    case 'table':
    case 'view':
      return `${properties.column_count ?? 0} 个字段`
    case 'field':
      return properties.data_type || '—'
    case 'index':
      return properties.is_unique ? '唯一索引' : '普通索引'
    case 'constraint':
      return ({ primary_key: '主键', foreign_key: '外键', unique: '唯一约束' } as Record<string, string>)[properties.kind || ''] || properties.kind || '—'
  }
}
