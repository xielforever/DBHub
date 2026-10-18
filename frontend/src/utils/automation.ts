import type {TaskStatus} from '../api/automation'

export const taskStatusLabels:Record<TaskStatus,string>={queued:'排队中',running:'执行中',cancel_requested:'正在取消',succeeded:'成功',failed:'失败',cancelled:'已取消',interrupted:'运行中断',stale:'配置或身份已变化',skipped:'已跳过',unknown:'结果待确认'}
export const activeTask=(status:TaskStatus)=>['queued','running','cancel_requested'].includes(status)
export const retryableTask=(status:TaskStatus)=>['failed','cancelled','interrupted','stale','skipped','unknown'].includes(status)

// Routes are derived from a closed resource vocabulary, never a stored URL.
export function inboxTarget(resource:string,id:number):string|undefined{
  if(resource==='operations')return '/settings?section=operations'
  if(resource==='source_tasks')return '/source-tasks'
  if(!Number.isSafeInteger(id)||id<1)return undefined
  if(resource==='published_report')return `/reports?view=published&report=${id}`
  if(resource==='published_dashboard')return `/dashboards?dashboard=${id}`
  if(resource==='report'||resource==='report_review')return `/reports?view=managed&report=${id}`
  if(resource==='dashboard'||resource==='dashboard_review')return `/dashboards?tab=managed&dashboard=${id}`
  if(['access_request','dashboard_access','report_access'].includes(resource))return '/dashboards?tab=access'
  return undefined
}
