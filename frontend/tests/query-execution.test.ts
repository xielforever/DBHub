import assert from 'node:assert/strict'
import { test } from 'node:test'
import { effectScope } from 'vue'
import { useSqlExecution, type ExecutionContext } from '../src/views/query/useSqlExecution.ts'
import { formatSql } from '../src/views/query/formatSql.ts'
import type { ExecResult } from '../src/api/workbench.ts'

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

test('execution freezes its target and prevents duplicate runs without a queue', async () => {
  const scope = effectScope()
  const pending = deferred<ExecResult>()
  let calls = 0
  let received: Readonly<ExecutionContext> | undefined
  const execution = scope.run(() => useSqlExecution(async snapshot => {
    calls++
    received = snapshot
    return pending.promise
  }))!
  const input = { ...context }
  const running = execution.run(input)
  input.database = 'different_db'
  input.sql = 'DELETE FROM another_table'
  assert.equal(await execution.run(input), false)
  assert.equal(calls, 1)
  assert.deepEqual(received, context)
  assert.equal(execution.busy.value, true)
  pending.resolve(result)
  await running
  assert.equal(execution.state.value.status, 'success')
  assert.equal(execution.state.value.context?.database, 'first_db')
  assert.equal(execution.state.value.context?.sql, 'SELECT 1')
  scope.stop()
})

test('cancel aborts the matching request and discards a success racing with cancellation', async () => {
  const scope = effectScope()
  const pending = deferred<ExecResult>()
  let signal: AbortSignal | undefined
  const execution = scope.run(() => useSqlExecution(async (_, current) => { signal = current; return pending.promise }))!
  const running = execution.run(context)
  execution.cancel()
  assert.equal(signal?.aborted, true)
  assert.equal(execution.state.value.status, 'canceling')
  assert.equal(execution.busy.value, true)
  assert.equal(await execution.run(context), false)
  pending.resolve(result)
  await running
  assert.equal(execution.state.value.status, 'canceled')
  assert.equal(execution.state.value.result, null)
  assert.equal(execution.busy.value, false)
  scope.stop()
})

test('cancel rejection settles without an error, and a later run receives only its own result', async () => {
  const scope = effectScope()
  const pending = deferred<ExecResult>()
  let first = true
  const execution = scope.run(() => useSqlExecution(async () => {
    if (first) { first = false; return pending.promise }
    return { ...result, rows: [[2]] }
  }))!
  const running = execution.run(context)
  execution.cancel()
  pending.reject(new Error('aborted'))
  await running
  assert.equal(execution.state.value.status, 'canceled')
  assert.equal(execution.state.value.error, '')
  await execution.run({ ...context, sql: 'SELECT 2' })
  assert.deepEqual(execution.state.value.result?.rows, [[2]])
  assert.equal(execution.state.value.context?.sql, 'SELECT 2')
  scope.stop()
})

test('execution errors remain attributable and do not contaminate a successful retry', async () => {
  const scope = effectScope()
  let fail = true
  const execution = scope.run(() => useSqlExecution(async () => {
    if (fail) throw new Error('字段不存在')
    return result
  }))!
  await execution.run(context)
  assert.equal(execution.state.value.status, 'error')
  assert.equal(execution.state.value.error, '字段不存在')
  assert.equal(execution.state.value.context?.connectionId, 1)
  fail = false
  await execution.run({ ...context, connectionId: 2, database: 'second_db' })
  assert.equal(execution.state.value.status, 'success')
  assert.equal(execution.state.value.error, '')
  assert.equal(execution.state.value.context?.database, 'second_db')
  scope.stop()
})

test('leaving the page aborts the active request and ignores its later response', async () => {
  const scope = effectScope()
  const pending = deferred<ExecResult>()
  let signal: AbortSignal | undefined
  const execution = scope.run(() => useSqlExecution(async (_, current) => { signal = current; return pending.promise }))!
  const running = execution.run(context)
  const stateBeforeLeaving = execution.state.value
  scope.stop()
  assert.equal(signal?.aborted, true)
  pending.resolve(result)
  await running
  assert.equal(execution.state.value, stateBeforeLeaving)
  assert.equal(await execution.run(context), false)
})

test('formatting supports MySQL identifiers and PostgreSQL casts without changing their dialect', async () => {
  const mysql = await formatSql('select \u0060id\u0060 from \u0060orders\u0060', 'mysql')
  assert.match(mysql, /\u0060id\u0060/)
  assert.match(mysql, /\u0060orders\u0060/)
  const postgres = await formatSql('select 1::text as value', 'postgres')
  assert.match(postgres, /1::TEXT/i)
  await assert.rejects(() => formatSql('SELECT \u0060id\u0060', 'postgres'))
})
