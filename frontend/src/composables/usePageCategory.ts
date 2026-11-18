import { computed, toValue, type MaybeRefOrGetter } from 'vue'
import { useRoute, useRouter } from 'vue-router'

export interface PageCategory {
  id: string
  name: string
}

export function usePageCategory(
  categories: MaybeRefOrGetter<readonly PageCategory[]>,
  options: {
    defaultId?: MaybeRefOrGetter<string>
    aliases?: Readonly<Record<string, string>>
  } = {},
) {
  const route = useRoute()
  const router = useRouter()
  const category = computed(() => {
    const available = toValue(categories)
    const requested = route.query.tab
    if (requested === undefined) {
      return available.find(item => item.id === toValue(options.defaultId)) ?? available[0]
    }
    if (typeof requested !== 'string') return undefined
    const id = options.aliases && Object.prototype.hasOwnProperty.call(options.aliases, requested)
      ? options.aliases[requested]
      : requested
    return available.find(item => item.id === id)
  })

  async function selectCategory(id: string) {
    if (id === category.value?.id || !toValue(categories).some(item => item.id === id)) return
    await router.push({
      path: route.path,
      query: { ...route.query, tab: id },
      hash: route.hash,
    })
  }

  return { category, selectCategory }
}
