import { readonly, ref } from 'vue'

export type ThemePreference = 'light' | 'dark' | 'system'
export type ResolvedTheme = 'light' | 'dark'

const preferenceKey = 'dbhub_theme'
const legacyKey = 'dbhub_iam_theme'
const preference = ref<ThemePreference>('system')
const resolved = ref<ResolvedTheme>('light')
const reducedMotion = ref(false)
let initialized = false
let colorMedia: MediaQueryList | undefined

export function isThemePreference(value: unknown): value is ThemePreference {
  return value === 'light' || value === 'dark' || value === 'system'
}

/** 保留用户原有明确偏好；存储受限时仍可使用当前页面的内存偏好。 */
function readPreference(): ThemePreference {
  try {
    const current = localStorage.getItem(preferenceKey)
    if (isThemePreference(current)) return current
    const legacy = localStorage.getItem(legacyKey)
    if (legacy === 'light' || legacy === 'dark') return legacy
  } catch {
    // 浏览器隐私策略可禁止存储；系统偏好与内存切换仍然可用。
  }
  return 'system'
}

function applyTheme(): void {
  resolved.value = preference.value === 'system' ? (colorMedia?.matches ? 'dark' : 'light') : preference.value
  const root = document.documentElement
  root.dataset.theme = resolved.value
  root.dataset.iamTheme = resolved.value // 仅兼容现有诊断，始终由同一主题状态驱动。
  root.classList.toggle('dark', resolved.value === 'dark')
  root.style.colorScheme = resolved.value
  const browserColor = document.querySelector('meta[name="theme-color"]')
  const pageColor = browserColor ? getComputedStyle(root).getPropertyValue('--db-page-bg').trim() : ''
  if (pageColor) browserColor?.setAttribute('content', pageColor)
}

/** 在应用挂载前调用一次，主题不依赖工作空间或布局的生命周期。 */
export function initTheme(): void {
  if (initialized || typeof window === 'undefined' || typeof document === 'undefined') return
  initialized = true
  preference.value = readPreference()
  if (typeof window.matchMedia === 'function') {
    colorMedia = window.matchMedia('(prefers-color-scheme: dark)')
    colorMedia.addEventListener('change', () => {
      if (preference.value === 'system') applyTheme()
    })
    const motionMedia = window.matchMedia('(prefers-reduced-motion: reduce)')
    reducedMotion.value = motionMedia.matches
    motionMedia.addEventListener('change', event => { reducedMotion.value = event.matches })
  }
  window.addEventListener('storage', event => {
    if (event.key === preferenceKey || event.key === legacyKey || event.key === null) {
      preference.value = readPreference()
      applyTheme()
    }
  })
  applyTheme()
}

export function setThemePreference(value: ThemePreference): void {
  initTheme()
  preference.value = value
  try {
    localStorage.setItem(preferenceKey, value)
  } catch {
    // 不因持久化失败阻止当前页面的外观切换。
  }
  if (typeof document !== 'undefined') applyTheme()
}

export function useTheme() {
  initTheme()
  return {
    themePreference: readonly(preference),
    resolvedTheme: readonly(resolved),
    reducedMotion: readonly(reducedMotion),
    setThemePreference,
  }
}
