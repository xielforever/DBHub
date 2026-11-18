import test from 'node:test'
import assert from 'node:assert/strict'
import fs from 'node:fs/promises'
import vm from 'node:vm'
import ts from 'typescript'
import { computed, effectScope, nextTick, reactive, ref, watch } from 'vue'
import { useQueryTarget, targetKind, environmentLabel } from '../src/views/query/useQueryTarget.ts'

// 执行真实父组件脚本及选择器，只替换外部API、路由和子组件渲染。
const file = await fs.readFile(new URL('../src/views/query/WorkspaceView.vue', import.meta.url), 'utf8')
const source = file.match(/<script setup lang="ts">([\s\S]*?)<\/script>/)[1]
const connection = (id, kind = 'sql') => ({ id, name: `source-${id}`, database: kind === 'keys' ? '0' : 'main', environment: 'test', capabilities: { sql: kind === 'sql', keys: kind === 'keys', metadata: kind === 'metadata' } })

async function setup(options = {}) {
  const routes = [], databaseCalls = [], scope = effectScope()
  const route = reactive({ query: options.query ?? {}, fullPath: '/query' })
  const user = reactive({ user: { id: 1 }, epoch: 1, permissionKey: 'space:1', identityEpoch() { return this.epoch } })
  const context = vm.createContext({ Error, console })
  let guard
  const values = {
    vue: { computed, nextTick, ref, watch: (...args) => scope.run(() => watch(...args)), onMounted() {} },
    'vue-router': {
      useRoute: () => route,
      useRouter: () => ({ replace: async target => { routes.push(target); if (options.failNavigation?.()) throw new Error('导航失败'); return undefined } }),
      isNavigationFailure: () => false, NavigationFailureType: { duplicated: 16 }, onBeforeRouteUpdate: fn => { guard = fn },
    },
    '../../api/datasource': {
      connectionApi: { list: async () => {
        if (options.failSources?.()) throw new Error('候选读取失败')
        return { items: [connection(1), connection(2, 'keys'), connection(3, 'metadata'), connection(4)] }
      } },
    },
    '../../api/workbench': { workbenchApi: { databases: async id => { databaseCalls.push(id); return { items: [{ name: 'main' }, { name: 'other' }] } } } },
    '../../stores/user': { useUserStore: () => user },
    './useQueryTarget': { useQueryTarget: deps => scope.run(() => useQueryTarget(deps)), targetKind, environmentLabel },
  }
  const exported = 'picker,sqlWorkspace,discardRequired,discardConfirmed,blocked,securityGeneration,closePicker,loadRouteTarget,routeTargetError'
  const module = new vm.SourceTextModule(ts.transpileModule(source + `\nexport {${exported}}`, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ESNext } }).outputText, { context })
  await module.link(specifier => {
    const value = values[specifier] ?? (specifier.endsWith('.vue') ? { default: {} } : specifier === 'lucide-vue-next' ? { ChevronDown: {} } : null)
    assert.ok(value, specifier)
    return new vm.SyntheticModule(Object.keys(value), function () { for (const [key, item] of Object.entries(value)) this.setExport(key, item) }, { context })
  })
  await module.evaluate()
  const c = module.namespace
  c.picker.current.value = { source: connection(1), database: 'main' }
  c.sqlWorkspace.value = { hasDraft: true }
  await nextTick()
  return { c, routes, databaseCalls, user, guard, scope }
}

test('SQL切换Redis或元数据必须明确确认，换候选重新确认', async t => {
  const { c, routes, databaseCalls, scope } = await setup(); t.after(() => scope.stop())
  await c.picker.show(2); await nextTick()
  assert.equal(c.discardRequired.value, true)
  assert.equal(await c.picker.confirm(), false); assert.equal(routes.length, 0)
  c.discardConfirmed.value = true
  await c.picker.chooseSource(3); await nextTick()
  assert.equal(c.discardConfirmed.value, false)
  assert.equal(await c.picker.confirm(), false)
  await c.picker.chooseSource(2); await nextTick(); c.discardConfirmed.value = true
  assert.equal(await c.picker.confirm(), true)
  assert.equal(c.picker.current.value.source.id, 2)
  assert.equal(c.picker.current.value.database, '0')
  assert.equal(databaseCalls.includes(2), false)
  assert.equal(routes.length, 1)
})

test('取消跨能力改选保持旧目标，重新打开不沿用丢弃确认', async t => {
  const { c, scope } = await setup(); t.after(() => scope.stop())
  await c.picker.show(2); await nextTick(); c.discardConfirmed.value = true
  await c.closePicker(false); await nextTick()
  assert.equal(c.picker.current.value.source.id, 1)
  assert.equal(c.sqlWorkspace.value.hasDraft, true)
  await c.picker.show(2); await nextTick()
  assert.equal(c.discardConfirmed.value, false)
  assert.equal(await c.picker.confirm(), false)
})

test('同类SQL目标无需丢弃确认，导航失败可重试且正式目标不变', async t => {
  let failed = true
  const { c, scope } = await setup({ failNavigation: () => failed }); t.after(() => scope.stop())
  await c.picker.show(4); await nextTick()
  assert.equal(c.discardRequired.value, false)
  assert.equal(await c.picker.confirm(), false)
  assert.equal(c.picker.current.value.source.id, 1)
  assert.equal(c.picker.open.value, true)
  assert.match(c.picker.submitError.value, /导航失败/)
  failed = false; assert.equal(await c.picker.confirm(), true)
  assert.equal(c.picker.current.value.source.id, 4)
})

test('身份代次失效同步清空目标、草稿及确认，并更换编辑上下文代次', async t => {
  const { c, user, scope } = await setup(); t.after(() => scope.stop())
  await c.picker.show(2); await nextTick(); c.discardConfirmed.value = true
  user.epoch++
  assert.equal(c.picker.current.value, null)
  assert.equal(c.picker.draft.value, null)
  assert.equal(c.picker.open.value, false)
  assert.equal(c.discardConfirmed.value, false)
  assert.equal(c.securityGeneration.value, 1)
})

test('旧地址的来源不可用时可选择获准来源，读取失败可重试', async t => {
  let failSources = true
  const { c, routes, scope } = await setup({ query: { connection: '99', database: 'main' }, failSources: () => failSources })
  t.after(() => scope.stop())
  c.picker.current.value = null

  await c.loadRouteTarget()
  assert.equal(c.picker.sourceError.value, '候选读取失败')
  assert.equal(c.routeTargetError.value, '')

  failSources = false
  await c.loadRouteTarget()
  assert.equal(c.routeTargetError.value, '指定数据源不可用，请重新选择。')
  assert.equal(c.picker.sourceError.value, '')
  assert.equal(c.picker.current.value, null)

  await c.picker.show()
  assert.equal(c.picker.sources.value.some(source => source.id === 1), true)
  assert.equal(c.picker.sources.value.some(source => source.id === 99), false)
  await c.picker.chooseSource(1)
  assert.equal(await c.picker.confirm(), true)
  assert.equal(c.routeTargetError.value, '')
  assert.equal(routes[0].query.connection, '1')
})

test('执行待核实时父级拦截目标路由跳转和跨能力提交', async t => {
  const { c, guard, routes, scope } = await setup(); t.after(() => scope.stop())
  await c.picker.show(2); await nextTick(); c.discardConfirmed.value = true
  c.blocked.value = '执行结果待核实'
  assert.equal(guard({ query: { connection: '2' } }, { query: { connection: '1' } }), false)
  assert.equal(await c.picker.confirm(), false)
  assert.equal(routes.length, 0)
})
