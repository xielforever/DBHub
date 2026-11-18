import request from '../utils/request'
import type {ParameterValues} from '../views/report/reportParameters'
import type {DataContract} from './managed-report'
import type {ExecResult} from './workbench'
import type {ChartKind,ChartConfig} from '../components/business/chart/ChartCard.vue'
export interface MetricAlertRule{column:string;operator:'lt'|'lte'|'gt'|'gte'|'eq'|'ne';threshold:string}
export interface SubscriptionDefinition{name:string;report_id:number;publication_no:number;interval_minutes:number;params:ParameterValues;alert_rule?:MetricAlertRule;webhook:boolean;enabled:boolean}
export interface ReportSubscription{id:number;version:number;archived:boolean;definition:SubscriptionDefinition;next_run_at:string;last_run_at?:string;last_status:string}
export interface SubscriptionRun{permission_bridge?:string;permission_supported?:boolean;id:string;delivery_id:number;subscription_id:number;subscription_version:number;report_id:number;publication_no:number;name:string;status:'queued'|'running'|'succeeded'|'failed'|'interrupted';message:string;row_count:number;snapshot_sha256:string;created_at:string;finished_at?:string;expires_at:string}
export interface SubscriptionSnapshot{workspace_id:number;run_id:string;report_id:number;publication_no:number;requester_id:number;name:string;created_at:string;params:ParameterValues;manifest_sha256:string;chart_type:ChartKind;chart_config:ChartConfig;data_contract:DataContract|null;result:ExecResult}
export interface MetricIncident{id:number;subscription_id:number;report_id:number;publication_no:number;name:string;rule:MetricAlertRule;value:string;status:'open'|'acknowledged'|'closed'|'superseded';condition_active:boolean;version:number;first_run_id:string;last_run_id:string;opened_at:string;last_seen_at:string;recovered_at?:string;closed_at?:string}
export interface IncidentEvent{id:number;event_type:string;comment:string;created_at:string}
export interface WebhookDelivery{id:string;event_type:string;status:'pending'|'sending'|'sent'|'failed';attempts:number;http_status?:number}
export interface DeliveryDetail{snapshot:SubscriptionSnapshot;webhook_deliveries:WebhookDelivery[]}
const quiet={silentError:true}
export const subscriptionApi={
 list:(archived=false,signal?:AbortSignal)=>request.get<unknown,{items:ReportSubscription[];webhook_configured:boolean}>('/report-subscriptions',{params:{include_archived:archived},signal,...quiet}),
 save:(id:number,version:number,definition:SubscriptionDefinition,key:string)=>id?request.put<unknown,{id:number;version:number}>(`/report-subscriptions/${id}`,{version,definition,request_id:key},quiet):request.post<unknown,{id:number;version:number}>('/report-subscriptions',{definition,request_id:key},quiet),
 archive:(id:number,version:number,key:string)=>request.post(`/report-subscriptions/${id}/archive`,{version,request_id:key},quiet),
 run:(id:number,version:number,key:string)=>request.post(`/report-subscriptions/${id}/runs`,{version,request_id:key},quiet),
 runs:(page=1,signal?:AbortSignal)=>request.get<unknown,{items:SubscriptionRun[];total:number}>('/report-deliveries',{params:{page,page_size:20},signal,...quiet}),
 delivery:(id:number,signal?:AbortSignal)=>request.get<unknown,DeliveryDetail>(`/report-deliveries/${id}`,{signal,...quiet}),
 incidents:(page=1,signal?:AbortSignal)=>request.get<unknown,{items:MetricIncident[];total:number}>('/metric-incidents',{params:{page,page_size:20},signal,...quiet}),
 incident:(id:number,signal?:AbortSignal)=>request.get<unknown,{incident:MetricIncident;events:IncidentEvent[]}>(`/metric-incidents/${id}`,{signal,...quiet}),
 action:(id:number,version:number,action:'acknowledge'|'comment'|'close',comment:string,key:string)=>request.post(`/metric-incidents/${id}/actions`,{version,action,comment,request_id:key},quiet),
}
