import request from '../utils/request'

export interface NotificationItem {
  id:number;kind:string;title:string;message:string;resource_type:string;resource_id:number;created_at:string;read_at:string|null
}
export interface TodoItem {id:number;kind:string;resource_id:number;title:string;created_at:string;expires_at:string|null}
export interface InboxPage {items:NotificationItem[];total:number;unread:number;through_id:number;todo_count:number}
export type TaskStatus='queued'|'running'|'cancel_requested'|'succeeded'|'failed'|'cancelled'|'interrupted'|'stale'|'skipped'|'unknown'
export interface SourceTask {
  id:string;connection_id:number;connection_name:string;kind:'check'|'collect';status:TaskStatus;progress:number;phase:string;
  result:{tables?:number;views?:number;elapsed_ms?:number;version?:string};created_at:string;started_at:string|null;finished_at:string|null;parent_id:string|null
}
export interface SourceSchedule {
  id:number;connection_id:number;kind:'check'|'collect';interval_minutes:number;enabled:boolean;version:number;
  next_run_at:string;last_run_at:string|null;last_status:string;config_updated_at:string
}
export interface CheckHistory {
  id:number;connection_id:number;connection_name:string;task_id:string|null;current_config:boolean;created_at:string;
  result:{ok:boolean;elapsed_ms:number;version?:string;error?:string;checked_at:string}
}
export interface JobConnection {id:number;name:string;type:string;capabilities?:{collect:boolean}}
export interface OperationState {name:string;status:string;started_at:string;finished_at:string|null;last_success_at:string|null;details:Record<string,unknown>;updated_at:string}
export interface OperationAlert {code:string;active:boolean;observed_at:string;changed_at:string}
export interface OperationsSnapshot {
  states:OperationState[];alerts:OperationAlert[];
  config:{audit_retention_days:number;history_retention_days:number;read_notification_retention_days:number;backup_expected_hours:number};
  audit:{ready:boolean;pending:number;capacity:number;oldest?:string}|null
}
const quiet={silentError:true}
export const automationApi={
  inbox:(params:{state?:'unread';page?:number;page_size?:number}={},signal?:AbortSignal)=>request.get<unknown,InboxPage>('/inbox',{params,signal,...quiet}),
  todos:(page=1,signal?:AbortSignal)=>request.get<unknown,{items:TodoItem[];total:number}>('/inbox/todos',{params:{page,page_size:20},signal,...quiet}),
  read:(body:{ids?:number[];through_id?:number},signal?:AbortSignal)=>request.post<unknown,{changed:number}>('/inbox/read',body,{signal,...quiet}),
  connections:(signal?:AbortSignal)=>request.get<unknown,{items:JobConnection[]}>('/connections',{signal,...quiet}),
  tasks:(params:{status?:TaskStatus;page?:number}={},signal?:AbortSignal)=>request.get<unknown,{items:SourceTask[];total:number}>('/source-tasks',{params:{...params,page_size:20},signal,...quiet}),
  task:(id:string,signal?:AbortSignal)=>request.get<unknown,SourceTask>(`/source-tasks/${id}`,{signal,...quiet}),
  createTask:(body:{connection_id:number;kind:'check'|'collect';request_id:string},signal?:AbortSignal)=>request.post<unknown,SourceTask>('/source-tasks',body,{signal,...quiet}),
  cancelTask:(id:string,signal?:AbortSignal)=>request.post<unknown,SourceTask>(`/source-tasks/${id}/cancel`,{},{signal,...quiet}),
  retryTask:(id:string,request_id:string,signal?:AbortSignal)=>request.post<unknown,SourceTask>(`/source-tasks/${id}/retry`,{request_id},{signal,...quiet}),
  schedules:(signal?:AbortSignal)=>request.get<unknown,{items:SourceSchedule[]}>('/source-schedules',{signal,...quiet}),
  saveSchedule:(body:{connection_id:number;kind:'check'|'collect';interval_minutes:number;enabled:boolean;version:number},signal?:AbortSignal)=>request.put<unknown,SourceSchedule>('/source-schedules',body,{signal,...quiet}),
  history:(params:{connection_id?:number;page?:number}={},signal?:AbortSignal)=>request.get<unknown,{items:CheckHistory[];total:number}>('/source-check-history',{params:{...params,page_size:20},signal,...quiet}),
  operations:(signal?:AbortSignal)=>request.get<unknown,OperationsSnapshot>('/platform/operations',{signal,...quiet}),
}
