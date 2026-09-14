<template>
  <div class="h-[calc(100vh-7rem)] flex gap-4">
    <!-- 左侧：连接树 -->
    <aside class="w-64 shrink-0 glass-panel p-3 flex flex-col overflow-hidden">
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
            <component :is="nodeIcon(data.type)" class="w-3.5 h-3.5 shrink-0" :style="{ color: nodeColor(data.type) }" />
            <span class="truncate text-white/75">{{ data.label }}</span>
          </span>
        </template>
      </el-tree>
    </aside>

    <!-- 中间：编辑器 + 结果 -->
    <section class="flex-1 min-w-0 flex flex-col gap-4">
      <!-- 编辑器卡片 -->
      <div class="glass-panel flex flex-col flex-1 min-h-0 overflow-hidden">
        <el-tabs v-model="activeTab" class="query-tabs flex-1 flex flex-col min-h-0">
          <el-tab-pane
            v-for="tab in tabs"
            :key="tab.id"
            :name="tab.id"
            class="flex flex-col min-h-0 flex-1"
          >
            <template #label>
              <span class="flex items-center gap-2 px-1">
                <FileCode2 class="w-3.5 h-3.5" />
                {{ tab.name }}
                <button
                  class="text-white/30 hover:text-white ml-1"
                  aria-label="关闭标签页"
                  @click.stop="closeTab(tab.id)"
                >
                  <X class="w-3.5 h-3.5" />
                </button>
              </span>
            </template>

            <!-- 工具栏 -->
            <div class="flex items-center gap-3 px-4 py-2.5 border-b border-white/10">
              <button class="liquid-button !px-4 !py-2 text-sm flex items-center gap-2" @click="runQuery">
                <Play class="w-4 h-4" /> 运行
              </button>
              <div class="h-5 w-px bg-white/10" />
              <select v-model="tab.database" class="glass-input !py-1.5 !w-44 text-xs">
                <option>Sales_DB</option>
                <option>User_Center</option>
              </select>
              <div class="flex items-center gap-2 text-xs text-white/45">
                限制行数
                <input v-model.number="tab.limit" type="number" min="1" max="10000" class="glass-input !py-1 w-20 text-xs" />
              </div>
              <div class="flex-1" />
              <span class="text-[11px] text-white/30">Monaco Editor 接入中</span>
            </div>

            <!-- SQL 编辑区（Monaco 落地前的占位） -->
            <textarea
              v-model="tab.sql"
              spellcheck="false"
              class="flex-1 w-full resize-none bg-transparent p-4 font-mono text-[13px] leading-6 text-indigo-100/90 outline-none"
              placeholder="-- 在此编写 SQL，例如：SELECT * FROM customers LIMIT 100;"
            />
          </el-tab-pane>
        </el-tabs>
      </div>

      <!-- 结果面板 -->
      <div class="glass-panel h-72 shrink-0 flex flex-col overflow-hidden">
        <el-tabs v-model="resultTab" class="flex-1 flex flex-col min-h-0">
          <el-tab-pane label="结果" name="result" class="flex flex-col min-h-0 flex-1">
            <div class="overflow-auto flex-1">
              <table class="w-full text-sm">
                <thead class="sticky top-0 bg-white/8 backdrop-blur">
                  <tr class="text-left text-white/50 text-xs">
                    <th v-for="col in resultColumns" :key="col" class="px-4 py-2.5 font-medium border-b border-white/10">
                      {{ col }}
                    </th>
                  </tr>
                </thead>
                <tbody>
                  <tr
                    v-for="(row, i) in resultRows"
                    :key="i"
                    class="border-b border-white/5 hover:bg-white/5 transition-colors"
                  >
                    <td v-for="(cell, ci) in row" :key="ci" class="px-4 py-2.5 text-white/75">{{ cell }}</td>
                  </tr>
                </tbody>
              </table>
            </div>
          </el-tab-pane>
          <el-tab-pane label="消息" name="message">
            <div class="p-4 text-sm text-white/45 font-mono space-y-1">
              <p>[{{ clock }}] 编辑器就绪，等待执行...</p>
              <p class="text-white/30">-- SQL 执行引擎接口对接后，这里将展示执行日志与错误详情</p>
            </div>
          </el-tab-pane>
        </el-tabs>
        <footer class="flex items-center gap-5 px-4 py-2 border-t border-white/10 text-xs text-white/45 shrink-0">
          <span>执行耗时：<span class="text-emerald-300">--</span></span>
          <span>行数：<span class="text-indigo-200">{{ resultRows.length }}</span></span>
          <span class="flex-1" />
          <span>UTF-8</span>
        </footer>
      </div>
    </section>

    <!-- 右侧：AI 助手（可折叠） -->
    <aside v-if="aiVisible" class="w-72 shrink-0 glass-panel flex flex-col overflow-hidden">
      <header class="flex items-center justify-between px-4 py-3 border-b border-white/10">
        <h2 class="text-sm font-medium flex items-center gap-2">
          <Sparkles class="w-4 h-4 text-indigo-300" /> AI 助手
        </h2>
        <button
          class="text-white/40 hover:text-white transition-colors"
          aria-label="收起 AI 助手"
          @click="aiVisible = false"
        >
          <PanelRightClose class="w-4 h-4" />
        </button>
      </header>
      <div class="flex-1 overflow-y-auto p-4 space-y-3">
        <div class="glass-card !rounded-2xl p-3 text-xs text-white/70 max-w-[85%]">
          你好！我可以帮你生成 SQL、解释执行计划与优化慢查询。
        </div>
        <div class="glass-card !rounded-2xl p-3 text-xs text-white/70 max-w-[85%] ml-auto text-right" style="background-image: var(--image-glass-gradient)">
          优化这条 SQL，谢谢。
        </div>
      </div>
      <div class="p-3 border-t border-white/10">
        <div class="glass-input !py-2 text-xs text-white/35">AI 能力接入中...</div>
      </div>
    </aside>
    <button
      v-else
      class="w-10 shrink-0 glass-panel flex items-center justify-center text-white/50 hover:text-white transition-colors"
      aria-label="展开 AI 助手"
      @click="aiVisible = true"
    >
      <PanelRightOpen class="w-5 h-5" />
    </button>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { ElMessage } from 'element-plus'
