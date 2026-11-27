import assert from 'node:assert/strict'
import test from 'node:test'
import {
  configuredEditorialGroup, editorialGroups, editorialMenuGroup, handlingCenterCategories,
  managementNavigationMenus, managementNavigationPath,
  contentDevelopmentEntries, contentNavigationMenus,
} from '../src/utils/editorial-navigation.ts'

test('内容开发只能从可见且授予设计动作的菜单派生，正式目录仍可访问', () => {
  const report = { id: 'report', path: '/reports', label: '报表' }
  const dashboard = { id: 'dashboard', path: '/dashboards', label: '仪表盘' }
  const menus = [report, dashboard]
  assert.deepEqual(contentDevelopmentEntries([report], kind => kind === 'dashboard'), [])
  assert.deepEqual(contentNavigationMenus(menus, contentDevelopmentEntries(menus, () => false)).map(menu => menu.label), ['报表目录', '仪表盘'])
  const entries = contentDevelopmentEntries(menus, kind => kind === 'dashboard')
  assert.deepEqual(entries.map(entry => entry.kind), ['dashboard'])
  assert.equal(contentNavigationMenus(menus, entries).at(-1)?.path, '/dashboards?tab=managed')
  assert.equal(report.label, '报表')
})

test('导航分组保留已获准菜单、原名称与未知入口，不修改权限快照', () => {
  const menus = [
    { id: 'M04', path: '/query', label: '自定义查询名称' },
    { id: 'M01', path: '/reports', label: '报表' },
    { id: 'M99', path: '/future-tool', label: '未来获准工具', group: '扩展' },
    { id: 'M08', path: '/inbox', label: '待办' },
  ]
  const original = structuredClone(menus)
  const groups = editorialGroups(menus)
  assert.deepEqual(groups.map(g => g.name), ['报表', '查询', '管理', '其他'])
  assert.deepEqual(groups.flatMap(g => g.items).map(m => m.id).sort(), menus.map(m => m.id).sort())
  assert.equal(groups[1]?.items[0]?.label, '自定义查询名称')
  assert.deepEqual(menus, original)
})

test('无获准菜单时不合成默认入口，撤权后的输入不保留旧分组', () => {
  assert.deepEqual(editorialGroups([]), [])
  const before = editorialGroups([{ id: 'M01', path: '/reports', label: '报表' }])
  const after = editorialGroups([{ id: 'M04', path: '/query', label: '查询' }])
  assert.equal(before[0]?.name, '报表')
  assert.deepEqual(after.map(g => g.name), ['查询'])
})

test('沿用目录默认分组，保存后的显式分类优先，分组内保持菜单顺序', () => {
  const legacyGroup = '管理'
  const sourceGroup = configuredEditorialGroup('管理', '/sources', legacyGroup)
  const menus = [
    { id: 'M10', path: '/space', label: '空间管理', group: '数据', catalogGroup: legacyGroup },
    { id: 'M06', path: '/connections', label: '我的连接', group: '数据工具', catalogGroup: '数据工具' },
    { id: 'M09', path: '/sources', label: '数据管理', group: sourceGroup, catalogGroup: legacyGroup },
    { id: 'M04', path: '/query', label: '查询', group: '原有自定义分类', catalogGroup: '数据工具' },
  ]
  assert.deepEqual(
    editorialGroups(menus).map(group => [group.name, group.items.map(item => item.id)]),
    [['数据', ['M10', 'M06']], ['管理', ['M09']], ['其他', ['M04']]],
  )
  assert.equal(editorialMenuGroup('/sources', legacyGroup, legacyGroup), '数据')
  assert.equal(configuredEditorialGroup('数据', '/sources', legacyGroup), legacyGroup)
  assert.equal(editorialMenuGroup('/future-tool', '查询', '查询'), '查询')
})

test('管理投影合并三项办理来源，保留原菜单数据并将成员深链归入空间入口', () => {
  const menus = [
    { id: 'M03', path: '/business-operations', label: '业务办理' },
    { id: 'M07', path: '/tickets', label: '工单' },
    { id: 'M08', path: '/inbox', label: '待办' },
    { id: 'M10', path: '/space', label: '空间管理' },
    { id: 'M11', path: '/audit', label: '操作审计' },
  ]
  const original = structuredClone(menus)
  const management = editorialGroups(managementNavigationMenus(menus)).find(group => group.name === '管理')
  assert.deepEqual(management?.items.map(menu => [menu.label, menu.path]), [
    ['空间管理', '/spaces'], ['办理中心', '/handling'], ['操作审计', '/audit'],
  ])
  assert.deepEqual(handlingCenterCategories(menus).map(tab => tab.id), ['todo', 'business', 'tickets'])
  assert.deepEqual(menus, original)
  assert.equal(managementNavigationPath('/space'), '/spaces')
  assert.equal(managementNavigationPath('/business-operations'), '/handling')
})

test('办理中心只由当前已获准且可见的来源生成', () => {
  const ticket = { id: 'M07', path: '/tickets', label: '数据库变更工单' }
  assert.deepEqual(handlingCenterCategories([ticket]), [{ id: 'tickets', name: '数据库变更工单' }])
  assert.deepEqual(managementNavigationMenus([ticket]).map(item => item.path), ['/handling'])
  const audit = { id: 'M11', path: '/audit', label: '操作审计' }
  assert.deepEqual(handlingCenterCategories([audit]), [])
  assert.deepEqual(managementNavigationMenus([audit]), [audit])
})

test('超管始终在管理菜单中拥有一个固定的空间目录，空间成员沿用已获准菜单', () => {
  const scopeMenu = { id: 'M10', path: '/space', label: '自定义空间入口', group: '数据' }
  const directory = { id: 'M10', path: '/spaces', label: '空间管理', group: '管理', catalogGroup: '管理' }
  const audit = { id: 'M11', path: '/audit', label: '操作审计' }
  assert.deepEqual(managementNavigationMenus([], true), [directory])
  assert.deepEqual(managementNavigationMenus([audit, scopeMenu], true), [directory, audit])
  assert.deepEqual(managementNavigationMenus([audit, scopeMenu]), [audit, { ...scopeMenu, path: '/spaces' }])
  assert.deepEqual(managementNavigationMenus([audit]), [audit])
})
