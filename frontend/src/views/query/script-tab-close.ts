export const scriptTabCloseLabels = {
  current: '关闭此标签页',
  right: '关闭右侧标签页',
  others: '关闭其他标签页',
  all: '关闭全部标签页',
} as const

export type ScriptTabCloseAction = keyof typeof scriptTabCloseLabels

export function scriptTabCloseKeys(keys: readonly number[], anchor: number, action: ScriptTabCloseAction) {
  const index = keys.indexOf(anchor)
  if (index === -1) return []
  switch (action) {
    case 'current': return [anchor]
    case 'right': return keys.slice(index + 1)
    case 'others': return keys.filter(key => key !== anchor)
    case 'all': return [...keys]
  }
}
