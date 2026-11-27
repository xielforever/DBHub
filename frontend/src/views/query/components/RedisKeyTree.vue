<script setup lang="ts">
import { computed } from 'vue'
import { ChevronRight, Folder, KeyRound } from 'lucide-vue-next'

interface KeyItem {
  key: string
  type: string
  ttl: number
}

interface TreeNode {
  name: string
  path: string
  fullKey: string
  type: string
  ttl?: number
  children: TreeNode[]
  count: number
}

const props = defineProps<{
  items: KeyItem[]
  selected?: Set<string>
  expanded?: Set<string>
}>()
const emit = defineEmits<{
  (e: 'select', key: string): void
  (e: 'toggle', node: TreeNode): void
}>()

const expanded = computed(() => props.expanded || new Set<string>())

function buildTree(items: KeyItem[]): TreeNode[] {
  const root: Record<string, TreeNode> = {}
  for (const item of items) {
    const parts = item.key.split(':')
    let currentLevel = root
    let currentPath = ''
    for (let i = 0; i < parts.length; i++) {
      const part = parts[i] as string
      const isLeaf = i === parts.length - 1
      currentPath = currentPath ? `${currentPath}:${part}` : part
      if (!currentLevel[part]) {
        currentLevel[part] = {
          name: part,
          path: currentPath,
          fullKey: isLeaf ? item.key : currentPath,
          type: isLeaf ? item.type : 'folder',
          ttl: isLeaf ? item.ttl : undefined,
          children: [],
          count: 0,
        } as any
        ;(currentLevel[part] as any)._map = {}
      }
      const node = currentLevel[part] as TreeNode
      node.count += 1
      if (isLeaf) {
        node.type = item.type
        node.ttl = item.ttl
        node.fullKey = item.key
      }
      if (!isLeaf) {
        if (!(node as any)._map) (node as any)._map = {}
        currentLevel = (node as any)._map as Record<string, TreeNode>
      }
    }
  }
  function mapToArray(map: Record<string, TreeNode>): TreeNode[] {
    return Object.values(map).map(n => {
      const childMap = (n as any)._map as Record<string, TreeNode> | undefined
      if (childMap) {
        n.children = mapToArray(childMap).sort((a, b) => {
          if (a.children.length && !b.children.length) return -1
          if (!a.children.length && b.children.length) return 1
          return a.name.localeCompare(b.name)
        })
        delete (n as any)._map
      }
      return n
    }).sort((a, b) => {
      if (a.children.length && !b.children.length) return -1
      if (!a.children.length && b.children.length) return 1
      return a.name.localeCompare(b.name)
    })
  }
  return mapToArray(root)
}

const tree = computed(() => buildTree(props.items))

function flattenChildren(node: TreeNode): KeyItem[] {
  // 为了递归复用，将子节点展平为 KeyItem 列表再重新构建
  const result: KeyItem[] = []
  function collect(n: TreeNode) {
    if (n.children.length === 0) {
      result.push({ key: n.fullKey, type: n.type, ttl: n.ttl ?? -1 })
    } else {
      n.children.forEach(collect)
    }
  }
  node.children.forEach(collect)
  // 同时保留文件夹结构：直接返回原始 items 过滤
  // 这里返回过滤后的 items 供子组件重新构建
  const prefix = node.path + ':'
  return props.items.filter(i => i.key === node.path || i.key.startsWith(prefix))
}

function toggle(node: TreeNode) {
  emit('toggle', node)
}
function typeBadge(t: string) {
  const map: Record<string, string> = {
    string: 'bg-success-soft text-success',
    hash: 'bg-accent-soft text-accent',
    list: 'bg-warning-soft text-warning',
    set: 'bg-accent-soft text-accent',
    zset: 'bg-danger-soft text-danger',
    folder: 'bg-surface-muted text-subtle',
  }
  return map[t] || 'bg-surface-muted text-muted'
}
function typeColor(t: string) {
  const map: Record<string, string> = {
    string: 'text-success',
    hash: 'text-accent',
    list: 'text-warning',
    set: 'text-accent',
    zset: 'text-danger',
  }
  return map[t] || 'text-subtle'
}
</script>

<template>
  <div class="redis-key-tree text-xs">
    <div v-for="node in tree" :key="node.name" class="pl-2">
      <button type="button" class="w-full text-left flex items-center gap-1.5 py-1 hover:bg-interactive rounded px-1 aria-pressed:bg-accent-soft ui-control-target" :aria-expanded="node.children.length ? expanded.has(node.path) : undefined" :aria-pressed="!node.children.length ? (selected?.has(node.fullKey) ?? false) : undefined" :aria-label="node.children.length ? (expanded.has(node.path) ? '折叠 ' : '展开 ') + node.path : '选择键 ' + node.fullKey" @click="node.children.length ? toggle(node) : emit('select', node.fullKey)">
        <ChevronRight v-if="node.children.length" class="w-3 h-3 text-subtle transition-transform" :class="{ 'rotate-90': expanded.has(node.path) }" aria-hidden="true" />
        <span v-else class="w-3" />
        <Folder v-if="node.children.length" class="w-3.5 h-3.5 text-warning" aria-hidden="true" />
        <KeyRound v-else class="w-3.5 h-3.5" :class="typeColor(node.type)" aria-hidden="true" />
        <span class="font-mono" :class="node.children.length ? 'text-muted' : 'text-content hover:text-accent'">{{ node.name }}</span>
        <span v-if="!node.children.length" class="px-1 py-0.5 rounded-full text-xs ml-1" :class="typeBadge(node.type)">{{ node.type }}</span>
        <span v-if="node.children.length" class="text-xs text-subtle ml-1">{{ node.count }} keys</span>
        <span v-if="!node.children.length && node.ttl !== undefined" class="text-xs text-subtle ml-1">{{ node.ttl === -1 ? '永久' : node.ttl + 's' }}</span>
      </button>
      <div v-if="node.children.length && expanded.has(node.path)" class="border-l border-stroke ml-2">
        <RedisKeyTree :items="flattenChildren(node)" :selected="selected" :expanded="expanded" @select="emit('select', $event)" @toggle="emit('toggle', $event)" />
      </div>
    </div>
  </div>
</template>
