import test from 'node:test'
import assert from 'node:assert/strict'
import fs from 'node:fs/promises'
import vm from 'node:vm'
import ts from 'typescript'
import { computed, nextTick, reactive, ref } from 'vue'

const file = await fs.readFile(new URL('../src/views/auth/LoginView.vue', import.meta.url), 'utf8')
const source = file.match(/<script setup lang="ts">([\s\S]*?)<\/script>/)[1]
class ApiRequestError extends Error { constructor(message, status) { super(message); this.status = status } }

async function setup(options = {}) {
  const calls = [], focus = [], focusWhileBusy = [], routes = [], removed = [], history = []
  let mounted, dispose, component
  const store = {
    login: async (...args) => { calls.push(args); return options.login ? options.login() : {} },
    verifyMFA: async (...args) => { calls.push(args); return options.mfa ? options.mfa() : {} },
    completeSSO: async () => { if (options.ssoError) throw options.ssoError; return options.ssoResult || {} },
  }
  const body = {}
  const browserDocument = {
    body,
    activeElement: body,
    getElementById: id => ({ focus: () => {
      focus.push(id)
      if (component?.busy.value) focusWhileBusy.push(id)
    } }),
  }
  const context = vm.createContext({
    Error, URL, document: browserDocument,
    localStorage: { removeItem: key => removed.push(key) },
    window: { location: { href: options.url || 'http://localhost/login' }, history: { replaceState: (_state, _title, url) => history.push(url) } },
    fetch: async () => ({ ok: true, json: async () => ({ code: 0, data: { configured: false, version: 'test' } }) }),
  })
  const values = {
    vue: { computed, nextTick, reactive, ref, onMounted: fn => { mounted = fn }, onScopeDispose: fn => { dispose = fn } },
    'vue-router': { useRoute: () => ({ query: options.query || {} }), useRouter: () => ({ replace: async url => { routes.push(url); if (options.navigationError) throw new Error('navigation'); return undefined } }), isNavigationFailure: value => !!value },
    '../../stores/user': { useUserStore: () => store },
    '../../utils/request': { ApiRequestError },
  }
  const exported = 'form,fieldErrors,authError,succeeded,busy,loading,remember,challenge,challengeFromSSO,factor,useRecovery,ssoStartURL,handleLogin,handleFactor,clearError,returnToPassword,enterWorkspace'
  const module = new vm.SourceTextModule(ts.transpileModule(source + `\nexport {${exported}}`, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ESNext } }).outputText, { context })
  await module.link(specifier => {
    const value = values[specifier]; assert.ok(value, specifier)
    return new vm.SyntheticModule(Object.keys(value), function () { for (const [key, item] of Object.entries(value)) this.setExport(key, item) }, { context })
  })
  await module.evaluate()
  component = module.namespace
  return { c: component, calls, focus, focusWhileBusy, routes, removed, history, mounted, dispose, browserDocument }
}

test('空字段不请求服务器，聚焦首个无效字段，输入仅清除对应字段错误', async () => {
  const { c, calls, focus } = await setup()
  await c.handleLogin(); assert.equal(calls.length, 0); assert.equal(focus[0], 'username')
  assert.equal(c.fieldErrors.password, '请输入密码')
  c.form.username = 'synthetic'; c.clearError('username')
  assert.equal(c.fieldErrors.username, ''); assert.equal(c.fieldErrors.password, '请输入密码')
  await c.handleLogin(); assert.equal(focus.at(-1), 'password')
})

test('错误凭据统一文案、保留输入、请求静默且修改后清错', async () => {
  const { c, calls } = await setup({ login: () => { throw new ApiRequestError('不可暴露的具体账号原因', 401) } })
  c.form.username = 'synthetic'; c.form.password = 'not-a-real-password'
  await c.handleLogin()
  assert.equal(c.authError.value, '用户名或密码错误，请重新输入'); assert.equal(c.form.password, 'not-a-real-password')
  assert.equal(calls[0][2], true); assert.equal(c.busy.value, false)
  c.clearError('password'); assert.equal(c.authError.value, '')
})

test('网络故障显示服务原因并允许再次提交', async () => {
  const { c } = await setup({ login: () => { throw new ApiRequestError('网络异常，请检查后端服务状态') } })
  c.form.username = 'synthetic'; c.form.password = 'test'
  await c.handleLogin(); assert.match(c.authError.value, /网络异常/); assert.equal(c.busy.value, false)
})

test('密码提交失败恢复被禁用控件的焦点，保留用户主动移开的焦点', async () => {
  let rejectLogin
  const { c, browserDocument } = await setup({ login: () => new Promise((_, reject) => { rejectLogin = reject }) })
  c.form.username = 'synthetic'
  c.form.password = 'invalid'
  const password = { focus() {
    assert.equal(c.busy.value, false)
    browserDocument.activeElement = password
  } }
  const help = {}
  const submit = { currentTarget: { contains: element => element === password } }

  browserDocument.activeElement = password
  const first = c.handleLogin(submit)
  browserDocument.activeElement = browserDocument.body
  rejectLogin(new ApiRequestError('错误密码', 401))
  await first
  assert.equal(browserDocument.activeElement, password)

  const second = c.handleLogin(submit)
  browserDocument.activeElement = help
  rejectLogin(new ApiRequestError('网络中断', 503))
  await second
  assert.equal(browserDocument.activeElement, help)
})

