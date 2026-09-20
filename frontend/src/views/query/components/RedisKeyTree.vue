<template>
  <div class="redis-key-tree text-xs">
    <div v-for="node in tree" :key="node.name" class="pl-2">
      <div class="flex items-center gap-1.5 py-1 hover:bg-white/5 rounded px-1 cursor-pointer" @click="toggle(node)">
        <ChevronRight v-if="node.children.length" class="w-3 h-3 text-white/30 transition-transform" :class="{ 'rotate-90': expanded.has(node.path) }" />
        <span v-else class="w-3" />
        <Folder v-if="node.children.length" class="w-3.5 h-3.5 text-amber-300/70" />
        <KeyRound v-else class="w-3.5 h-3.5" :class="typeColor(node.type)" />
        <span class="font-mono" :class="node.children.length ? 'text-white/60' : 'text-white/80 hover:text-indigo-300'" @click.stop="node.children.length ? toggle(node) : emit('select', node.fullKey)">{{ node.name }}</span>
        <span v-if="!node.children.length" class="px-1 py-0.5 rounded-full text-[10px] ml-1" :class="typeBadge(node.type)">{{ node.type }}</span>
        <span v-if="node.children.length" class="text-[10px] text-white/25 ml-1">{{ node.count }} keys</span>
        <span v-if="!node.children.length && node.ttl !== undefined" class="text-[10px] text-white/30 ml-1">{{ node.ttl === -1 ? '永久' : node.ttl + 's' }}</span>
      </div>
      <div v-if="node.children.length && expanded.has(node.path)" class="border-l border-white/5 ml-2">
        <RedisKeyTree :items="flattenChildren(node)" :selected="selected" :expanded="expanded" @select="emit('select', $event)" @toggle="emit('toggle', $event)" />
      </div>
    </div>
  </div>
</template>

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
    string: 'bg-emerald-400/15 text-emerald-300',
    hash: 'bg-indigo-400/15 text-indigo-300',
    list: 'bg-amber-400/15 text-amber-300',
    set: 'bg-violet-400/15 text-violet-300',
    zset: 'bg-rose-400/15 text-rose-300',
    folder: 'bg-white/10 text-white/40',
  }
  return map[t] || 'bg-white/10 text-white/50'
}
function typeColor(t: string) {
  const map: Record<string, string> = {
    string: 'text-emerald-300/70',
    hash: 'text-indigo-300/70',
    list: 'text-amber-300/70',
    set: 'text-violet-300/70',
    zset: 'text-rose-300/70',
  }
  return map[t] || 'text-white/40'
}
</script>
