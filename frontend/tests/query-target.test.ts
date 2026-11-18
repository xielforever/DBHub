import assert from 'node:assert/strict'
import { test } from 'node:test'
import { effectScope } from 'vue'
import { useQueryTarget, targetKind, sameTarget, environmentLabel } from '../src/views/query/useQueryTarget.ts'
import type { ConnectionItem } from '../src/api/datasource.ts'

const source = (id: number, kind = 'sql', database = 'main') => ({ id, name: `source-${id}`, database, environment: 'test', capabilities: { sql: kind === 'sql', keys: kind === 'keys', metadata: kind === 'metadata' } }) as ConnectionItem

test('关闭后重开只接受本次打开目标，旧来源请求即使忽略取消也不能改选', async () => {
  const old = deferred<ConnectionItem[]>(), recent = deferred<ConnectionItem[]>()
  let calls = 0
  const signals: AbortSignal[] = []
  const { picker: p, scope } = setup({ sources: signal => { signals.push(signal); return ++calls === 1 ? old.promise : recent.promise } })
  const first = p.show(1)
  p.close()
  const second = p.show(2)
  recent.resolve([source(1), source(2)]); await second
  old.resolve([source(1), source(2)]); await first
  assert.equal(signals[0]?.aborted, true)
  assert.equal(p.draft.value?.source.id, 2)
  assert.equal(p.open.value, true)
  scope.stop()
})

test('确认在途刷新不能清空草稿或启动并发来源请求', async () => {
  const pending = deferred<ConnectionItem[]>()
  let calls = 0
  const { picker: p, scope } = setup({ sources: () => ++calls === 1 ? Promise.resolve([source(1)]) : pending.promise })
  await p.show(1)
  const confirm = p.confirm()
  const refresh = p.loadSources()
  assert.equal(p.draft.value?.source.id, 1)
  assert.equal(calls, 2)
  pending.resolve([source(1)])
  await refresh; assert.equal(await confirm, true)
  scope.stop()
})

test('来源刷新发现能力或Redis固定库变化时清除正式目标', async () => {
  for (const replacement of [source(1, 'metadata'), source(1, 'keys', '1')]) {
    const { picker: p, scope } = setup({ sources: async () => [replacement] })
    p.current.value = { source: source(1, 'keys', '0'), database: '0' }
    await p.loadSources()
    assert.equal(p.current.value, null)
    scope.stop()
  }
})

test('确认复核发现当前来源已撤销时即使候选失败也清除旧正式目标', async () => {
  let calls = 0
  const { picker: p, scope, navigations } = setup({ sources: async () => ++calls === 1 ? [source(1), source(2)] : [] })
  p.current.value = { source: source(1), database: 'main' }
  await p.show(2)
  assert.equal(await p.confirm(), false)
  assert.equal(p.current.value, null)
  assert.match(p.submitError.value, /权限或能力/)
  assert.equal(navigations.length, 0)
  scope.stop()
})

test('来源网络失败与SQL默认库变化不会误清除有效正式上下文', async () => {
  let fail = true
  const { picker: p, scope } = setup({ sources: async () => {
    if (fail) throw new Error('读取超时')
    return [source(1, 'sql', 'other')]
  } })
  p.current.value = { source: source(1), database: 'main' }
  await p.loadSources()
  assert.equal(p.sourceError.value, '读取超时')
  assert.equal(p.current.value?.database, 'main')
  fail = false; await p.loadSources()
  assert.equal(p.current.value?.database, 'main')
  scope.stop()
})

test('关闭后的来源失败不在已关闭弹窗留下错误', async () => {
  const pending = deferred<ConnectionItem[]>()
  const { picker: p, scope } = setup({ sources: () => pending.promise })
  const opening = p.show(1)
  p.close(); pending.reject(new Error('旧请求失败')); await opening
  assert.equal(p.sourceError.value, '')
  assert.equal(p.sourceLoading.value, false)
  assert.equal(p.draft.value, null)
  scope.stop()
})
function deferred<T>() { let resolve!: (value: T) => void; let reject!: (reason: Error) => void; const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no }); return { promise, resolve, reject } }
function setup(overrides: Partial<Parameters<typeof useQueryTarget>[0]> = {}) {
  const scope = effectScope(), navigations: unknown[] = []
  const picker = scope.run(() => useQueryTarget({ sources: async () => [source(1), source(2), source(3, 'keys', '0')], databases: async () => ['main', 'other'], context: () => 'identity:space:epoch', blocked: () => '', navigate: async target => { navigations.push(target) }, ...overrides }))!
  return { picker, scope, navigations }
}

test('候选改选和取消不更改正式目标；确认后整体生效', async () => {
  const { picker: p, scope, navigations } = setup()
  p.current.value = { source: source(1), database: 'main' }
  await p.show(); assert.match(p.disabledReason.value, /当前/)
  await p.chooseSource(2); p.chooseDatabase('other')
  assert.equal(p.current.value.source.id, 1); assert.equal(navigations.length, 0)
  p.close(); assert.equal(p.current.value.source.id, 1); assert.equal(p.draft.value, null)
  await p.show(2); p.chooseDatabase('other'); assert.equal(await p.confirm(), true)
  assert.equal(p.current.value.source.id, 2); assert.equal(p.current.value.database, 'other'); assert.equal(p.open.value, false); assert.equal(navigations.length, 1)
  scope.stop()
})

