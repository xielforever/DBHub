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
  '/space': '管理', '/inbox': '管理', '/tickets': '管理', '/business-operations': '管理', '/audit': '管理', '/users': '管理', '/dashboard': '管理',
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
