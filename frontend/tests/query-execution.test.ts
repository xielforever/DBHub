import assert from 'node:assert/strict'
import { test } from 'node:test'
import { effectScope } from 'vue'
import { useSqlExecution, type ExecutionContext, type ExecutionSnapshot } from '../src/views/query/useSqlExecution.ts'
import { formatSql } from '../src/views/query/formatSql.ts'
import type { ExecResult, ExecutionReceipt } from '../src/api/workbench.ts'

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: Error) => void
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}
const context: ExecutionContext = {
  connectionId: 1, connectionName: 'PostgreSQL', database: 'first_db', sql: 'SELECT 1', selected: false,
}
const result: ExecResult = { kind: 'query', columns: ['value'], rows: [[1]], duration_ms: 1 }
function receipt(id: string, status: ExecutionReceipt['status'], kind: 'query' | 'write' = 'query'): ExecutionReceipt {
  return { request_id: id, connection_id: 1, database: 'first_db', status, transactional: false, kind, affected_rows: 1, duration_ms: 5, message: '服务端已确认未完成' }
}
const rejected = async (id: string) => receipt(id, 'rejected')
function memoryStore(id: string | null = null) {
  return { id, read() { return this.id }, write(value: string) { this.id = value }, clear() { this.id = null } }
}

test('one in-flight execution freezes its target and includes a stable request ID', async () => {
  const scope = effectScope(), pending = deferred<ExecResult>(), store = memoryStore()
  let calls = 0, received: Readonly<ExecutionSnapshot> | undefined
  const execution = scope.run(() => useSqlExecution(async snapshot => {
    calls++; received = snapshot; return pending.promise
  }, rejected, store))!
  const input = { ...context }, running = execution.run(input)
  input.database = 'different_db'; input.sql = 'DELETE FROM another_table'
  assert.equal(await execution.run(input), false)
  assert.equal(calls, 1)
  assert.equal(received?.database, context.database)
  assert.equal(received?.sql, context.sql)
  assert.match(received!.requestId, /^[a-f0-9-]{36}$/)
  assert.equal(store.id, received?.requestId)
  pending.resolve(result); await running
  assert.equal(execution.state.value.status, 'success')
  assert.equal(store.id, null)
  scope.stop()
})

test('intentional reruns after confirmed success use different IDs', async () => {
  const scope = effectScope(), ids: string[] = []
  const execution = scope.run(() => useSqlExecution(async snapshot => { ids.push(snapshot.requestId); return result }, rejected))!
  await execution.run(context); await execution.run(context)
  assert.equal(ids.length, 2)
  assert.notEqual(ids[0], ids[1])
  scope.stop()
})

test('cancel racing with success checks the receipt instead of displaying late rows', async () => {
  const scope = effectScope(), pending = deferred<ExecResult>()
  let signal: AbortSignal | undefined, lookups = 0
  const execution = scope.run(() => useSqlExecution(async (_, current) => { signal = current; return pending.promise }, async id => { lookups++; return receipt(id, 'failed') }))!
  const running = execution.run(context)
  execution.cancel()
  assert.equal(signal?.aborted, true)
  assert.equal(await execution.run(context), false)
  pending.resolve(result); await running
  assert.equal(lookups, 1)
  assert.equal(execution.state.value.status, 'canceled')
  assert.equal(execution.state.value.result, null)
  scope.stop()
})

test('lost write response is recovered from its receipt without executing SQL again', async () => {
  const scope = effectScope(), store = memoryStore()
  let calls = 0
  const execution = scope.run(() => useSqlExecution(async () => { calls++; throw new Error('connection reset') }, async id => receipt(id, 'succeeded', 'write'), store))!
  await execution.run({ ...context, sql: 'UPDATE items SET count=count+1 WHERE id=1' })
  assert.equal(calls, 1)
  assert.equal(execution.state.value.status, 'success')
  assert.equal(execution.state.value.result?.affected_rows, 1)
  assert.equal(store.id, null)
  scope.stop()
})

test('unknown cancellation blocks new execution until explicit acknowledgement', async () => {
  const scope = effectScope(), pending = deferred<ExecResult>(), store = memoryStore()
  const execution = scope.run(() => useSqlExecution(async () => pending.promise, async id => receipt(id, 'unknown'), store))!
  const running = execution.run(context)
  execution.cancel(); pending.reject(new Error('aborted')); await running
  assert.equal(execution.state.value.status, 'unknown')
  assert.equal(execution.needsReview.value, true)
  assert.equal(await execution.run(context), false)
  assert.equal(store.id, execution.state.value.requestId)
  execution.acknowledge()
  assert.equal(execution.needsReview.value, false)
  assert.equal(store.id, null)
  scope.stop()
})

