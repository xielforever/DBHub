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

let proxyIdSeq = 2
const proxies = [
  { id: 1, user_id: 1, name: '公司 HTTP 代理', type: 'http', host: 'proxy.example.com', port: 8080, username: 'proxyuser', has_password: true, description: '办公网出网代理', status: 'active', created_at: new Date().toISOString(), updated_at: new Date().toISOString() },
  { id: 2, user_id: 1, name: 'SOCKS5 全局', type: 'socks5', host: '10.0.0.9', port: 1080, username: '', has_password: false, description: '通用代理占位', status: 'active', created_at: new Date().toISOString(), updated_at: new Date().toISOString() },
  { id: 3, user_id: 1, name: 'PgBouncer 网关', type: 'db_proxy', host: '10.0.0.20', port: 6432, username: 'pbbouncer', has_password: true, description: 'PG 连接池代理', status: 'active', created_at: new Date().toISOString(), updated_at: new Date().toISOString() },
]

let tunnelIdSeq = 1
const tunnels = [
  { id: 1, user_id: 1, name: '生产跳板机(废弃)', host: '10.0.0.9', port: 22, username: 'ops', auth_type: 'private_key', has_private_key: true, has_passphrase: false, has_password: false, created_at: new Date().toISOString(), updated_at: new Date().toISOString() },
]

const connections = [
  { id: 1, user_id: 1, name: '订单核心库', type: 'postgres', host: '10.0.0.11', port: 5432, database: 'orders', username: 'app_rw', ssl_mode: 'require', connection_timeout: 10, environment: 'prod', color_label: 'red', has_password: true, proxy_id: 1, proxy_name: '公司 HTTP 代理', ssh_tunnel_id: null, tunnel_name: '', created_at: new Date(Date.now()-86400000*2).toISOString(), updated_at: new Date().toISOString() },
  { id: 2, user_id: 1, name: '用户中心', type: 'mysql', host: '10.0.0.12', port: 3306, database: 'users', username: 'app_ro', ssl_mode: 'disable', connection_timeout: 10, environment: 'dev', color_label: 'blue', has_password: true, proxy_id: null, proxy_name: '', ssh_tunnel_id: null, tunnel_name: '', created_at: new Date(Date.now()-86400000*5).toISOString(), updated_at: new Date(Date.now()-86400000*1).toISOString() },
  { id: 3, user_id: 2, name: 'Redis 缓存集群', type: 'redis', host: '10.0.0.30', port: 6379, database: '0', username: '', ssl_mode: 'disable', connection_timeout: 5, environment: 'prod', color_label: 'green', has_password: false, proxy_id: 2, proxy_name: 'SOCKS5 全局', ssh_tunnel_id: null, tunnel_name: '', created_at: new Date(Date.now()-86400000*10).toISOString(), updated_at: new Date(Date.now()-86400000*3).toISOString() },
  { id: 4, user_id: 1, name: '订单归档库', type: 'postgres', host: '10.0.0.13', port: 5432, database: 'orders_archive', username: 'archiver', ssl_mode: 'require', connection_timeout: 15, environment: 'test', color_label: '', has_password: true, proxy_id: 3, proxy_name: 'PgBouncer 网关', ssh_tunnel_id: null, tunnel_name: '', created_at: new Date(Date.now()-86400000*7).toISOString(), updated_at: new Date(Date.now()-86400000*2).toISOString() },
  { id: 5, user_id: 2, name: '测试 MySQL', type: 'mysql', host: '10.0.0.14', port: 3306, database: 'test_db', username: 'tester', ssl_mode: 'disable', connection_timeout: 10, environment: 'test', color_label: '', has_password: false, proxy_id: null, proxy_name: '', ssh_tunnel_id: 1, tunnel_name: '生产跳板机(废弃)', created_at: new Date(Date.now()-86400000*1).toISOString(), updated_at: new Date().toISOString() },
]
let connectionIdSeq = 5

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

let snippetIdSeq = 5
const snippets = [
  { id: 1, user_id: 1, name: '近7天订单统计', sql_text: 'SELECT DATE(created_at) as day, COUNT(*) as cnt FROM orders WHERE created_at >= NOW() - INTERVAL 7 DAY GROUP BY 1 ORDER BY 1', database_name: 'orders', connection_id: 1, visibility: 'shared', owner_name: 'admin', created_at: new Date(Date.now()-86400000*2).toISOString(), updated_at: new Date().toISOString() },
  { id: 2, user_id: 2, name: '高价值用户', sql_text: 'SELECT * FROM users WHERE total_spent > 10000 ORDER BY total_spent DESC LIMIT 50', database_name: 'users', connection_id: 2, visibility: 'shared', owner_name: 'dev1', created_at: new Date(Date.now()-86400000*5).toISOString(), updated_at: new Date().toISOString() },
  { id: 3, user_id: 1, name: '慢查询TOP', sql_text: 'SELECT query, mean_exec_time FROM pg_stat_statements ORDER BY mean_exec_time DESC LIMIT 20', database_name: 'orders', connection_id: 1, visibility: 'private', owner_name: 'admin', created_at: new Date().toISOString(), updated_at: new Date().toISOString() },
  { id: 4, user_id: 1, name: 'Redis大Key扫描', sql_text: '-- SCAN 0 MATCH * COUNT 100\n-- 结合 MEMORY USAGE key', database_name: '', connection_id: 3, visibility: 'shared', owner_name: 'admin', created_at: new Date().toISOString(), updated_at: new Date().toISOString() },
  { id: 5, user_id: 2, name: '订单状态分布', sql_text: 'SELECT status, COUNT(*) FROM orders GROUP BY status', database_name: 'orders', connection_id: 1, visibility: 'private', owner_name: 'dev1', created_at: new Date().toISOString(), updated_at: new Date().toISOString() },
]

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

