import { newRequestId } from '../../utils/request-id.ts'
import type { ReportDefinition } from '../../api/managed-report'

/** Uncertain mutations keep their UUID on retry. Nothing is persisted in browser storage. */
export function reportCommandKeys() {
 const keys = new Map<string,string>()
 return { get(operation:string,payload:unknown) { const signature=operation+JSON.stringify(payload);let key=keys.get(signature);if(!key){key=newRequestId();keys.set(signature,key)}return key }, clear(){keys.clear()} }
}
export function reportDefinitionDiff(before:ReportDefinition,after:ReportDefinition) {
 const fields: [keyof ReportDefinition,string][]=[['name','名称'],['description','描述'],['definition_revision_id','数据源修订'],['database_name','数据库'],['sql_text','SQL'],['chart_type','展示方式'],['chart_config','图表配置']]
 return fields.filter(([key])=>JSON.stringify(before[key])!==JSON.stringify(after[key])).map(([,label])=>label)
}
