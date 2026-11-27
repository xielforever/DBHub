import test from 'node:test'
import assert from 'node:assert/strict'
import fs from 'node:fs/promises'
import vm from 'node:vm'
import ts from 'typescript'

const source = await fs.readFile(new URL('../src/utils/notification-state.ts', import.meta.url), 'utf8')
async function setup() {
  const module = new vm.SourceTextModule(ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ESNext } }).outputText, { context: vm.createContext({ AbortController, Number, Error }) })
  await module.link(() => { throw new Error('Unexpected dependency') })
  await module.evaluate()
  const requests = [], states = []
  let allowed = true
  const loader = module.namespace.createNotificationSummaryLoader(signal => new Promise((resolve, reject) => requests.push({ signal, resolve, reject })), state => states.push({ ...state }), () => allowed)
  const settle = async () => { for (let i = 0; i < 8; i++) await Promise.resolve() }
  return { loader, requests, states, settle, last: () => states.at(-1), allow: value => { allowed = value } }
}

test('通知摘要在切空间、换人或撤销资格后清空并忽略迟到响应', async () => {
  const a = await setup(), old = a.loader.setContext('user1:space1:epoch1')
  const current = a.loader.setContext('user1:space2:epoch2')
  assert.equal(a.requests[0].signal.aborted, true)
  a.requests[1].resolve({ unread: 2 }); await current
  a.requests[0].resolve({ unread: 99 }); await old
  assert.equal(a.last().unread, 2)
  const switched = a.loader.setContext('user2:space2:epoch2')
  assert.equal(a.last().unread, 0)
  await a.loader.setContext(null)
  a.requests[2].resolve({ unread: 77 }); await switched
  assert.equal(a.last().unread, 0); assert.equal(a.last().failed, false)
})

test('读取失败明确标记未知，恢复后刷新，不将失败保留为旧未读数量', async () => {
  const a = await setup(), first = a.loader.setContext('qualified')
  a.requests[0].resolve({ unread: 5 }); await first
  const second = a.loader.refresh(); a.requests[1].reject(new Error('Synthetic unavailable')); await second
  assert.deepEqual(a.last(), { unread: 0, known: false, loading: false, failed: true })
  const recovered = a.loader.refresh(); a.requests[2].resolve({ unread: 0 }); await recovered
  assert.deepEqual(a.last(), { unread: 0, known: true, loading: false, failed: false })
})

test('并发已读刷新合并为后续一次请求，卸载或无资格不再发布', async () => {
  const a = await setup(), first = a.loader.setContext('qualified')
  await a.loader.refresh(); await a.loader.refresh()
  assert.equal(a.requests.length, 1)
  a.requests[0].resolve({ unread: 4 }); await first; await a.settle()
  assert.equal(a.requests.length, 2)
  a.requests[1].resolve({ unread: 0 }); await a.settle()
  assert.equal(a.last().unread, 0)
  a.allow(false); await a.loader.refresh(); assert.equal(a.requests.length, 2)
  a.allow(true); const pending = a.loader.refresh(), before = a.states.length
  a.loader.dispose(); assert.equal(a.requests[2].signal.aborted, true)
  a.requests[2].resolve({ unread: 12 }); await pending
  assert.equal(a.states.length, before)
})

test('未发出请求或收到无效数量时保持未确认，不能解释为零未读', async () => {
  const a = await setup()
  a.allow(false); await a.loader.setContext('qualified')
  assert.equal(a.requests.length, 0); assert.equal(a.last().known, false)
  a.allow(true); const invalid = a.loader.refresh()
  a.requests[0].resolve({ unread: -1 }); await invalid
  assert.equal(a.last().known, false); assert.equal(a.last().failed, true)
})
