<script setup lang="ts">
import { reactive } from 'vue'
import {
  Box,
  ChevronRight,
  Database,
  Eye,
  Layers,
  Table2,
} from 'lucide-vue-next'
import type { EnvKind, TreeConnection } from '../../../api/asset'

interface Selection {
  connection_id: number
  database: string
  schema: string
}
const props = defineProps<{ tree: TreeConnection[]; selection: Selection }>()
const emit = defineEmits<{
  (e: 'select', s: Selection): void
  (e: 'open-table', loc: { connection_id: number; database: string; schema: string; table: string }): void
}>()

const envMeta: Record<EnvKind, { label: string; cls: string }> = {
  dev: { label: '开发', cls: 'bg-accent-soft text-accent' },
  test: { label: '测试', cls: 'bg-warning-soft text-warning' },
  prod: { label: '生产', cls: 'bg-danger-soft text-danger' },
}

const openKeys = reactive(new Set<string>())
function key(kind: string, ...parts: (string | number)[]): string {
  return `${kind}:${parts.join('/')}`
}
function toggle(kind: string, ...parts: (string | number)[]) {
  const k = key(kind, ...parts)
  if (openKeys.has(k)) openKeys.delete(k)
  else openKeys.add(k)
}

function isConnActive(conn: TreeConnection): boolean {
  return props.selection.connection_id === conn.id && !props.selection.database
}
function isSchemaActive(connID: number, db: string, schema: string): boolean {
  return (
    props.selection.connection_id === connID &&
    props.selection.database === db &&
    props.selection.schema === schema
  )
}
function selectSchema(connID: number, db: string, schema: string) {
  toggle('s', connID, db, schema)
  emit('select', { connection_id: connID, database: db, schema })
}
</script>

<template>
  <div class="space-y-0.5">
    <div v-for="conn in tree" :key="conn.id">
      <!-- 连接 -->
      <button
        class="tree-row w-full ui-control-target"
        :class="[
          isConnActive(conn) ? 'bg-surface-muted text-content' : '',
          conn.is_empty ? 'opacity-70' : ''
        ]"
        @click="toggle('c', conn.id)"
      >
        <ChevronRight class="w-3.5 h-3.5 shrink-0 transition-transform" :class="openKeys.has(key('c', conn.id)) ? 'rotate-90' : ''" />
        <Database class="w-4 h-4 shrink-0" :class="conn.is_empty ? 'text-subtle' : 'text-accent'" />
        <span class="truncate flex-1 text-left">{{ conn.name }}</span>
        <span class="text-xs px-1.5 py-0.5 rounded-full shrink-0" :class="envMeta[conn.environment].cls">
          {{ envMeta[conn.environment].label }}
        </span>
        <span v-if="conn.is_empty" class="text-xs px-1.5 py-0.5 rounded-full bg-surface-muted text-subtle shrink-0">未同步</span>
        <span v-else class="text-xs text-subtle shrink-0 tabular-nums">{{ conn.table_count }}</span>
      </button>

      <div v-if="openKeys.has(key('c', conn.id))" class="ml-4 border-l border-stroke pl-1.5 space-y-0.5">
        <template v-if="conn.databases.length">
          <div v-for="db in conn.databases" :key="db.name">
            <!-- 库 -->
            <button class="tree-row w-full ui-control-target" @click="toggle('d', conn.id, db.name)">
              <ChevronRight class="w-3.5 h-3.5 shrink-0 transition-transform" :class="openKeys.has(key('d', conn.id, db.name)) ? 'rotate-90' : ''" />
              <Box class="w-4 h-4 shrink-0 text-accent" />
              <span class="truncate flex-1 text-left text-sm">{{ db.name }}</span>
              <span class="text-xs text-subtle shrink-0 tabular-nums">{{ db.table_count }}</span>
            </button>

            <div v-if="openKeys.has(key('d', conn.id, db.name))" class="ml-4 border-l border-stroke pl-1.5 space-y-0.5">
              <div v-for="sc in db.schemas" :key="sc.name">
                <!-- Schema -->
                <button
                  class="tree-row w-full ui-control-target"
                  :class="isSchemaActive(conn.id, db.name, sc.name) ? 'bg-surface-muted text-content' : ''"
                  @click="selectSchema(conn.id, db.name, sc.name)"
                >
                  <ChevronRight class="w-3.5 h-3.5 shrink-0 transition-transform" :class="openKeys.has(key('s', conn.id, db.name, sc.name)) ? 'rotate-90' : ''" />
                  <Layers class="w-4 h-4 shrink-0 text-accent" />
                  <span class="truncate flex-1 text-left text-sm">{{ sc.name }}</span>
                  <span class="text-xs text-subtle shrink-0 tabular-nums">{{ sc.table_count ?? sc.tables.length }}</span>
                </button>

                <div v-if="openKeys.has(key('s', conn.id, db.name, sc.name))" class="ml-4 border-l border-stroke pl-1.5 space-y-0.5">
                  <button
                    v-for="t in sc.tables"
                    :key="t.name"
                    class="tree-row w-full ui-control-target"
                    @click="emit('open-table', { connection_id: conn.id, database: db.name, schema: sc.name, table: t.name })"
                  >
                    <component
                      :is="t.type === 'view' ? Eye : Table2"
                      class="w-3.5 h-3.5 shrink-0"
                      :class="t.type === 'view' ? 'text-accent' : 'text-subtle'"
                    />
                    <span class="truncate flex-1 text-left text-sm font-normal">{{ t.name }}</span>
                  </button>
                </div>
              </div>
            </div>
          </div>
        </template>
        <p v-else class="text-xs text-subtle px-2 py-2">
          <template v-if="conn.is_empty">暂无字典，点右上角同步</template>
          <template v-else>空</template>
        </p>
      </div>
    </div>
  </div>
</template>



<style scoped>
.tree-row {
  display: flex;
  align-items: center;
  gap: 0.4rem;
  padding: 0.4rem 0.5rem;
  border-radius: 0.65rem;
  color: var(--db-text-secondary);
  font-size: 0.875rem;
  font-weight: 500;
  transition: background-color 0.15s ease, color 0.15s ease;
}
.tree-row:hover {
  background: var(--db-surface-muted);
  color: var(--db-text-secondary);
}
</style>
