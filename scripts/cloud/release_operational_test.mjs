// 仅供独立的本地 release-check 项目使用；状态文件中的临时令牌不输出。
import { randomBytes, randomUUID } from 'node:crypto'
import { readFile, writeFile, unlink } from 'node:fs/promises'
import assert from 'node:assert/strict'

const base = 'http://frontend'
const statePath = '/runtime/release-operational-state.json'
const reportPath = '/runtime/release-operational-checks.json'
let token = ''
async function api(method, path, body, expected = 200) {
  const response = await fetch(base + '/api/v1' + path, {
    method,
    signal: AbortSignal.timeout(15_000),
    headers: { 'Content-Type': 'application/json', Authorization: 'Bearer ' + token },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  assert.equal(response.status, expected, `${method} ${path}: unexpected HTTP status`)
  const result = await response.json()
  if (expected < 400) assert.equal(result.code, 0, 'unexpected API error')
  return result.data
}
const health = await (await fetch(base + '/api/health')).json()
assert.equal(health.data.env, 'production', 'production backend required')
const mode = process.argv[2]
if (mode === 'prepare') {
  const login = await api('POST', '/auth/login', { username: process.env.APP_ADMIN_USERNAME, password: process.env.APP_ADMIN_PASSWORD })
  token = login.access_token
  const conns = await api('GET', '/connections')
  const connection = conns.items.find(c => c.name === 'Cloud PostgreSQL')
  assert.ok(connection, 'run browser acceptance first')
  const table = 'dbhub_release_receipt_' + randomBytes(8).toString('hex')
  const execute = sql => api('POST', '/query/execute', { request_id: randomUUID(), connection_id: connection.id, sql })
  // 先独占状态文件，以免重复准备时覆盖上次待核实编号。
  const state = { token, table, connectionId: connection.id, preparedAt: new Date().toISOString() }
  await writeFile(statePath, JSON.stringify(state), { mode: 0o600, flag: 'wx' })
  await execute(`CREATE TABLE ${table} (id INT PRIMARY KEY, amount INT NOT NULL)`)
  await execute(`INSERT INTO ${table} VALUES (1,0)`)
  state.body = { request_id: randomUUID(), connection_id: connection.id, sql: `UPDATE ${table} SET amount=amount+1 WHERE id=1` }
  const result = await api('POST', '/query/execute', state.body)
  assert.equal(result.affected_rows, 1)
  state.startedAt = (await api('GET', '/metrics/query')).started_at
  await writeFile(statePath, JSON.stringify(state), { mode: 0o600 })
  console.log('PASS prepared one durable write and a private recovery ID')
} else if (mode === 'verify') {
  const state = JSON.parse(await readFile(statePath, 'utf8'))
  token = state.token
  const checks = []
  const passed = text => { checks.push(text); console.log('PASS ' + text) }
  const before = await api('GET', '/metrics/query')
  assert.notEqual(before.started_at, state.startedAt, 'backend must be replaced before verification')
  passed('existing authenticated session survives backend replacement')
  const receipt = await api('GET', '/query/executions/' + state.body.request_id)
  assert.equal(receipt.status, 'succeeded')
  const duplicate = await api('POST', '/query/execute', state.body, 409)
  assert.equal(duplicate.status, 'succeeded')
  await api('POST', '/query/execute', { ...state.body, sql: state.body.sql.replace('amount+1', 'amount+2') }, 409)
  const afterDuplicate = await api('GET', '/metrics/query')
  assert.equal(afterDuplicate.executions.total, before.executions.total)
  assert.equal(afterDuplicate.requests.completed.duplicate.count - before.requests.completed.duplicate.count, 1)
  assert.equal(afterDuplicate.requests.completed.conflict.count - before.requests.completed.conflict.count, 1)
  passed('durable receipt, duplicate rejection and request conflict survive backend replacement')
  const result = await api('POST', '/query/execute', { request_id: randomUUID(), connection_id: state.connectionId, sql: `SELECT amount FROM ${state.table} WHERE id=1` })
  assert.deepEqual(result.rows, [[1]])
  passed('confirmed write remains applied exactly once for the retained request ID')
  await api('POST', '/query/execute', { request_id: randomUUID(), connection_id: state.connectionId, sql: `DROP TABLE ${state.table}` })
  await api('POST', '/auth/logout')
  await unlink(statePath)
  await writeFile(reportPath, JSON.stringify({ scope: 'local-production-backend-replacement', verifiedAt: new Date().toISOString(), checks, cleanupSucceeded: true }, null, 2), { mode: 0o600 })
} else {
  throw new Error('use prepare or verify')
}
