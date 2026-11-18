import request from '../utils/request'

export interface ShareItem {
  id: number
  subject_type: 'report' | 'dashboard'
  subject_id: number
  expire_at: string | null
  access_count: number
  revoked: boolean
  created_at: string
}

export const shareApi = {
  create(payload: { subject_type: 'report' | 'dashboard'; subject_id: number; expire_days?: number }) {
    return request.post<unknown, { id: number; token: string; expire_at: string | null }>('/shares', payload)
  },
  list(subject_type: string, subject_id: number) {
    return request.get<unknown, { items: ShareItem[] }>('/shares', { params: { subject_type, subject_id } })
  },
  revoke(id: number) {
    return request.post<unknown, { revoked: boolean }>(`/shares/${id}/revoke`)
  },
  remove(id: number) {
    return request.delete<unknown, { deleted: boolean }>(`/shares/${id}`)
  },
}
