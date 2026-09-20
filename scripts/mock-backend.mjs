#!/usr/bin/env node
// Arena 专属 Mock 后端 - 无需 Go/PostgreSQL 即可跑通 UI
// 覆盖前端所需全部 /api 路由，返回符合 API 契约的假数据
import http from 'node:http'
import { URL } from 'node:url'

const PORT = Number(process.env.MOCK_PORT || process.env.PORT || 8080)

// ---------- 内存数据 ----------
let reportIdSeq = 3
let dashboardIdSeq = 2
let shareIdSeq = 0

const users = [
  { id: 1, username: 'admin', role: 'admin' },
  { id: 2, username: 'dev1', role: 'developer' },
  { id: 3, username: 'readonly', role: 'readonly' },
]

const connections = [
  { id: 1, user_id: 1, name: '订单核心库', type: 'postgres', host: '10.0.0.11', port: 5432, database: 'orders', username: 'app', ssl_mode: 'require', connection_timeout: 10, environment: 'prod', has_password: true, created_at: new Date().toISOString(), updated_at: new Date().toISOString() },
  { id: 2, user_id: 1, name: '用户中心', type: 'mysql', host: '10.0.0.12', port: 3306, database: 'users', username: 'app', ssl_mode: '', connection_timeout: 10, environment: 'dev', has_password: true, created_at: new Date().toISOString(), updated_at: new Date().toISOString() },
]

const reports = [
  { id: 1, name: '月度销售趋势', description: '按月聚合', connection_id: 1, database_name: 'orders', sql_text: "SELECT date_trunc('month', created_at) AS month, SUM(total_amount) AS sum FROM shop.orders GROUP BY 1 ORDER BY 1", chart_type: 'bar', chart_config: { dimension: 'month', metrics: ['sum'], aggregation: 'sum', sort: 'asc', topN: 20 }, visibility: 'shared', owner_user_id: 1, owner_name: 'admin', starred_by: [1], starred: true, created_at: new Date().toISOString(), updated_at: new Date().toISOString() },
  { id: 2, name: '用户注册漏斗', description: '漏斗分析', connection_id: 2, database_name: 'users', sql_text: 'SELECT status, COUNT(*) AS cnt FROM users GROUP BY status', chart_type: 'pie', chart_config: { dimension: 'status', metrics: ['cnt'], aggregation: 'sum' }, visibility: 'private', owner_user_id: 1, owner_name: 'admin', starred_by: [], starred: false, created_at: new Date().toISOString(), updated_at: new Date().toISOString() },
  { id: 3, name: '今日订单指标', description: '指标卡', connection_id: 1, database_name: 'orders', sql_text: 'SELECT COUNT(*) AS cnt FROM orders WHERE created_at >= CURRENT_DATE', chart_type: 'metric', chart_config: { dimension: 'cnt', metrics: ['cnt'], aggregation: 'count' }, visibility: 'shared', owner_user_id: 2, owner_name: 'dev1', starred_by: [1], starred: false, created_at: new Date().toISOString(), updated_at: new Date().toISOString() },
]

const dashboards = [
  { id: 1, name: '销售总览', description: '月度指标组合', visibility: 'shared', layout: [{ report_id: 1, x: 0, y: 0, w: 6, h: 4 }, { report_id: 3, x: 6, y: 0, w: 6, h: 4 }], owner_user_id: 1, owner_name: 'admin', starred_by: [1], starred: true, created_at: new Date().toISOString(), updated_at: new Date().toISOString() },
  { id: 2, name: '运营监控', description: '核心监控', visibility: 'private', layout: [{ report_id: 2, x: 0, y: 0, w: 12, h: 4 }], owner_user_id: 1, owner_name: 'admin', starred_by: [], starred: false, created_at: new Date().toISOString(), updated_at: new Date().toISOString() },
]

const shares = []

