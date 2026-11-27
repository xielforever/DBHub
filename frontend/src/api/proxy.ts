import request from '../utils/request'

export interface ProxyItem {
  id: number
  user_id: number
  name: string
  type: 'http' | 'https' | 'socks5' | 'db_proxy'
  host: string
  port: number
  username?: string
  has_password?: boolean
  description: string
  status: 'active' | 'inactive'
  created_at: string
  updated_at: string
}

export interface ProxyInput {
  name: string
  type: ProxyItem['type']
  host: string
  port: number
  username?: string
  password?: string
  description?: string
}

export const proxyApi = {
  list(silentError = false) {
    return request.get<unknown, { items: ProxyItem[]; total: number; note?: string }>('/proxies', { silentError })
  },
  create(payload: ProxyInput) {
    return request.post<unknown, ProxyItem>('/proxies', payload, { silentError: true })
  },
  update(id: number, payload: Partial<ProxyInput & { status: ProxyItem['status'] }>) {
    return request.put<unknown, ProxyItem>(`/proxies/${id}`, payload, { silentError: true })
  },
  remove(id: number) {
    return request.delete<unknown, { id: number }>(`/proxies/${id}`, { silentError: true })
  },
  test(payload: { id?: number; target_host?: string; target_port?: number; type: string; host: string; port: number; username?: string; password?: string }) {
    return request.post<unknown, { ok: boolean; latency_ms: number }>('/proxies/test', payload, { silentError: true })
  },
}
