import request from '../utils/request'
import type { Effective, ProposalCommand } from '../types/iam'

const configuredEffectiveTimeout = Number(import.meta.env.VITE_IAM_EFFECTIVE_TIMEOUT_MS)
const effectiveTimeout = Number.isInteger(configuredEffectiveTimeout)
  && configuredEffectiveTimeout >= 15_000 && configuredEffectiveTimeout <= 300_000
  ? configuredEffectiveTimeout
  : undefined

export const iamApi = {
 effective: (workspace?: { id: number; signal: AbortSignal }) => request.get<unknown, Effective>(
   workspace ? `/workspaces/${workspace.id}/iam/effective` : '/iam/effective',
   { silentError: true, timeout: effectiveTimeout, signal: workspace?.signal, workspacePermissionCheck: !!workspace },
 ),
 read: <T>(section:string, signal?: AbortSignal):Promise<T> => request.get<unknown,T>(`/iam/${section}`,{silentError:true,signal}) as Promise<T>,
 preview: (input:ProposalCommand) => request.post<unknown,{beneficiaries:number[];actions:string[];request_hash:string}>('/iam/proposals/preview',input,{silentError:true}),
 propose: (input:ProposalCommand) => request.post<unknown,{id:string;state:string}>('/iam/proposals',input,{silentError:true}),
 review: (input:Record<string,unknown>) => request.post<unknown,{state:string}>('/iam/proposals/review',input,{silentError:true}),
 restrict: (input:Record<string,unknown>) => request.post<unknown,{state:string;execution_fence:'confirmed'|'pending';status_url:string}>('/iam/restrictions',input,{silentError:true}),
 menus: (input:Record<string,unknown>) => request.put('/iam/menus',input,{silentError:true}),
 home: (menu_id:string) => request.put('/iam/home',{menu_id},{silentError:true}),
 shadow: <T>(user_ids:number[])=>request.post<unknown,T>('/iam/migration/shadow',{user_ids},{silentError:true}),
 bundle: (input:Record<string,unknown>)=>request.post<unknown,{id:string}>('/iam/bundles',input,{silentError:true}),
 rollback: (input:Record<string,unknown>)=>request.post<unknown,{state:string;prior_bundle_id:string}>('/iam/bundles/rollback',input,{silentError:true}),
}