test('快速换源忽略迟到列表，默认库不存在时不选择第一项', async () => {
  const slow = deferred<string[]>()
  const { picker: p, scope } = setup({ databases: id => id === 1 ? slow.promise : Promise.resolve(['different']) })
  await p.show(); const first = p.chooseSource(1); await p.chooseSource(2)
  assert.equal(p.draftDatabase.value, ''); slow.resolve(['main']); await first
  assert.deepEqual(p.databases.value, ['different']); assert.equal(p.draftSource.value?.id, 2)
  p.chooseDatabase('forged'); assert.equal(p.draftDatabase.value, '')
  scope.stop()
})

test('读取失败保留错误而不是空状态，重试只刷新草稿', async () => {
  let failed = true
  const { picker: p, scope, navigations } = setup({ databases: async () => { if (failed) throw new Error('数据库读取失败'); return ['main'] } })
  await p.show(1); assert.equal(p.databaseError.value, '数据库读取失败'); assert.equal(await p.confirm(), false)
  failed = false; await p.chooseSource(1); assert.equal(p.databaseError.value, ''); assert.equal(navigations.length, 0)
  scope.stop()
})

test('确认重新读取候选，数据库撤销后拒绝切换', async () => {
  let calls = 0
  const { picker: p, scope, navigations } = setup({ databases: async () => ++calls === 1 ? ['main'] : [] })
  await p.show(1); assert.equal(await p.confirm(), false); assert.match(p.submitError.value, /不可用/); assert.equal(navigations.length, 0); assert.equal(p.current.value, null)
  scope.stop()
})

test('确认期间权限变化清空状态，迟到响应不能恢复目标', async () => {
  const pending = deferred<ConnectionItem[]>()
  let calls = 0, context = 'a'
  const { picker: p, scope, navigations } = setup({ sources: () => ++calls === 1 ? Promise.resolve([source(1)]) : pending.promise, context: () => context })
  await p.show(1); const run = p.confirm(); context = 'b'; p.invalidate(); pending.resolve([source(1)]); await run
  assert.equal(p.current.value, null); assert.equal(p.open.value, false); assert.equal(p.sources.value.length, 0); assert.equal(navigations.length, 0)
  scope.stop()
})

test('取消后迟到数据库响应不恢复草稿', async () => {
  const pending = deferred<string[]>(); const { picker: p, scope } = setup({ databases: () => pending.promise })
  await p.show(); const run = p.chooseSource(1); p.close(); pending.resolve(['main']); await run
  assert.equal(p.draft.value, null); assert.equal(p.databases.value.length, 0); assert.equal(p.databaseLoading.value, false)
  scope.stop()
})

test('执行保护在确认之前和复核结束后都生效', async () => {
  let blocked = ''
  const { picker: p, scope, navigations } = setup({ blocked: () => blocked, databases: async () => ['main'] })
  await p.show(1); blocked = '执行中'; assert.equal(await p.confirm(), false); assert.equal(navigations.length, 0)
  scope.stop()
  let calls = 0
  const b = setup({ blocked: () => blocked, databases: async () => { if (++calls === 2) blocked = '待核实'; return ['main'] } })
  blocked = ''; await b.picker.show(1); assert.equal(await b.picker.confirm(), false); assert.equal(b.navigations.length, 0); b.scope.stop()
})

test('提交期间防重且禁止关闭，导航失败保留旧目标和草稿', async () => {
  const pending = deferred<void>(); let calls = 0
  const { picker: p, scope } = setup({ navigate: () => { calls++; return pending.promise } })
  p.current.value = { source: source(2), database: 'main' }; await p.show(1)
  const run = p.confirm(); await Promise.resolve(); await Promise.resolve()
  assert.equal(await p.confirm(), false); p.close(); assert.equal(p.open.value, true)
  pending.reject(new Error('导航失败')); await run
  assert.equal(calls, 1); assert.equal(p.current.value.source.id, 2); assert.equal(p.draft.value?.source.id, 1); assert.equal(p.submitError.value, '导航失败'); assert.equal(p.submitting.value, false)
  scope.stop()
})

test('Redis固定上下文不请求虚构数据库列表，不可用来源不可提交', async () => {
  let databases = 0
  const { picker: p, scope } = setup({ databases: async () => { databases++; return [] }, sources: async () => [source(3, 'keys', '0'), source(4, 'none')] })
  await p.show(3); assert.equal(p.draftDatabase.value, '0'); assert.equal(await p.confirm(), true); assert.equal(databases, 0)
  await p.show(4); assert.equal(await p.confirm(), false); assert.match(p.databaseError.value, /没有可用/); scope.stop()
})

test('刷新发现当前来源已撤权立即清除正式目标；同名数据库按源区分', async () => {
  const { picker: p, scope } = setup({ sources: async () => [source(2)] })
  p.current.value = { source: source(1), database: 'main' }; await p.loadSources(); assert.equal(p.current.value, null)
  assert.equal(sameTarget({ source: source(1), database: 'main' }, { source: source(2), database: 'main' }), false)
  assert.equal(targetKind(source(2, 'metadata')), 'metadata'); assert.equal(environmentLabel({ ...source(1), environment: undefined } as unknown as ConnectionItem), '环境未标注')
  scope.stop()
})
