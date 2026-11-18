import assert from 'node:assert/strict'
import test from 'node:test'
// 仅使用Vue响应式能力；先在无DOM的Node环境加载，之后再模拟浏览器主题接口。
import 'vue'

let sequence = 0
async function withBrowser({ stored = {}, systemDark = false, blocked = false } = {}, run) {
  const values = new Map(Object.entries(stored))
  const events = new Map()
  const media = new Map()
  const classes = new Set()
  const element = { dataset: {}, style: {}, classList: { toggle(name, active) { active ? classes.add(name) : classes.delete(name) } } }
  const storage = { getItem(key) { if (blocked) throw new Error('storage denied'); return values.get(key) ?? null }, setItem(key, value) { if (blocked) throw new Error('storage denied'); values.set(key, value) } }
  const fakeWindow = {
    addEventListener(name, callback) { events.set(name, callback) },
    matchMedia(query) {
      const listeners = []
      const result = { matches: query.includes('color-scheme') ? systemDark : false, addEventListener(_name, callback) { listeners.push(callback) }, change(matches) { result.matches = matches; listeners.forEach(callback => callback({ matches })) } }
      media.set(query, result)
      return result
    },
  }
  const globals = { window: fakeWindow, document: { documentElement: element, querySelector: () => null }, localStorage: storage }
  const previous = new Map(Object.keys(globals).map(key => [key, Object.getOwnPropertyDescriptor(globalThis, key)]))
  for (const [key, value] of Object.entries(globals)) Object.defineProperty(globalThis, key, { configurable: true, value })
  try {
    const module = await import(`../src/utils/theme.ts?test=${++sequence}`)
    await run({ module, element, media, values, classes, events })
  } finally {
    for (const [key, descriptor] of previous) {
      if (descriptor) Object.defineProperty(globalThis, key, descriptor)
      else delete globalThis[key]
    }
  }
}

test('迁移旧的明确主题，新的偏好优先，并同步Element Plus暗色类', async () => {
  await withBrowser({ stored: { dbhub_iam_theme: 'dark' } }, ({ module, element, classes, values }) => {
    module.initTheme()
    assert.equal(element.dataset.theme, 'dark')
    assert.equal(element.dataset.iamTheme, 'dark')
    assert.equal(classes.has('dark'), true)
    module.setThemePreference('light')
    assert.equal(values.get('dbhub_theme'), 'light')
    assert.equal(element.dataset.theme, 'light')
    assert.equal(element.style.colorScheme, 'light')
    assert.equal(classes.has('dark'), false)
  })
  await withBrowser({ stored: { dbhub_theme: 'system', dbhub_iam_theme: 'dark' }, systemDark: false }, ({ module, element }) => {
    module.initTheme()
    assert.equal(element.dataset.theme, 'light')
  })
})

test('跟随系统响应变化，显式选择不被系统覆盖，跨标签变更可同步', async () => {
  await withBrowser({ systemDark: true }, ({ module, element, media, values, events }) => {
    const theme = module.useTheme()
    assert.equal(theme.themePreference.value, 'system')
    assert.equal(element.dataset.theme, 'dark')
    media.get('(prefers-color-scheme: dark)').change(false)
    assert.equal(element.dataset.theme, 'light')
    module.setThemePreference('dark')
    media.get('(prefers-color-scheme: dark)').change(false)
    assert.equal(element.dataset.theme, 'dark')
    values.set('dbhub_theme', 'light')
    events.get('storage')({ key: 'dbhub_theme' })
    assert.equal(element.dataset.theme, 'light')
    media.get('(prefers-reduced-motion: reduce)').change(true)
    assert.equal(theme.reducedMotion.value, true)
  })
})

test('存储受限时保留内存切换，不阻塞界面', async () => {
  await withBrowser({ blocked: true }, ({ module, element }) => {
    module.initTheme()
    assert.equal(element.dataset.theme, 'light')
    assert.doesNotThrow(() => module.setThemePreference('dark'))
    assert.equal(element.dataset.theme, 'dark')
  })
})
