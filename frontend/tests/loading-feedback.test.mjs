import assert from 'node:assert/strict'
import test from 'node:test'
import { effectScope } from 'vue'
import { createLoadingFeedback } from '../src/utils/loading-feedback.ts'
import { useReadActivity } from '../src/composables/useReadActivity.ts'

test('短请求立即busy，结束后不补放延迟Loading', t => {
  t.mock.timers.enable({ apis: ['setTimeout'] })
  const phases = []
  const feedback = createLoadingFeedback(p => phases.push(p))
  feedback.set(true)
  t.mock.timers.tick(50)
  feedback.set(false)
  t.mock.timers.tick(6000)
  assert.deepEqual(phases, ['pending', 'idle'])
})

test('150ms提示、5秒说明，结束立即退出', t => {
  t.mock.timers.enable({ apis: ['setTimeout'] })
  const phases = []
  const feedback = createLoadingFeedback(p => phases.push(p))
  feedback.set(true)
  t.mock.timers.tick(149)
  assert.deepEqual(phases, ['pending'])
  t.mock.timers.tick(1)
  assert.equal(phases.at(-1), 'visible')
  t.mock.timers.tick(4850)
  assert.equal(phases.at(-1), 'long')
  feedback.set(false)
  assert.equal(phases.at(-1), 'idle')
})

test('重复渲染不重置计时，快速重入不继承上次长等待', t => {
  t.mock.timers.enable({ apis: ['setTimeout'] })
  const phases = []
  const feedback = createLoadingFeedback(p => phases.push(p))
  feedback.set(true)
  t.mock.timers.tick(100)
  feedback.set(true)
  t.mock.timers.tick(50)
  assert.equal(phases.at(-1), 'visible')
  feedback.set(false)
  feedback.set(true)
  t.mock.timers.tick(4850)
  assert.equal(phases.at(-1), 'visible')
  feedback.dispose()
  const before = [...phases]
  t.mock.timers.tick(10000)
  assert.deepEqual(phases, before)
})

test('并发读取在最后一个请求结束后退出busy，拒绝也释放', async () => {
  const scope = effectScope()
  const activity = scope.run(useReadActivity)
  let finishFirst, finishSecond
  const first = activity.run(() => new Promise(resolve => { finishFirst = resolve }))
  const second = activity.run(() => new Promise(resolve => { finishSecond = resolve }))
  assert.equal(activity.busy.value, true)
  finishSecond()
  await second
  assert.equal(activity.busy.value, true)
  finishFirst()
  await first
  assert.equal(activity.busy.value, false)
  await assert.rejects(activity.run(async () => { throw Error('read failed') }))
  assert.equal(activity.busy.value, false)
  scope.stop()
})

test('卸载后旧读取不能恢复busy', async () => {
  const scope = effectScope()
  const activity = scope.run(useReadActivity)
  let finish
  const pending = activity.run(() => new Promise(resolve => { finish = resolve }))
  scope.stop()
  assert.equal(activity.busy.value, false)
  finish()
  await pending
  assert.equal(activity.busy.value, false)
})
