import assert from 'node:assert/strict'
import {test} from 'node:test'
import {deliveryInboxTarget, inboxTarget, inboxTodoTarget} from '../src/utils/automation.ts'

test('platform operations alerts open the runtime settings category', () => {
  assert.equal(inboxTarget('operations', 0), '/platform?tab=operations')
})

test('notifications reject stored URLs and malformed resource IDs',()=>{
  for(const resource of ['https://example.test','javascript:alert(1)','/users','report/../../users'])assert.equal(inboxTarget(resource,1),undefined)
  for(const id of [-1,0,NaN,Infinity,1.5,Number.MAX_SAFE_INTEGER+1])for(const resource of ['report','business_operation'])assert.equal(inboxTarget(resource,id),undefined)
})
test('published audience links use viewing pages while owner links use management',()=>{
  assert.equal(inboxTarget('published_report',12),'/reports?view=published&report=12')
  assert.equal(inboxTarget('report_export',12),'/reports?view=exports&export=12')
  assert.equal(inboxTarget('report_delivery',12),'/reports?view=subscriptions&delivery=12')
  assert.equal(inboxTarget('metric_incident',12),'/reports?view=subscriptions&incident=12')
  assert.equal(inboxTarget('report_subscription',12),'/reports?view=subscriptions&subscription=12')
  assert.equal(inboxTarget('business_operation',12),'/business-operations?request=12')
  assert.equal(inboxTarget('report',12),'/reports?view=managed&report=12')
  assert.equal(inboxTarget('dashboard_review',8),'/dashboards?tab=managed&dashboard=8')
  assert.equal(inboxTarget('published_dashboard',8),'/dashboards?dashboard=8')
})

test('dashboard review and publication tasks carry the exact review and dashboard IDs', () => {
  for (const kind of ['dashboard_review', 'dashboard_publish']) {
    assert.equal(inboxTodoTarget({ kind, id: 18, resource_id: 7 }), '/dashboards?tab=managed&dashboard=7&review=18')
    assert.equal(inboxTodoTarget({ kind, id: 0, resource_id: 7 }), undefined)
    assert.equal(inboxTodoTarget({ kind, id: 18, resource_id: 7.5 }), undefined)
    assert.equal(inboxTodoTarget({ kind, id: Number.MAX_SAFE_INTEGER + 1, resource_id: 7 }), undefined)
  }
  assert.equal(inboxTodoTarget({ kind: 'business_operation', id: 18, resource_id: 7 }), '/business-operations?request=7')
})

test('resolved delivery notifications use an allowed resource and the signed-in member result ID', () => {
  const delivery = '3cbbf267-361a-41ef-a3e2-f4e2f96416ad'
  assert.equal(deliveryInboxTarget({ resource_type: 'team_report_delivery', resource_id: 7, delivery_id: delivery }), `/reports?view=subscriptions&team_delivery=${delivery}`)
  assert.equal(deliveryInboxTarget({ resource_type: 'team_report_delivery', resource_id: 7 }), undefined)
  assert.equal(deliveryInboxTarget({ resource_type: 'team_report_delivery', resource_id: 7, delivery_id: '../reports' }), undefined)
  assert.equal(deliveryInboxTarget({ resource_type: 'formal_export_delivery', resource_id: 8 }), '/reports?view=exports&export=8')
  assert.equal(deliveryInboxTarget({ resource_type: 'dashboard_delivery', resource_id: 9 }), '/dashboards?tab=delivery&delivery=9')
  assert.equal(deliveryInboxTarget({ resource_type: 'published_report', resource_id: 8 }), undefined)
})