function filterConnections(items, query) {
  let out = [...items]
  if (query.type && query.type !== 'all' && query.type !== '') {
    out = out.filter(c => c.type === query.type)
  }
  if (query.keyword && query.keyword.trim()) {
    const kw = query.keyword.toLowerCase()
    out = out.filter(c => c.name.toLowerCase().includes(kw) || c.host.toLowerCase().includes(kw) || (c.database && c.database.toLowerCase().includes(kw)))
  }
  if (query.q && query.q.trim()) {
    const kw = query.q.toLowerCase()
    out = out.filter(c => c.name.toLowerCase().includes(kw) || c.host.toLowerCase().includes(kw))
  }
  return out
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
    return ok(res, { status: 'ok', version: 'mock-0.14.0', env: 'arena', note: 'Step15: 火焰图+对比+Vim+主题+命令面板+AI优化+计划对比+参数化+快照' })
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
  if (pathname === '/api/v1/auth/change-password' && method === 'POST') {
    return ok(res, { ok: true })
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
      connection_types: { mysql: connections.filter(c=>c.type==='mysql').length, postgres: connections.filter(c=>c.type==='postgres').length, redis: connections.filter(c=>c.type==='redis').length },
      trend,
      rank: connections.map(c => ({ connection_id: c.id, name: c.name, count: 100 + Math.floor(Math.random() * 200) })),
      recent: [
        { id: 1, connection_id: 1, user_id: 1, connection_name: '订单核心库', database_name: 'orders', sql_text: 'SELECT * FROM orders LIMIT 100', status: 1, affected_rows: null, execution_time_ms: 72, error_message: null, created_at: new Date().toISOString() },
      ],
      days,
    })
  }

  // ---------- 数据源 ----------
  if (pathname === '/api/v1/connections' && method === 'GET') {
    let items = filterConnections(connections, query)
    items = items.map(c => {
      const proxy = c.proxy_id ? proxies.find(p => p.id === c.proxy_id) : null
      const tun = c.ssh_tunnel_id ? tunnels.find(t => t.id === c.ssh_tunnel_id) : null
      return { ...c, proxy_name: proxy ? proxy.name : c.proxy_name || '', tunnel_name: tun ? tun.name : c.tunnel_name || '' }
    })
    return ok(res, { items, total: items.length })
  }
  if (pathname === '/api/v1/connections' && method === 'POST') {
    const body = await readBody(req)
    if (!body.name || !body.host) {
      return json(res, 40000, { message: 'name/host 必填' }, 400)
    }
    connectionIdSeq += 1
    const proxy = body.proxy_id ? proxies.find(p => p.id === body.proxy_id) : null
    const tun = body.ssh_tunnel_id ? tunnels.find(t => t.id === body.ssh_tunnel_id) : null
    const conn = {
      id: connectionIdSeq,
      user_id: 1,
      name: body.name,
      type: body.type || 'mysql',
      host: body.host,
      port: body.port || (body.type === 'postgres' ? 5432 : body.type === 'redis' ? 6379 : 3306),
      database: body.database || '',
      username: body.username || '',
      ssl_mode: body.ssl_mode || 'disable',
      connection_timeout: body.connection_timeout || 10,
      environment: body.environment || 'dev',
      color_label: body.color_label || '',
      has_password: !!body.password,
      proxy_id: body.proxy_id || null,
      proxy_name: proxy ? proxy.name : '',
      ssh_tunnel_id: body.ssh_tunnel_id || null,
      tunnel_name: tun ? tun.name : '',
      created_at: new Date().toISOString(),
      updated_at: new Date().toISOString(),
    }
    connections.push(conn)
    return ok(res, conn)
  }
  if (pathname.match(/^\/api\/v1\/connections\/\d+$/) && method === 'GET') {
    const id = Number(pathname.split('/')[4])
    const c = connections.find(x => x.id === id)
    if (!c) return json(res, 40400, null, 404)
    const proxy = c.proxy_id ? proxies.find(p => p.id === c.proxy_id) : null
    const tun = c.ssh_tunnel_id ? tunnels.find(t => t.id === c.ssh_tunnel_id) : null
    return ok(res, { ...c, proxy_name: proxy ? proxy.name : c.proxy_name || '', tunnel_name: tun ? tun.name : c.tunnel_name || '' })
  }
  if (pathname.match(/^\/api\/v1\/connections\/\d+$/) && method === 'PUT') {
    const id = Number(pathname.split('/')[4])
    const body = await readBody(req)
    const c = connections.find(x => x.id === id)
    if (!c) return json(res, 40400, null, 404)
    const proxy = body.proxy_id ? proxies.find(p => p.id === body.proxy_id) : (body.proxy_id === null ? null : (c.proxy_id ? proxies.find(p=>p.id===c.proxy_id) : null))
    const tun = body.ssh_tunnel_id ? tunnels.find(t => t.id === body.ssh_tunnel_id) : null
    Object.assign(c, {
      name: body.name !== undefined ? body.name : c.name,
      type: body.type || c.type,
      host: body.host || c.host,
      port: body.port || c.port,
      database: body.database !== undefined ? body.database : c.database,
      username: body.username !== undefined ? body.username : c.username,
      ssl_mode: body.ssl_mode || c.ssl_mode,
      connection_timeout: body.connection_timeout || c.connection_timeout,
      environment: body.environment || c.environment,
      color_label: body.color_label !== undefined ? body.color_label : c.color_label,
      proxy_id: body.proxy_id !== undefined ? body.proxy_id : c.proxy_id,
      proxy_name: body.proxy_id === null ? '' : (proxy ? proxy.name : c.proxy_name),
      ssh_tunnel_id: body.ssh_tunnel_id !== undefined ? body.ssh_tunnel_id : c.ssh_tunnel_id,
      tunnel_name: body.ssh_tunnel_id === null ? '' : (tun ? tun.name : c.tunnel_name),
      updated_at: new Date().toISOString(),
    })
    if (body.password) c.has_password = true
    return ok(res, c)
  }
  if (pathname.match(/^\/api\/v1\/connections\/\d+$/) && method === 'DELETE') {
    const id = Number(pathname.split('/')[4])
    const idx = connections.findIndex(x => x.id === id)
    if (idx >= 0) connections.splice(idx, 1)
    return ok(res, { id })
  }
  if (pathname === '/api/v1/connections/test' && method === 'POST') {
    const body = await readBody(req)
    const t = body.type || 'postgres'
    const versions = { postgres: 'PostgreSQL 16.4 mock', mysql: 'MySQL 8.0.36 mock', redis: 'Redis 7.2 mock' }
    return ok(res, { ok: true, type: t, version: versions[t] || 'mock', elapsed_ms: 20 + Math.floor(Math.random()*60) })
  }
  if (pathname.match(/^\/api\/v1\/connections\/\d+\/test$/) && method === 'POST') {
    const id = Number(pathname.split('/')[4])
    const c = connections.find(x => x.id === id)
    const t = c ? c.type : 'postgres'
    const versions = { postgres: 'PostgreSQL 16.4 mock', mysql: 'MySQL 8.0.36 mock', redis: 'Redis 7.2 mock' }
    // 模拟 prod 环境二次确认：若 prod 且无代理，返回慢
    const elapsed = c && c.environment === 'prod' ? 120 + Math.floor(Math.random()*80) : 20 + Math.floor(Math.random()*60)
    return ok(res, { ok: true, type: t, version: versions[t] || 'mock', elapsed_ms: elapsed, via_proxy: c && c.proxy_id ? proxies.find(p=>p.id===c.proxy_id)?.name : null })
  }

  // SSH 隧道兼容（废弃）
  if (pathname === '/api/v1/ssh-tunnels' && method === 'GET') {
    return ok(res, { items: tunnels, total: tunnels.length, deprecated: true, note: '已废弃，请使用 /api/v1/proxies' })
  }
  if (pathname === '/api/v1/ssh-tunnels' && method === 'POST') {
    const body = await readBody(req)
    tunnelIdSeq += 1
    const t = { id: tunnelIdSeq, user_id: 1, name: body.name || '未命名隧道', host: body.host || '', port: body.port || 22, username: body.username || '', auth_type: body.auth_type || 'private_key', has_private_key: !!body.private_key, has_passphrase: !!body.passphrase, has_password: !!body.password, created_at: new Date().toISOString(), updated_at: new Date().toISOString() }
    tunnels.push(t)
    return ok(res, t)
  }
  if (pathname === '/api/v1/ssh-tunnels/test' && method === 'POST') {
    return ok(res, { ok: true, deprecated: true })
  }
  if (pathname.match(/^\/api\/v1\/ssh-tunnels\/\d+$/) && method === 'PUT') {
    const id = Number(pathname.split('/')[4])
    const body = await readBody(req)
    const t = tunnels.find(x => x.id === id)
    if (t) Object.assign(t, { name: body.name || t.name, host: body.host || t.host, port: body.port || t.port, username: body.username || t.username, auth_type: body.auth_type || t.auth_type, updated_at: new Date().toISOString() })
    return ok(res, t || {})
  }
  if (pathname.match(/^\/api\/v1\/ssh-tunnels\/\d+$/) && method === 'DELETE') {
    const id = Number(pathname.split('/')[4])
    const idx = tunnels.findIndex(x => x.id === id)
    if (idx >= 0) tunnels.splice(idx, 1)
    // 清理 connections 引用
    connections.forEach(c => { if (c.ssh_tunnel_id === id) { c.ssh_tunnel_id = null; c.tunnel_name = '' } })
    return ok(res, { id })
  }

  // ---------- 资产 ----------
  if (pathname === '/api/v1/assets/overview' && method === 'GET') {
    return ok(res, { connection_total: connections.length, table_total: 128, view_total: 14, owned_tables: 73, sensitive_fields: 19 })
  }
  if (pathname === '/api/v1/assets/tree' && method === 'GET') {
    let items = [
      { id: 1, name: '订单核心库', type: 'postgres', environment: 'prod', table_count: 26, is_empty: false, databases: [{ name: 'orders', table_count: 26, schemas: [{ name: 'public', table_count: 4, tables: [{ name: 'orders', type: 'table' }, { name: 'v_monthly_sales', type: 'view' }] }] }] },
      { id: 2, name: '用户中心', type: 'mysql', environment: 'dev', table_count: 12, is_empty: false, databases: [{ name: 'users', table_count: 12, schemas: [{ name: 'users', table_count: 12, tables: [{ name: 'users', type: 'table' }] }] }] },
      { id: 3, name: 'Redis 缓存集群', type: 'redis', environment: 'prod', table_count: 0, is_empty: true, databases: [] },
    ]
    if (query.connection_id) {
      const cid = Number(query.connection_id)
      items = items.filter(i => i.id === cid)
    }
    return ok(res, { items })
  }
  if (pathname === '/api/v1/assets/tables' && method === 'GET') {
    const all = [
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
      {
        snapshot: { id: 2, connection_id: 2, database_name: 'users', schema_name: 'users', table_name: 'users', table_type: 'table', table_comment: '用户表', estimated_rows: 54320, data_bytes: 1234567, raw_columns: [{ name: 'id', ordinal: 1, data_type: 'bigint', is_primary: true, is_nullable: false, default: '', comment: '' }], raw_indexes: [], raw_keys: [], ddl_text: 'CREATE TABLE users ...', synced_at: new Date().toISOString() },
        connection_name: '用户中心',
        owner_user_id: 2,
        owner_name: 'dev1',
        business_desc: '用户主表',
        tags: ['用户'],
        sensitivity: 'sensitive',
        starred: false,
        query_count_30d: 120,
        sensitive_columns: 1,
      },
    ]
    let items = [...all]
    if (query.connection_id) items = items.filter(r => r.snapshot.connection_id === Number(query.connection_id))
    if (query.q) {
      const kw = query.q.toLowerCase()
      items = items.filter(r => r.snapshot.table_name.toLowerCase().includes(kw) || (r.snapshot.table_comment && r.snapshot.table_comment.toLowerCase().includes(kw)))
    }
    if (query.type) items = items.filter(r => r.snapshot.table_type === query.type)
    if (query.no_owner === '1') items = items.filter(r => !r.owner_user_id)
    if (query.starred === '1') items = items.filter(r => r.starred)
    const page = Number(query.page || 1)
    const pageSize = Number(query.page_size || 20)
    const start = (page-1)*pageSize
    return ok(res, { items: items.slice(start, start+pageSize), total: items.length, page, page_size: pageSize })
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
      connections: q ? filterConnections(connections, { keyword: q }).slice(0,3) : [],
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
    const body = await readBody(req)
    const targetConns = body.connection_id ? connections.filter(c=>c.id===body.connection_id) : connections.filter(c=>c.type!=='redis')
    return ok(res, {
      connections: targetConns.map(c => ({
        connection_id: c.id, connection_name: c.name, type: c.type, skipped: false,
        databases: [c.database || 'default'], tables: 24, views: 2, skipped_databases: c.type==='postgres' ? ['template0'] : [], errors: [], by_database: [{ database: c.database || 'default', schemas: 3, tables: 24, views: 2 }]
      })),
      tables: targetConns.length * 24, views: targetConns.length * 2
    })
  }
  if (pathname === '/api/v1/assets/star' && method === 'POST') {
    return ok(res, { starred: true })
  }
  if (pathname === '/api/v1/assets/annotations' && method === 'PUT') {
    return ok(res, { ok: true })
  }

  // 事务模拟
  if (!globalThis.__transactions) globalThis.__transactions = {}
  if (pathname === '/api/v1/query/transaction/begin' && method === 'POST') {
    const body = await readBody(req)
    const cid = Number(body.connection_id)
    const txId = `tx_${cid}_${Date.now()}`
    globalThis.__transactions[cid] = { transaction_id: txId, status: 'active', started_at: new Date().toISOString(), queries: 0, database: body.database || '' }
    return ok(res, { transaction_id: txId, status: 'active', connection_id: cid, started_at: globalThis.__transactions[cid].started_at })
  }
  if (pathname === '/api/v1/query/transaction/commit' && method === 'POST') {
    const body = await readBody(req)
    const cid = Number(body.connection_id)
    const tx = globalThis.__transactions[cid]
    if (tx) {
      tx.status = 'committed'
      delete globalThis.__transactions[cid]
    }
    return ok(res, { transaction_id: body.transaction_id, status: 'committed', committed: true })
  }
  if (pathname === '/api/v1/query/transaction/rollback' && method === 'POST') {
    const body = await readBody(req)
    const cid = Number(body.connection_id)
    const tx = globalThis.__transactions[cid]
    if (tx) {
      tx.status = 'rolled_back'
      delete globalThis.__transactions[cid]
    }
    return ok(res, { transaction_id: body.transaction_id, status: 'rolled_back', rolled_back: true })
  }
  if (pathname === '/api/v1/query/transaction/status' && method === 'GET') {
    const cid = Number(query.connection_id)
    const tx = globalThis.__transactions[cid]
    if (tx) {
      return ok(res, { active: true, transaction_id: tx.transaction_id, started_at: tx.started_at, queries: tx.queries, status: tx.status })
    }
    return ok(res, { active: false })
  }
  // AI 优化建议 mock
  if (pathname === '/api/v1/query/ai-optimize' && method === 'POST') {
    const body = await readBody(req)
    const sql = (body.sql || '').toLowerCase()
    const issues = []
    const indexes = []
    if (sql.includes('select *')) issues.push({ severity: 'medium', title: 'SELECT *', description: '建议显式列名' })
    if (!sql.includes('where')) issues.push({ severity: 'high', title: '全表扫描', description: '缺少 WHERE' })
    if (sql.includes('join')) indexes.push({ table: 'orders', type: 'B-Tree', ddl: 'CREATE INDEX idx_orders_user_id ON orders (user_id);', benefit: '90%' })
    return ok(res, { score: 75, summary: '存在优化空间', tags: ['全表扫描'], issues, indexes, rewrites: [], cost: { estimated_rows: '1000', estimated_cost: '120.5', actual_time: 22 } })
  }

  // 查询执行 - 支持 EXPLAIN + 参数化
  if (pathname === '/api/v1/query/execute' && method === 'POST') {
    const body = await readBody(req)
    const rawSql = body.sql || ''
    const params = body.params || {}
    // 参数替换预览（mock 仅记录，不真实替换执行，仅用于演示）
    let replacedSql = rawSql
    try {
      for (const [k, v] of Object.entries(params)) {
        const esc = String(v).replace(/'/g, "''")
        const quoted = isNaN(Number(v)) || v === '' ? `'${esc}'` : String(v)
        if (k.startsWith('$')) {
          replacedSql = replacedSql.split(k).join(quoted)
        } else {
          replacedSql = replacedSql.split(`{{${k}}}`).join(quoted)
          replacedSql = replacedSql.split(`{{ ${k} }}`).join(quoted)
          replacedSql = replacedSql.replace(new RegExp(`(?<!:):${k}\\b`, 'g'), quoted)
        }
      }
    } catch {}
    const sql = replacedSql.toLowerCase()
    const cid = Number(body.connection_id)
    if (globalThis.__transactions && globalThis.__transactions[cid]) {
      globalThis.__transactions[cid].queries = (globalThis.__transactions[cid].queries || 0) + 1
    }
    const conn = connections.find(c => c.id === cid)
    const connType = conn?.type || 'postgres'
    if (sql.trim().startsWith('explain')) {
      if (connType === 'mysql') {
        // MySQL EXPLAIN with filesort and temporary
        if (sql.includes('join')) {
          return ok(res, { kind: 'query', columns: ['id', 'select_type', 'table', 'type', 'possible_keys', 'key', 'rows', 'Extra'], rows: [[1, 'SIMPLE', 'orders', 'ALL', 'idx_user', null, 120000, 'Using where'], [1, 'SIMPLE', 'users', 'ref', 'PRIMARY', 'PRIMARY', 1, 'Using index']], truncated: false, duration_ms: 22 })
        }
        if (sql.includes('group') || sql.includes('order')) {
          return ok(res, { kind: 'query', columns: ['id', 'select_type', 'table', 'type', 'possible_keys', 'key', 'rows', 'Extra'], rows: [[1, 'SIMPLE', 'orders', 'ALL', null, null, 120000, 'Using temporary; Using filesort']], truncated: false, duration_ms: 18 })
        }
        return ok(res, { kind: 'query', columns: ['id', 'select_type', 'table', 'type', 'possible_keys', 'key', 'rows', 'Extra'], rows: [[1, 'SIMPLE', 'orders', 'ALL', 'idx_status', null, 120000, 'Using where']], truncated: false, duration_ms: 15 })
      } else {
        // PG: richer plan
        if (sql.includes('join')) {
          return ok(res, { kind: 'query', columns: ['QUERY PLAN'], rows: [['Hash Join  (cost=120.30..340.50 rows=1000 width=128)'], ['  Hash Cond: (orders.user_id = users.id)'], ['  ->  Seq Scan on orders  (cost=0.00..120.30 rows=1000 width=64)'], ['        Filter: (amount > 1000)'], ['  ->  Hash  (cost=20.00..20.00 rows=500 width=64)'], ['        ->  Seq Scan on users  (cost=0.00..20.00 rows=500 width=64)'], ['Planning Time: 0.24 ms'], ['Execution Time: 12.34 ms']], truncated: false, duration_ms: 28 })
        }
        if (sql.includes('group') || sql.includes('order')) {
          return ok(res, { kind: 'query', columns: ['QUERY PLAN'], rows: [['GroupAggregate  (cost=200.00..300.00 rows=30 width=32)'], ['  Group Key: status'], ['  ->  Sort  (cost=200.00..210.00 rows=1000 width=32)'], ['        Sort Key: status'], ['        ->  Seq Scan on orders  (cost=0.00..120.30 rows=1000 width=32)'], ['Planning Time: 0.18 ms'], ['Execution Time: 8.12 ms']], truncated: false, duration_ms: 22 })
        }
        return ok(res, { kind: 'query', columns: ['QUERY PLAN'], rows: [['Seq Scan on orders  (cost=0.00..120.30 rows=100 width=64)'], ['  Filter: (amount > 1000)'], ['Planning Time: 0.12 ms'], ['Execution Time: 2.34 ms']], truncated: false, duration_ms: 18 })
      }
    }
    if (sql.includes('count')) {
      return ok(res, { kind: 'query', columns: ['cnt'], rows: [[128]], truncated: false, duration_ms: 12 })
    }
    return ok(res, { kind: 'query', columns: ['month', 'sum'], rows: [['2026-01', 12000], ['2026-02', 15000], ['2026-03', 18000], ['2026-04', 21000]], truncated: false, duration_ms: 22 })
  }
  if (pathname === '/api/v1/metadata/databases' && method === 'GET') {
    const cid = Number(query.connection_id)
    const conn = connections.find(c=>c.id===cid)
    if (!conn) return ok(res, { items: [] })
    const dbs = conn.type === 'postgres' ? ['orders', 'orders_archive'] : conn.type === 'mysql' ? ['users', 'test_db'] : []
    return ok(res, { items: dbs.map(name=>({ name })) })
  }
  if (pathname === '/api/v1/metadata/schemas' && method === 'GET') {
    return ok(res, { items: [{ name: 'public' }, { name: 'shop' }] })
  }
  if (pathname === '/api/v1/metadata/tables' && method === 'GET') {
    return ok(res, { items: [{ name: 'orders', type: 'table', comment: '订单主表' }, { name: 'v_monthly_sales', type: 'view', comment: '月度视图' }] })
  }
  if (pathname === '/api/v1/metadata/columns' && method === 'GET') {
    const table = (query.table || '').toLowerCase()
    const common = {
      orders: [
        { name: 'id', data_type: 'bigint', is_primary: true, is_nullable: false, ordinal: 1 },
        { name: 'order_no', data_type: 'varchar(32)', is_primary: false, is_nullable: false, ordinal: 2 },
        { name: 'user_id', data_type: 'bigint', is_primary: false, is_nullable: false, ordinal: 3 },
        { name: 'total_amount', data_type: 'numeric(12,2)', is_primary: false, is_nullable: true, ordinal: 4 },
        { name: 'status', data_type: 'smallint', is_primary: false, is_nullable: false, ordinal: 5 },
        { name: 'created_at', data_type: 'timestamptz', is_primary: false, is_nullable: false, ordinal: 6 },
        { name: 'updated_at', data_type: 'timestamptz', is_primary: false, is_nullable: true, ordinal: 7 },
      ],
      users: [
        { name: 'id', data_type: 'bigint', is_primary: true, is_nullable: false, ordinal: 1 },
        { name: 'username', data_type: 'varchar(64)', is_primary: false, is_nullable: false, ordinal: 2 },
        { name: 'email', data_type: 'varchar(128)', is_primary: false, is_nullable: true, ordinal: 3 },
        { name: 'status', data_type: 'smallint', is_primary: false, is_nullable: false, ordinal: 4 },
        { name: 'created_at', data_type: 'datetime', is_primary: false, is_nullable: false, ordinal: 5 },
      ],
    }
    const items = common[table] || common['orders']
    return ok(res, { items })
  }
  if (pathname === '/api/v1/data/preview' && method === 'GET') {
    return ok(res, { columns: ['id', 'total_amount'], rows: [[1, 100], [2, 200], [3, 300]], total: 120, page: Number(query.page||1), page_size: Number(query.page_size||20), has_more: true })
  }
  if (pathname === '/api/v1/redis/overview' && method === 'GET') {
    const cid = Number(query.connection_id)
    const conn = connections.find(c=>c.id===cid)
    return ok(res, { version: '7.2.4 mock', mode: 'standalone', os: 'Linux 5.15 x86_64', uptime_days: 12 + Math.floor(Math.random()*5), connected_clients: 5 + Math.floor(Math.random()*10), used_memory_mb: 128 + Math.floor(Math.random()*50), used_memory_human: '128M', total_commands: 123456 + Math.floor(Math.random()*10000), total_keys: 1234, keyspaces: [{ db: 'db0', keys: 800, expires: 120 }, { db: 'db1', keys: 434, expires: 30 }] })
  }
  // Redis 键空间 - 增强
  if (!globalThis.__redisKeys) {
    globalThis.__redisKeys = [
      { key: 'user:1', type: 'string', ttl: 3600, size: 128, value: JSON.stringify({ id: 1, name: '张三', email: 'zhangsan@example.com' }) },
      { key: 'user:2', type: 'string', ttl: 3600, size: 128, value: JSON.stringify({ id: 2, name: '李四' }) },
      { key: 'user:1001:profile', type: 'hash', ttl: -1, size: 5, value: { name: '王五', age: '28', city: '上海' } },
      { key: 'order:100', type: 'hash', ttl: -1, size: 8, value: { id: '100', amount: '299.99', status: 'paid' } },
      { key: 'cart:42', type: 'hash', ttl: 1800, size: 3, value: { 'item:1': '2', 'item:2': '1' } },
      { key: 'session:abc123', type: 'string', ttl: 7200, size: 256, value: 'eyJhbGciOiJIUzI1NiJ9.mock' },
      { key: 'queue:orders', type: 'list', ttl: -1, size: 12, value: ['order:101', 'order:102', 'order:103'] },
      { key: 'tags:popular', type: 'set', ttl: -1, size: 8, value: ['electronics', 'books', 'clothing', 'food'] },
      { key: 'leaderboard:weekly', type: 'zset', ttl: 86400, size: 100, value: [{ member: 'user:1', score: 1500 }, { member: 'user:2', score: 1200 }] },
      { key: 'cache:product:123', type: 'string', ttl: 600, size: 512, value: JSON.stringify({ id: 123, name: 'iPhone 15', price: 5999 }) },
      { key: 'stats:daily:2026-05-11', type: 'hash', ttl: 86400*7, size: 24, value: { pv: '12345', uv: '3456' } },
      { key: 'lock:order:100', type: 'string', ttl: 30, size: 32, value: 'locked' },
    ]
  }
  if (pathname === '/api/v1/redis/keys' && method === 'GET') {
    let items = [...(globalThis.__redisKeys || [])]
    const pattern = query.pattern || '*'
    const typeFilter = query.type || ''
    const limit = Number(query.limit || 200)
    if (pattern && pattern !== '*') {
      // 简单通配：支持 * 前后缀
      const regexStr = pattern.replace(/[.+^${}()|[\]\\]/g, '\\$&').replace(/\*/g, '.*').replace(/\?/g, '.')
      const re = new RegExp(`^${regexStr}$`, 'i')
      items = items.filter(k => re.test(k.key))
    }
    if (typeFilter && typeFilter !== 'all') {
      items = items.filter(k => k.type === typeFilter)
    }
    const returned = items.slice(0, limit).map(({ key, type, ttl }) => ({ key, type, ttl }))
    return ok(res, { items: returned, returned: returned.length, total: items.length, cursor: 0 })
  }
  if (pathname === '/api/v1/redis/value' && method === 'GET') {
    const key = query.key || ''
    const found = (globalThis.__redisKeys || []).find((k) => k.key === key)
    if (!found) {
      return json(res, 40400, null, 404)
    }
    return ok(res, { key: found.key, type: found.type, ttl: found.ttl, size: found.size, value: found.value })
  }
  if (pathname === '/api/v1/redis/key' && method === 'DELETE') {
    const body = await readBody(req)
    const key = body.key || query.key || ''
    if (globalThis.__redisKeys) {
      const idx = globalThis.__redisKeys.findIndex((k) => k.key === key)
      if (idx >= 0) globalThis.__redisKeys.splice(idx, 1)
    }
    return ok(res, { key, deleted: true })
  }
  if (pathname === '/api/v1/redis/key/ttl' && method === 'PUT') {
    const body = await readBody(req)
    const key = body.key || ''
    const ttl = Number(body.ttl)
    const found = (globalThis.__redisKeys || []).find((k) => k.key === key)
    if (found) found.ttl = ttl
    return ok(res, { key, ttl })
  }
  if (pathname === '/api/v1/redis/key' && method === 'POST') {
    const body = await readBody(req)
    const key = body.key || ''
    const type = body.type || 'string'
    const value = body.value ?? ''
    const ttl = body.ttl !== undefined ? Number(body.ttl) : -1
    if (!key) return json(res, 40000, null, 400)
    if (!globalThis.__redisKeys) globalThis.__redisKeys = []
    const exists = globalThis.__redisKeys.find((k) => k.key === key)
    if (exists) {
      exists.type = type
      exists.value = value
      exists.ttl = ttl
      exists.size = typeof value === 'string' ? value.length : JSON.stringify(value).length
    } else {
      globalThis.__redisKeys.unshift({ key, type, ttl, size: typeof value === 'string' ? value.length : 100, value })
    }
    return ok(res, { key, type, ttl })
  }
  if (pathname === '/api/v1/redis/key/value' && method === 'PUT') {
    const body = await readBody(req)
    const key = body.key || ''
    const value = body.value
    const found = (globalThis.__redisKeys || []).find((k) => k.key === key)
    if (!found) return json(res, 40400, null, 404)
    found.value = value
    found.size = typeof value === 'string' ? value.length : JSON.stringify(value).length
    return ok(res, { key, type: found.type, value: found.value })
  }
  if (pathname === '/api/v1/redis/hash/field' && method === 'PUT') {
    const body = await readBody(req)
    const key = body.key || ''
    const field = body.field || ''
    const value = body.value || ''
    const found = (globalThis.__redisKeys || []).find((k) => k.key === key)
    if (!found || found.type !== 'hash') return json(res, 40400, null, 404)
    if (typeof found.value !== 'object' || Array.isArray(found.value)) found.value = {}
    found.value[field] = value
    return ok(res, { key, field, value })
  }
  if (pathname === '/api/v1/redis/hash/field' && method === 'DELETE') {
    const body = await readBody(req)
    const key = body.key || query.key || ''
    const field = body.field || query.field || ''
    const found = (globalThis.__redisKeys || []).find((k) => k.key === key)
    if (found && found.type === 'hash' && typeof found.value === 'object') {
      delete found.value[field]
    }
    return ok(res, { key, field, deleted: true })
  }
  if (pathname === '/api/v1/redis/list/push' && method === 'POST') {
    const body = await readBody(req)
    const key = body.key || ''
    const value = body.value || ''
    const direction = body.direction || 'right'
    const found = (globalThis.__redisKeys || []).find((k) => k.key === key)
    if (!found || found.type !== 'list') return json(res, 40400, null, 404)
    if (!Array.isArray(found.value)) found.value = []
    if (direction === 'left') found.value.unshift(value)
    else found.value.push(value)
    return ok(res, { key, value })
  }
  if (pathname === '/api/v1/redis/list/pop' && method === 'POST') {
    const body = await readBody(req)
    const key = body.key || ''
    const direction = body.direction || 'right'
    const found = (globalThis.__redisKeys || []).find((k) => k.key === key)
    if (!found || found.type !== 'list' || !Array.isArray(found.value)) return json(res, 40400, null, 404)
    const val = direction === 'left' ? found.value.shift() : found.value.pop()
    return ok(res, { key, value: val })
  }
  if (pathname === '/api/v1/redis/set/member' && method === 'POST') {
    const body = await readBody(req)
    const key = body.key || ''
    const member = body.member || ''
    const found = (globalThis.__redisKeys || []).find((k) => k.key === key)
    if (!found || found.type !== 'set') return json(res, 40400, null, 404)
    if (!Array.isArray(found.value)) found.value = []
    if (!found.value.includes(member)) found.value.push(member)
    return ok(res, { key, member })
  }
  if (pathname === '/api/v1/redis/set/member' && method === 'DELETE') {
    const body = await readBody(req)
    const key = body.key || query.key || ''
    const member = body.member || query.member || ''
    const found = (globalThis.__redisKeys || []).find((k) => k.key === key)
    if (found && Array.isArray(found.value)) {
      found.value = found.value.filter((m) => m !== member)
    }
    return ok(res, { key, member, deleted: true })
  }
  if (pathname === '/api/v1/redis/zset/member' && method === 'POST') {
    const body = await readBody(req)
    const key = body.key || ''
    const member = body.member || ''
    const score = Number(body.score || 0)
    const found = (globalThis.__redisKeys || []).find((k) => k.key === key)
    if (!found || found.type !== 'zset') return json(res, 40400, null, 404)
    if (!Array.isArray(found.value)) found.value = []
    const idx = found.value.findIndex((x) => x.member === member)
    if (idx >= 0) found.value[idx].score = score
    else found.value.push({ member, score })
    return ok(res, { key, member, score })
  }
  if (pathname === '/api/v1/redis/zset/member' && method === 'DELETE') {
    const body = await readBody(req)
    const key = body.key || query.key || ''
    const member = body.member || query.member || ''
    const found = (globalThis.__redisKeys || []).find((k) => k.key === key)
    if (found && Array.isArray(found.value)) {
      found.value = found.value.filter((x) => x.member !== member)
    }
    return ok(res, { key, member, deleted: true })
  }
  // 查询历史 - 增强版 v0.6.0：30条，支持 keyword/connection_id/status 过滤
  if (!globalThis.__historySeeded) {
    globalThis.__historySeeded = true
    globalThis.__queryHistory = []
    const sampleSQLs = [
      'SELECT * FROM orders LIMIT 100',
      'SELECT COUNT(*) FROM users WHERE status = 1',
      'SELECT DATE(created_at) as day, SUM(amount) as total FROM orders GROUP BY 1 ORDER BY 1',
      'UPDATE orders SET status = 2 WHERE id = 1001',
      'DELETE FROM temp_orders WHERE created_at < NOW() - INTERVAL 30 DAY',
      'SELECT * FROM orders WHERE amount > 1000 ORDER BY created_at DESC LIMIT 20',
      'SELECT u.name, COUNT(o.id) as order_cnt FROM users u LEFT JOIN orders o ON u.id = o.user_id GROUP BY u.name',
      'SELECT * FROM users WHERE email LIKE %test%',
      'INSERT INTO audit_log (action, user_id) VALUES (login, 1)',
      'SELECT * FROM orders_archive WHERE created_at < 2024-01-01',
    ]
    const now = Date.now()
    for (let i = 0; i < 30; i++) {
      const conn = connections[i % connections.length]
      const sql = sampleSQLs[i % sampleSQLs.length]
      const status = i % 7 === 0 ? 0 : 1
      globalThis.__queryHistory.push({
        id: 100 - i,
        connection_id: conn.id,
        connection_name: conn.name,
        user_id: 1,
        database_name: conn.database || (conn.type === 'postgres' ? 'orders' : 'users'),
        sql_text: sql,
        status,
        affected_rows: status === 1 && (sql.toUpperCase().includes('UPDATE') || sql.toUpperCase().includes('DELETE') || sql.toUpperCase().includes('INSERT')) ? Math.floor(Math.random()*20)+1 : null,
        execution_time_ms: 10 + Math.floor(Math.random()*300),
        row_count: status === 1 ? Math.floor(Math.random()*500) : 0,
        error_message: status === 0 ? 'syntax error at or near "WHERE"' : null,
        created_at: new Date(now - i * 3600 * 1000 - Math.random()*3600*1000).toISOString(),
      })
    }
  }
  if (pathname === '/api/v1/query/history' && method === 'GET') {
    let items = [...(globalThis.__queryHistory || [])]
    if (query.connection_id) {
      const cid = Number(query.connection_id)
      items = items.filter(h => h.connection_id === cid)
    }
    if (query.status === 'success' || query.status === '1') items = items.filter(h => h.status === 1)
    if (query.status === 'failed' || query.status === '0') items = items.filter(h => h.status === 0)
    if (query.keyword || query.q) {
      const kw = (query.keyword || query.q || '').toLowerCase()
      if (kw) items = items.filter(h => h.sql_text.toLowerCase().includes(kw) || (h.database_name||'').toLowerCase().includes(kw) || (h.connection_name||'').toLowerCase().includes(kw))
    }
    const page = Number(query.page || 1)
    const pageSize = Number(query.page_size || 20)
    const start = (page - 1) * pageSize
    return ok(res, { items: items.slice(start, start + pageSize), total: items.length, page, page_size: pageSize })
  }
  if (pathname.startsWith('/api/v1/query/history/') && method === 'DELETE') {
    const id = Number(pathname.split('/').pop())
    if (globalThis.__queryHistory) {
      const idx = globalThis.__queryHistory.findIndex((h) => h.id === id)
      if (idx >= 0) globalThis.__queryHistory.splice(idx, 1)
    }
    return ok(res, { id })
  }
  if (pathname === '/api/v1/query/history' && method === 'DELETE') {
    if (globalThis.__queryHistory) globalThis.__queryHistory = []
    return ok(res, { ok: true })
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
    if (query.connection_id) items = items.filter(r => r.connection_id === Number(query.connection_id))
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
    if (query.q) {
      const kw = query.q.toLowerCase()
      items = items.filter(d => d.name.toLowerCase().includes(kw))
    }
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

  // 代理管理占位
  if (pathname === '/api/v1/proxies' && method === 'GET') {
    let items = [...proxies]
    if (query.q) {
      const kw = query.q.toLowerCase()
      items = items.filter(p => p.name.toLowerCase().includes(kw) || p.host.toLowerCase().includes(kw))
    }
    return ok(res, { items, total: items.length, note: '占位，M5 实现真实拨号' })
  }
  if (pathname === '/api/v1/proxies' && method === 'POST') {
    const body = await readBody(req)
    if (!body.name || !body.host) return json(res, 40000, null, 400)
    proxyIdSeq += 1
    const p = { id: proxyIdSeq, user_id: 1, name: body.name, type: body.type || 'http', host: body.host, port: body.port || 8080, username: body.username || '', has_password: !!body.password, description: body.description || '', status: 'active', created_at: new Date().toISOString(), updated_at: new Date().toISOString() }
    proxies.push(p)
    return ok(res, p)
  }
  if (pathname === '/api/v1/proxies/test' && method === 'POST') {
    return ok(res, { ok: true, note: '占位，M5 将实现 HTTP/SOCKS5/DB Proxy 连通测试' })
  }
  if (pathname.match(/^\/api\/v1\/proxies\/\d+$/) && method === 'PUT') {
    const id = Number(pathname.split('/')[4])
    const body = await readBody(req)
    const p = proxies.find(x => x.id === id)
    if (!p) return json(res, 40400, null, 404)
    Object.assign(p, { name: body.name || p.name, type: body.type || p.type, host: body.host || p.host, port: body.port || p.port, username: body.username ?? p.username, description: body.description ?? p.description, status: body.status || p.status, updated_at: new Date().toISOString() })
    if (body.password) p.has_password = true
    return ok(res, p)
  }
  if (pathname.match(/^\/api\/v1\/proxies\/\d+$/) && method === 'DELETE') {
    const id = Number(pathname.split('/')[4])
    const idx = proxies.findIndex(x => x.id === id)
    if (idx >= 0) proxies.splice(idx, 1)
    connections.forEach(c => { if (c.proxy_id === id) { c.proxy_id = null; c.proxy_name = '' } })
    return ok(res, { id })
  }

  // SQL 片段团队共享 - Step6
  if (pathname === '/api/v1/snippets' && method === 'GET') {
    let items = [...snippets]
    if (query.q) {
      const kw = query.q.toLowerCase()
      items = items.filter(s => s.name.toLowerCase().includes(kw) || s.sql_text.toLowerCase().includes(kw))
    }
    if (query.visibility) {
      items = items.filter(s => s.visibility === query.visibility)
    }
    if (query.connection_id) {
      items = items.filter(s => s.connection_id === Number(query.connection_id))
    }
    const page = Number(query.page || 1)
    const pageSize = Number(query.page_size || 20)
    const start = (page - 1) * pageSize
    return ok(res, { items: items.slice(start, start + pageSize), total: items.length, page, page_size: pageSize })
  }
  if (pathname === '/api/v1/snippets' && method === 'POST') {
    const body = await readBody(req)
    if (!body.name || !body.sql_text) return json(res, 40000, null, 400)
    snippetIdSeq += 1
    const s = {
      id: snippetIdSeq,
      user_id: 1,
      name: body.name,
      sql_text: body.sql_text,
      database_name: body.database_name || '',
      connection_id: body.connection_id || null,
      visibility: body.visibility || 'private',
      owner_name: 'admin',
      created_at: new Date().toISOString(),
      updated_at: new Date().toISOString(),
    }
    snippets.unshift(s)
    return ok(res, s)
  }
  if (pathname.match(/^\/api\/v1\/snippets\/\d+$/) && method === 'GET') {
    const id = Number(pathname.split('/')[4])
    const s = snippets.find(x => x.id === id)
    if (!s) return json(res, 40400, null, 404)
    return ok(res, s)
  }
  if (pathname.match(/^\/api\/v1\/snippets\/\d+$/) && method === 'PUT') {
    const id = Number(pathname.split('/')[4])
    const body = await readBody(req)
    const s = snippets.find(x => x.id === id)
    if (!s) return json(res, 40400, null, 404)
    Object.assign(s, { name: body.name || s.name, sql_text: body.sql_text || s.sql_text, database_name: body.database_name ?? s.database_name, visibility: body.visibility || s.visibility, updated_at: new Date().toISOString() })
    return ok(res, s)
  }
  if (pathname.match(/^\/api\/v1\/snippets\/\d+$/) && method === 'DELETE') {
    const id = Number(pathname.split('/')[4])
    const idx = snippets.findIndex(x => x.id === id)
    if (idx >= 0) snippets.splice(idx, 1)
    return ok(res, { id })
  }

  // 其他兜底
  if (pathname === '/api/v1/roles' && method === 'GET') {
    return ok(res, { items: [{ code: 'admin', name: '管理员' }, { code: 'developer', name: '开发者' }, { code: 'readonly', name: '只读' }] })
  }
  if (pathname === '/api/v1/users' && method === 'GET') {
    return ok(res, { items: users, total: users.length, page: 1, page_size: 20 })
  }
  if (pathname === '/api/v1/audit-logs' && method === 'GET') {
    return ok(res, { items: [], total: 0, page: 1, page_size: 20 })
  }
  if (pathname.startsWith('/api/v1/')) {
    return ok(res, { items: [], total: 0 })
  }

  json(res, 40400, null, 404)
})

server.listen(PORT, '0.0.0.0', () => {
  console.log(`[mock-backend] listening on 0.0.0.0:${PORT} - datasource mock v0.5.0: ${connections.length} conns, ${proxies.length} proxies, ${tunnels.length} tunnels(deprecated)`)
})
