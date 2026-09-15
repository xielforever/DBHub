<template>
  <div class="flex flex-col lg:flex-row gap-4 lg:h-[calc(100vh-7rem)]">
    <!-- 左侧：连接树（lg 及以上常驻） -->
    <ConnectionTreePanel class="hidden lg:flex w-60 xl:w-64 shrink-0" />

    <!-- 连接树抽屉（lg 以下） -->
    <el-drawer
      v-model="treeDrawerOpen"
      title="数据库"
      direction="ltr"
      size="78%"
      class="glass-drawer"
    >
      <ConnectionTreePanel embedded />
    </el-drawer>

    <!-- 中间：编辑器 + 结果 -->
    <section class="flex-1 min-w-0 flex flex-col gap-4">
      <!-- 编辑器卡片 -->
      <div class="glass-panel flex flex-col h-[56vh] lg:h-auto lg:flex-1 min-h-[320px] overflow-hidden">
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

            <!-- 工具栏（小屏可换行） -->
            <div class="flex flex-wrap items-center gap-2 sm:gap-3 px-3 sm:px-4 py-2.5 border-b border-white/10">
              <!-- 小屏呼出连接树 -->
              <button
                class="ghost-button !py-1.5 !px-3 text-xs flex items-center gap-1.5 lg:hidden"
                @click="treeDrawerOpen = true"
              >
                <FolderTree class="w-3.5 h-3.5" /> 连接
              </button>
              <button class="liquid-button !px-4 !py-2 text-sm flex items-center gap-2" @click="runQuery">
                <Play class="w-4 h-4" /> 运行
              </button>
              <div class="hidden sm:block h-5 w-px bg-white/10" />
              <select v-model="tab.database" class="glass-input !py-1.5 !w-32 sm:!w-44 text-xs">
                <option>Sales_DB</option>
                <option>User_Center</option>
              </select>
              <label class="flex items-center gap-1.5 text-xs text-white/45">
                <span class="hidden sm:inline">限制行数</span>
                <span class="sm:hidden">行</span>
                <input
                  v-model.number="tab.limit"
                  type="number"
                  min="1"
                  max="10000"
                  class="glass-input !py-1 w-16 sm:w-20 text-xs"
                />
              </label>
              <div class="flex-1" />
              <!-- 小屏呼出 AI 助手（xl 以下无常驻面板） -->
              <button
                class="ghost-button !py-1.5 !px-3 text-xs flex items-center gap-1.5 xl:hidden"
                @click="aiDrawerOpen = true"
              >
                <Sparkles class="w-3.5 h-3.5" /> AI
              </button>
              <span class="hidden xl:inline text-[11px] text-white/30">Monaco Editor 接入中</span>
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
      <div class="glass-panel h-64 lg:h-72 shrink-0 flex flex-col overflow-hidden">
        <el-tabs v-model="resultTab" class="flex-1 flex flex-col min-h-0">
          <el-tab-pane label="结果" name="result" class="flex flex-col min-h-0 flex-1">
            <div class="overflow-auto flex-1">
              <table class="w-full text-sm">
                <thead class="sticky top-0 bg-white/10 backdrop-blur">
                  <tr class="text-left text-white/50 text-xs">
                    <th
                      v-for="col in resultColumns"
                      :key="col"
                      class="px-4 py-2.5 font-medium border-b border-white/10 whitespace-nowrap"
                    >
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
                    <td v-for="(cell, ci) in row" :key="ci" class="px-4 py-2.5 text-white/75 whitespace-nowrap">
                      {{ cell }}
                    </td>
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
        <footer
          class="flex items-center gap-4 sm:gap-5 px-4 py-2 border-t border-white/10 text-xs text-white/45 shrink-0"
        >
          <span>执行耗时：<span class="text-emerald-300">--</span></span>
          <span>行数：<span class="text-indigo-200">{{ resultRows.length }}</span></span>
          <span class="flex-1" />
          <span class="hidden sm:inline">UTF-8</span>
        </footer>
      </div>
    </section>

    <!-- 右侧：AI 助手（xl 及以上常驻，可收起） -->
    <AiAssistantPanel
      v-if="aiVisible"
      class="hidden xl:flex w-72 shrink-0"
      @close="aiVisible = false"
    />
    <button
      v-else
      class="hidden xl:flex w-10 shrink-0 glass-panel items-center justify-center text-white/50 hover:text-white transition-colors"
      aria-label="展开 AI 助手"
      @click="aiVisible = true"
    >
      <PanelRightOpen class="w-5 h-5" />
    </button>

    <!-- AI 助手抽屉（xl 以下） -->
    <el-drawer
      v-model="aiDrawerOpen"
      title="AI 助手"
      direction="rtl"
      size="85%"
      class="glass-drawer"
    >
      <AiAssistantPanel embedded :closable="false" />
    </el-drawer>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { ElMessage } from 'element-plus'
import {
  FileCode2,
  FolderTree,
  PanelRightOpen,
  Play,
  Sparkles,
  X,
} from 'lucide-vue-next'
import ConnectionTreePanel from './components/ConnectionTreePanel.vue'
import AiAssistantPanel from './components/AiAssistantPanel.vue'

interface EditorTab {
  id: number
  name: string
  database: string
  limit: number
  sql: string
}

let tabSeq = 2
const tabs = ref<EditorTab[]>([
  {
    id: 1,
    name: 'SQL Editor 1',
    database: 'Sales_DB',
    limit: 100,
    sql: "SELECT *\nFROM customers\nWHERE city = 'Beijing';",
  },
  { id: 2, name: 'SQL Editor 2', database: 'Sales_DB', limit: 100, sql: '' },
])
const activeTab = ref(1)
const resultTab = ref('result')
const aiVisible = ref(true)
const treeDrawerOpen = ref(false)
const aiDrawerOpen = ref(false)
const clock = ref(new Date().toLocaleTimeString('zh-CN', { hour12: false }))

const resultColumns = ['id', 'name', 'email', 'city']
const resultRows = [
  ['1', 'Alice', 'alice@example.com', 'Beijing'],
  ['2', 'Bob', 'bob@example.com', 'Beijing'],
  ['3', 'Mike', 'mike@example.com', 'Shanghai'],
]

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
    tabs.value.push({
      id: tabSeq,
      name: `SQL Editor ${tabSeq}`,
      database: 'Sales_DB',
      limit: 100,
      sql: '',
    })
    activeTab.value = tabSeq
  } else if (activeTab.value === id) {
    const neighbor = tabs.value[Math.max(0, idx - 1)]
    if (neighbor) activeTab.value = neighbor.id
  }
}
</script>

<style scoped>
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
