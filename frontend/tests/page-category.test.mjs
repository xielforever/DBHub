import assert from 'node:assert/strict'
import fs from 'node:fs/promises'
import test from 'node:test'
import vm from 'node:vm'
import ts from 'typescript'
import * as vue from 'vue'
import * as vueRouter from 'vue-router'

const source = await fs.readFile(new URL('../src/composables/usePageCategory.ts', import.meta.url), 'utf8')
const context = vm.createContext({})
const module = new vm.SourceTextModule(ts.transpileModule(source, {
  compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ESNext },
}).outputText, { context })
await module.link(specifier => {
  const imports = { vue, 'vue-router': vueRouter }[specifier]
  assert.ok(imports, `Unexpected import ${specifier}`)
  return new vm.SyntheticModule(Object.keys(imports), function () {
    for (const [key, value] of Object.entries(imports)) this.setExport(key, value)
  }, { context })
})
await module.evaluate()

const members = { id: 'members', name: '成员' }
const menus = { id: 'menus', name: '菜单配置' }
const governance = { id: 'governance', name: '治理审批' }

async function setup(url, allowed = [members, menus, governance], options) {
  const router = vueRouter.createRouter({
    history: vueRouter.createMemoryHistory(),
    routes: [{ path: '/space', component: {} }],
  })
  const app = vue.createApp({})
  app.use(router)
  await router.push(url)
  const categories = vue.ref(allowed)
  const selection = app.runWithContext(() => module.namespace.usePageCategory(categories, options))
  return { ...selection, categories, router }
}

test('a same-page selection and history navigation follow the URL and keep its space, other parameters and hash', async () => {
  const { category, selectCategory, router } = await setup('/space?workspace_id=2&view=review&tab=members#detail')
  assert.equal(category.value.id, 'members')

  await selectCategory('menus')
  assert.equal(category.value.id, 'menus')
  assert.deepEqual(router.currentRoute.value.query, { workspace_id: '2', view: 'review', tab: 'menus' })
  assert.equal(router.currentRoute.value.hash, '#detail')

  const navigated = () => new Promise(resolve => {
    const remove = router.afterEach(() => {
      remove()
      resolve()
    })
  })
  const back = navigated()
  router.back()
  await back
  assert.equal(category.value.id, 'members')

  const forward = navigated()
  router.forward()
  await forward
  assert.equal(category.value.id, 'menus')

  await router.push('/space?workspace_id=2&tab=governance')
  assert.equal(category.value.id, 'governance')
})

test('a missing category defaults to an available choice, including after permission changes', async () => {
  const { category, categories } = await setup('/space?workspace_id=2', [governance], { defaultId: 'menus' })
  assert.equal(category.value.id, 'governance')
  categories.value = [members, menus]
  assert.equal(category.value.id, 'menus')
  categories.value = []
  assert.equal(category.value, undefined)
})

test('an explicit unavailable or malformed category cannot select a different panel', async () => {
  const { category, categories, router, selectCategory } = await setup('/space?tab=members', [menus])
  assert.equal(category.value, undefined)
  await selectCategory('members')
  assert.equal(router.currentRoute.value.query.tab, 'members')

  for (const url of ['/space?tab=menus&tab=members', '/space?tab', '/space?tab=', '/space?tab=unknown']) {
    await router.push(url)
    assert.equal(category.value, undefined)
  }

  await selectCategory('menus')
  assert.equal(category.value.id, 'menus')
  categories.value = [members]
  assert.equal(category.value, undefined)
})

test('an old notification link opens the matching category without rewriting the address', async () => {
  const business = { id: 'business', name: '业务审批与通知' }
  const { category, router } = await setup('/space?tab=notifications', [business], {
    aliases: { notifications: 'business' },
  })
  assert.equal(category.value.id, 'business')
  assert.equal(router.currentRoute.value.query.tab, 'notifications')
})
