import request from '../utils/request'

export interface SnippetItem {
  id: number
  user_id: number
  name: string
  sql_text: string
  database_name: string
  connection_id?: number | null
  visibility: 'private' | 'shared'
  owner_name: string
  created_at: string
  updated_at: string
}

export const snippetApi = {
  list(params: { q?: string; visibility?: string; connection_id?: number; page?: number; page_size?: number } = {}) {
    return request.get<unknown, { items: SnippetItem[]; total: number; page: number; page_size: number }>('/snippets', { params })
  },
  create(payload: { name: string; sql_text: string; database_name?: string; connection_id?: number | null; visibility?: 'private' | 'shared' }) {
    return request.post<unknown, SnippetItem>('/snippets', payload)
  },
  update(id: number, payload: Partial<{ name: string; sql_text: string; database_name: string; visibility: 'private' | 'shared' }>) {
    return request.put<unknown, SnippetItem>(`/snippets/${id}`, payload)
  },
  remove(id: number) {
    return request.delete<unknown, { id: number }>(`/snippets/${id}`)
  },
}
