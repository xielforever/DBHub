import { computed, toValue, type MaybeRefOrGetter } from 'vue'
import { useRoute, useRouter, type HistoryState, type RouteLocationRaw } from 'vue-router'
import type { DashboardTodoKind } from '../api/automation'
import { useUserStore } from '../stores/user'
import { isDashboardTodoKind } from '../utils/automation'

interface ReturnScope {
  workspaceId: number
  userId: number
}

export type HandlingOrigin = ReturnScope & (
  | { kind: 'governance'; proposalId: string }
  | {
    kind: 'business'; requestId: number; entryId: number; page: number
    category: 'todos' | 'notifications'; onlyUnread: boolean; summaryOpen: boolean
  }
  | {
    kind: 'dashboard-review'; dashboardId: number; reviewId: number; page: number
    todoKind: DashboardTodoKind; todoFilter: 'all' | DashboardTodoKind; summaryOpen: boolean
  }
)

const returnKey = 'handlingReturn'
const restoredKey = 'handlingRestored'

function positiveId(value: unknown): value is number {
  return typeof value === 'number' && Number.isSafeInteger(value) && value > 0
}

function readOrigin(raw: unknown): HandlingOrigin | undefined {
  if (typeof raw !== 'string' || raw.length > 500) return undefined
  try {
    const origin = JSON.parse(raw) as Record<string, unknown> | null
    if (!origin || !positiveId(origin.workspaceId) || !positiveId(origin.userId)) return undefined
    const scope = { workspaceId: origin.workspaceId, userId: origin.userId }
    if (origin.kind === 'governance' && typeof origin.proposalId === 'string' &&
      /^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$/i.test(origin.proposalId)) {
      return { ...scope, kind: origin.kind, proposalId: origin.proposalId }
    }
    if (origin.kind === 'business' && positiveId(origin.requestId) && positiveId(origin.entryId) &&
      positiveId(origin.page) && (origin.category === 'todos' || origin.category === 'notifications') &&
      typeof origin.onlyUnread === 'boolean' && typeof origin.summaryOpen === 'boolean') {
      return {
        ...scope, kind: origin.kind, requestId: origin.requestId, entryId: origin.entryId,
        page: origin.page, category: origin.category, onlyUnread: origin.onlyUnread, summaryOpen: origin.summaryOpen,
      }
    }
    if (origin.kind === 'dashboard-review' && positiveId(origin.dashboardId) && positiveId(origin.reviewId) &&
      positiveId(origin.page) && isDashboardTodoKind(origin.todoKind) &&
      (origin.todoFilter === 'all' || isDashboardTodoKind(origin.todoFilter) && origin.todoFilter === origin.todoKind) &&
      typeof origin.summaryOpen === 'boolean') {
      return {
        ...scope, kind: origin.kind, dashboardId: origin.dashboardId, reviewId: origin.reviewId,
        page: origin.page, todoKind: origin.todoKind, todoFilter: origin.todoFilter, summaryOpen: origin.summaryOpen,
      }
    }
  } catch { /* Invalid browser state does not establish a return destination. */ }
}

export function handlingReturnState(origin: HandlingOrigin): HistoryState {
  return { [returnKey]: JSON.stringify(origin) }
}

function useScopedOrigin(key: string) {
  const route = useRoute()
  const router = useRouter()
  const user = useUserStore()

  return computed(() => {
    if (!user.visibleMenus.some(menu => menu.path === '/inbox') ||
      route.query.workspace_id !== String(user.workspaceId)) return undefined
    const origin = readOrigin(router.options.history.state[key])
    if (origin?.workspaceId === user.workspaceId && origin.userId === user.user?.id) return origin
    return undefined
  })
}

export function useHandlingReturn(
  kind: HandlingOrigin['kind'],
  objectId: MaybeRefOrGetter<string | number | undefined>,
) {
  const route = useRoute()
  const origin = useScopedOrigin(returnKey)
  return computed<RouteLocationRaw | undefined>(() => {
    const source = origin.value
    const id = toValue(objectId)
    if (!source || source.kind !== kind || id === undefined) return undefined
    if (source.kind === 'governance') {
      if (route.query.proposal_id !== String(id) || source.proposalId !== id) return undefined
    } else if (source.kind === 'business') {
      if (route.query.request !== String(id) || source.requestId !== id) return undefined
    } else if (route.query.review !== String(id) || source.reviewId !== id ||
      route.query.dashboard !== String(source.dashboardId)) return undefined

    return {
      path: '/handling',
      query: {
        workspace_id: String(source.workspaceId),
        section: 'todo',
        tab: source.kind === 'governance' ? 'governance' :
          source.kind === 'business' && source.category === 'notifications' ? 'notifications' : 'business',
      },
      state: { [restoredKey]: JSON.stringify(source) },
    }
  })
}

export function useRestoredHandlingList() {
  const route = useRoute()
  const origin = useScopedOrigin(restoredKey)
  return computed(() => {
    if (route.name === 'handling-center' && route.query.section === 'todo') return origin.value
    return undefined
  })
}