import {
  Database,
  FileCode2,
  Folder,
  Layers,
  PanelRightClose,
  PanelRightOpen,
  Play,
  RefreshCw,
  Server,
  Sparkles,
  Table2,
  X,
} from 'lucide-vue-next'

interface EditorTab {
  id: number
  name: string
  database: string
  limit: number
  sql: string
}

let tabSeq = 2
const tabs = ref<EditorTab[]>([
  { id: 1, name: 'SQL Editor 1', database: 'Sales_DB', limit: 100, sql: 'SELECT *\nFROM customers\nWHERE city = \'Beijing\';' },
  { id: 2, name: 'SQL Editor 2', database: 'Sales_DB', limit: 100, sql: '' },
])
const activeTab = ref(1)
const resultTab = ref('result')
const aiVisible = ref(true)
const clock = ref(new Date().toLocaleTimeString('zh-CN', { hour12: false }))

const resultColumns = ['id', 'name', 'email', 'city']
const resultRows = [
  ['1', 'Alice', 'alice@example.com', 'Beijing'],
  ['2', 'Bob', 'bob@example.com', 'Beijing'],
  ['3', 'Mike', 'mike@example.com', 'Shanghai'],
]

interface TreeNode {
  id: string
  label: string
  type: 'server' | 'database' | 'folder' | 'table'
  children?: TreeNode[]
}

const treeData: TreeNode[] = [
  {
    id: 's1', label: 'PostgreSQL Server', type: 'server',
    children: [
      {
        id: 'd1', label: 'Sales_DB', type: 'database',
        children: [
          {
            id: 'f1', label: '表/视图', type: 'folder',
            children: [
              { id: 't1', label: 'customers', type: 'table' },
              { id: 't2', label: 'orders', type: 'table' },
              { id: 't3', label: 'products', type: 'table' },
            ],
          },
        ],
      },
      {
        id: 'd2', label: 'User_Center', type: 'database',
        children: [
          {
            id: 'f2', label: '表/视图', type: 'folder',
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

function runQuery() {
  ElMessage.info('SQL 执行引擎接口开发中，敬请期待')
  resultTab.value = 'message'
}

function closeTab(id: number) {
  const idx = tabs.value.findIndex((t) => t.id === id)
  if (idx === -1) return
  tabs.value.splice(idx, 1)
  if (tabs.value.length === 0) {
    tabSeq += 1
    tabs.value.push({ id: tabSeq, name: `SQL Editor ${tabSeq}`, database: 'Sales_DB', limit: 100, sql: '' })
    activeTab.value = tabSeq
  } else if (activeTab.value === id) {
    const neighbor = tabs.value[Math.max(0, idx - 1)]
    if (neighbor) activeTab.value = neighbor.id
  }
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
.query-tabs {
  padding: 0 0.5rem;
}
.query-tabs :deep(.el-tabs__content) {
  flex: 1;
  display: flex;
  flex-direction: column;
  min-height: 0;
}
.query-tabs :deep(.el-tab-pane) {
  display: flex;
  flex-direction: column;
  flex: 1;
  min-height: 0;
}
</style>
