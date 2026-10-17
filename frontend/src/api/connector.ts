import request from '../utils/request'

export interface DriverArtifact {id:string;name:string;version:string;class_name:string;sha256:string;ready:boolean}
export interface JDBCDriver {visibility:'private'|'global';created_by:number;id:number;artifact_id:string;name:string;version:string;class_name:string;sha256:string;enabled:boolean;created_at:string}
export interface TemplateField {name:string;label:string;type:'string'|'integer';required:boolean;default?:string}
export interface TemplatePayload {visibility?:'private'|'global';template_key:string;name:string;product:string;driver_id:number;dialect:''|'mysql'|'postgres';url_template:string;fields:TemplateField[]}
export interface DatasourceTemplate extends TemplatePayload {id:number;created_by:number;revision:number;driver:JDBCDriver;enabled:boolean;created_at:string}
interface List<T>{items:T[];total:number}
export const connectorApi={
 artifacts:()=>request.get<unknown,List<DriverArtifact>>('/connector-drivers/available',{silentError:true}),
 drivers:()=>request.get<unknown,List<JDBCDriver>>('/connector-drivers'),
 register:(artifact_id:string,name:string,visibility:'private'|'global'='private')=>request.post<unknown,JDBCDriver>('/connector-drivers',{artifact_id,name,visibility}),
 templates:()=>request.get<unknown,List<DatasourceTemplate>>('/datasource-templates'),
 publish:(payload:TemplatePayload,id?:number)=>id?request.put<unknown,DatasourceTemplate>(`/datasource-templates/${id}`,payload):request.post<unknown,DatasourceTemplate>('/datasource-templates',payload),
 status:(kind:'driver'|'template',id:number,enabled:boolean)=>request.patch(`/`+(kind==='driver'?'connector-drivers':'datasource-templates')+`/${id}/status`,{enabled}),
 remove:(kind:'driver'|'template',id:number)=>request.delete(`/`+(kind==='driver'?'connector-drivers':'datasource-templates')+`/${id}`),
}
