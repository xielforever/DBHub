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

test('target reset refuses in-flight and unknown work and clears only settled results', async () => {
  const scope = effectScope(), pending = deferred<ExecResult>()
  const execution = scope.run(() => useSqlExecution(() => pending.promise, rejected))!
  const running = execution.run(context)
  assert.equal(execution.reset(), false)
  pending.resolve(result); await running
  assert.equal(execution.reset(), true)
  assert.equal(execution.state.value.result, null)
  assert.equal(execution.state.value.context, null)
  assert.equal(execution.state.value.status, 'idle')
  scope.stop()
  const other = effectScope()
  const unknown = other.run(() => useSqlExecution(async () => result, rejected, memoryStore('pending-target')))!
  assert.equal(unknown.reset(), false)
  assert.equal(unknown.state.value.requestId, 'pending-target')
  other.stop()
})
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

test('cancel keeps the pending phase and honors the verified outcome over late rows', async () => {
  const cases = [
    { status: 'failed', confirmed: true, expected: 'canceled' },
    { status: 'failed', confirmed: undefined, expected: 'error' },
    { status: 'succeeded', confirmed: undefined, expected: 'completed' },
  ] as const
  for (const branch of cases) {
    const scope = effectScope()
    const pending = deferred<ExecResult>(), lookupStarted = deferred<string>(), outcome = deferred<ExecutionReceipt>()
    let signal: AbortSignal | undefined
    const execution = scope.run(() => useSqlExecution(async (_, current) => {
      signal = current
      return pending.promise
    }, id => {
      lookupStarted.resolve(id)
      return outcome.promise
    }))!
    try {
      const running = execution.run(context)
      const originalID = execution.state.value.requestId
      execution.cancel()
      assert.equal(signal?.aborted, true)
      pending.resolve(result)
      const id = await lookupStarted.promise
      assert.equal(id, originalID)
      assert.equal(execution.state.value.status, 'canceling')
      assert.equal(await execution.run(context), false)
      outcome.resolve({ ...receipt(id, branch.status), cancellation_confirmed: branch.confirmed })
      await running
      assert.equal(execution.state.value.status, branch.expected)
      assert.equal(execution.state.value.result, null)
    } finally {
      scope.stop()
    }
  }
})

test('blocked delivery keeps the original ID until an authorized receipt can be read', async () => {
  const scope = effectScope(), store = memoryStore()
  let sends = 0, lookups = 0
  const execution = scope.run(() => useSqlExecution(async () => {
    sends++
    throw Object.assign(new Error('执行结果停止返回'), { status: 409, code: 40902 })
  }, async id => {
    lookups++
    return receipt(id, 'succeeded', 'write')
  }, store))!
  await execution.run({ ...context, sql: 'UPDATE items SET count=count+1 WHERE id=1' })
  assert.equal(execution.state.value.status, 'unknown')
  assert.equal(execution.state.value.result, null)
  assert.match(store.id!, /^[a-f0-9-]{36}$/)
  assert.equal(lookups, 0)
  assert.equal(await execution.run(context), false)
  await execution.check()
  assert.equal(execution.state.value.status, 'success')
  assert.equal(store.id, null)
  assert.equal(sends, 1)
  assert.equal(lookups, 1)
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

test('adopting a verified legacy receipt preserves only matching unresolved execution IDs', () => {
  const scope = effectScope(), store = memoryStore(), id = crypto.randomUUID(), other = crypto.randomUUID()
  const execution = scope.run(() => useSqlExecution(async () => result, rejected, store))!
  assert.equal(execution.adoptReceipt(id, receipt(crypto.randomUUID(), 'running')), false)
  assert.equal(store.id, null)
  assert.equal(execution.adoptReceipt(id, receipt(id, 'running')), true)
  assert.equal(store.id, id)
  assert.equal(execution.needsReview.value, true)
  assert.equal(execution.adoptReceipt(other, receipt(other, 'succeeded')), false)
  assert.equal(store.id, id)
  scope.stop()
})

test('adopting a completed legacy read leaves its receipt visible and clears the pending record', () => {
  const scope = effectScope(), store = memoryStore(), id = crypto.randomUUID()
  const execution = scope.run(() => useSqlExecution(async () => result, rejected, store))!
  assert.equal(execution.adoptReceipt(id, receipt(id, 'succeeded')), true)
  assert.equal(store.id, null)
  assert.equal(execution.state.value.status, 'completed')
  assert.equal(execution.state.value.requestId, id)
  assert.equal(execution.state.value.result, null)
  scope.stop()
})

test('formatting preserves MySQL identifiers and PostgreSQL casts', async () => {
  const mysql = await formatSql('select `id` from `orders`', 'mysql')
  assert.match(mysql, /`id`/); assert.match(mysql, /`orders`/)
  const postgres = await formatSql('select 1::text as value', 'postgres')
  assert.match(postgres, /1::TEXT/i)
  await assert.rejects(() => formatSql('SELECT `id`', 'postgres'))
})
