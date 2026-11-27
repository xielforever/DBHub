import test from 'node:test'
import assert from 'node:assert/strict'
import fs from 'node:fs/promises'
import vm from 'node:vm'
import ts from 'typescript'

async function setup(value) {
  const calls = [], context = vm.createContext({})
  const cache = new Map()
  async function module(name) {
    if (cache.has(name)) return cache.get(name)
    const code = await fs.readFile(new URL(`../src/api/${name}.ts`, import.meta.url), 'utf8')
    const m = new vm.SourceTextModule(ts.transpileModule(code, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ESNext } }).outputText, {
      context, initializeImportMeta: meta => { meta.env = { VITE_CONNECTION_TEST_TIMEOUT_MS: value } },
    })
    cache.set(name, m)
    await m.link(async specifier => {
      if (specifier === './connection-test-timeout') return module('connection-test-timeout')
      assert.equal(specifier, '../utils/request')
      return new vm.SyntheticModule(['default'], function () { this.setExport('default', { post: (...args) => { calls.push(args); return Promise.resolve({}) } }) }, { context })
    })
    return m
  }
  const ordinary = await module('datasource'); await ordinary.evaluate()
  const catalog = await module('source-catalog'); await catalog.evaluate()
  return { calls, ordinary: ordinary.namespace.connectionApi, catalog: catalog.namespace.sourceCatalogApi }
}

test('三个测试入口共用配置预算，目录新增绑定仍使用写请求默认预算', async () => {
  const { calls, ordinary, catalog } = await setup('90000')
  await ordinary.test({}); await ordinary.testSaved(1); await catalog.test(18, {})
  assert.deepEqual(calls.map(c => c[2].timeout), [90000, 90000, 90000])
  assert.equal(calls[2][0], '/datasource-definitions/18/connections?test=true')
  await catalog.add(18, {})
  assert.equal(calls[3][2], undefined)
})

test('连接诊断可取消并由页面就地处理错误，同时保留三个入口的配置预算', async () => {
  const { calls, ordinary, catalog } = await setup('90000')
  const { signal } = new AbortController()
  const options = { signal, silentError: true }
  await ordinary.test({}, options)
  await ordinary.testSaved(7, undefined, options)
  await catalog.test(18, {}, options)
  for (const [, , config] of calls) {
    assert.equal(config.signal, signal)
    assert.equal(config.silentError, true)
    assert.equal(config.timeout, 90000)
  }
})

test('空值非法值和越界值回退默认预算，边界配置有效', async () => {
  for (const [value, expected] of [[undefined,25000],['',25000],['invalid',25000],['Infinity',25000],['24999',25000],['300001',25000],['25000',25000],['300000',300000]]) {
    const { calls, catalog } = await setup(value)
    await catalog.test(18, {})
    assert.equal(calls[0][2].timeout, expected, String(value))
  }
})
