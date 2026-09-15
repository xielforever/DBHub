<template>
  <div :class="['flex flex-col h-full min-h-0', embedded ? '' : 'glass-panel p-3']">
    <div class="flex items-center justify-between px-1 pb-3">
      <h2 class="text-sm font-medium">数据库</h2>
      <button
        class="w-7 h-7 rounded-lg flex items-center justify-center text-white/50 hover:bg-white/10 hover:text-white transition-colors"
        aria-label="刷新连接树"
        @click="refreshTree"
      >
        <RefreshCw class="w-4 h-4" />
      </button>
    </div>
    <el-tree
      :data="treeData"
      :props="treeProps"
      node-key="id"
      default-expand-all
      class="query-tree flex-1 overflow-y-auto"
    >
      <template #default="{ data }">
        <span class="flex items-center gap-2 py-0.5 text-sm">
          <component
            :is="nodeIcon(data.type)"
            class="w-3.5 h-3.5 shrink-0"
            :style="{ color: nodeColor(data.type) }"
          />
          <span class="truncate text-white/75">{{ data.label }}</span>
        </span>
      </template>
    </el-tree>
  </div>
</template>

<script setup lang="ts">
import { ElMessage } from 'element-plus'
import { Database, Folder, Layers, RefreshCw, Server, Table2 } from 'lucide-vue-next'

/** embedded: 作为抽屉内容嵌入时不重复绘制玻璃面板背景 */
withDefaults(defineProps<{ embedded?: boolean }>(), { embedded: false })

interface TreeNode {
  id: string
  label: string
  type: 'server' | 'database' | 'folder' | 'table'
  children?: TreeNode[]
}

const treeData: TreeNode[] = [
  {
    id: 's1',
    label: 'PostgreSQL Server',
    type: 'server',
    children: [
      {
        id: 'd1',
        label: 'Sales_DB',
        type: 'database',
        children: [
          {
            id: 'f1',
            label: '表/视图',
            type: 'folder',
            children: [
              { id: 't1', label: 'customers', type: 'table' },
              { id: 't2', label: 'orders', type: 'table' },
              { id: 't3', label: 'products', type: 'table' },
            ],
          },
        ],
      },
      {
        id: 'd2',
        label: 'User_Center',
        type: 'database',
        children: [
          {
            id: 'f2',
            label: '表/视图',
            type: 'folder',
            children: [
              { id: 't4', label: 'users', type: 'table' },
              { id: 't5', label: 'roles', type: 'table' },
            ],
          },
        ],
      },
    ],
  },
]
const treeProps = { children: 'children', label: 'label' }

function nodeIcon(type: TreeNode['type']) {
  return { server: Server, database: Database, folder: Folder, table: Table2 }[type] ?? Layers
}
function nodeColor(type: TreeNode['type']) {
  return { server: '#818cf8', database: '#a78bfa', folder: '#fbbf24', table: '#34d399' }[type]
}
function refreshTree() {
  ElMessage.success('连接树已刷新（示例数据）')
}
</script>

<style scoped>
.query-tree :deep(.el-tree-node__content) {
  border-radius: 0.5rem;
  height: 2rem;
}
.query-tree :deep(.el-tree-node__content:hover) {
  background: rgba(255, 255, 255, 0.06);
}
</style>
