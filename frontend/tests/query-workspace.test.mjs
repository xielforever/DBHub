import test from 'node:test'
import assert from 'node:assert/strict'
import fs from 'node:fs/promises'
import vm from 'node:vm'
import ts from 'typescript'
import { computed, effectScope, nextTick, onScopeDispose, reactive, ref, watch } from 'vue'
import { useQueryTarget, targetKind, environmentLabel } from '../src/views/query/useQueryTarget.ts'

// 运行实际父组件和目标状态机，替换路由、连接服务及子组件渲染。
const file = await fs.readFile(new URL('../src/views/query/WorkspaceView.vue', import.meta.url), 'utf8')
const source = file.match(/<script setup lang="ts">([\s\S]*?)<\/script>/)[1]
const connection = (id, kind = 'sql') => ({
  id, name: `source-${id}`, type: kind === 'sql' ? 'postgres' : kind === 'keys' ? 'redis' : 'jdbc',
  database: kind === 'keys' ? '0' : 'main', environment: 'test',
  capabilities: { sql: kind === 'sql', keys: kind === 'keys', metadata: kind !== 'keys' },
})
const script = (id, connectionId, database = 'main') => ({
  id, script_type: 'postgres', default_target: { connection_id: connectionId, database, state: 'available' },
})

async function setup(options = {}) {
  const routes = [], databaseCalls = [], scope = effectScope()
  const route = reactive({ query: options.query ?? {}, fullPath: '/query' })
  const user = reactive({ user: { id: 1 }, epoch: 1, permissionKey: 'space:1', identityEpoch() { return this.epoch } })
  const context = vm.createContext({ AbortController, Error, console })
  let guard
  const values = {
    vue: {
      computed, nextTick, reactive, ref, watch: (...args) => scope.run(() => watch(...args)),
      onScopeDispose: callback => scope.run(() => onScopeDispose(callback)), onMounted() {},
    },
    'vue-router': {
      useRoute: () => route,
      useRouter: () => ({ replace: async target => { routes.push(target); if (options.failNavigation?.()) throw new Error('导航失败') } }),
      isNavigationFailure: () => false, NavigationFailureType: { duplicated: 16 }, onBeforeRouteUpdate: fn => { guard = fn },
    },
    '../../api/datasource': {
      connectionApi: { list: async () => {
        if (options.failSources?.()) throw new Error('候选读取失败')
        return { items: [connection(1), connection(2, 'keys'), connection(3, 'metadata'), connection(4)] }
      } },
    },
    '../../api/workbench': { workbenchApi: { databases: async id => {
      databaseCalls.push(id)
      await options.waitDatabase?.(id)
      return { items: [{ name: 'main' }, { name: 'other' }] }
    } } },
    '../../api/workbench-scripts': { matchesScriptType: (item, type) => item.capabilities.sql && item.type === type },
    '../../stores/user': { useUserStore: () => user },
    './useQueryTarget': { useQueryTarget: deps => scope.run(() => useQueryTarget(deps)), targetKind, environmentLabel },
    'lucide-vue-next': { ArrowLeft: {}, ChevronDown: {} },
  }
  const exported = 'execution,browser,browseTargets,prepareBrowse,executionBlocked,securityGeneration,loadRouteTarget,routeTargetError,restoreScriptTarget,restoringDefault,defaultError,applyScriptTarget'
  const module = new vm.SourceTextModule(ts.transpileModule(source + `\nexport {${exported}}`, {
    compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ESNext },
  }).outputText, { context })
  await module.link(specifier => {
    const value = values[specifier] ?? (specifier.endsWith('.vue') ? { default: {} } : null)
    assert.ok(value, specifier)
    return new vm.SyntheticModule(Object.keys(value), function () {
      for (const [key, item] of Object.entries(value)) this.setExport(key, item)
    }, { context })
  })
  await module.evaluate()
  return { c: module.namespace, routes, databaseCalls, user, guard, scope }
}

test('正常进入工作台时不要求目标，不执行来源或数据库选择', async t => {
  const { c, routes, databaseCalls, scope } = await setup(); t.after(() => scope.stop())
  await c.loadRouteTarget()
  assert.equal(c.execution.current.value, null)
  assert.equal(c.execution.open.value, false)
  assert.equal(c.execution.sources.value.length, 0)
  assert.equal(databaseCalls.length, 0)
  assert.equal(routes.length, 0)
})

test('脚本默认目标各自恢复，临时切换只修改当前执行目标', async t => {
  const { c, scope } = await setup(); t.after(() => scope.stop())
  const first = script(10, 1)
  await c.restoreScriptTarget(first)
  assert.equal(c.execution.current.value.source.id, 1)
  await c.applyScriptTarget({ source: connection(4), database: 'other' })
  assert.equal(c.execution.current.value.source.id, 4)
  assert.equal(first.default_target.connection_id, 1)
  await c.restoreScriptTarget(script(11, 4, 'other'))
  assert.equal(c.execution.current.value.database, 'other')
  await c.restoreScriptTarget(first)
  assert.equal(c.execution.current.value.source.id, 1)
  assert.equal(c.execution.current.value.database, 'main')
})

test('切回脚本复核该标签的临时目标，失效时不回落到脚本默认库', async t => {
  const { c, databaseCalls, scope } = await setup(); t.after(() => scope.stop())
  const saved = script(10, 1)
  await c.restoreScriptTarget(saved, { connection_id: 4, database: 'other' })
  assert.equal(c.execution.current.value.source.id, 4)
  assert.equal(c.execution.current.value.database, 'other')
  assert.equal(saved.default_target.connection_id, 1)
  await c.restoreScriptTarget(saved, { connection_id: 4, database: 'revoked' })
  assert.equal(c.execution.current.value, null)
  assert.match(c.defaultError.value, /上次选择/)
  assert.deepEqual(databaseCalls, [4, 4])
})

