import request from '../utils/request'
import { newRequestId } from '../utils/request-id'
import type { ChartKind, ChartConfig } from '../components/business/chart/ChartCard.vue'
import type { ExecResult } from './workbench'
import type { ReportPayload } from './report'

export interface DataContract {semantic_key:string;meaning:string;result_schema:{name:string;logical_type:string;nullable:boolean}[];dimensions:string[];metrics:string[];chart_type:string;unit:string;currency:string;timezone:string;scope_key:string;scope_description:string;time_window:Record<string,unknown>;comparison_key:string;freshness:{mode:string;column:string;max_data_age_seconds:number;max_future_skew_seconds:number}}
export interface ReportDefinition {data_contract?:DataContract;name:string;description:string;definition_revision_id:number;database_name:string;sql_text:string;chart_type:ChartKind;chart_config:ChartConfig}
export interface ManagedReport {id:number;owner_id:number;owner_name:string;draft:ReportDefinition;draft_version:number;latest_version:number;publication_no:number;active_release_id:number|null;connection_id:number;pending_count:number;active_review_id:number|null;active_version:number|null;updated_at:string}
export interface ReportManifest {version_id:number;content_hash:string;account_id:number;definition_revision_id:number;user_ids:number[];group_ids:number[];max_rows:number;timeout_ms:number;data_contract?:{declaration:DataContract;hash:string}}
export interface ReportVersion {audience_change?:{base_publication_no:number;account_id:number;user_ids:number[];group_ids:number[];request_ids:number[]};id:number;report_id:number;number:number;definition:ReportDefinition;content_hash:string;submitted_by:number|null;submitter_name:string;created_at:string;review_id:number;status:'pending'|'approved'|'rejected'|'withdrawn';decider_name:string;comment:string;account_id:number|null;account_name:string;manifest?:ReportManifest;was_published:boolean}
export interface ReportRelease {id:number;number:number;kind:'publish'|'rollback'|'unpublish';review_id:number|null;version:number|null;performer_name:string;created_at:string}
export interface ReportAccount {id:number;name:string;definition_revision_id:number;source_name:string;username:string;enabled:boolean;version:number;credential_generation:number;report_count:number}
export interface PublishedReport {id:number;name:string;description:string;version:number;publication_no:number;chart_type:ChartKind;chart_config:ChartConfig}
export interface ReportValidation {result:ExecResult;validation_id:string;manifest_hash:string}
export interface ReportCommand {id:number;version?:number}
export interface ReportPage<T> {items:T[];total:number;page:number;page_size:number}
const silent = {silentError:true}
export const managedReportApi = {
 list:(params:{q?:string;scope?:'all'|'mine'|'pending';page:number;page_size:number},signal?:AbortSignal)=>request.get<unknown,ReportPage<ManagedReport>>('/managed-reports',{params,signal,...silent}),
 get:(id:number,signal?:AbortSignal)=>request.get<unknown,ManagedReport>(`/managed-reports/${id}`,{signal,...silent}),
 save:(id:number,payload:Omit<ReportPayload,'visibility'> & {version?:number;request_id:string})=>id?request.put<unknown,ReportCommand>(`/managed-reports/${id}/draft`,payload,silent):request.post<unknown,ReportCommand>('/managed-reports',payload,silent),
 submit:(id:number,version:number,key:string)=>request.post<unknown,ReportCommand>(`/managed-reports/${id}/submit`,{version,request_id:key},silent),
 versions:(id:number,page=1,signal?:AbortSignal)=>request.get<unknown,ReportPage<ReportVersion>>(`/managed-reports/${id}/versions`,{params:{page,page_size:10},signal,...silent}),
 releases:(id:number,page=1,signal?:AbortSignal)=>request.get<unknown,ReportPage<ReportRelease>>(`/managed-reports/${id}/releases`,{params:{page,page_size:10},signal,...silent}),
 validate:(id:number,payload:{account_id:number;user_ids:number[];group_ids:number[]},signal?:AbortSignal)=>request.post<unknown,ReportValidation>(`/report-reviews/${id}/validate`,{...payload,request_id:newRequestId()},{signal,timeout:90000,...silent}),
 decide:(id:number,payload:{decision:'approved'|'rejected'|'withdrawn';validation_id?:string;comment?:string;contract_confirmed?:boolean},key:string)=>request.post<unknown,ReportCommand>(`/report-reviews/${id}/decision`,{...payload,request_id:key},silent),
 dashboardDependencies:(id:number,scope:string,page:number,signal?:AbortSignal)=>request.get<unknown,{items:{dashboard_id:number;name:string;version:number;kind:string;lifecycle:string}[];total:number}>(`/managed-reports/${id}/dashboard-dependencies`,{params:{scope,page,page_size:10},signal,...silent}),
 dashboardImpact:(id:number,review_id:number)=>request.get<unknown,{current_dashboards:number;blocked_dashboards:number;development_dashboards:number;historical_versions:number}>(`/managed-reports/${id}/dashboard-impact`,{params:{review_id},...silent}),
 release:(id:number,payload:{expected_publication_no:number;review_id:number;kind:'publish'|'rollback'|'unpublish';expected_affected_dashboards?:number},key:string)=>request.post<unknown,ReportCommand>(`/managed-reports/${id}/releases`,{...payload,request_id:key},silent),
 transfer:(id:number,version:number,owner_id:number,key:string)=>request.post<unknown,ReportCommand>(`/managed-reports/${id}/transfer`,{version,owner_id,request_id:key},silent),
 accounts:()=>request.get<unknown,{items:ReportAccount[]}>('/report-accounts',silent),
 createAccount:(payload:{name:string;definition_revision_id:number;username:string;password:string},key:string)=>request.post<unknown,ReportCommand>('/report-accounts',{...payload,request_id:key},silent),
 updateAccount:(id:number,payload:{version:number;enabled?:boolean;password?:string;expected_reports?:number},key:string)=>request.patch<unknown,ReportCommand>(`/report-accounts/${id}`,{...payload,request_id:key},silent),
 testAccount:(id:number)=>request.post<unknown,{connected:boolean;read_only:boolean}>(`/report-accounts/${id}/test`,{},{timeout:25000,...silent}),
 published:(params:{q?:string;page:number;page_size:number},signal?:AbortSignal)=>request.get<unknown,ReportPage<PublishedReport>>('/published-reports',{params,signal,...silent}),
 publishedGet:(id:number,signal?:AbortSignal)=>request.get<unknown,PublishedReport>(`/published-reports/${id}`,{signal,...silent}),
 execute:(id:number,publication_no:number,signal?:AbortSignal)=>request.post<unknown,ExecResult>(`/published-reports/${id}/execute`,{publication_no,request_id:newRequestId()},{signal,timeout:90000,...silent}),
}
