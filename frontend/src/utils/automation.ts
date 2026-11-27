import type { DashboardTodoKind, NotificationDelivery, NotificationItem, TaskStatus, TodoItem } from '../api/automation'

export const taskStatusLabels:Record<TaskStatus,string>={queued:'排队中',running:'执行中',cancel_requested:'正在取消',succeeded:'成功',failed:'失败',cancelled:'已取消',interrupted:'运行中断',stale:'配置或身份已变化',skipped:'已跳过',unknown:'结果待确认'}
export const activeTask=(status:TaskStatus)=>['queued','running','cancel_requested'].includes(status)
export const retryableTask=(status:TaskStatus)=>['failed','cancelled','interrupted','stale','skipped','unknown'].includes(status)

// Routes are derived from a closed resource vocabulary, never a stored URL.
export function inboxTarget(resource:string,id:number):string|undefined{
  if(resource==='operations')return '/platform?tab=operations'
  if(resource==='source_tasks')return '/source-tasks'
  if(!Number.isSafeInteger(id)||id<1)return undefined
  if(resource==='published_report')return `/reports?view=published&report=${id}`
  if(resource==='report_export')return `/reports?view=exports&export=${id}`
  if(resource==='report_subscription')return `/reports?view=subscriptions&subscription=${id}`
  if(resource==='report_delivery')return `/reports?view=subscriptions&delivery=${id}`
  if(resource==='metric_incident')return `/reports?view=subscriptions&incident=${id}`
  if(resource==='business_operation')return `/business-operations?request=${id}`
  if(resource==='published_dashboard')return `/dashboards?dashboard=${id}`
  if(resource==='report'||resource==='report_review')return `/reports?view=managed&report=${id}`
  if(resource==='dashboard'||resource==='dashboard_review')return `/dashboards?tab=managed&dashboard=${id}`
  if(['access_request','dashboard_access','report_access'].includes(resource))return '/dashboards?tab=access'
  return undefined
}

export function isDashboardTodoKind(kind: unknown): kind is DashboardTodoKind {
  return kind === 'dashboard_review' || kind === 'dashboard_publish'
}

export function inboxTodoTarget(todo: Pick<TodoItem, 'kind' | 'resource_id' | 'id'>) {
  if (!isDashboardTodoKind(todo.kind)) return inboxTarget(todo.kind, todo.resource_id)
  if (!Number.isSafeInteger(todo.id) || todo.id < 1 ||
    !Number.isSafeInteger(todo.resource_id) || todo.resource_id < 1) return undefined
  return `/dashboards?tab=managed&dashboard=${todo.resource_id}&review=${todo.id}`
}

export function hasDeliveryTarget(notice: NotificationItem) {
  return ['formal_export_delivery', 'team_report_delivery', 'dashboard_delivery'].includes(notice.resource_type)
    || (notice.resource_type === 'report_delivery' && !!notice.link_expires_at)
}

export function deliveryInboxTarget(resource: NotificationDelivery): string | undefined {
  const { resource_type: kind, resource_id: id, delivery_id: delivery } = resource
  if (!Number.isSafeInteger(id) || id < 1) return undefined
  if (kind === 'formal_export_delivery') return inboxTarget('report_export', id)
  if (kind === 'dashboard_delivery') return `/dashboards?tab=delivery&delivery=${id}`
  if (kind === 'report_delivery') return inboxTarget(kind, id)
  if (kind === 'team_report_delivery' && delivery
    && /^[a-f0-9]{8}(?:-[a-f0-9]{4}){3}-[a-f0-9]{12}$/i.test(delivery)) {
    return `/reports?view=subscriptions&team_delivery=${delivery}`
  }
  return undefined
}
