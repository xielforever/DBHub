import request from '../utils/request'
export interface OperationSpec{kind:string;name:string;target_label:string;amount_label:string;min:number;max:number;description:string}
export interface OperationTemplate{id:number;kind:string;version:number;enabled:boolean;spec:OperationSpec;account_id?:number;user_ids?:number[];group_ids?:number[]}
export interface OperationAccount{id:number;name:string;definition_revision_id:number;username:string;installation_id:string;enabled:boolean;version:number}
export interface OperationPreview{id?:string;workspace_id:number;actor_id:number;kind:string;target_key:string;amount:number;preview_at:number;target_version:string;before_sha256:string;after_sha256:string;before:Record<string,string|number>;after:Record<string,string|number>;expires_at?:string}
export interface OperationReceipt extends OperationPreview{operation_id:string;reviewer_id:number;affected_rows:number;executed_at:string}
export interface OperationRequest{id:number;operation_id:string;user_id:number;user_name:string;template_id:number;kind:string;preview:OperationPreview;reason:string;status:'pending'|'approved'|'rejected'|'executing'|'executed'|'stale'|'failed'|'unknown';version:number;reviewer_id?:number;review_comment:string;receipt?:OperationReceipt;receipt_sha256:string;created_at:string;expires_at:string;execution_started_at?:string}
const quiet={silentError:true}
const base='/business-operations'
export const operationApi={
 templates:(manage=false,signal?:AbortSignal)=>request.get<unknown,{items:OperationTemplate[]}>(base+'/templates',{params:{manage},signal,...quiet}),
 accounts:(signal?:AbortSignal)=>request.get<unknown,{items:OperationAccount[]}>(base+'/accounts',{signal,...quiet}),
 saveAccount:(account:OperationAccount,password:string,key:string)=>account.id?request.put(base+`/accounts/${account.id}`,{account,password,request_id:key},quiet):request.post(base+'/accounts',{account,password,request_id:key},quiet),
 saveTemplate:(template:Omit<OperationTemplate,'spec'>,key:string)=>template.id?request.put(base+`/templates/${template.id}`,{template,request_id:key},quiet):request.post(base+'/templates',{template,request_id:key},quiet),
 preview:(template:OperationTemplate,target_key:string,amount:number)=>request.post<unknown,OperationPreview>(base+'/previews',{template_id:template.id,version:template.version,target_key,amount},quiet),
 submit:(preview_id:string,reason:string,key:string)=>request.post<unknown,{id:number;version:number}>(base+'/requests',{preview_id,reason,request_id:key},quiet),
 requests:(all=false,page=1,signal?:AbortSignal)=>request.get<unknown,{items:OperationRequest[];total:number}>(base+'/requests',{params:{all,page},signal,...quiet}),
 get:(id:number,signal?:AbortSignal)=>request.get<unknown,OperationRequest>(base+`/requests/${id}`,{signal,...quiet}),
 validate:(item:OperationRequest)=>request.post<unknown,{validation_id:string;expires_in_seconds:number;preview:OperationPreview}>(base+`/requests/${item.id}/validate`,{version:item.version},quiet),
 decide:(item:OperationRequest,decision:'approved'|'rejected',validation_id:string,comment:string,confirmed:boolean,key:string)=>request.post(base+`/requests/${item.id}/decision`,{version:item.version,decision,validation_id,comment,confirmed,request_id:key},quiet),
 execute:(item:OperationRequest)=>request.post<unknown,OperationRequest>(base+`/requests/${item.id}/execute`,{version:item.version,confirmed:true},quiet),
 reconcile:(id:number)=>request.post<unknown,OperationRequest>(base+`/requests/${id}/reconcile`,{},quiet),
}