test('草稿按选定类型恢复目标，拒绝连接改为其他类型后恢复', async t => {
  const { c, databaseCalls, scope } = await setup(); t.after(() => scope.stop())
  const selected = { connection_id: 4, database: 'other' }
  await c.restoreScriptTarget(undefined, selected, 'postgres')
  assert.equal(c.execution.current.value.database, 'other')
  await c.restoreScriptTarget(undefined, selected, 'mysql')
  assert.equal(c.execution.current.value, null)
  assert.match(c.defaultError.value, /上次选择/)
  assert.deepEqual(databaseCalls, [4])
})

test('脚本重开丢弃旧异步默认目标，失效默认保存空执行目标', async t => {
  let release
  const waiting = new Promise(resolve => { release = resolve })
  const { c, scope } = await setup({ waitDatabase: id => id === 1 ? waiting : undefined }); t.after(() => scope.stop())
  const pending = c.restoreScriptTarget(script(10, 1))
  assert.equal(c.restoringDefault.value, true)
  assert.equal(c.execution.current.value, null)
  await c.restoreScriptTarget(script(11, 4))
  release()
  await pending
  assert.equal(c.execution.current.value.source.id, 4)
  await c.restoreScriptTarget(script(12, 99))
  assert.equal(c.execution.current.value, null)
  assert.match(c.defaultError.value, /默认连接/)
})

test('元数据和 Redis 浏览目标独立于执行目标，Redis 不请求数据库列表', async t => {
  const { c, routes, databaseCalls, scope } = await setup(); t.after(() => scope.stop())
  await c.restoreScriptTarget(script(10, 1))
  await nextTick()
  c.prepareBrowse('metadata')
  await c.browser.show(3, 'main')
  assert.equal(await c.browser.confirm(), true)
  assert.equal(c.browseTargets.value.metadata.source.id, 3)
  assert.equal(c.execution.current.value.source.id, 1)
  c.prepareBrowse('keys')
  await c.browser.show(2)
  assert.equal(await c.browser.confirm(), true)
  assert.equal(c.browseTargets.value.keys.database, '0')
  assert.equal(c.browseTargets.value.metadata.source.id, 3)
  assert.equal(c.execution.current.value.source.id, 1)
  assert.equal(databaseCalls.includes(2), false)
  assert.equal(routes.length, 0)
})

test('取消和导航失败保持执行目标，成功重试才应用新目标', async t => {
  let failed = true
  const { c, scope } = await setup({ failNavigation: () => failed }); t.after(() => scope.stop())
  await c.restoreScriptTarget(script(10, 1))
  await c.execution.show(4); c.execution.close()
  assert.equal(c.execution.current.value.source.id, 1)
  await c.execution.show(4)
  assert.equal(await c.execution.confirm(), false)
  assert.equal(c.execution.current.value.source.id, 1)
  assert.equal(c.execution.open.value, true)
  assert.match(c.execution.submitError.value, /导航失败/)
  failed = false; assert.equal(await c.execution.confirm(), true)
  assert.equal(c.execution.current.value.source.id, 4)
})

test('身份代次更换同步清理执行目标、浏览上下文及候选', async t => {
  const { c, user, scope } = await setup(); t.after(() => scope.stop())
  await c.restoreScriptTarget(script(10, 1))
  await nextTick()
  c.prepareBrowse('objects')
  await c.browser.show(4)
  user.epoch++
  assert.equal(c.execution.current.value, null)
  assert.equal(c.browser.draft.value, null)
  assert.equal(c.browser.open.value, false)
  assert.equal(Object.keys(c.browseTargets.value).length, 0)
  assert.equal(c.securityGeneration.value, 1)
})

test('旧链接指定有效元数据时只恢复浏览目标', async t => {
  const { c, scope } = await setup({ query: { connection: '3', database: 'main' } }); t.after(() => scope.stop())
  await c.loadRouteTarget()
  assert.equal(c.execution.current.value, null)
  assert.equal(c.browseTargets.value.metadata.source.id, 3)
})

test('旧链接不可用或读取失败时保留空执行目标，可重新选择', async t => {
  let failSources = true
  const { c, routes, scope } = await setup({ query: { connection: '99', database: 'main' }, failSources: () => failSources })
  t.after(() => scope.stop())
  await c.loadRouteTarget()
  assert.match(c.routeTargetError.value, /无法核对链接/)
  failSources = false
  await c.loadRouteTarget()
  assert.match(c.routeTargetError.value, /指定数据源不可用/)
  assert.equal(c.execution.current.value, null)
  await c.execution.show(1)
  assert.equal(await c.execution.confirm(), true)
  assert.equal(c.routeTargetError.value, '')
  assert.equal(routes[0].query.connection, '1')
})

test('执行待核实时拦截目标切换和目标路由变更', async t => {
  const { c, guard, routes, scope } = await setup(); t.after(() => scope.stop())
  await c.execution.show(4)
  c.executionBlocked.value = '执行结果待核实'
  assert.equal(guard({ query: { connection: '4' } }, { query: { connection: '1' } }), false)
  assert.equal(await c.execution.confirm(), false)
  assert.equal(routes.length, 0)
})

test('刷新旧目标链接时待核实的回执不会被目标选择器遮挡', async t => {
  const { c, databaseCalls, routes, scope } = await setup({ query: { connection: '1', database: 'main' } })
  t.after(() => scope.stop())
  c.executionBlocked.value = '执行结果待核实'
  await c.loadRouteTarget()
  assert.equal(c.execution.open.value, false)
  assert.equal(c.execution.current.value, null)
  assert.equal(databaseCalls.length, 0)
  assert.equal(routes.length, 0)
})
