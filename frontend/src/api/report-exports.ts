import request from '../utils/request'
import type { ExecResult } from './workbench'
import type { ParameterValues } from '../views/report/reportParameters'

export interface ReportExportPolicy { enabled: boolean; max_rows: number; page_size_parameter?: string; offset_parameter?: string }
export interface ReportExport {
  permission_bridge?:string;permission_supported?:boolean;
  generation_status?: string
  file_expires_at?: string
  id:number;user_id:number;requester_name:string;report_id:number;report_name:string;publication_no:number;request_id:string;format:'csv'|'json'|'xlsx';reason:string;row_count:number;snapshot_sha256:string;status:'pending'|'approved'|'rejected';reviewer_name:string;review_comment:string;version:number;created_at:string;expires_at:string;can_review:boolean;can_download:boolean
}
const quiet={silentError:true}
export const reportExportApi={
  create:(id:number,payload:{publication_no:number;request_id:string;format:string;reason:string;params:ParameterValues})=>request.post<unknown,ReportExport>(`/published-reports/${id}/exports`,payload,{...quiet,timeout:90000}),
  list:(page=1,signal?:AbortSignal)=>request.get<unknown,{items:ReportExport[];total:number}>('/report-exports',{params:{page,page_size:20},signal,...quiet}),
  get:(id:number,signal?:AbortSignal)=>request.get<unknown,ReportExport>(`/report-exports/${id}`,{signal,...quiet}),
  preview:(id:number,signal?:AbortSignal)=>request.get<unknown,{export:ReportExport;result:ExecResult;params:ParameterValues;labels:Record<string,string>;scope:string}>(`/report-exports/${id}/preview`,{signal,...quiet}),
  review:(id:number,payload:{version:number;decision:'approved'|'rejected';comment:string})=>request.post<unknown,ReportExport>(`/report-exports/${id}/review`,payload,quiet),
  download:(id:number)=>request.get<unknown,Blob>(`/report-exports/${id}/download`,{responseType:'blob',timeout:90000,...quiet}),
  credential:(id:number)=>request.post<unknown,{credential:string;expires_at:string}>(`/report-exports/${id}/credential`,{},quiet),
  redeem:(id:number,credential:string)=>request.post<unknown,Blob>(`/report-exports/${id}/redeem`,{credential},{responseType:'blob',timeout:90000,...quiet}),
}
