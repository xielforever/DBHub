import { computed, shallowRef, type ShallowRef } from 'vue'
import { useRouter, type RouteLocationNormalizedLoaded } from 'vue-router'
import { useUserStore } from '../../stores/user'

const defaults = { receipt: '', groupPage: 1, groupKeyword: '', groupSearch: '', groupOperationError: '' }
type PageState = { identity: number; workspace: number; values: typeof defaults }
const states = new WeakMap<RouteLocationNormalizedLoaded, ShallowRef<PageState | undefined>>()

export function useUserManagementPageState<Key extends keyof typeof defaults>(field: Key) {
  const router = useRouter()
  const user = useUserStore()
  const location = router.currentRoute.value
  const identity = user.identityEpoch()
  const actor = user.user?.id
  const workspace = user.workspaceId
  const slot = states.get(location) ?? shallowRef<PageState>()
  // A permission refresh remounts the view, but keeps the same route location.
  states.set(location, slot)

  const current = () => router.currentRoute.value === location &&
    user.user?.id === actor && user.identityEpoch() === identity && user.workspaceId === workspace

  return computed({
    get() {
      const received = slot.value
      return current() && received?.identity === identity && received.workspace === workspace
        ? received.values[field]
        : defaults[field]
    },
    set(value: (typeof defaults)[Key]) {
      if (!current()) return
      const previous = slot.value
      const values = previous?.identity === identity && previous.workspace === workspace ? previous.values : defaults
      slot.value = { identity, workspace, values: { ...values, [field]: value } }
    },
  })
}

export function useUserWriteReceipt() {
  return useUserManagementPageState('receipt')
}
