export interface RecentReportVisit {
  id: number
  openedAt: string
}

const limit = 5

function storageKey(userId: number, workspaceId: number) {
  return `dbhub:published-report-visits:${userId}:${workspaceId}`
}

export function recentReportVisits(userId: number, workspaceId: number): RecentReportVisit[] {
  try {
    const records: unknown = JSON.parse(sessionStorage.getItem(storageKey(userId, workspaceId)) ?? '[]')
    if (!Array.isArray(records)) return []
    return records.filter((item): item is RecentReportVisit => item && typeof item.id === 'number' &&
      Number.isSafeInteger(item.id) && item.id > 0 && typeof item.openedAt === 'string' &&
      Number.isFinite(Date.parse(item.openedAt))).slice(0, limit)
  } catch {
    return []
  }
}

export function recordReportVisit(userId: number, workspaceId: number, id: number) {
  const visits = recentReportVisits(userId, workspaceId).filter(visit => visit.id !== id)
  try {
    const current = [{ id, openedAt: new Date().toISOString() }, ...visits].slice(0, limit)
    sessionStorage.setItem(storageKey(userId, workspaceId), JSON.stringify(current))
  } catch {
    // Reading a report remains available when browser storage is blocked.
  }
}
