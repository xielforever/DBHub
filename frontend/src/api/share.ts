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

// 公开接口（免登录）
export const publicShareApi = {
  get(token: string) {
    return fetch(`/api/v1/public/s/${encodeURIComponent(token)}`).then(async (r) => {
      if (!r.ok) {
        const text = await r.text()
        throw new Error(text || `HTTP ${r.status}`)
      }
      const body = await r.json() as { code: number; message: string; data: any }
      // 后端统一包装 {code,message,data}，data 为分享对象
      return (body.data || body) as {
        share_id: number
        subject_type: string
        subject: any
        owner_name: string
        expire_at: string | null
        access_count: number
      }
    })
  },
}
