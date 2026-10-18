import assert from 'node:assert/strict'
import {test} from 'node:test'
import {inboxTarget} from '../src/utils/automation.ts'

test('notifications reject stored URLs and malformed resource IDs',()=>{
  for(const resource of ['https://example.test','javascript:alert(1)','/users','report/../../users'])assert.equal(inboxTarget(resource,1),undefined)
  for(const id of [-1,0,NaN,Infinity,1.5,Number.MAX_SAFE_INTEGER+1])assert.equal(inboxTarget('report',id),undefined)
})
test('published audience links use viewing pages while owner links use management',()=>{
  assert.equal(inboxTarget('published_report',12),'/reports?view=published&report=12')
  assert.equal(inboxTarget('report',12),'/reports?view=managed&report=12')
  assert.equal(inboxTarget('dashboard_review',8),'/dashboards?tab=managed&dashboard=8')
  assert.equal(inboxTarget('published_dashboard',8),'/dashboards?dashboard=8')
})