test('known HTTP rejection remains attributable and permits a fresh request', async () => {
  const scope = effectScope()
  let fail = true, lookups = 0
  const execution = scope.run(() => useSqlExecution(async () => {
    if (fail) throw Object.assign(new Error('字段不存在'), { status: 400 })
    return result
  }, async id => { lookups++; return receipt(id, 'unknown') }))!
  await execution.run(context)
  assert.equal(execution.state.value.status, 'error')
  assert.equal(execution.state.value.error, '字段不存在')
  assert.equal(lookups, 0)
  fail = false
  await execution.run({ ...context, connectionId: 2, database: 'second_db' })
  assert.equal(execution.state.value.status, 'success')
  assert.equal(execution.state.value.context?.database, 'second_db')
  scope.stop()
})

test('an unknown receipt overrides HTTP 400 and never suggests a safe retry', async () => {
  const scope = effectScope(), store = memoryStore()
  const execution = scope.run(() => useSqlExecution(async snapshot => {
    throw Object.assign(new Error('execution failed'), { status: 400, data: receipt(snapshot.requestId, 'unknown') })
  }, rejected, store))!
  await execution.run(context)
  assert.equal(execution.state.value.status, 'unknown')
  assert.match(execution.state.value.error, /不代表写入已回滚/)
  assert.ok(store.id)
  scope.stop()
})

test('refresh restores only the pending ID and queries status without resending SQL', async () => {
  const scope = effectScope(), id = crypto.randomUUID(), store = memoryStore(id)
  let executions = 0, lookups = 0
  const execution = scope.run(() => useSqlExecution(async () => { executions++; return result }, async current => { lookups++; assert.equal(current, id); return receipt(id, 'succeeded', 'write') }, store))!
  assert.equal(execution.needsReview.value, true)
  assert.equal(execution.state.value.context, null)
  assert.equal(await execution.run(context), false)
  await execution.check()
  assert.equal(executions, 0)
  assert.equal(lookups, 1)
  assert.equal(execution.state.value.status, 'success')
  assert.equal(execution.state.value.receipt?.database, 'first_db')
  scope.stop()
})

test('missing or unavailable receipt remains unknown and preserves recovery', async () => {
  const scope = effectScope(), store = memoryStore()
  const execution = scope.run(() => useSqlExecution(async () => { throw new Error('timeout') }, async () => { throw Object.assign(new Error('not found'), { status: 404 }) }, store))!
  await execution.run(context)
  assert.equal(execution.state.value.status, 'unknown')
  assert.equal(await execution.run(context), false)
  assert.ok(store.id)
  scope.stop()
})

test('storage failure prevents sending a request whose recovery ID would be lost', async () => {
  const scope = effectScope()
  let calls = 0
  const execution = scope.run(() => useSqlExecution(async () => { calls++; return result }, rejected, {
    read: () => null, write: () => { throw new Error('quota') }, clear: () => {},
  }))!
  assert.equal(await execution.run(context), false)
  assert.equal(calls, 0)
  assert.equal(execution.state.value.status, 'error')
  scope.stop()
})

test('leaving the page aborts and retains the pending ID without applying late results', async () => {
  const scope = effectScope(), pending = deferred<ExecResult>(), store = memoryStore()
  let signal: AbortSignal | undefined
  const execution = scope.run(() => useSqlExecution(async (_, current) => { signal = current; return pending.promise }, rejected, store))!
  const running = execution.run(context), stateBeforeLeaving = execution.state.value
  scope.stop()
  assert.equal(signal?.aborted, true)
  pending.resolve(result); await running
  assert.equal(execution.state.value, stateBeforeLeaving)
  assert.ok(store.id)
})

test('concurrent status checks use one request and never enter the SQL executor', async () => {
  const scope = effectScope(), pending = deferred<ExecutionReceipt>(), id = crypto.randomUUID()
  let calls = 0
  const execution = scope.run(() => useSqlExecution(async () => { throw new Error('must not execute') }, async () => { calls++; return pending.promise }, memoryStore(id)))!
  const checking = execution.check()
  await execution.check()
  assert.equal(calls, 1)
  pending.resolve(receipt(id, 'running')); await checking
  assert.equal(execution.state.value.status, 'unknown')
  scope.stop()
})

test('completed query recovery does not present an empty table as a saved result', async () => {
  const scope = effectScope()
  const execution = scope.run(() => useSqlExecution(async () => { throw new Error('lost') }, async id => receipt(id, 'succeeded')))!
  await execution.run(context)
  assert.equal(execution.state.value.status, 'completed')
  assert.equal(execution.state.value.result, null)
  assert.match(execution.state.value.error, /查询结果未保存/)
  scope.stop()
})

test('formatting preserves MySQL identifiers and PostgreSQL casts', async () => {
  const mysql = await formatSql('select `id` from `orders`', 'mysql')
  assert.match(mysql, /`id`/); assert.match(mysql, /`orders`/)
  const postgres = await formatSql('select 1::text as value', 'postgres')
  assert.match(postgres, /1::TEXT/i)
  await assert.rejects(() => formatSql('SELECT `id`', 'postgres'))
})
