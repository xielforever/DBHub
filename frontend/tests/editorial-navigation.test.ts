import assert from 'node:assert/strict'
import test from 'node:test'
import { configuredEditorialGroup, editorialGroups, editorialMenuGroup } from '../src/utils/editorial-navigation.ts'

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
