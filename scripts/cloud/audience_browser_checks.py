"""Real browser acceptance for audience, groups, directory and audit snapshots."""
from playwright.sync_api import expect


def run_checks(page, test, fixture, values, prefix, source_name, test_source, readonly_source,
               test_token, ro_token, latest, bound_revision, app_users, out,
               api, choose, passed, assert_no_overflow):
    group_name = prefix + ' 库存团队'
    page.goto('http://frontend:5173/settings', wait_until='networkidle')
    section = page.get_by_role('region', name='用户组管理')
    section.get_by_role('button', name='新建用户组', exact=True).click()
    dialog = page.get_by_role('dialog', name='新建用户组', exact=True)
    dialog.get_by_label('组名称', exact=True).fill(group_name)
    picker = dialog.get_by_role('combobox', name='搜索用户组成员')
    picker.locator('xpath=ancestor::div[contains(@class,"el-select__wrapper")][1]').click()
    picker.fill(fixture['app_readonly'])
    page.get_by_role('option', name=fixture['app_readonly'], exact=True).click()
    with page.expect_response(lambda r: r.url.endswith('/user-groups') and r.request.method == 'POST') as response:
        dialog.get_by_role('button', name='保存用户组', exact=True).click()
    assert response.value.status == 200
    group = response.value.json()['data']
    assert group['member_ids'] == [app_users[fixture['app_readonly']]['id']]
    passed('administrators create a team and choose its real members in the browser')

    def catalog():
        page.goto('http://frontend:5173/connections', wait_until='networkidle')
        page.get_by_role('tab', name='数据源目录', exact=True).click()
        page.get_by_label('搜索数据源目录', exact=True).fill(source_name)
        card = page.locator('article').filter(has=page.get_by_role('heading', name=source_name, exact=True))
        expect(card).to_be_visible()
        return card

    def manage():
        card = catalog()
        card.get_by_role('button', name='管理 '+source_name, exact=True).click()
        page.get_by_role('menuitem', name='资料与发布范围', exact=True).click()
        dialog = page.get_by_role('dialog', name='管理数据源', exact=True)
        expect(dialog.get_by_role('button', name='保存设置', exact=True)).to_be_enabled()
        return dialog

    dialog = manage()
    dialog.get_by_label('目录分组', exact=True).fill(prefix+' 供应链')
    tags = dialog.get_by_role('combobox', name='数据源标签')
    tags.fill('库存'); tags.press('Enter')
    dialog.get_by_label('数据源负责人', exact=True).fill('演示负责人')
    dialog.get_by_label('负责人联系方式', exact=True).fill('inventory@example.test')
    with page.expect_response(lambda r: r.url.endswith('/profile') and r.request.method == 'PUT') as response:
        dialog.get_by_role('button', name='保存设置', exact=True).click()
    assert response.value.status == 200
    card = catalog(); expect(card).to_contain_text('演示负责人'); expect(card).to_contain_text('库存')
    passed('directory groups tags and maintainer contact are saved independently of target revisions')

    dialog = manage()
    dialog.get_by_role('tab', name='发布范围', exact=True).click()
    choose(page, dialog.get_by_role('combobox', name='发布范围'), '指定用户或用户组')
    picker = dialog.get_by_role('combobox', name='授权用户', exact=True)
    picker.locator('xpath=ancestor::div[contains(@class,"el-select__wrapper")][1]').click()
    picker.fill(fixture['app_user']); page.get_by_role('option', name=fixture['app_user'], exact=True).click()
    dialog.locator('.el-dialog__header').click()
    expect(picker).to_have_attribute('aria-expanded', 'false')
    picker = dialog.get_by_role('combobox', name='授权用户组', exact=True)
    picker.locator('xpath=ancestor::div[contains(@class,"el-select__wrapper")][1]').click()
    picker.fill(group_name); page.get_by_role('option', name=group_name, exact=True).click()
    dialog.locator('.el-dialog__header').click()
    expect(picker).to_have_attribute('aria-expanded', 'false')
    dialog.get_by_role('button', name='保存设置', exact=True).click()
    with page.expect_response(lambda r: r.url.endswith('/access') and r.request.method == 'PUT') as response:
        page.get_by_role('dialog', name='确认发布范围', exact=True).get_by_role('button', name='确认保存', exact=True).click()
    assert response.value.status == 200
    access = api('GET', f"/datasource-definitions/{latest['id']}/access")
    assert access['user_ids'] == [app_users[fixture['app_user']]['id']] and access['group_ids'] == [group['id']]
    for bearer in [test_token, ro_token]:
        api('GET', f"/datasource-definitions/{bound_revision['id']}/revisions", bearer=bearer)
        api('GET', f"/datasource-definitions/{latest['id']}/access", want=404, bearer=bearer)
    passed('one publication supports both direct users and teams while hiding member details from consumers')

    page.set_viewport_size({'width': 390, 'height': 844})
    dialog = manage()
    expect(dialog.get_by_role('button', name='保存设置', exact=True)).to_be_in_viewport()
    assert_no_overflow(page)
    page.screenshot(path=str(out/'source-management-mobile.png'), animations='disabled')
    dialog.locator('button.el-dialog__headerbtn').click()
    expect(dialog).not_to_be_visible()
    page.set_viewport_size({'width': 1440, 'height': 1000})
    page.screenshot(path=str(out/'source-directory-desktop.png'), animations='disabled')
    passed('directory management fits mobile and keeps its save action inside the viewport')

    test.goto('http://frontend:5173/connections', wait_until='networkidle')
    test.get_by_role('tab', name='数据源目录', exact=True).click()
    test.get_by_label('搜索数据源目录', exact=True).fill(source_name)
    card = test.locator('article').filter(has=test.get_by_role('heading', name=source_name, exact=True))
    expect(card).to_contain_text('演示负责人')
    with test.expect_response(lambda r: r.url.endswith('/favorite') and r.request.method == 'PUT') as response:
        card.get_by_role('button', name='收藏 '+source_name, exact=True).click()
    assert response.value.status == 200
    choose(test, test.get_by_role('combobox', name='目录视图'), '我的收藏')
    expect(card.get_by_role('button', name='收藏 '+source_name, exact=True)).to_have_attribute('aria-pressed', 'true')
    assert api('GET', '/datasource-definitions?view=favorites', bearer=ro_token)['total'] == 0
    choose(test, test.get_by_role('combobox', name='目录视图'), '最近使用')
    expect(card).to_contain_text('最近连接')
    assert_no_overflow(test)
    test.screenshot(path=str(out/'source-directory-mobile.png'), animations='disabled')
    passed('mobile favorites are personal and recent use is based on a successful target connection')

    page.goto('http://frontend:5173/audit', wait_until='networkidle')
    page.get_by_label('按数据库账号搜索', exact=True).fill(fixture['reader'])
    with page.expect_response(lambda r: '/audit-logs?' in r.url and 'outcome=succeeded' in r.url and 'database_account=' in r.url) as response:
        choose(page, page.get_by_role('combobox', name='按执行状态筛选'), '执行成功')
    assert response.value.status == 200
    expect(page.locator('tbody tr')).to_have_count(len(response.value.json()['data']['items']))
    row = page.locator('tbody tr').filter(has_text=fixture['reader']).first
    expect(row).to_contain_text('修订 2')
    expect(row).to_contain_text(fixture['app_user'])
    expect(row).to_contain_text('postgresql-42.7.7')
    page.screenshot(path=str(out/'source-audit-desktop.png'), animations='disabled')
    passed('audit UI associates the platform actor personal database login driver and pinned revision')

    page.goto('http://frontend:5173/settings', wait_until='networkidle')
    section = page.get_by_role('region', name='用户组管理')
    section.locator('article').filter(has_text=group_name).get_by_role('button', name='编辑成员', exact=True).click()
    dialog = page.get_by_role('dialog', name='编辑用户组', exact=True)
    dialog.locator('.el-tag__close').click()
    with page.expect_response(lambda r: r.url.endswith(f"/user-groups/{group['id']}") and r.request.method == 'PUT') as response:
        dialog.get_by_role('button', name='保存用户组', exact=True).click()
    assert response.value.status == 200
    api('POST', '/query/execute', {'connection_id': readonly_source['id'], 'sql': 'SELECT current_user'}, 403, bearer=ro_token)
    result = api('POST', '/query/execute', {'connection_id': test_source['id'], 'sql': 'SELECT current_user'}, bearer=test_token)
    assert result['rows'][0][0] == fixture['reader']
    passed('removing a team member revokes an existing connection and preserves a separate direct grant')

    section.locator('article').filter(has_text=group_name).get_by_role('button', name='删除', exact=True).click()
    with page.expect_response(lambda r: r.url.endswith(f"/user-groups/{group['id']}") and r.request.method == 'DELETE') as response:
        page.get_by_role('dialog', name='删除用户组', exact=True).get_by_role('button', name='确认删除', exact=True).click()
    assert response.value.status == 200
    passed('team deletion requires an explicit impact confirmation and removes the owned fixture')

    dialog = manage()
    dialog.get_by_role('tab', name='发布范围', exact=True).click()
    dialog.get_by_role('tabpanel', name='发布范围', exact=True).locator('.el-tag__close').click()
    dialog.get_by_role('button', name='保存设置', exact=True).click()
    with page.expect_response(lambda r: r.url.endswith('/access') and r.request.method == 'PUT') as response:
        page.get_by_role('dialog', name='确认发布范围', exact=True).get_by_role('button', name='确认保存', exact=True).click()
    assert response.value.status == 200
    api('POST', '/query/execute', {'connection_id': test_source['id'], 'sql': 'SELECT current_user'}, 403, bearer=test_token)
    visible = api('GET', '/datasource-definitions?view=favorites', bearer=test_token)
    assert visible['total'] == 0
    assert api('GET', f"/connections/{test_source['id']}", bearer=test_token)['capabilities']['sql'] is False
    passed('revoking publication hides favorites and blocks a historical personal binding without deleting its credentials')
