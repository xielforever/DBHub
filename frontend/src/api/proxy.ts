import request from '../utils/request'

export interface ProxyItem {
  id: number
  user_id: number
  name: string
  type: 'http' | 'https' | 'socks5' | 'db_proxy' | 'custom'
  host: string
  port: number
  username?: string
  has_password?: boolean
  description: string
  status: 'active' | 'inactive'
  created_at: string
  updated_at: string
}

export const proxyApi = {
  list() {
    return request.get<unknown, { items: ProxyItem[]; total: number; note?: string }>('/proxies')
  },
  create(payload: { name: string; type: string; host: string; port: number; username?: string; password?: string; description?: string }) {
    return request.post<unknown, ProxyItem>('/proxies', payload)
  },
  update(id: number, payload: Partial<{ name: string; type: string; host: string; port: number; username: string; password: string; description: string; status: string }>) {
    return request.put<unknown, ProxyItem>(`/proxies/${id}`, payload)
  },
  remove(id: number) {
    return request.delete<unknown, { id: number }>(`/proxies/${id}`)
  },
  test(payload: { type: string; host: string; port: number; username?: string; password?: string }) {
    return request.post<unknown, { ok: boolean; note?: string }>('/proxies/test', payload)
  },
}
