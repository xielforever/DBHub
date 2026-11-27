export interface EditorialMenu {
  id: string
  label: string
  path: string
  group?: string
  catalogGroup?: string
}
export interface EditorialGroup { name: string; items: EditorialMenu[] }

export const editorialGroupNames = ['报表', '查询', '数据', '管理', '其他'] as const
type EditorialGroupName = typeof editorialGroupNames[number]

const defaultGroupByPath: Readonly<Partial<Record<string, EditorialGroupName>>> = {
  '/reports': '报表', '/dashboards': '报表', '/home': '报表',
  '/query': '查询',
  '/assets': '数据', '/connections': '数据', '/sources': '数据', '/metrics': '数据', '/proxies': '数据', '/source-tasks': '数据',
  '/space': '管理', '/spaces': '管理', '/handling': '管理', '/inbox': '管理', '/tickets': '管理', '/business-operations': '管理',
  '/audit': '管理', '/users': '管理', '/dashboard': '管理',
}

const contentDevelopment = [
  { kind: 'report', path: '/reports', target: '/reports?view=managed', label: '报表' },
  { kind: 'dashboard', path: '/dashboards', target: '/dashboards?tab=managed', label: '仪表盘' },
] as const

export function contentDevelopmentEntries(menus: readonly EditorialMenu[], canDesign: (kind: 'report' | 'dashboard') => boolean) {
  return contentDevelopment.filter(entry => canDesign(entry.kind) && menus.some(menu => menu.path === entry.path))
}

export function contentNavigationMenus(menus: readonly EditorialMenu[], entries: ReturnType<typeof contentDevelopmentEntries>) {
  const projected = menus.map(menu => menu.path === '/reports' ? { ...menu, label: '报表目录' } : menu)
  const target = entries[0]?.target
  if (target) projected.push({ id: 'content-development', path: target, label: '内容开发', group: '报表' })
  return projected
}

const handlingSources = [
  { id: 'todo', name: '待办', path: '/inbox' },
  { id: 'business', name: '业务办理', path: '/business-operations' },
  { id: 'tickets', name: '数据库变更工单', path: '/tickets' },
] as const

const managementOrder = ['/spaces', '/handling', '/audit']

function isHandlingSource(path: string) {
  return handlingSources.some(source => source.path === path)
}

export function handlingCenterCategories(menus: readonly EditorialMenu[]) {
  return handlingSources.filter(source => menus.some(menu => menu.path === source.path))
    .map(({ id, name }) => ({ id, name }))
}

export function managementNavigationPath(path: string): string {
  if (path === '/space') return '/spaces'
  return isHandlingSource(path) ? '/handling' : path
}

/** 将空间菜单映射到共享目录，平台管理员的入口由平台资格决定。 */
export function managementNavigationMenus(menus: readonly EditorialMenu[], platformAdmin = false): EditorialMenu[] {
  const projected = menus.filter(menu => !isHandlingSource(menu.path))
    .map(menu => menu.path === '/space' ? { ...menu, path: '/spaces' } : menu)
    .filter(menu => !platformAdmin || menu.path !== '/spaces')
  if (platformAdmin) {
    projected.push({ id: 'M10', label: '空间管理', path: '/spaces', group: '管理', catalogGroup: '管理' })
  }
  if (handlingCenterCategories(menus).length) {
    const entry: EditorialMenu = { id: 'handling-center', label: '办理中心', path: '/handling', group: '管理', catalogGroup: '管理' }
    const knownSlot = projected.findIndex(menu => managementOrder.includes(menu.path))
    projected.splice(knownSlot === -1 ? projected.length : knownSlot, 0, entry)
  }
  const isStandardEntry = (menu: EditorialMenu) => managementOrder.includes(menu.path) &&
    editorialMenuGroup(menu.path, menu.group, menu.catalogGroup) === '管理'
  const standard = projected.filter(isStandardEntry)
    .sort((a, b) => managementOrder.indexOf(a.path) - managementOrder.indexOf(b.path))
  let index = 0
  return projected.map(menu => isStandardEntry(menu) ? standard[index++]! : menu)
}

// 区分显式选择与旧目录原值。例如 /sources 的旧目录原值是「管理」，A2 默认在「数据」。
const explicitGroupPrefix = 'A2:'

export function editorialMenuGroup(path: string, group?: string, catalogGroup?: string): EditorialGroupName {
  const name = group?.trim()
  if (!name || name === catalogGroup) {
    const defaultGroup = defaultGroupByPath[path]
    if (defaultGroup) return defaultGroup
  }
  const label = name?.startsWith(explicitGroupPrefix) ? name.slice(explicitGroupPrefix.length) : name
  return editorialGroupNames.find(groupName => groupName === label) ?? '其他'
}

export function configuredEditorialGroup(selection: string, path: string, catalogGroup: string): string {
  if (selection === editorialMenuGroup(path, catalogGroup, catalogGroup)) return catalogGroup
  return selection === catalogGroup ? explicitGroupPrefix + selection : selection
}

/** 只重排已获准的菜单，既不授予入口也不丢弃未来新增菜单。 */
export function editorialGroups(menus: readonly EditorialMenu[]): EditorialGroup[] {
  const groups = new Map<EditorialGroupName, EditorialMenu[]>(editorialGroupNames.map(name => [name, []]))
  for (const menu of menus) {
    groups.get(editorialMenuGroup(menu.path, menu.group, menu.catalogGroup))!.push(menu)
  }
  return Array.from(groups, ([name, items]) => ({ name, items })).filter(group => group.items.length > 0)
}
