/**
 * 工单中心 Step26：后端落库（sys_tickets），前端由 localStorage Mock 切换为真实 API
 */
import request from '../utils/request'

export interface TicketItem {
  id: number
  title: string
  type: 'ddl' | 'dml' | 'index'
  table: string
  database_name: string
  connection_id?: number | null
  sql: string
  risk: 'low' | 'medium' | 'high'
  status: 'pending' | 'approved' | 'rejected' | 'executed'
  requester_id: number
  requester: string
  reason: string
  created_at: string
  updated_at: string
}

// 后端返回的平铺字段与前端 TicketItem 的映射：后端为 table_name/sql_text/database_name 等
// 在 api 层统一做一次适配，保持前端历史字段名不变
type RawTicket = {
  id: number
  title: string
  type: string
  table: string
  table_name: string
  database_name: string
  connection_id: number | null
  sql: string
  sql_text: string
  risk: string
  status: string
  requester_id: number
  requester: string
  reason: string
  created_at: string
  updated_at: string
}

function adapt(raw: RawTicket): TicketItem {
  return {
    id: raw.id,
    title: raw.title,
    type: (raw.type || (raw as any)['type']) as TicketItem['type'],
    table: raw.table || raw.table_name,
    database_name: raw.database_name || '',
    connection_id: raw.connection_id,
    sql: raw.sql || raw.sql_text,
    risk: raw.risk as TicketItem['risk'],
    status: raw.status as TicketItem['status'],
    requester_id: raw.requester_id,
    requester: raw.requester || '',
    reason: raw.reason || '',
    created_at: raw.created_at,
    updated_at: raw.updated_at,
  }
}

export interface TicketPayload {
  title: string
  type: 'ddl' | 'dml' | 'index'
  table: string
  database?: string
  connection_id?: number | null
  sql: string
  risk: 'low' | 'medium' | 'high'
  reason?: string
}

export const ticketApi = {
  async list(params: { status?: string; q?: string; type?: string; page?: number; page_size?: number }) {
    const data = await request.get<unknown, { items: RawTicket[]; total: number; page: number; page_size: number }>('/tickets', { params })
    return { ...data, items: data.items.map(adapt) }
  },
  async create(payload: TicketPayload) {
    const raw = await request.post<unknown, RawTicket>('/tickets', payload)
    return adapt(raw)
  },
  async get(id: number) {
    const raw = await request.get<unknown, RawTicket>(`/tickets/${id}`)
    return adapt(raw)
  },
  async updateStatus(id: number, status: TicketItem['status']) {
    return request.post<unknown, { id: number; status: string }>(`/tickets/${id}/status`, { status })
  },
}