// ---------- 工具 ----------
function json(res, code, data, status = 200) {
  const body = JSON.stringify({ code, message: code === 0 ? 'ok' : 'error', data })
  res.writeHead(status, {
    'Content-Type': 'application/json; charset=utf-8',
    'Access-Control-Allow-Origin': '*',
    'Access-Control-Allow-Methods': 'GET,POST,PUT,PATCH,DELETE,OPTIONS',
    'Access-Control-Allow-Headers': 'Content-Type, Authorization, X-Access-Token, X-Requested-With',
  })
  res.end(body)
}
function ok(res, data) { json(res, 0, data, 200) }
function fail(res, httpStatus, code, msg) { json(res, code, null, httpStatus); res.writeHead(httpStatus); /* override */ json(res, code, { message: msg }, httpStatus) }

async function readBody(req) {
  const chunks = []
  for await (const c of req) chunks.push(c)
  const raw = Buffer.concat(chunks).toString('utf-8')
  if (!raw) return {}
  try { return JSON.parse(raw) } catch { return {} }
}

function parseQuery(url) {
  const u = new URL(url, 'http://localhost')
  const q = {}
  for (const [k, v] of u.searchParams.entries()) q[k] = v
  return { pathname: u.pathname, query: q }
}

// ---------- 路由 ----------
const server = http.createServer(async (req, res) => {
  if (req.method === 'OPTIONS') {
    res.writeHead(204, {
      'Access-Control-Allow-Origin': '*',
      'Access-Control-Allow-Methods': 'GET,POST,PUT,PATCH,DELETE,OPTIONS',
      'Access-Control-Allow-Headers': 'Content-Type, Authorization, X-Access-Token, X-Requested-With',
    })
    res.end()
    return
  }

  const { pathname, query } = parseQuery(req.url || '/')
  const method = req.method || 'GET'

  // 健康
  if (pathname === '/api/health' && method === 'GET') {
    return ok(res, { status: 'ok', version: 'mock-0.4.0', env: 'arena' })
  }

  // 认证
  if (pathname === '/api/v1/auth/login' && method === 'POST') {
    const body = await readBody(req)
    const username = body.username || 'admin'
    const user = users.find(u => u.username === username) || users[0]
    return ok(res, {
      token_type: 'Bearer',
      access_token: 'mock_access_' + Date.now(),
      refresh_token: 'mock_refresh_' + Date.now(),
      expires_at: new Date(Date.now() + 15 * 60 * 1000).toISOString(),
      user,
    })
  }
  if (pathname === '/api/v1/auth/refresh' && method === 'POST') {
    return ok(res, {
      token_type: 'Bearer',
      access_token: 'mock_access_' + Date.now(),
      refresh_token: 'mock_refresh_' + Date.now(),
      expires_at: new Date(Date.now() + 15 * 60 * 1000).toISOString(),
      user: users[0],
    })
  }
  if (pathname === '/api/v1/auth/me' && method === 'GET') {
    return ok(res, users[0])
  }

  // 指标总览
  if (pathname === '/api/v1/metrics/overview' && method === 'GET') {
    const days = Number(query.days || 14)
    const trend = Array.from({ length: days }, (_, i) => {
      const d = new Date()
      d.setDate(d.getDate() - (days - 1 - i))
      const iso = d.toISOString().slice(0, 10)
      return { date: iso, total: 40 + Math.floor(Math.random() * 30), failed: Math.floor(Math.random() * 3), slow: Math.floor(Math.random() * 8) }
    })
    return ok(res, {
      kpi: { connections: connections.length, today_queries: 58, today_active_users: 3, audit_events: 193 },
      connection_types: { mysql: 1, postgres: 1, redis: 0 },
      trend,
      rank: connections.map(c => ({ connection_id: c.id, name: c.name, count: 100 + Math.floor(Math.random() * 200) })),
      recent: [
        { id: 1, connection_id: 1, user_id: 1, connection_name: '订单核心库', database_name: 'orders', sql_text: 'SELECT * FROM orders LIMIT 100', status: 1, affected_rows: null, execution_time_ms: 72, error_message: null, created_at: new Date().toISOString() },
      ],
      days,
    })
  }

  // 连接
  if (pathname === '/api/v1/connections' && method === 'GET') {
    return ok(res, { items: connections, total: connections.length })
  }
  if (pathname.startsWith('/api/v1/connections/') && pathname.endsWith('/test') && method === 'POST') {
    return ok(res, { ok: true, type: 'postgres', version: 'PostgreSQL 16.4 mock', elapsed_ms: 48 })
  }
  if (pathname === '/api/v1/connections/test' && method === 'POST') {
    return ok(res, { ok: true, type: 'postgres', version: 'PostgreSQL 16.4 mock', elapsed_ms: 48 })
  }

  // 资产
  if (pathname === '/api/v1/assets/overview' && method === 'GET') {
    return ok(res, { connection_total: 2, table_total: 128, view_total: 14, owned_tables: 73, sensitive_fields: 19 })
  }
  if (pathname === '/api/v1/assets/tree' && method === 'GET') {
    return ok(res, {
      items: [
        { id: 1, name: '订单核心库', type: 'postgres', environment: 'prod', table_count: 26, is_empty: false, databases: [{ name: 'orders', table_count: 26, schemas: [{ name: 'public', table_count: 4, tables: [{ name: 'orders', type: 'table' }, { name: 'v_monthly_sales', type: 'view' }] }] }] },
        { id: 2, name: '用户中心', type: 'mysql', environment: 'dev', table_count: 12, is_empty: false, databases: [{ name: 'users', table_count: 12, schemas: [{ name: 'users', table_count: 12, tables: [{ name: 'users', type: 'table' }] }] }] },
      ]
    })
  }
  if (pathname === '/api/v1/assets/tables' && method === 'GET') {
    const items = [
      {
        snapshot: { id: 1, connection_id: 1, database_name: 'orders', schema_name: 'public', table_name: 'orders', table_type: 'table', table_comment: '订单主表', estimated_rows: 120330, data_bytes: 98765432, raw_columns: [{ name: 'id', ordinal: 1, data_type: 'bigint', is_primary: true, is_nullable: false, default: '', comment: '' }], raw_indexes: [], raw_keys: [], ddl_text: 'CREATE TABLE orders ...', synced_at: new Date().toISOString() },
        connection_name: '订单核心库',
        owner_user_id: 1,
        owner_name: 'admin',
        business_desc: '订单主表，记录每笔交易',
        tags: ['核心', '交易'],
        sensitivity: 'confidential',
        starred: true,
        query_count_30d: 304,
        sensitive_columns: 2,
      },
    ]
    return ok(res, { items, total: items.length, page: Number(query.page || 1), page_size: Number(query.page_size || 20) })
  }
  if (pathname === '/api/v1/assets/table' && method === 'GET') {
    return ok(res, {
      snapshot: { id: 1, connection_id: 1, database_name: 'orders', schema_name: 'public', table_name: 'orders', table_type: 'table', table_comment: '订单主表', estimated_rows: 120330, data_bytes: 98765432, raw_columns: [{ name: 'id', ordinal: 1, data_type: 'bigint', is_primary: true, is_nullable: false, default: '', comment: '主键' }, { name: 'total_amount', ordinal: 2, data_type: 'numeric', is_primary: false, is_nullable: true, default: '', comment: '金额' }], raw_indexes: [{ name: 'orders_pkey', columns: ['id'], is_unique: true, is_primary: true }], raw_keys: [], ddl_text: 'CREATE TABLE orders (id bigint PRIMARY KEY, total_amount numeric);', synced_at: new Date().toISOString() },
      connection_name: '订单核心库',
      environment: 'prod',
      table_annotation: { id: 1, connection_id: 1, database_name: 'orders', schema_name: 'public', table_name: 'orders', column_name: '', owner_user_id: 1, business_desc: '订单主表', tags: ['核心'], sensitivity: 'confidential', starred_by: [1], updated_by: 1, updated_at: new Date().toISOString() },
      column_annotations: [{ column_name: 'total_amount', owner_user_id: null, business_desc: '订单总金额（元）', tags: ['金额'], sensitivity: 'sensitive' }],
      starred: true,
      query_count_30d: 304,
    })
  }
  if (pathname === '/api/v1/assets/search' && method === 'GET') {
    const q = query.q || ''
    return ok(res, {
      connections: q ? connections.slice(0, 1) : [],
      tables: q ? [{ connection_id: 1, connection_name: '订单核心库', environment: 'prod', database: 'orders', schema: 'public', name: 'orders', type: 'table', comment: '订单主表' }] : [],
      columns: q ? [{ connection_id: 1, connection_name: '订单核心库', database: 'orders', schema: 'public', table: 'orders', column: 'order_no', data_type: 'varchar(32)', comment: '订单号' }] : [],
      reports: q ? reports.slice(0, 2).map(r => ({ id: r.id, name: r.name, chart_type: r.chart_type })) : [],
      history: [],
    })
  }
  if (pathname === '/api/v1/users/brief' && method === 'GET') {
    return ok(res, { items: users.map(u => ({ id: u.id, username: u.username, role: u.role })) })
  }
  if (pathname === '/api/v1/assets/sync' && method === 'POST') {
    return ok(res, { connections: [{ connection_id: 1, connection_name: '订单核心库', type: 'postgres', skipped: false, databases: ['orders'], tables: 24, views: 2, skipped_databases: [], errors: [], by_database: [{ database: 'orders', schemas: 3, tables: 24, views: 2 }] }], tables: 24, views: 2 })
  }
  if (pathname === '/api/v1/assets/star' && method === 'POST') {
    return ok(res, { starred: true })
  }
  if (pathname === '/api/v1/assets/annotations' && method === 'PUT') {
    return ok(res, { ok: true })
  }

  // 查询执行
  if (pathname === '/api/v1/query/execute' && method === 'POST') {
    const body = await readBody(req)
    const sql = (body.sql || '').toLowerCase()
    if (sql.includes('count')) {
      return ok(res, { kind: 'query', columns: ['cnt'], rows: [[128]], truncated: false, duration_ms: 12 })
    }
    // 默认返回示例
    return ok(res, { kind: 'query', columns: ['month', 'sum'], rows: [['2026-01', 12000], ['2026-02', 15000], ['2026-03', 18000], ['2026-04', 21000]], truncated: false, duration_ms: 22 })
  }
  if (pathname === '/api/v1/data/preview' && method === 'GET') {
    return ok(res, { columns: ['id', 'total_amount'], rows: [[1, 100], [2, 200]], total: 2, page: 1, page_size: 20, has_more: false })
  }
  if (pathname === '/api/v1/query/history' && method === 'GET') {
    return ok(res, { items: [], total: 0, page: 1, page_size: 20 })
  }

  // 报表
  if (pathname === '/api/v1/reports' && method === 'GET') {
    let items = [...reports]
    const scope = query.scope
    if (scope === 'mine') items = items.filter(r => r.owner_user_id === 1)
    if (scope === 'shared') items = items.filter(r => r.visibility === 'shared')
    if (scope === 'starred') items = items.filter(r => r.starred)
    if (query.q) {
      const q = query.q.toLowerCase()
      items = items.filter(r => r.name.toLowerCase().includes(q) || r.description.toLowerCase().includes(q))
    }
    const page = Number(query.page || 1)
    const pageSize = Number(query.page_size || 20)
    const start = (page - 1) * pageSize
    return ok(res, { items: items.slice(start, start + pageSize), total: items.length, page, page_size: pageSize })
  }
  if (pathname === '/api/v1/reports' && method === 'POST') {
    const body = await readBody(req)
    reportIdSeq += 1
    const rep = {
      id: reportIdSeq,
      name: body.name || '未命名报表',
      description: body.description || '',
      connection_id: body.connection_id || 1,
      database_name: body.database || 'orders',
      sql_text: body.sql || 'SELECT 1',
      chart_type: body.chart_type || 'table',
      chart_config: body.chart_config || {},
      visibility: body.visibility || 'private',
      owner_user_id: 1,
      owner_name: 'admin',
      starred_by: [],
      starred: false,
      created_at: new Date().toISOString(),
      updated_at: new Date().toISOString(),
    }
    reports.push(rep)
    return ok(res, rep)
  }
  if (pathname.startsWith('/api/v1/reports/') && pathname.endsWith('/star') && method === 'POST') {
    const id = Number(pathname.split('/')[4])
    const body = await readBody(req)
    const rep = reports.find(r => r.id === id)
    if (rep) rep.starred = !!body.star
    return ok(res, { starred: !!body.star })
  }
  if (pathname.match(/^\/api\/v1\/reports\/\d+$/) && method === 'GET') {
    const id = Number(pathname.split('/')[4])
    const rep = reports.find(r => r.id === id)
    if (!rep) return json(res, 40400, null, 404)
    return ok(res, rep)
  }
  if (pathname.match(/^\/api\/v1\/reports\/\d+$/) && method === 'PUT') {
    const id = Number(pathname.split('/')[4])
    const body = await readBody(req)
    const rep = reports.find(r => r.id === id)
    if (!rep) return json(res, 40400, null, 404)
    Object.assign(rep, { name: body.name || rep.name, description: body.description ?? rep.description, visibility: body.visibility || rep.visibility, updated_at: new Date().toISOString() })
    return ok(res, rep)
  }
  if (pathname.match(/^\/api\/v1\/reports\/\d+$/) && method === 'DELETE') {
    const id = Number(pathname.split('/')[4])
    const idx = reports.findIndex(r => r.id === id)
    if (idx >= 0) reports.splice(idx, 1)
    return ok(res, { id })
  }

  // 仪表盘
  if (pathname === '/api/v1/dashboards' && method === 'GET') {
    let items = [...dashboards]
    const scope = query.scope
    if (scope === 'mine') items = items.filter(d => d.owner_user_id === 1)
    if (scope === 'shared') items = items.filter(d => d.visibility === 'shared')
    if (scope === 'starred') items = items.filter(d => d.starred)
    const page = Number(query.page || 1)
    const pageSize = Number(query.page_size || 20)
    const start = (page - 1) * pageSize
    return ok(res, { items: items.slice(start, start + pageSize), total: items.length, page, page_size: pageSize })
  }
  if (pathname === '/api/v1/dashboards' && method === 'POST') {
    const body = await readBody(req)
    dashboardIdSeq += 1
    const dash = {
      id: dashboardIdSeq,
      name: body.name || '未命名仪表盘',
      description: body.description || '',
      visibility: body.visibility || 'private',
      layout: body.layout || [],
      owner_user_id: 1,
      owner_name: 'admin',
      starred_by: [],
      starred: false,
      created_at: new Date().toISOString(),
      updated_at: new Date().toISOString(),
    }
    dashboards.push(dash)
    return ok(res, { id: dash.id })
  }
  if (pathname.match(/^\/api\/v1\/dashboards\/\d+$/) && method === 'GET') {
    const id = Number(pathname.split('/')[4])
    const dash = dashboards.find(d => d.id === id)
    if (!dash) return json(res, 40400, null, 404)
    return ok(res, dash)
  }
  if (pathname.match(/^\/api\/v1\/dashboards\/\d+$/) && method === 'PUT') {
    const id = Number(pathname.split('/')[4])
    const body = await readBody(req)
    const dash = dashboards.find(d => d.id === id)
    if (!dash) return json(res, 40400, null, 404)
    Object.assign(dash, { name: body.name || dash.name, description: body.description ?? dash.description, visibility: body.visibility || dash.visibility, layout: body.layout ?? dash.layout, updated_at: new Date().toISOString() })
    return ok(res, dash)
  }
  if (pathname.match(/^\/api\/v1\/dashboards\/\d+$/) && method === 'DELETE') {
    const id = Number(pathname.split('/')[4])
    const idx = dashboards.findIndex(d => d.id === id)
    if (idx >= 0) dashboards.splice(idx, 1)
    return ok(res, { id })
  }
  if (pathname.match(/^\/api\/v1\/dashboards\/\d+\/star$/) && method === 'POST') {
    const id = Number(pathname.split('/')[4])
    const body = await readBody(req)
    const dash = dashboards.find(d => d.id === id)
    if (dash) dash.starred = !!body.starred
    return ok(res, { starred: !!body.starred })
  }

  // 分享
  if (pathname === '/api/v1/shares' && method === 'POST') {
    const body = await readBody(req)
    shareIdSeq += 1
    const token = Math.random().toString(36).slice(2) + Math.random().toString(36).slice(2)
    const expireDays = body.expire_days
    const expireAt = expireDays ? new Date(Date.now() + expireDays * 24 * 3600 * 1000).toISOString() : null
    const share = { id: shareIdSeq, subject_type: body.subject_type, subject_id: body.subject_id, token_hash: 'mock', token_plain: token, expire_at: expireAt, access_count: 0, revoked: false, created_at: new Date().toISOString() }
    shares.push(share)
    return ok(res, { id: share.id, subject_type: share.subject_type, subject_id: share.subject_id, token, expire_at: expireAt, created_at: share.created_at })
  }
  if (pathname === '/api/v1/shares' && method === 'GET') {
    const st = query.subject_type
    const sid = Number(query.subject_id)
    const items = shares.filter(s => s.subject_type === st && s.subject_id === sid).map(s => ({ id: s.id, subject_type: s.subject_type, subject_id: s.subject_id, expire_at: s.expire_at, access_count: s.access_count, revoked: s.revoked, created_at: s.created_at }))
    return ok(res, { items })
  }
  if (pathname.match(/^\/api\/v1\/shares\/\d+\/revoke$/) && method === 'POST') {
    const id = Number(pathname.split('/')[4])
    const s = shares.find(x => x.id === id)
    if (s) s.revoked = true
    return ok(res, { id, revoked: true })
  }
  if (pathname.match(/^\/api\/v1\/shares\/\d+$/) && method === 'DELETE') {
    const id = Number(pathname.split('/')[4])
    const idx = shares.findIndex(x => x.id === id)
    if (idx >= 0) shares.splice(idx, 1)
    return ok(res, { id })
  }

  // 公开分享
  if (pathname.startsWith('/api/v1/public/s/') && method === 'GET') {
    const token = pathname.split('/').pop() || ''
    // 简单匹配：取最后创建的分享
    const s = shares.find(x => x.token_plain === token) || shares[shares.length - 1]
    if (!s) return json(res, 40400, null, 404)
    if (s.revoked) return json(res, 40400, null, 404)
    s.access_count += 1
    if (s.subject_type === 'report') {
      const rep = reports.find(r => r.id === s.subject_id) || reports[0]
      return ok(res, { share_id: s.id, subject_type: s.subject_type, subject: rep, owner_name: rep.owner_name, expire_at: s.expire_at, access_count: s.access_count, type: 'report', report: rep, share: s })
    } else {
      const dash = dashboards.find(d => d.id === s.subject_id) || dashboards[0]
      const dashMap = { id: dash.id, name: dash.name, description: dash.description, visibility: dash.visibility, layout: dash.layout, owner_name: dash.owner_name, reports: reports.filter(r => dash.layout.some(l => l.report_id === r.id)) }
      return ok(res, { share_id: s.id, subject_type: s.subject_type, subject: dashMap, owner_name: dash.owner_name, expire_at: s.expire_at, access_count: s.access_count, type: 'dashboard', dashboard: dashMap, share: s })
    }
  }

  // 用户与审计等兜底
  if (pathname.startsWith('/api/v1/')) {
    return ok(res, { items: [], total: 0 })
  }

  json(res, 40400, null, 404)
})

server.listen(PORT, '0.0.0.0', () => {
  console.log(`[mock-backend] listening on 0.0.0.0:${PORT}`)
})
