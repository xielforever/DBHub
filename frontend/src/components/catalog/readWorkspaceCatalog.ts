import { catalogApi, type CatalogObject, type CatalogSource } from '../../api/catalog'

export interface WorkspaceCatalogObject extends CatalogObject {
  key: string
  source: CatalogSource
  database: string
  path: string
  sameName: boolean
}

export async function readWorkspaceCatalog(signal: AbortSignal) {
  const { items: sources } = await catalogApi.sources(signal)
  const objects = new Map<string, WorkspaceCatalogObject>()
  const unavailable = new Set<string>()

  async function readSource(source: CatalogSource) {
    let databases: string[]
    try {
      const result = await catalogApi.databases(source.id, signal)
      databases = [...new Set((result.items ?? []).map(item => item.name).filter(Boolean))]
    } catch {
      signal.throwIfAborted()
      unavailable.add(source.name)
      return
    }

    for (const database of databases) {
      signal.throwIfAborted()
      const location = `${source.name} / ${database}`
      try {
        const page = await catalogApi.objects(source.id, database, signal)
        if (page.incomplete) unavailable.add(location)
        for (const object of page.items) {
          const key = JSON.stringify([source.id, database, object.schema, object.type, object.name])
          const path = !object.schema || object.schema === database && source.type === 'mysql'
            ? location
            : `${location} / ${object.schema}`
          objects.set(key, { ...object, key, source, database, path, sameName: false })
        }
      } catch {
        signal.throwIfAborted()
        unavailable.add(location)
      }
    }
  }

  // Read independent sources in parallel while limiting the number of live targets.
  const pending = sources.values()
  async function readPending() {
    for (const source of pending) {
      signal.throwIfAborted()
      await readSource(source)
    }
  }
  await Promise.all([readPending(), readPending()])

  const counts = new Map<string, number>()
  for (const { name } of objects.values()) {
    const lookup = name.toLocaleLowerCase()
    counts.set(lookup, (counts.get(lookup) ?? 0) + 1)
  }
  const items = [...objects.values()].sort((left, right) =>
    left.name.localeCompare(right.name) || left.path.localeCompare(right.path) || left.key.localeCompare(right.key))
  for (const item of items) item.sameName = (counts.get(item.name.toLocaleLowerCase()) ?? 0) > 1
  return { items, unavailable: [...unavailable].sort() }
}