test('处理中只发一次请求，成功后禁用重提并保持安全跳转', async () => {
  let resolve
  const { c, calls, routes, removed } = await setup({ login: () => new Promise(r => { resolve = r }), query: { redirect: '//external.invalid' } })
  c.form.username = 'synthetic'; c.form.password = 'test'; c.remember.value = false
  const run = c.handleLogin(); await c.handleLogin(); assert.equal(calls.length, 1)
  resolve({}); await run
  assert.equal(c.succeeded.value, true); assert.equal(c.busy.value, true); assert.equal(c.form.password, '')
  assert.deepEqual(routes, ['/']); assert.equal(removed.length, 3)
  await c.handleLogin(); assert.equal(calls.length, 1)
})

test('跳转失败保留成功状态，重试入口不重复认证', async () => {
  const { c, calls, routes } = await setup({ navigationError: true })
  c.form.username = 'synthetic'; c.form.password = 'test'; await c.handleLogin()
  assert.equal(c.succeeded.value, true); assert.match(c.authError.value, /已登录/)
  await c.enterWorkspace(); assert.equal(calls.length, 1); assert.equal(routes.length, 2)
})

test('MFA挑战不提前报成功，因子错误清空因子但保留挑战，恢复码请求静默', async () => {
  const { c, calls, focus, focusWhileBusy } = await setup({ login: () => ({ mfa_required: true, challenge_token: 'synthetic-challenge' }), mfa: () => { throw new ApiRequestError('验证码错误', 401) } })
  c.form.username = 'synthetic'; c.form.password = 'test'; await c.handleLogin()
  assert.equal(c.succeeded.value, false); assert.equal(c.challenge.value, 'synthetic-challenge')
  assert.deepEqual(focus, ['mfa-factor']); assert.deepEqual(focusWhileBusy, [])
  await c.handleFactor(); assert.equal(c.fieldErrors.factor, '请输入动态验证码')
  c.factor.value = 'synthetic-recovery'; c.useRecovery.value = true; await c.handleFactor()
  assert.equal(c.factor.value, ''); assert.equal(c.challenge.value, 'synthetic-challenge'); assert.equal(c.authError.value, '验证码错误')
  assert.deepEqual(calls[1].slice(2), [true, true])
  assert.equal(focus.at(-1), 'mfa-factor'); assert.deepEqual(focusWhileBusy, [])
  await c.returnToPassword(); assert.equal(c.authError.value, ''); assert.equal(c.challenge.value, '')
  assert.equal(focus.at(-1), 'username')
})

test('MFA成功只在验证后跳转', async () => {
  const { c, routes } = await setup(); c.challenge.value = 'synthetic'; c.factor.value = '000000'
  await c.handleFactor(); assert.equal(c.succeeded.value, true); assert.equal(c.factor.value, ''); assert.deepEqual(routes, ['/'])
})

test('SSO失败有本地错误，卸载后迟到响应不触发跳转', async () => {
  const a = await setup({ query: { sso: 'complete' }, url: 'http://localhost/login?sso=complete&redirect=%2Fquery', ssoError: new Error('单点登录交接失败') })
  await a.mounted(); assert.equal(a.c.authError.value, '单点登录交接失败')
  assert.deepEqual(a.history, ['/login?redirect=%2Fquery'])
  let resolve
  const b = await setup({ login: () => new Promise(r => { resolve = r }) })
  b.c.form.username = 'synthetic'; b.c.form.password = 'test'
  const run = b.c.handleLogin(); b.dispose(); resolve({}); await run
  assert.deepEqual(b.routes, []); assert.equal(b.c.succeeded.value, false)
})

test('SSO 回跳失败使用表单反馈，密码输入后清除旧提示', async () => {
  const { c, history, mounted } = await setup({
    query: { sso: 'failed' }, url: 'http://localhost/login?sso=failed&redirect=%2Fquery#login',
    login: () => { throw new ApiRequestError('认证失败', 401) },
  })
  await mounted()
  assert.match(c.authError.value, /单点登录未完成/)
  assert.deepEqual(history, ['/login?redirect=%2Fquery#login'])
  c.clearError('username')
  assert.equal(c.authError.value, '')
  c.form.username = 'synthetic'
  c.form.password = 'test'
  await c.handleLogin()
  assert.equal(c.authError.value, '用户名或密码错误，请重新输入')
})

test('SSO 两步验证显示对应挑战，因子可编辑后再聚焦', async () => {
  const destination = '/profile?workspace_id=1#security'
  const { c, history, focus, focusWhileBusy, mounted } = await setup({
    query: { sso: 'complete', redirect: destination }, url: 'http://localhost/login?sso=complete&redirect=' + encodeURIComponent(destination),
    ssoResult: { mfa_required: true, challenge_token: 'synthetic-sso-challenge' },
  })
  await mounted()
  assert.equal(c.challenge.value, 'synthetic-sso-challenge')
  assert.equal(c.challengeFromSSO.value, true)
  assert.equal(c.succeeded.value, false)
  assert.deepEqual(focus, ['mfa-factor']); assert.deepEqual(focusWhileBusy, [])
  assert.deepEqual(history, ['/login?redirect=' + encodeURIComponent(destination)])
  assert.equal(new URL(c.ssoStartURL.value, 'http://localhost').searchParams.get('redirect'), destination)
})

test('单点登录和密码共用站内返回地址检查', async () => {
  const { c, routes } = await setup({ query: { redirect: '//external.invalid' } })
  assert.equal(c.ssoStartURL.value, '/api/v1/auth/sso/start')
  await c.enterWorkspace()
  assert.deepEqual(routes, ['/'])
})
