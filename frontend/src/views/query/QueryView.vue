<template>
  <div class="flex flex-col lg:flex-row gap-4 lg:h-[calc(100vh-7rem)]" :class="{ 'fixed inset-0 z-[2000] bg-[#0a0a14] p-4 gap-0': resultFullscreen }">
    <!-- 左侧：连接树 -->
    <ConnectionTreePanel
      v-if="!resultFullscreen"
      ref="treeRef"
      class="hidden lg:flex w-60 xl:w-64 shrink-0"
      @select-connection="onSelectConnection"
      @databases-loaded="onDatabasesLoaded"
      @tables-loaded="onTablesLoaded"
      @preview-table="onPreviewTable"
      @generate-select="onGenerateSelect"
      @redis-overview="onRedisOverview"
      @redis-keys="onRedisKeys"
      @insert-table-name="onInsertTableName"
    />

    <el-drawer v-model="treeDrawerOpen" title="数据源" direction="ltr" size="82%" lazy class="glass-drawer">
      <ConnectionTreePanel
        embedded
        @select-connection="onSelectConnection"
        @databases-loaded="onDatabasesLoaded"
        @tables-loaded="onTablesLoaded"
        @preview-table="(p) => { onPreviewTable(p); treeDrawerOpen = false }"
        @generate-select="(p) => { onGenerateSelect(p); treeDrawerOpen = false }"
        @redis-overview="(c) => { onRedisOverview(c); treeDrawerOpen = false }"
        @redis-keys="(c) => { onRedisKeys(c); treeDrawerOpen = false }"
        @insert-table-name="(n) => { onInsertTableName(n); treeDrawerOpen = false }"
      />
    </el-drawer>

    <section class="flex-1 min-w-0 flex flex-col gap-0 overflow-hidden">
      <!-- 编辑器 -->
      <div
        v-if="!resultFullscreen"
        class="glass-panel flex flex-col overflow-hidden shrink-0"
        :style="{ height: editorHeight + '%' }"
      >
        <el-tabs
          v-model="activeTab"
          class="query-tabs flex-1 flex flex-col min-h-0"
          @tab-remove="closeTab"
        >
          <el-tab-pane
            v-for="tab in tabs"
            :key="tab.id"
            :name="tab.id"
            :closable="tabs.length > 1"
            class="flex flex-col min-h-0 flex-1"
          >
            <template #label>
              <span class="flex items-center gap-2 px-1">
                <FileCode2 class="w-3.5 h-3.5" />{{ tab.name }}
                <span v-if="tab.sql.trim().length" class="w-1.5 h-1.5 rounded-full bg-indigo-400/80 ml-1" />
              </span>
            </template>

            <div class="flex flex-wrap items-center gap-2 sm:gap-3 px-3 sm:px-4 py-2.5 border-b border-white/10 shrink-0">
              <button class="ghost-button !py-1.5 !px-3 text-xs flex items-center gap-1.5 lg:hidden" @click="treeDrawerOpen = true">
                <FolderTree class="w-3.5 h-3.5" /> 连接
              </button>
              <button class="liquid-button !px-4 !py-2 text-sm flex items-center gap-2" :disabled="!currentConn || running" @click="runQuery">
                <Play class="w-4 h-4" />{{ running ? '执行中…' : '运行' }}
                <span class="hidden sm:inline text-[10px] opacity-60 ml-1">⌘↵</span>
              </button>
              <button v-if="running" class="ghost-button !py-1.5 !px-3 text-xs flex items-center gap-1.5 text-rose-300/80" @click="cancelQuery">
                <Square class="w-3.5 h-3.5" /> 取消
              </button>
              <button class="ghost-button !py-1.5 !px-3 text-xs flex items-center gap-1.5" :disabled="!currentConn || running || currentConn.type === 'redis'" @click="explainQuery">
                <FileSearch class="w-3.5 h-3.5" /> EXPLAIN
              </button>
              <button class="ghost-button !py-1.5 !px-3 text-xs flex items-center gap-1.5" @click="formatCurrent">
                <Wand2 class="w-3.5 h-3.5" /> 格式化
              </button>
              <button class="ghost-button !py-1.5 !px-3 text-xs flex items-center gap-1.5" @click="addTab">
                <Plus class="w-3.5 h-3.5" /> 新查询
              </button>
              <el-dropdown v-if="currentConn && currentConn.type !== 'redis'" trigger="click" popper-class="glass-popper">
                <button class="ghost-button !py-1.5 !px-2.5 text-xs flex items-center gap-1" :class="transactionActive ? 'bg-amber-500/20 text-amber-300 border-amber-400/30' : ''">
                  <span class="w-1.5 h-1.5 rounded-full" :class="transactionActive ? 'bg-amber-400 animate-pulse' : 'bg-white/20'"></span>
                  {{ transactionActive ? '事务中' : '事务' }}
                </button>
                <template #dropdown>
                  <el-dropdown-menu>
                    <el-dropdown-item v-if="!transactionActive" @click="beginTransaction">BEGIN 开启事务</el-dropdown-item>
                    <el-dropdown-item v-if="transactionActive" @click="commitTransaction">COMMIT 提交</el-dropdown-item>
                    <el-dropdown-item v-if="transactionActive" @click="rollbackTransaction">ROLLBACK 回滚</el-dropdown-item>
                    <el-dropdown-item divided @click="loadTransactionStatus">刷新状态</el-dropdown-item>
                  </el-dropdown-menu>
                </template>
              </el-dropdown>
              <button class="ghost-button !py-1.5 !px-2.5 text-xs flex items-center gap-1.5" :class="paramDetected ? 'bg-amber-500/20 text-amber-300 border-amber-400/30' : ''" @click="paramDrawerOpen = true">
                <Braces class="w-3.5 h-3.5" /> 参数
                <span v-if="paramDetected" class="px-1 py-0.5 rounded-full bg-amber-400/20 text-[10px]">{{ paramDetected }}</span>
              </button>
              <el-dropdown trigger="click" popper-class="glass-popper" @command="onEditorOptionCommand">
                <button class="ghost-button !py-1.5 !px-2.5 text-xs flex items-center gap-1"><Settings2 class="w-3.5 h-3.5" /> 选项</button>
                <template #dropdown>
                  <el-dropdown-menu>
                    <el-dropdown-item command="toggle-minimap">{{ minimapEnabled ? '关闭小地图' : '开启小地图' }}</el-dropdown-item>
                    <el-dropdown-item command="toggle-autolimit">{{ autoLimitEnabled ? '关闭自动 LIMIT 1000' : '开启自动 LIMIT 1000' }}</el-dropdown-item>
                    <el-dropdown-item command="add-limit">为当前 SELECT 添加 LIMIT 100</el-dropdown-item>
                  </el-dropdown-menu>
                </template>
              </el-dropdown>
              <div class="hidden sm:block h-5 w-px bg-white/10" />
              <span v-if="currentConn" class="flex items-center gap-1.5 text-xs text-white/65">
                <component :is="currentConn.type === 'redis' ? KeyRound : Database" class="w-3.5 h-3.5" :style="{ color: connColor(currentConn.type) }" />
                {{ currentConn.name }}
                <span v-if="currentConn.environment === 'prod'" class="px-1.5 py-0.5 rounded-full bg-rose-500/20 text-rose-300 text-[10px]">PROD</span>
                <span v-if="currentConn.proxy_name" class="px-1.5 py-0.5 rounded-full bg-violet-500/20 text-violet-300 text-[10px] flex items-center gap-1"><Waypoints class="w-3 h-3" />{{ currentConn.proxy_name }}</span>
                <span v-if="autoLimitEnabled" class="px-1.5 py-0.5 rounded-full bg-emerald-500/20 text-emerald-300 text-[10px]">自动 LIMIT</span>
              </span>
              <div v-if="currentConn && currentConn.type !== 'redis'" class="w-32 sm:w-40 shrink-0">
                <el-select
                  v-model="tab.database"
                  size="small"
                  aria-label="目标数据库"
                  class="w-full"
                  popper-class="glass-popper"
                >
                  <el-option v-for="d in databaseOptions" :key="d.name" :label="d.name" :value="d.name" />
                </el-select>
              </div>
              <div class="flex-1" />
              <div class="flex items-center gap-1">
                <span class="text-[10px] text-white/25 hidden md:inline">{{ editorStats.chars }} 字符 · {{ editorStats.lines }} 行<span v-if="editorStats.selected"> · 已选 {{ editorStats.selected }} 字符</span></span>
                <button class="ghost-button !py-1.5 !px-2.5 text-xs flex items-center gap-1" @click="openSnippetDialog" title="保存当前 SQL 为片段">
                  <Bookmark class="w-3.5 h-3.5" /> 收藏
                </button>
                <button class="ghost-button !py-1.5 !px-2.5 text-xs flex items-center gap-1" @click="snippetListOpen = true" title="片段列表">
                  <Library class="w-3.5 h-3.5" /> {{ snippets.length }}
                </button>
                <button class="ghost-button !py-1.5 !px-2.5 text-xs" @click="shortcutsOpen = true" title="快捷键 ?">
                  <Keyboard class="w-3.5 h-3.5" />
                </button>
              </div>
              <span class="hidden lg:flex items-center gap-1 text-[10px] text-white/25 ml-1"><Keyboard class="w-3 h-3" /> 选中执行 · ⌘+Enter 运行 · ⇧⌘+F 格式化 · ? 帮助</span>
              <button class="ghost-button !py-1.5 !px-3 text-xs flex items-center gap-1.5 xl:hidden" @click="aiDrawerOpen = true">
                <Sparkles class="w-3.5 h-3.5" /> AI
              </button>
            </div>

            <div class="flex-1 min-h-0 relative">
              <SqlMonaco
                v-if="monacoReady"
                :ref="(el: any) => { if (el) monacoRefs[tab.id] = el }"
                v-model="tab.sql"
                :tables="allTableNames"
                :columns="allColumnNames"
                :minimap="minimapEnabled"
                :placeholder="currentConn && currentConn.type === 'redis'
                  ? '-- Redis 数据源不支持 SQL，请在左下方结果区浏览键空间'
                  : '-- 先在左侧选择数据源与表，然后在此编写 SQL，Ctrl/Cmd + Enter 运行，选中部分 SQL 仅执行选中段'"
                @run="runQuery"
                @format="formatCurrent"
                @selection-change="onSelectionChange"
              />
              <textarea
                v-else
                v-model="tab.sql"
                spellcheck="false"
                role="textbox"
                aria-label="SQL 编辑器"
                class="flex-1 w-full h-full resize-none bg-transparent p-4 font-mono text-[13px] leading-6 text-indigo-100/90 outline-none focus:outline-none"
                :placeholder="currentConn && currentConn.type === 'redis'
                  ? '-- Redis 数据源不支持 SQL，请在左下方结果区浏览键空间'
                  : '-- 先在左侧选择数据源与表，然后在此编写 SQL，Ctrl/Cmd + Enter 运行'"
                @keydown.ctrl.enter.prevent="runQuery"
                @keydown.meta.enter.prevent="runQuery"
              />
            </div>
          </el-tab-pane>
        </el-tabs>
      </div>

      <!-- 分割线 -->
      <div
        v-if="!resultFullscreen"
        class="h-2 shrink-0 flex items-center justify-center cursor-row-resize group/splitter select-none"
        @mousedown="startSplitterDrag"
      >
        <div class="w-12 h-1 rounded-full bg-white/10 group-hover/splitter:bg-indigo-400/50 transition-colors" />
      </div>

      <!-- 结果面板 -->
      <div
        class="glass-panel flex flex-col overflow-hidden min-h-[180px]"
        :class="resultFullscreen ? 'flex-1' : ''"
        :style="resultFullscreen ? {} : { height: (100 - editorHeight) + '%' }"
      >
        <div v-if="transactionActive" class="px-4 py-2 flex items-center gap-3 bg-amber-500/10 border-b border-amber-400/20 text-xs shrink-0">
          <span class="w-2 h-2 rounded-full bg-amber-400 animate-pulse"></span>
          <span class="text-amber-300">事务进行中</span>
          <span class="font-mono text-white/50">{{ transactionId }}</span>
          <span class="text-white/40">{{ transactionStartedAt ? '始于 ' + formatTime(transactionStartedAt) : '' }} · {{ transactionQueries }} 条已执行</span>
          <div class="flex-1" />
          <button class="ghost-button !py-1 !px-2 text-[11px] text-emerald-300" @click="commitTransaction">COMMIT</button>
          <button class="ghost-button !py-1 !px-2 text-[11px] text-rose-300" @click="rollbackTransaction">ROLLBACK</button>
        </div>
        <el-tabs v-model="resultTab" class="flex-1 flex flex-col min-h-0" @tab-change="onResultTabChange">
          <el-tab-pane label="结果" name="result" class="flex flex-col min-h-0 flex-1">
            <div v-if="viewMode === 'preview'" class="flex flex-wrap items-center gap-3 px-4 py-2 border-b border-white/10 text-xs text-white/60 shrink-0">
              <Table2 class="w-3.5 h-3.5 text-emerald-300" />
              <span class="font-mono">{{ preview.schema || preview.database }}.{{ preview.table }}</span>
              <span class="text-white/35">共 {{ preview.total }} 行</span>
              <div class="flex-1" />
              <button class="ghost-button !py-1 !px-2.5" :disabled="preview.page <= 1" @click="previewPage(-1)">上一页</button>
              <span>第 {{ preview.page }} 页</span>
              <button class="ghost-button !py-1 !px-2.5" :disabled="!preview.hasMore" @click="previewPage(1)">下一页</button>
            </div>
            <div v-if="writeResult" class="flex-1 flex flex-col items-center justify-center gap-2 text-sm">
              <CheckCircle2 class="w-8 h-8 text-emerald-400" />
              <p class="text-white/80">执行成功，影响 {{ writeResult.affected_rows ?? 0 }} 行 · 耗时 {{ writeResult.duration_ms }} ms</p>
              <button class="ghost-button !py-1 !px-3 text-xs mt-2" @click="resetGrid">清空结果</button>
            </div>
            <ExplainPlan
              v-else-if="isExplainResult"
              :columns="grid.columns"
              :rows="grid.rows as any"
            />
            <ResultGrid
              v-else
              :columns="grid.columns"
              :rows="grid.rows as any"
              :truncated="grid.truncated"
              :base-index="viewMode === 'preview' ? (preview.page - 1) * preview.pageSize : 0"
              :sql="currentTab.sql"
              :fullscreen="resultFullscreen"
              :table-name="preview.table || 'result_table'"
              @toggle-fullscreen="resultFullscreen = !resultFullscreen"
            />
          </el-tab-pane>

          <el-tab-pane label="图表" name="chart" class="flex flex-col min-h-0 flex-1">
            <ResultChartPane
              :columns="grid.columns"
              :rows="(grid.rows as unknown[][])"
              :sql="currentTab.sql"
              :connection-id="currentConn?.id ?? 0"
              :database="currentTab.database"
            />
          </el-tab-pane>

          <el-tab-pane label="统计" name="stats" class="flex flex-col min-h-0 flex-1">
            <ColumnStatsPane :columns="grid.columns" :rows="(grid.rows as unknown[][])" />
          </el-tab-pane>

          <el-tab-pane label="快照" name="snapshot" class="flex flex-col min-h-0 flex-1">
            <ResultSnapshotPane :columns="grid.columns" :rows="(grid.rows as unknown[][])" :sql="currentTab.sql" :duration="lastDuration" @restore="onRestoreSnapshot" />
          </el-tab-pane>

          <!-- 历史增强 -->
          <el-tab-pane label="历史" name="history" class="flex flex-col min-h-0 flex-1">
            <div class="flex flex-wrap items-center gap-2 px-3 py-2 border-b border-white/10 bg-white/[0.02]">
              <div class="relative">
                <Search class="w-3.5 h-3.5 absolute left-2 top-1/2 -translate-y-1/2 text-white/30" />
                <input v-model.trim="historyKeyword" class="glass-input !py-1.5 !pl-7 text-xs w-40 sm:w-52" placeholder="搜索 SQL/库/连接" @keydown.enter="loadHistory" />
              </div>
              <el-select v-model="historyConnFilter" size="small" class="w-32" clearable placeholder="连接" popper-class="glass-popper" @change="loadHistory">
                <el-option v-for="c in allConnections" :key="c.id" :label="c.name" :value="c.id" />
              </el-select>
              <el-select v-model="historyFilter" size="small" class="w-24" popper-class="glass-popper" @change="loadHistory">
                <el-option label="全部" value="all" />
                <el-option label="成功" value="success" />
                <el-option label="失败" value="failed" />
              </el-select>
              <div class="flex-1" />
              <span class="text-[11px] text-white/30 hidden sm:inline">{{ historyTotal }} 条</span>
              <button class="ghost-button !py-1 !px-2.5 text-xs flex items-center gap-1" @click="loadHistory">
                <RefreshCw class="w-3 h-3" /> 刷新
              </button>
              <button class="ghost-button !py-1 !px-2.5 text-xs text-rose-300/80" @click="clearHistory">清空</button>
            </div>
            <div class="overflow-auto flex-1" v-loading="historyLoading">
              <div
                v-for="h in sortedHistory"
                :key="h.id"
                class="group px-4 py-2.5 border-b border-white/5 hover:bg-white/5 cursor-pointer relative"
                :class="{ 'bg-amber-500/5 border-amber-400/10': isPinned(h.id) }"
                @click="reuseHistory(h)"
              >
                <div class="absolute left-0 top-0 bottom-0 w-0.5" :class="isPinned(h.id) ? 'bg-amber-400/60' : 'bg-transparent'" />
                <div class="flex items-center gap-2 text-xs mb-1">
                  <span :class="h.status === 1 ? 'bg-emerald-400/15 text-emerald-300' : 'bg-rose-400/15 text-rose-300'" class="px-1.5 py-0.5 rounded-full">
                    {{ h.status === 1 ? '成功' : '失败' }}
                  </span>
                  <span class="text-white/45">{{ h.connection_name || `#${h.connection_id}` }}</span>
                  <span v-if="h.database_name" class="text-white/35">{{ h.database_name }}</span>
                  <span class="text-white/30">{{ h.execution_time_ms ?? 0 }} ms</span>
                  <span v-if="h.row_count" class="text-indigo-300/60">{{ h.row_count }} 行</span>
                  <span v-if="h.affected_rows" class="text-amber-300/60">影响 {{ h.affected_rows }}</span>
                  <span class="flex-1" />
                  <span class="text-white/30">{{ formatTime(h.created_at) }}</span>
                  <button
                    class="p-1 rounded hover:bg-white/10"
                    :class="isPinned(h.id) ? 'text-amber-300 opacity-100' : 'text-white/30 opacity-0 group-hover:opacity-100'"
                    @click.stop="togglePin(h.id)"
                  >
                    <Pin class="w-3.5 h-3.5" />
                  </button>
                  <button
                    class="opacity-0 group-hover:opacity-100 text-white/40 hover:text-rose-300 transition-opacity p-1"
                    aria-label="删除该历史"
                    @click.stop="removeHistory(h.id)"
                  >
                    <Trash2 class="w-3.5 h-3.5" />
                  </button>
                </div>
                <p class="font-mono text-xs truncate pr-12" :class="h.status === 1 ? 'text-indigo-200/80' : 'text-rose-200/80'">{{ h.sql_text }}</p>
                <p v-if="h.error_message" class="text-[11px] text-rose-300/70 truncate mt-0.5">{{ h.error_message }}</p>
              </div>
              <p v-if="!sortedHistory.length && !historyLoading" class="text-center text-xs text-white/35 py-10">暂无查询历史</p>
            </div>
          </el-tab-pane>

          <!-- Redis 增强 -->
          <el-tab-pane :label="viewMode === 'redis' ? 'Redis' : 'Redis'" name="redis" class="flex flex-col min-h-0 flex-1">
            <div v-if="viewMode !== 'redis'" class="flex-1 flex items-center justify-center text-sm text-white/35">
              在左侧选择 Redis 数据源的「服务器概览」或「键空间浏览」
            </div>
            <div v-else class="overflow-auto flex-1 flex flex-col min-h-0" v-loading="redisLoading">
              <div v-if="redisView === 'overview' && redisOverview" class="p-4 grid grid-cols-2 sm:grid-cols-4 gap-3">
                <div v-for="card in redisStatCards" :key="card.label" class="rounded-xl bg-white/5 border border-white/10 p-3">
                  <p class="text-[11px] text-white/45">{{ card.label }}</p>
                  <p class="text-lg font-semibold mt-1 truncate">{{ card.value }}</p>
                </div>
              </div>

              <div v-else-if="redisView === 'keys'" class="flex flex-col min-h-0 flex-1">
                <div class="p-3 border-b border-white/10 flex flex-wrap gap-2 items-center bg-white/[0.02]">
                  <div class="relative flex-1 min-w-[180px]">
                    <Search class="w-3.5 h-3.5 absolute left-2.5 top-1/2 -translate-y-1/2 text-white/30" />
                    <input v-model.trim="redisPattern" class="glass-input !py-1.5 !pl-8 text-xs w-full" placeholder="键匹配，如 user:*  支持 * ?" @keydown.enter="loadRedisKeys" list="redis-pattern-history" />
                    <datalist id="redis-pattern-history">
                      <option v-for="p in redisPatternHistory" :key="p" :value="p" />
                    </datalist>
                  </div>
                  <el-select v-model="redisTypeFilter" size="small" class="w-24" popper-class="glass-popper" @change="loadRedisKeys">
                    <el-option label="全部类型" value="all" />
                    <el-option label="string" value="string" />
                    <el-option label="hash" value="hash" />
                    <el-option label="list" value="list" />
                    <el-option label="set" value="set" />
                    <el-option label="zset" value="zset" />
                  </el-select>
                  <button class="ghost-button !py-1.5 !px-3 text-xs" @click="loadRedisKeys">扫描</button>
                  <button class="ghost-button !py-1.5 !px-3 text-xs flex items-center gap-1" @click="openNewKeyDialog"><Plus class="w-3 h-3" /> 新建</button>
                  <div class="flex items-center gap-1 ml-1">
                    <button class="ghost-button !py-1 !px-2 text-[11px]" :class="redisViewMode==='list' ? 'bg-white/10' : ''" @click="redisViewMode='list'">列表</button>
                    <button class="ghost-button !py-1 !px-2 text-[11px]" :class="redisViewMode==='tree' ? 'bg-white/10' : ''" @click="redisViewMode='tree'">树</button>
                  </div>
                  <span class="text-[11px] text-white/30">{{ redisKeys.length }} / {{ redisKeysTotal }}</span>
                </div>

                <!-- 批量操作条 -->
                <div v-if="redisSelectedKeys.size" class="px-3 py-2 flex items-center gap-2 bg-amber-500/10 border-b border-amber-400/20 text-xs">
                  <span class="text-amber-300">已选 {{ redisSelectedKeys.size }} 个</span>
                  <div class="flex-1" />
                  <button class="ghost-button !py-1 !px-2 text-[11px]" @click="exportSelectedKeys">导出</button>
                  <el-input-number v-model="redisBatchTTL" :min="-1" :max="86400*30" size="small" class="w-24" placeholder="TTL" />
                  <button class="ghost-button !py-1 !px-2 text-[11px]" :disabled="isReadonly" @click="batchUpdateTTL">批量 TTL</button>
                  <button class="ghost-button !py-1 !px-2 text-[11px] text-rose-300/80" :disabled="isReadonly" @click="batchDeleteKeys">批量删除</button>
                  <button class="ghost-button !py-1 !px-2 text-[11px]" @click="redisSelectedKeys.clear()">清空选择</button>
                </div>

                <div v-if="redisViewMode==='tree'" class="flex-1 overflow-auto p-2">
                  <RedisKeyTree :items="redisKeys" :expanded="redisTreeExpanded" @select="onRedisTreeSelect" @toggle="onRedisTreeToggle" />
                </div>
                <div v-else class="flex-1 overflow-auto">
                  <table class="w-full text-xs">
                    <thead class="text-white/45 sticky top-0 bg-[#141428]/90 backdrop-blur z-10">
                      <tr>
                        <th class="w-8 px-2 py-2"><input type="checkbox" :checked="isAllRedisSelected" @change="toggleAllRedis" /></th>
                        <th class="text-left px-3 py-2 font-medium">键</th>
                        <th class="text-left px-3 py-2 font-medium w-20">类型</th>
                        <th class="text-left px-3 py-2 font-medium w-20">TTL</th>
                        <th class="text-right px-3 py-2 font-medium w-16">操作</th>
                      </tr>
                    </thead>
                    <tbody>
                      <tr v-for="k in redisKeys" :key="k.key" class="border-b border-white/5 hover:bg-white/5 group" :class="{ 'bg-amber-500/5': redisSelectedKeys.has(k.key) }">
                        <td class="px-2 py-2"><input type="checkbox" :checked="redisSelectedKeys.has(k.key)" @change="toggleRedisKey(k.key)" /></td>
                        <td class="px-3 py-2 font-mono break-all cursor-pointer hover:text-indigo-300" @click="inspectRedisKey(k.key)">{{ k.key }}</td>
                        <td class="px-3 py-2"><span class="px-1.5 py-0.5 rounded-full text-[10px]" :class="typeBadge(k.type)">{{ k.type }}</span></td>
                        <td class="px-3 py-2 text-white/50">{{ k.ttl === -1 ? '永久' : k.ttl === -2 ? '已过期' : k.ttl + 's' }}</td>
                        <td class="px-3 py-2 text-right">
                          <div class="flex justify-end gap-1 opacity-0 group-hover:opacity-100">
                            <button class="p-1 rounded hover:bg-white/10 text-white/40 hover:text-white" @click="inspectRedisKey(k.key)"><Eye class="w-3 h-3" /></button>
                            <button class="p-1 rounded hover:bg-white/10 text-white/40 hover:text-rose-300" :disabled="isReadonly" @click="deleteRedisKey(k.key)"><Trash2 class="w-3 h-3" /></button>
                          </div>
                        </td>
                      </tr>
                    </tbody>
                  </table>
                </div>
                <p class="text-[11px] text-white/35 px-3 py-2 border-t border-white/5">支持通配：* 任意字符，? 单字符；点击键查看内容；类型筛选与 pattern 历史已启用；批量选择支持批量删除/TTL/导出</p>
              </div>

              <div v-else-if="redisView === 'value' && redisValue" class="p-4 space-y-3 flex-1 overflow-auto">
                <div class="flex flex-wrap items-center gap-2 text-xs">
                  <span class="font-mono text-white/80">{{ redisValue.key }}</span>
                  <span class="px-1.5 py-0.5 rounded-full text-[10px]" :class="typeBadge(redisValue.type)">{{ redisValue.type }}</span>
                  <span class="text-white/45">大小 {{ redisValue.size }}</span>
                  <span class="text-white/45">TTL {{ redisValue.ttl === -1 ? '永久' : redisValue.ttl + ' s' }}</span>
                  <div class="flex-1" />
                  <button class="ghost-button !py-1 !px-2 text-[11px]" @click="redisView = 'keys'">← 返回</button>
                </div>

                <div class="flex flex-wrap gap-2 items-center">
                  <div class="flex items-center gap-2">
                    <span class="text-[11px] text-white/40">TTL</span>
                    <el-input-number v-model="redisTTL" :min="-1" :max="86400*30" size="small" class="w-28" controls-position="right" />
                    <button class="ghost-button !py-1 !px-2 text-[11px]" :disabled="isReadonly" @click="updateTTL">更新</button>
                    <span class="text-[10px] text-white/25">-1 永久</span>
                  </div>
                  <div class="flex-1" />
                  <button class="ghost-button !py-1 !px-2 text-[11px] flex items-center gap-1" @click="copyRedisValue"><Copy class="w-3 h-3" /> 复制</button>
                  <button class="ghost-button !py-1 !px-2 text-[11px] text-rose-300/80 flex items-center gap-1" :disabled="isReadonly" @click="deleteCurrentRedisKey"><Trash2 class="w-3 h-3" /> 删除</button>
                </div>

                <!-- String 编辑 -->
                <div v-if="redisValue.type === 'string'" class="rounded-xl bg-black/30 border border-white/10 overflow-hidden">
                  <div class="flex items-center justify-between px-3 py-2 border-b border-white/10 bg-white/5">
                    <span class="text-[11px] text-white/40">String 值 · {{ prettyIsJSON ? 'JSON' : '文本' }}</span>
                    <div class="flex gap-1">
                      <button class="ghost-button !py-0.5 !px-2 text-[10px]" @click="prettyToggle = !prettyToggle">{{ prettyToggle ? '原始' : '美化' }}</button>
                      <button class="ghost-button !py-0.5 !px-2 text-[10px] text-emerald-300" :disabled="isReadonly" @click="saveRedisString">保存</button>
                    </div>
                  </div>
                  <textarea v-model="redisStringEdit" class="w-full min-h-[120px] bg-transparent p-3 text-xs font-mono outline-none resize-y" placeholder="输入新值" />
                </div>

                <!-- Hash 编辑 -->
                <div v-else-if="redisValue.type === 'hash'" class="rounded-xl bg-black/30 border border-white/10 overflow-hidden">
                  <div class="flex items-center justify-between px-3 py-2 border-b border-white/10 bg-white/5">
                    <span class="text-[11px] text-white/40">Hash 字段 · {{ Object.keys(redisHashEdit).length }} 个</span>
                    <button class="ghost-button !py-0.5 !px-2 text-[10px]" @click="addHashField">+ 字段</button>
                  </div>
                  <div class="p-2 space-y-1 max-h-[40vh] overflow-auto">
                    <div v-for="(_v, field) in redisHashEdit" :key="field" class="flex gap-1 items-center">
                      <input :value="field" class="glass-input !py-1 text-[11px] w-32" disabled />
                      <input v-model="(redisHashEdit as any)[field]" class="glass-input !py-1 text-[11px] flex-1" placeholder="值" />
                      <button class="ghost-button !py-1 !px-1.5 text-[10px]" :disabled="isReadonly" @click="saveHashField(String(field))">保存</button>
                      <button class="ghost-button !py-1 !px-1.5 text-[10px] text-rose-300" :disabled="isReadonly" @click="deleteHashField(String(field))"><Trash2 class="w-3 h-3" /></button>
                    </div>
                    <div v-if="newHashField.show" class="flex gap-1 items-center pt-2 border-t border-white/10">
                      <input v-model="newHashField.field" class="glass-input !py-1 text-[11px] w-32" placeholder="新字段" />
                      <input v-model="newHashField.value" class="glass-input !py-1 text-[11px] flex-1" placeholder="值" />
                      <button class="ghost-button !py-1 !px-2 text-[10px]" @click="confirmAddHashField">确认</button>
                      <button class="ghost-button !py-1 !px-2 text-[10px]" @click="newHashField.show=false">取消</button>
                    </div>
                  </div>
                </div>

                <!-- List 编辑 -->
                <div v-else-if="redisValue.type === 'list'" class="rounded-xl bg-black/30 border border-white/10 overflow-hidden">
                  <div class="flex items-center justify-between px-3 py-2 border-b border-white/10 bg-white/5">
                    <span class="text-[11px] text-white/40">List 元素 · {{ (redisListEdit as any[]).length }} 个</span>
                    <div class="flex gap-1">
                      <button class="ghost-button !py-0.5 !px-2 text-[10px]" @click="redisListPop('left')">LPOP</button>
                      <button class="ghost-button !py-0.5 !px-2 text-[10px]" @click="redisListPop('right')">RPOP</button>
                    </div>
                  </div>
                  <div class="p-2 space-y-1 max-h-[30vh] overflow-auto">
                    <div v-for="(item, idx) in redisListEdit" :key="idx" class="flex gap-2 items-center text-xs">
                      <span class="text-white/30 w-6 text-right">{{ idx }}</span>
                      <span class="flex-1 font-mono truncate bg-white/5 rounded px-2 py-1">{{ item }}</span>
                    </div>
                  </div>
                  <div class="p-2 border-t border-white/10 flex gap-1">
                    <input v-model="newListValue" class="glass-input !py-1 text-[11px] flex-1" placeholder="新元素值" />
                    <button class="ghost-button !py-1 !px-2 text-[11px]" @click="redisListPush('left')">LPUSH</button>
                    <button class="ghost-button !py-1 !px-2 text-[11px]" @click="redisListPush('right')">RPUSH</button>
                  </div>
                </div>

                <!-- Set 编辑 -->
                <div v-else-if="redisValue.type === 'set'" class="rounded-xl bg-black/30 border border-white/10 overflow-hidden">
                  <div class="px-3 py-2 border-b border-white/10 bg-white/5 text-[11px] text-white/40">Set 成员 · {{ (redisSetEdit as any[]).length }} 个</div>
                  <div class="p-2 flex flex-wrap gap-1.5 max-h-[30vh] overflow-auto">
                    <div v-for="m in redisSetEdit" :key="m" class="flex items-center gap-1 px-2 py-1 rounded-full bg-white/10 text-xs">
                      <span class="font-mono">{{ m }}</span>
                      <button class="text-white/30 hover:text-rose-300" @click="removeSetMember(m)"><Trash2 class="w-3 h-3" /></button>
                    </div>
                  </div>
                  <div class="p-2 border-t border-white/10 flex gap-1">
                    <input v-model="newSetMember" class="glass-input !py-1 text-[11px] flex-1" placeholder="新成员" />
                    <button class="ghost-button !py-1 !px-2 text-[11px]" @click="addSetMember">SADD</button>
                  </div>
                </div>

                <!-- ZSet 编辑 -->
                <div v-else-if="redisValue.type === 'zset'" class="rounded-xl bg-black/30 border border-white/10 overflow-hidden">
                  <div class="px-3 py-2 border-b border-white/10 bg-white/5 text-[11px] text-white/40">ZSet 成员 · 按分数排序</div>
                  <div class="p-2 space-y-1 max-h-[35vh] overflow-auto">
                    <div v-for="z in redisZSetEdit" :key="z.member" class="flex gap-2 items-center text-xs">
                      <input v-model.number="z.score" class="glass-input !py-1 text-[11px] w-20" type="number" />
                      <span class="flex-1 font-mono truncate bg-white/5 rounded px-2 py-1">{{ z.member }}</span>
                      <button class="ghost-button !py-1 !px-1.5 text-[10px]" @click="saveZSetMember(z)">保存分数</button>
                      <button class="ghost-button !py-1 !px-1.5 text-[10px] text-rose-300" @click="removeZSetMember(z.member)"><Trash2 class="w-3 h-3" /></button>
                    </div>
                  </div>
                  <div class="p-2 border-t border-white/10 flex gap-1">
                    <input v-model="newZSetMember.member" class="glass-input !py-1 text-[11px] flex-1" placeholder="成员" />
                    <input v-model.number="newZSetMember.score" class="glass-input !py-1 text-[11px] w-24" type="number" placeholder="分数" />
                    <button class="ghost-button !py-1 !px-2 text-[11px]" @click="addZSetMember">ZADD</button>
                  </div>
                </div>

                <!-- 通用预览兜底 -->
                <div v-else class="rounded-xl bg-black/30 border border-white/10 overflow-hidden">
                  <div class="flex items-center justify-between px-3 py-2 border-b border-white/10 bg-white/5">
                    <span class="text-[11px] text-white/40">值预览 · {{ prettyIsJSON ? 'JSON' : redisValue.type }}</span>
                    <button class="ghost-button !py-0.5 !px-2 text-[10px]" @click="prettyToggle = !prettyToggle">{{ prettyToggle ? '原始' : '美化' }}</button>
                  </div>
                  <pre class="text-xs font-mono p-3 overflow-auto max-h-[40vh] whitespace-pre-wrap break-all">{{ displayedRedisValue }}</pre>
                </div>

                <!-- 新建 Key 按钮 -->
                <div class="flex justify-end pt-2">
                  <button class="ghost-button !py-1 !px-3 text-[11px] flex items-center gap-1" @click="openNewKeyDialog"><Plus class="w-3 h-3" /> 新建 Key</button>
                </div>
              </div>
            </div>
          </el-tab-pane>

          <el-tab-pane label="消息" name="message" class="flex flex-col min-h-0 flex-1">
            <div class="p-4 text-sm font-mono space-y-1 overflow-auto flex-1">
              <p v-if="!messages.length" class="text-white/35">编辑器就绪，等待执行… 选中部分 SQL 时仅执行选中段</p>
              <p v-for="(m, i) in messages" :key="i" :class="m.level === 'error' ? 'text-rose-300' : m.level === 'success' ? 'text-emerald-300' : 'text-white/50'">
                [{{ m.time }}] {{ m.text }}
              </p>
            </div>
          </el-tab-pane>
        </el-tabs>

        <footer class="flex items-center gap-4 sm:gap-5 px-4 py-2 border-t border-white/10 text-xs text-white/45 shrink-0">
          <span v-if="lastDuration !== null">执行耗时：<span class="text-emerald-300">{{ lastDuration }} ms</span></span>
          <span v-if="grid.columns.length">行数：<span class="text-indigo-200">{{ grid.rows.length }}</span></span>
          <span v-if="currentConn?.type === 'redis'" class="text-rose-300/80">Redis 模式</span>
          <span v-if="currentConn?.environment === 'prod'" class="text-rose-300/80 flex items-center gap-1"><ShieldAlert class="w-3 h-3" /> 生产环境</span>
          <span class="flex-1" />
          <span v-if="isReadonly" class="text-amber-300/80">只读角色：写操作将被拒绝</span>
          <span class="hidden sm:inline">UTF-8</span>
        </footer>
      </div>
    </section>

    <AiAssistantPanel v-if="aiVisible && !resultFullscreen" class="hidden xl:flex w-72 shrink-0" :current-connection="currentConn" :current-sql="currentTab.sql" :tables="allTableNames" @close="aiVisible = false" @insert-sql="onAiInsert" />
    <button
      v-if="!aiVisible && !resultFullscreen"
      class="hidden xl:flex w-10 shrink-0 glass-panel items-center justify-center text-white/50 hover:text-white transition-colors"
      aria-label="展开 AI 助手"
      @click="aiVisible = true"
    >
      <PanelRightOpen class="w-5 h-5" />
    </button>
    <el-drawer v-model="aiDrawerOpen" title="AI 助手" direction="rtl" size="85%" class="glass-drawer">
      <AiAssistantPanel embedded :closable="false" :current-connection="currentConn" :current-sql="currentTab.sql" :tables="allTableNames" @insert-sql="onAiInsert" />
    </el-drawer>

    <!-- 片段保存 Dialog -->
    <el-dialog v-model="snippetDialogOpen" title="收藏为片段" width="480px" class="glass-dialog" :close-on-click-modal="false">
      <div class="space-y-3">
        <div>
          <p class="text-xs text-white/50 mb-1">片段名称</p>
          <el-input v-model="snippetForm.name" placeholder="例如：近7天订单统计" maxlength="64" />
        </div>
        <div>
          <p class="text-xs text-white/50 mb-1">可见性</p>
          <el-radio-group v-model="snippetForm.visibility" size="small">
            <el-radio-button value="private">仅自己</el-radio-button>
            <el-radio-button value="shared">团队共享</el-radio-button>
          </el-radio-group>
          <p class="text-[11px] text-white/30 mt-1">团队共享将保存到后端，团队成员可在“团队”Tab 中查看</p>
        </div>
        <div>
          <p class="text-xs text-white/50 mb-1">SQL 预览</p>
          <pre class="text-xs font-mono bg-black/30 rounded-xl p-3 max-h-32 overflow-auto whitespace-pre-wrap break-all">{{ snippetForm.sql }}</pre>
        </div>
      </div>
      <template #footer>
        <div class="flex justify-end gap-2">
          <button class="ghost-button" @click="snippetDialogOpen=false">取消</button>
          <button class="liquid-button" @click="saveSnippet">保存</button>
        </div>
      </template>
    </el-dialog>

    <!-- 片段列表 Drawer -->
    <el-drawer v-model="snippetListOpen" title="SQL 片段" direction="rtl" size="400px" class="glass-drawer">
      <div class="flex flex-col h-full min-h-0">
        <div class="flex items-center gap-2 px-3 py-2 border-b border-white/10 shrink-0">
          <el-tabs v-model="snippetTab" class="flex-1" @tab-change="onSnippetTabChange">
            <el-tab-pane label="本地" name="local" />
            <el-tab-pane label="团队" name="team" />
          </el-tabs>
          <button class="ghost-button !py-1 !px-2 text-[11px] flex items-center gap-1" @click="exportSnippets"><Download class="w-3 h-3" /> 导出</button>
          <label class="ghost-button !py-1 !px-2 text-[11px] flex items-center gap-1 cursor-pointer">
            <Upload class="w-3 h-3" /> 导入
            <input type="file" accept=".json" class="hidden" @change="importSnippets" />
          </label>
        </div>

        <div v-if="snippetTab === 'local'" class="flex-1 overflow-auto p-3 space-y-2">
          <div v-if="!snippets.length" class="text-center text-xs text-white/35 py-12 flex flex-col items-center gap-2">
            <Library class="w-8 h-8 text-white/20" />
            暂无本地片段<br/><span class="text-[11px]">在编辑器中编写 SQL 后点击「收藏」</span>
          </div>
          <div v-for="s in snippets" :key="s.id" class="group rounded-xl bg-white/5 border border-white/10 p-3 hover:bg-white/10 transition-colors">
            <div class="flex items-start justify-between gap-2">
              <p class="text-xs font-medium text-white/80 truncate flex-1">{{ s.name }}</p>
              <span class="text-[10px] text-white/30">{{ formatTime(s.created_at) }}</span>
            </div>
            <p class="text-[11px] font-mono text-white/45 truncate mt-1">{{ s.sql.replace(/\s+/g,' ').slice(0,80) }}</p>
            <div class="flex gap-1 mt-2">
              <button class="ghost-button !py-1 !px-2 text-[11px]" @click="insertSnippet(s)">插入</button>
              <button class="ghost-button !py-1 !px-2 text-[11px]" @click="copyText(s.sql)">复制</button>
              <button class="ghost-button !py-1 !px-2 text-[11px] flex items-center gap-1" @click="shareSnippet(s)"><Share2 class="w-3 h-3" /> 分享</button>
              <div class="flex-1" />
              <button class="ghost-button !py-1 !px-2 text-[11px] text-rose-300/60" @click="deleteSnippet(s.id)"><Trash2 class="w-3 h-3" /></button>
            </div>
          </div>
        </div>

        <div v-else class="flex-1 overflow-auto p-3 space-y-2" v-loading="teamSnippetsLoading">
          <div class="flex gap-2 mb-2">
            <div class="relative flex-1">
              <Search class="w-3 h-3 absolute left-2 top-1/2 -translate-y-1/2 text-white/30" />
              <input v-model.trim="teamSnippetKeyword" class="glass-input !py-1 !pl-6 text-[11px] w-full" placeholder="搜索团队片段" @keydown.enter="loadTeamSnippets" />
            </div>
            <button class="ghost-button !py-1 !px-2 text-[11px]" @click="loadTeamSnippets"><RefreshCw class="w-3 h-3" /></button>
          </div>
          <div v-if="!teamSnippets.length && !teamSnippetsLoading" class="text-center text-xs text-white/35 py-12 flex flex-col items-center gap-2">
            <Library class="w-8 h-8 text-white/20" />
            暂无团队片段<br/><span class="text-[11px]">收藏时选“团队共享”即可同步</span>
          </div>
          <div v-for="s in teamSnippets" :key="s.id" class="group rounded-xl bg-white/5 border border-white/10 p-3 hover:bg-white/10 transition-colors">
            <div class="flex items-start justify-between gap-2">
              <p class="text-xs font-medium text-white/80 truncate flex-1 flex items-center gap-1.5">
                {{ s.name }}
                <span class="px-1 py-0.5 rounded text-[9px]" :class="s.visibility === 'shared' ? 'bg-violet-500/20 text-violet-300' : 'bg-white/10 text-white/40'">{{ s.visibility === 'shared' ? '共享' : '私有' }}</span>
              </p>
              <span class="text-[10px] text-white/30">{{ s.owner_name }}</span>
            </div>
            <p class="text-[11px] font-mono text-white/45 truncate mt-1">{{ s.sql_text.replace(/\s+/g,' ').slice(0,80) }}</p>
            <div class="flex gap-1 mt-2">
              <button class="ghost-button !py-1 !px-2 text-[11px]" @click="insertTeamSnippet(s)">插入</button>
              <button class="ghost-button !py-1 !px-2 text-[11px]" @click="copyText(s.sql_text)">复制</button>
              <div class="flex-1" />
              <button class="ghost-button !py-1 !px-2 text-[11px] text-rose-300/60" @click="deleteTeamSnippet(s.id)"><Trash2 class="w-3 h-3" /></button>
            </div>
          </div>
        </div>
      </div>
    </el-drawer>

    <!-- 快捷键面板 -->
    <el-dialog v-model="shortcutsOpen" title="快捷键" width="520px" class="glass-dialog">
      <div class="grid grid-cols-2 gap-3 text-xs">
        <div v-for="k in shortcuts" :key="k.keys" class="flex items-center justify-between p-2.5 rounded-xl bg-white/5 border border-white/10">
          <span class="text-white/60">{{ k.desc }}</span>
          <span class="font-mono text-[11px] px-2 py-0.5 rounded bg-white/10 text-indigo-300">{{ k.keys }}</span>
        </div>
      </div>
      <div class="mt-4 p-3 rounded-xl bg-indigo-500/10 border border-indigo-400/20 text-[11px] text-indigo-200/70">
        <p class="font-medium mb-1">💡 小技巧</p>
        <ul class="list-disc pl-4 space-y-1">
          <li>选中部分 SQL 后运行仅执行选中段，未选中则执行光标所在语句或全部</li>
          <li>历史可 Pin 置顶，Redis 支持 * ? 通配与类型筛选</li>
          <li>生产环境执行写操作会二次确认，保障安全</li>
          <li>编辑器与结果区可拖拽分割，比例自动记忆；结果区支持列显隐/冻结/行选/导出 INSERT/Markdown</li>
          <li>双击行查看详情，右键单元格可复制行 JSON/INSERT</li>
          <li>Redis 支持 String/Hash/List/Set/ZSet 全量编辑，TTL 更新，新建 Key</li>
        </ul>
      </div>
    </el-dialog>

    <!-- 参数面板 -->
    <el-drawer v-model="paramDrawerOpen" title="查询参数" direction="rtl" size="380px" class="glass-drawer">
      <ParamPanel :sql="currentTab.sql" @apply="onParamApply" @update:values="onParamValuesUpdate" />
    </el-drawer>

    <!-- Redis 新建 Key -->
    <el-dialog v-model="newKeyDialogOpen" title="新建 Redis Key" width="480px" class="glass-dialog" :close-on-click-modal="false">
      <div class="space-y-3">
        <div>
          <p class="text-xs text-white/50 mb-1">Key</p>
          <el-input v-model="newKeyForm.key" placeholder="例如：user:1001:profile" />
        </div>
        <div class="flex gap-3">
          <div class="flex-1">
            <p class="text-xs text-white/50 mb-1">类型</p>
            <el-select v-model="newKeyForm.type" class="w-full" popper-class="glass-popper">
              <el-option label="string" value="string" />
              <el-option label="hash" value="hash" />
              <el-option label="list" value="list" />
              <el-option label="set" value="set" />
              <el-option label="zset" value="zset" />
            </el-select>
          </div>
          <div class="w-28">
            <p class="text-xs text-white/50 mb-1">TTL (秒)</p>
            <el-input-number v-model="newKeyForm.ttl" :min="-1" :max="86400*30" size="default" class="w-full" />
          </div>
        </div>
        <div>
          <p class="text-xs text-white/50 mb-1">初始值</p>
          <el-input v-model="newKeyForm.value" type="textarea" :rows="4" :placeholder="newKeyPlaceholder" />
          <p class="text-[11px] text-white/30 mt-1">复杂类型支持 JSON 数组/对象，或单值会自动包装</p>
        </div>
      </div>
      <template #footer>
        <div class="flex justify-end gap-2">
          <button class="ghost-button" @click="newKeyDialogOpen=false">取消</button>
          <button class="liquid-button" @click="createNewKey">创建</button>
        </div>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  Bookmark,
  Braces,
  CheckCircle2,
  Copy,
  Database,
  Download,
  Eye,
  FileCode2,
  FileSearch,
  FolderTree,
  KeyRound,
  Keyboard,
  Library,
  PanelRightOpen,
  Pin,
  Play,
  Plus,
  RefreshCw,
  Search,
  Settings2,
  Share2,
  ShieldAlert,
  Sparkles,
  Square,
  Table2,
  Trash2,
  Upload,
  Wand2,
  Waypoints,
} from 'lucide-vue-next'
import ConnectionTreePanel from './components/ConnectionTreePanel.vue'
import AiAssistantPanel from './components/AiAssistantPanel.vue'
import ResultChartPane from './components/ResultChartPane.vue'
import SqlMonaco from './components/SqlMonaco.vue'
import ResultGrid from './components/ResultGrid.vue'
import ExplainPlan from './components/ExplainPlan.vue'
import RedisKeyTree from './components/RedisKeyTree.vue'
import ColumnStatsPane from './components/ColumnStatsPane.vue'
import ParamPanel from './components/ParamPanel.vue'
import ResultSnapshotPane from './components/ResultSnapshotPane.vue'
import type { ConnectionItem, DbType } from '../../api/datasource'
import {
  workbenchApi,
  type ExecResult,
  type QueryHistoryItem,
  type RedisKey,
  type RedisOverview,
  type RedisValue,
  type TableInfo,
} from '../../api/workbench'
import { useUserStore } from '../../stores/user'

const route = useRoute()
const userStore = useUserStore()
const isReadonly = computed(() => userStore.role === 'readonly')
const treeRef = ref<InstanceType<typeof ConnectionTreePanel> | null>(null)

interface EditorTab {
  id: number
  name: string
  database: string
  sql: string
}
let tabSeq = 1
function newTab(sql = ''): EditorTab {
  tabSeq += 1
  return { id: tabSeq, name: `SQL Editor ${tabSeq}`, database: '', sql }
}

const STORAGE_TABS = 'dbhub_query_tabs_v2'
const STORAGE_SPLIT = 'dbhub_query_split'
const STORAGE_PINNED = 'dbhub_history_pinned'
const STORAGE_REDIS_PATTERNS = 'dbhub_redis_patterns'
const STORAGE_EDITOR_OPTS = 'dbhub_editor_opts'

function loadTabsFromStorage(): { tabs: EditorTab[]; active: number } | null {
  try {
    const raw = localStorage.getItem(STORAGE_TABS)
    if (!raw) return null
    const parsed = JSON.parse(raw)
    if (Array.isArray(parsed.tabs) && parsed.tabs.length) {
      const maxId = Math.max(...parsed.tabs.map((t: any) => t.id), 0)
      tabSeq = Math.max(tabSeq, maxId)
      return parsed
    }
  } catch {}
  return null
}

const stored = loadTabsFromStorage()
const tabs = ref<EditorTab[]>(stored?.tabs ?? [{ id: 1, name: 'SQL Editor 1', database: '', sql: '' }])
const activeTab = ref(stored?.active ?? 1)
const currentTab = computed<EditorTab>(
  () => tabs.value.find((t) => t.id === activeTab.value) ?? tabs.value[0]!,
)

watch(
  [tabs, activeTab],
  () => {
    try {
      localStorage.setItem(STORAGE_TABS, JSON.stringify({ tabs: tabs.value, active: activeTab.value }))
    } catch {}
  },
  { deep: true },
)

const currentConn = ref<ConnectionItem | null>(null)
const databaseOptions = ref<{ name: string }[]>([])
const allTableNames = ref<string[]>([])
const allColumnNames = ref<string[]>([])
const running = ref(false)

const resultTab = ref('result')
const viewMode = ref<'sql' | 'preview' | 'redis'>('sql')
const messages = ref<{ time: string; text: string; level: 'info' | 'success' | 'error' }[]>([])
const lastDuration = ref<number | null>(null)

const grid = reactive<{ columns: string[]; rows: unknown[][]; truncated: boolean }>({
  columns: [],
  rows: [],
  truncated: false,
})
const writeResult = ref<ExecResult | null>(null)
const preview = reactive({
  schema: '',
  database: '',
  table: '',
  page: 1,
  pageSize: 50,
  total: 0,
  hasMore: false,
})

const aiVisible = ref(true)
const treeDrawerOpen = ref(false)
const aiDrawerOpen = ref(false)
const resultFullscreen = ref(false)

const editorHeight = ref<number>(55)
try {
  const v = Number(localStorage.getItem(STORAGE_SPLIT))
  if (v >= 20 && v <= 80) editorHeight.value = v
} catch {}

function startSplitterDrag(e: MouseEvent) {
  const startY = e.clientY
  const startH = editorHeight.value
  const container = (e.currentTarget as HTMLElement).parentElement
  const containerHeight = container?.clientHeight || window.innerHeight * 0.8
  const onMove = (ev: MouseEvent) => {
    const delta = ev.clientY - startY
    const deltaPct = (delta / containerHeight) * 100
    let newH = startH + deltaPct
    newH = Math.max(20, Math.min(80, newH))
    editorHeight.value = newH
  }
  const onUp = () => {
    window.removeEventListener('mousemove', onMove)
    window.removeEventListener('mouseup', onUp)
    try {
      localStorage.setItem(STORAGE_SPLIT, String(editorHeight.value))
    } catch {}
  }
  window.addEventListener('mousemove', onMove)
  window.addEventListener('mouseup', onUp)
}

const monacoReady = ref(false)
const monacoRefs = reactive<Record<number, any>>({})

// 编辑器选项
const minimapEnabled = ref(false)
const autoLimitEnabled = ref(true)
const editorStats = reactive({ chars: 0, lines: 1, selected: 0 })
try {
  const raw = localStorage.getItem(STORAGE_EDITOR_OPTS)
  if (raw) {
    const parsed = JSON.parse(raw)
    minimapEnabled.value = !!parsed.minimap
    autoLimitEnabled.value = parsed.autoLimit !== false
  }
} catch {}
watch([minimapEnabled, autoLimitEnabled], () => {
  try {
    localStorage.setItem(STORAGE_EDITOR_OPTS, JSON.stringify({ minimap: minimapEnabled.value, autoLimit: autoLimitEnabled.value }))
  } catch {}
})

function updateEditorStats() {
  const sql = currentTab.value.sql || ''
  editorStats.chars = sql.length
  editorStats.lines = sql ? sql.split('\n').length : 1
}
watch(() => currentTab.value.sql, updateEditorStats, { immediate: true })
function onSelectionChange(len: number) {
  editorStats.selected = len
}
function onEditorOptionCommand(cmd: string) {
  if (cmd === 'toggle-minimap') {
    minimapEnabled.value = !minimapEnabled.value
    ElMessage.success(minimapEnabled.value ? '已开启小地图' : '已关闭小地图')
  } else if (cmd === 'toggle-autolimit') {
    autoLimitEnabled.value = !autoLimitEnabled.value
    ElMessage.success(autoLimitEnabled.value ? '已开启自动 LIMIT 1000' : '已关闭自动 LIMIT')
  } else if (cmd === 'add-limit') {
    const sql = currentTab.value.sql
    if (!sql.toUpperCase().includes('LIMIT')) {
      const monaco = monacoRefs[currentTab.value.id]
      if (monaco) monaco.insertText(' LIMIT 100')
      else currentTab.value.sql = sql + ' LIMIT 100'
    } else {
      ElMessage.info('已包含 LIMIT')
    }
  }
}

onMounted(() => {
  import('monaco-editor')
    .then(() => {
      monacoReady.value = true
    })
    .catch(() => {
      monacoReady.value = false
    })
})

function connColor(t: DbType) {
  return { mysql: '#60a5fa', postgres: '#a78bfa', redis: '#f472b6' }[t] ?? '#94a3b8'
}

function log(text: string, level: 'info' | 'success' | 'error' = 'info') {
  const time = new Date().toLocaleTimeString('zh-CN', { hour12: false })
  messages.value.unshift({ time, text, level })
}

function resetGrid() {
  grid.columns = []
  grid.rows = []
  grid.truncated = false
  writeResult.value = null
}

function onSelectConnection(conn: ConnectionItem) {
  currentConn.value = conn
  viewMode.value = conn.type === 'redis' ? 'redis' : 'sql'
  if (conn.type === 'redis') {
    resultTab.value = 'redis'
    redisView.value = 'overview'
    loadRedisOverview(conn)
    return
  }
  resultTab.value = 'result'
  databaseOptions.value = []
  allTableNames.value = []
  loadTransactionStatus()
}

function onDatabasesLoaded({ conn, items }: { conn: ConnectionItem; items: { name: string }[] }) {
  if (currentConn.value?.id !== conn.id) return
  databaseOptions.value = items
  const preferred = items.find((d) => d.name === conn.database) ?? items[0]
  if (preferred && !currentTab.value.database) {
    currentTab.value.database = preferred.name
  }
}

function onTablesLoaded({ tables }: { tables: TableInfo[] }) {
  allTableNames.value = tables.map((t) => t.name)
}

async function formatCurrent() {
  const tab = currentTab.value
  if (!tab.sql.trim()) return
  try {
    const { format } = await import('sql-formatter')
    const formatted = format(tab.sql, { language: 'postgresql', tabWidth: 2, keywordCase: 'upper' })
    tab.sql = formatted
    ElMessage.success({ message: '已格式化', duration: 1000 })
  } catch {
    ElMessage.warning('格式化失败')
  }
}

function isDangerousSQL(sql: string) {
  const up = sql.toUpperCase()
  return /\b(DELETE|UPDATE|DROP|TRUNCATE|ALTER)\b/.test(up)
}

function getExecutionSQL(): string {
  // 优先使用 Monaco 选中文本
  const monaco = monacoRefs[currentTab.value.id]
  if (monaco) {
    try {
      const sel = monaco.getSelection?.()
      if (sel && sel.trim().length) return sel.trim()
    } catch {}
  }
  return currentTab.value.sql.trim()
}

function applyAutoLimit(sql: string): string {
  if (!autoLimitEnabled.value) return sql
  const up = sql.toUpperCase()
  // 仅对单条 SELECT 且无 LIMIT 的语句自动加 LIMIT 1000
  if (!up.startsWith('SELECT')) return sql
  if (up.includes('LIMIT')) return sql
  // 如果包含多条语句（分号），不自动加
  if ((sql.match(/;/g) || []).length > 1) return sql
  return `${sql.replace(/;?\s*$/, '')} LIMIT 1000`
}

async function runQuery() {
  if (!currentConn.value) {
    ElMessage.warning('请先在左侧选择数据源')
    return
  }
  if (currentConn.value.type === 'redis') {
    ElMessage.info('Redis 不支持 SQL，请使用 Redis 浏览页签')
    return
  }
  let sql = getExecutionSQL()
  if (!sql) {
    ElMessage.warning('SQL 内容不能为空')
    return
  }
  // 参数化检测：如果有 {{}} 或 :param 且未填充，提示打开参数面板
  if (paramDetected.value > 0) {
    const hasUnfilled = Object.keys(paramValues.value).length < paramDetected.value
    // 简单检查：若 sql 中仍包含 {{ 或 :param 且对应值为空，则打开参数面板
    const missing = (sql.match(/\{\{\s*([a-zA-Z_][a-zA-Z0-9_]*)\s*\}\}/g) || []).some((raw: string) => {
      const name = raw.replace(/\{\{|\}\}|\s/g, '')
      return !paramValues.value[name]
    })
    if (missing && hasUnfilled) {
      ElMessage.info(`检测到 ${paramDetected.value} 个参数，请先填写参数`)
      paramDrawerOpen.value = true
      return
    }
  }
  const originalSQL = sql
  sql = applyAutoLimit(sql)
  const autoLimited = sql !== originalSQL
  if (currentConn.value.environment === 'prod' && isDangerousSQL(sql)) {
    try {
      await ElMessageBox.confirm(
        `当前连接为生产环境（${currentConn.value.name}），即将执行写操作：\n${sql.slice(0, 200)}\n\n确认继续？`,
        '生产环境二次确认',
        { type: 'warning', confirmButtonText: '确认执行', cancelButtonText: '取消' },
      )
    } catch {
      return
    }
  }
  running.value = true
  abortController.value = new AbortController()
  resetGrid()
  viewMode.value = 'sql'
  resultTab.value = 'result'
  log(`${autoLimited ? '[自动 LIMIT 1000] ' : ''}开始执行：${sql.replace(/\s+/g, ' ').slice(0, 80)}`)
  try {
    const res = await workbenchApi.execute(currentConn.value.id, sql, currentTab.value.database, abortController.value.signal)
    lastDuration.value = res.duration_ms
    if (res.kind === 'query') {
      grid.columns = res.columns ?? []
      grid.rows = res.rows ?? []
      grid.truncated = Boolean(res.truncated)
      log(`查询成功，返回 ${grid.rows.length} 行，耗时 ${res.duration_ms} ms${autoLimited ? '（已自动 LIMIT）' : ''}`, 'success')
    } else {
      writeResult.value = res
      log(`执行成功，影响 ${res.affected_rows ?? 0} 行，耗时 ${res.duration_ms} ms`, 'success')
    }
  } catch (err: any) {
    if (err?.name === 'CanceledError' || err?.code === 'ERR_CANCELED' || err?.message?.includes('canceled')) {
      log('查询已取消', 'info')
    } else {
      log(err instanceof Error ? err.message : '执行失败', 'error')
      resultTab.value = 'message'
    }
  } finally {
    if (transactionActive.value) transactionQueries.value++
    running.value = false
    abortController.value = null
    if (resultTab.value === 'history') loadHistory()
  }
}

async function onPreviewTable(payload: {
  conn: ConnectionItem
  database: string
  schema: string
  table: TableInfo
}) {
  currentConn.value = payload.conn
  viewMode.value = 'preview'
  resultTab.value = 'result'
  Object.assign(preview, {
    schema: payload.schema || payload.database,
    database: payload.database,
    table: payload.table.name,
    page: 1,
  })
  currentTab.value.database = payload.database
  if (!databaseOptions.value.find((d) => d.name === payload.database)) {
    databaseOptions.value = [{ name: payload.database }, ...databaseOptions.value]
  }
  await loadPreview()
}

async function loadPreview() {
  if (!currentConn.value) return
  resetGrid()
  const isPg = currentConn.value.type === 'postgres'
  try {
    const res = await workbenchApi.preview(currentConn.value.id, {
      database: isPg ? preview.database : preview.database,
      schema: isPg ? preview.schema || undefined : undefined,
      table: preview.table,
      page: preview.page,
      page_size: preview.pageSize,
    })
    grid.columns = res.columns
    grid.rows = res.rows
    preview.total = res.total
    preview.hasMore = res.has_more
    try {
      const cols = await workbenchApi.columns(currentConn.value.id, {
        database: preview.database,
        schema: preview.schema || undefined,
        table: preview.table,
      })
      allColumnNames.value = cols.items.map((c) => c.name)
    } catch {}
  } catch {}
}

function previewPage(delta: number) {
  preview.page = Math.max(1, preview.page + delta)
  loadPreview()
}

// ---------- 历史增强 ----------
const history = ref<QueryHistoryItem[]>([])
const historyLoading = ref(false)
const historyFilter = ref('all')
const historyKeyword = ref('')
const historyConnFilter = ref<number | ''>('')
const historyTotal = ref(0)
const pinnedIds = ref<Set<number>>(new Set())

try {
  const raw = localStorage.getItem(STORAGE_PINNED)
  if (raw) pinnedIds.value = new Set(JSON.parse(raw))
} catch {}

function isPinned(id: number) {
  return pinnedIds.value.has(id)
}
function togglePin(id: number) {
  if (pinnedIds.value.has(id)) pinnedIds.value.delete(id)
  else pinnedIds.value.add(id)
  try {
    localStorage.setItem(STORAGE_PINNED, JSON.stringify([...pinnedIds.value]))
  } catch {}
}

const allConnections = computed(() => {
  return (treeRef.value as any)?.connectionsRef?.value || []
})

const sortedHistory = computed(() => {
  const kw = historyKeyword.value.toLowerCase()
  let list = [...history.value]
  if (kw) {
    list = list.filter((h) => h.sql_text.toLowerCase().includes(kw) || (h.database_name || '').toLowerCase().includes(kw) || (h.connection_name || '').toLowerCase().includes(kw))
  }
  return list.sort((a, b) => {
    const pa = isPinned(a.id) ? 0 : 1
    const pb = isPinned(b.id) ? 0 : 1
    if (pa !== pb) return pa - pb
    return new Date(b.created_at).getTime() - new Date(a.created_at).getTime()
  })
})

function onResultTabChange(name: string | number) {
  if (name === 'history') loadHistory()
}

async function loadHistory() {
  historyLoading.value = true
  try {
    const res = await workbenchApi.history({
      status: historyFilter.value === 'all' ? '' : historyFilter.value,
      connection_id: historyConnFilter.value ? Number(historyConnFilter.value) : undefined,
      keyword: historyKeyword.value || undefined,
      page: 1,
      page_size: 100,
    })
    history.value = res.items
    historyTotal.value = res.total
  } finally {
    historyLoading.value = false
  }
}
function reuseHistory(h: QueryHistoryItem) {
  const tab = tabs.value.find((t) => t.id === activeTab.value) ?? tabs.value[0]!
  tab.sql = h.sql_text
  if (h.database_name) tab.database = h.database_name
  resultTab.value = 'result'
  ElMessage.success('SQL 已回填到编辑器')
}
async function removeHistory(id: number) {
  await workbenchApi.deleteHistory(id)
  loadHistory()
}
async function clearHistory() {
  try {
    await ElMessageBox.confirm('确认清空你的全部查询历史？', '清空确认', {
      type: 'warning',
      confirmButtonText: '清空',
      cancelButtonText: '取消',
    })
  } catch {
    return
  }
  await workbenchApi.clearHistory()
  ElMessage.success('已清空')
  loadHistory()
}

// ---------- Redis 增强 ----------
const redisLoading = ref(false)
const redisView = ref<'overview' | 'keys' | 'value'>('overview')
const redisOverview = ref<RedisOverview | null>(null)
const redisKeys = ref<RedisKey[]>([])
const redisKeysTotal = ref(0)
const redisValue = ref<RedisValue | null>(null)
const redisPattern = ref('*')
const redisTypeFilter = ref('all')
const redisTTL = ref<number>(-1)
const redisPatternHistory = ref<string[]>([])
const prettyToggle = ref(true)

const redisStringEdit = ref('')
const redisHashEdit = reactive<Record<string, string>>({})
const newHashField = reactive({ show: false, field: '', value: '' })
const redisListEdit = ref<string[]>([])
const newListValue = ref('')
const redisSetEdit = ref<string[]>([])
const newSetMember = ref('')
const redisZSetEdit = ref<{ member: string; score: number }[]>([])
const newZSetMember = reactive({ member: '', score: 0 })
const newKeyDialogOpen = ref(false)
const newKeyForm = reactive({ key: '', type: 'string', value: '', ttl: -1 })
const newKeyPlaceholder = computed(() => {
  switch (newKeyForm.type) {
    case 'string': return '字符串值'
    case 'hash': return '{"field":"value"}'
    case 'list': return '["a","b"]'
    case 'set': return '["a","b"]'
    case 'zset': return '[{"member":"a","score":1}]'
    default: return ''
  }
})
const redisSelectedKeys = reactive(new Set<string>())
const redisBatchTTL = ref<number>(-1)
const isAllRedisSelected = computed(() => redisKeys.value.length > 0 && redisSelectedKeys.size === redisKeys.value.length)
const redisViewMode = ref<'list' | 'tree'>('list')
const redisTreeExpanded = reactive(new Set<string>())
const transactionActive = ref(false)
const transactionId = ref('')
const transactionStartedAt = ref('')
const transactionQueries = ref(0)
const paramDrawerOpen = ref(false)
const paramValues = ref<Record<string, string>>({})
const paramDetected = computed(() => {
  const sql = currentTab.value.sql || ''
  const count = (sql.match(/\{\{\s*[a-zA-Z_][a-zA-Z0-9_]*\s*\}\}/g) || []).length + (sql.match(/(?<!:):[a-zA-Z_][a-zA-Z0-9_]*\b/g) || []).length + (sql.match(/\$\d+\b/g) || []).length
  return count
})
const isExplainResult = computed(() => {
  const cols = grid.columns.map(c => c.toLowerCase())
  return cols.includes('query plan') || cols.includes('select_type') || (grid.columns.length === 1 && (grid.columns[0] || '').toLowerCase().includes('plan'))
})

const abortController = ref<AbortController | null>(null)

// ---------- Step4: EXPLAIN / Snippet / Shortcuts ----------
const STORAGE_SNIPPETS = 'dbhub_sql_snippets'
interface Snippet {
  id: number
  name: string
  sql: string
  database: string
  connection_id?: number
  created_at: string
}
const snippets = ref<Snippet[]>([])
const snippetDialogOpen = ref(false)
const snippetListOpen = ref(false)
const snippetForm = reactive({ name: '', sql: '', visibility: 'private' as 'private' | 'shared' })
const shortcutsOpen = ref(false)

const shortcuts = [
  { keys: '⌘ + Enter', desc: '运行查询（选中仅执行选中）' },
  { keys: '⇧ + ⌘ + F', desc: '格式化 SQL' },
  { keys: '⌘ + /', desc: '注释/取消注释' },
  { keys: 'Ctrl + Space', desc: '触发补全' },
  { keys: '?', desc: '打开快捷键面板' },
  { keys: '拖拽分割线', desc: '调整编辑器/结果比例' },
  { keys: '单击单元格', desc: '复制单元格' },
  { keys: '双击行', desc: '打开行详情' },
  { keys: '右键单元格', desc: '复制行 JSON/INSERT/列名' },
  { keys: '↑↓', desc: '结果区键盘导航' },
  { keys: '拖拽表名', desc: '拖拽表到编辑器插入' },
]

function loadSnippets() {
  try {
    const raw = localStorage.getItem(STORAGE_SNIPPETS)
    if (raw) snippets.value = JSON.parse(raw)
  } catch {}
}
loadSnippets()

const snippetTab = ref<'local' | 'team'>('local')
const teamSnippets = ref<any[]>([])
const teamSnippetsLoading = ref(false)
const teamSnippetKeyword = ref('')

async function loadTeamSnippets() {
  teamSnippetsLoading.value = true
  try {
    const { snippetApi } = await import('../../api/snippet')
    const res = await snippetApi.list({ q: teamSnippetKeyword.value || undefined, page: 1, page_size: 50 })
    teamSnippets.value = res.items
  } catch {
    teamSnippets.value = []
  } finally {
    teamSnippetsLoading.value = false
  }
}
function onSnippetTabChange(tab: string | number) {
  if (tab === 'team') loadTeamSnippets()
}
function insertTeamSnippet(s: any) {
  const tab = currentTab.value
  const monaco = monacoRefs[tab.id]
  if (monaco) monaco.insertText(s.sql_text)
  else tab.sql = tab.sql ? `${tab.sql}\n${s.sql_text}` : s.sql_text
  if (s.database_name) tab.database = s.database_name
  snippetListOpen.value = false
  ElMessage.success(`已插入：${s.name}`)
}
async function deleteTeamSnippet(id: number) {
  try {
    await ElMessageBox.confirm('确认删除该团队片段？', '删除确认', { type: 'warning' })
  } catch { return }
  try {
    const { snippetApi } = await import('../../api/snippet')
    await snippetApi.remove(id)
    ElMessage.success('已删除')
    loadTeamSnippets()
  } catch {}
}

function openSnippetDialog() {
  const sql = getExecutionSQL()
  if (!sql) {
    ElMessage.warning('当前编辑器无 SQL')
    return
  }
  snippetForm.name = `片段 ${new Date().toLocaleDateString()}`
  snippetForm.sql = sql
  snippetForm.visibility = 'private'
  snippetDialogOpen.value = true
}
async function saveSnippet() {
  if (!snippetForm.name.trim() || !snippetForm.sql.trim()) {
    ElMessage.warning('名称与 SQL 必填')
    return
  }
  const s: Snippet = {
    id: Date.now(),
    name: snippetForm.name.trim(),
    sql: snippetForm.sql.trim(),
    database: currentTab.value.database,
    connection_id: currentConn.value?.id,
    created_at: new Date().toISOString(),
  }
  snippets.value.unshift(s)
  try {
    localStorage.setItem(STORAGE_SNIPPETS, JSON.stringify(snippets.value.slice(0, 100)))
  } catch {}
  try {
    const { snippetApi } = await import('../../api/snippet')
    await snippetApi.create({
      name: snippetForm.name.trim(),
      sql_text: snippetForm.sql.trim(),
      database_name: currentTab.value.database || undefined,
      connection_id: currentConn.value?.id,
      visibility: snippetForm.visibility,
    })
    if (snippetForm.visibility === 'shared') {
      ElMessage.success('已收藏并共享到团队')
      loadTeamSnippets()
    } else {
      ElMessage.success('已收藏（私有）')
    }
  } catch {
    ElMessage.success('已收藏到本地')
  }
  snippetDialogOpen.value = false
}
function insertSnippet(s: Snippet) {
  const tab = currentTab.value
  const monaco = monacoRefs[tab.id]
  if (monaco) monaco.insertText(s.sql)
  else tab.sql = tab.sql ? `${tab.sql}\n${s.sql}` : s.sql
  if (s.database) tab.database = s.database
  snippetListOpen.value = false
  ElMessage.success(`已插入：${s.name}`)
}
function deleteSnippet(id: number) {
  snippets.value = snippets.value.filter((x) => x.id !== id)
  try {
    localStorage.setItem(STORAGE_SNIPPETS, JSON.stringify(snippets.value))
  } catch {}
}
function exportSnippets() {
  if (!snippets.value.length) {
    ElMessage.warning('无片段可导出')
    return
  }
  const blob = new Blob([JSON.stringify(snippets.value, null, 2)], { type: 'application/json' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = `dbhub_snippets_${new Date().toISOString().slice(0,10)}.json`
  a.click()
  URL.revokeObjectURL(url)
}
function importSnippets(e: Event) {
  const input = e.target as HTMLInputElement
  const file = input.files?.[0]
  if (!file) return
  const reader = new FileReader()
  reader.onload = () => {
    try {
      const parsed = JSON.parse(reader.result as string)
      if (Array.isArray(parsed)) {
        const valid = parsed.filter((x: any) => x.name && x.sql)
        snippets.value = [...valid.map((x: any) => ({ ...x, id: x.id || Date.now() + Math.random(), created_at: x.created_at || new Date().toISOString() })), ...snippets.value].slice(0, 100)
        localStorage.setItem(STORAGE_SNIPPETS, JSON.stringify(snippets.value))
        ElMessage.success(`已导入 ${valid.length} 条`)
      }
    } catch {
      ElMessage.error('导入失败，JSON 格式错误')
    }
  }
  reader.readAsText(file)
  input.value = ''
}
function shareSnippet(s: Snippet) {
  const text = `-- ${s.name}\n${s.sql}`
  navigator.clipboard.writeText(text).then(() => {
    ElMessage.success('片段已复制，可分享给同事')
  })
}
function copyText(t: string) {
  navigator.clipboard.writeText(t).then(() => ElMessage.success('已复制'))
}

async function explainQuery() {
  if (!currentConn.value) {
    ElMessage.warning('请先选择数据源')
    return
  }
  const sql = getExecutionSQL()
  if (!sql) {
    ElMessage.warning('SQL 为空')
    return
  }
  if (currentConn.value.type === 'redis') {
    ElMessage.info('Redis 不支持 EXPLAIN')
    return
  }
  running.value = true
  abortController.value = new AbortController()
  resetGrid()
  viewMode.value = 'sql'
  resultTab.value = 'result'
  log(`EXPLAIN: ${sql.replace(/\s+/g, ' ').slice(0, 80)}`)
  try {
    const res = await workbenchApi.execute(currentConn.value.id, `EXPLAIN ${sql}`, currentTab.value.database, abortController.value.signal)
    lastDuration.value = res.duration_ms
    grid.columns = res.columns ?? []
    grid.rows = res.rows ?? []
    grid.truncated = false
    log(`EXPLAIN 完成，耗时 ${res.duration_ms} ms`, 'success')
  } catch (err: any) {
    if (err?.name === 'CanceledError' || err?.code === 'ERR_CANCELED') {
      log('EXPLAIN 已取消', 'info')
    } else {
      log(err instanceof Error ? err.message : 'EXPLAIN 失败', 'error')
      resultTab.value = 'message'
    }
  } finally {
    running.value = false
    abortController.value = null
  }
}

function cancelQuery() {
  if (abortController.value) {
    abortController.value.abort()
    ElMessage.info('已取消执行')
  }
  running.value = false
}

function onKeydown(e: KeyboardEvent) {
  if (e.key === '?' && !e.ctrlKey && !e.metaKey && !e.altKey) {
    const target = e.target as HTMLElement
    if (target && (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.isContentEditable)) return
    shortcutsOpen.value = true
  }
}
onMounted(() => {
  window.addEventListener('keydown', onKeydown)
})

try {
  const raw = localStorage.getItem(STORAGE_REDIS_PATTERNS)
  if (raw) redisPatternHistory.value = JSON.parse(raw)
} catch {}

function typeBadge(t: string) {
  const map: Record<string, string> = {
    string: 'bg-emerald-400/15 text-emerald-300',
    hash: 'bg-indigo-400/15 text-indigo-300',
    list: 'bg-amber-400/15 text-amber-300',
    set: 'bg-violet-400/15 text-violet-300',
    zset: 'bg-rose-400/15 text-rose-300',
  }
  return map[t] || 'bg-white/10 text-white/50'
}

const redisStatCards = computed(() => {
  const o = redisOverview.value
  if (!o) return []
  return [
    { label: '版本', value: o.version || '-' },
    { label: '运行模式', value: o.mode || '-' },
    { label: '运行天数', value: o.uptime_days },
    { label: '连接客户端', value: o.connected_clients },
    { label: '内存占用 (MB)', value: o.used_memory_mb },
    { label: '累计命令数', value: o.total_commands },
    ...o.keyspaces.map((k) => ({ label: `${k.db} 键数量`, value: k.keys })),
  ]
})

async function onRedisOverview(conn: ConnectionItem) {
  currentConn.value = conn
  viewMode.value = 'redis'
  redisView.value = 'overview'
  resultTab.value = 'redis'
  await loadRedisOverview(conn)
}
async function loadRedisOverview(conn: ConnectionItem) {
  redisLoading.value = true
  try {
    redisOverview.value = await workbenchApi.redisOverview(conn.id)
  } finally {
    redisLoading.value = false
  }
}
async function onRedisKeys(conn: ConnectionItem) {
  currentConn.value = conn
  viewMode.value = 'redis'
  redisView.value = 'keys'
  resultTab.value = 'redis'
  await loadRedisKeys()
}
async function loadRedisKeys() {
  if (!currentConn.value) return
  redisLoading.value = true
  try {
    const res = await workbenchApi.redisKeys(currentConn.value.id, redisPattern.value || '*', 200, redisTypeFilter.value)
    redisKeys.value = res.items
    redisKeysTotal.value = (res as any).total ?? res.items.length
    const pat = redisPattern.value.trim()
    if (pat && pat !== '*' && !redisPatternHistory.value.includes(pat)) {
      redisPatternHistory.value = [pat, ...redisPatternHistory.value].slice(0, 10)
      try {
        localStorage.setItem(STORAGE_REDIS_PATTERNS, JSON.stringify(redisPatternHistory.value))
      } catch {}
    }
  } finally {
    redisLoading.value = false
  }
}
async function inspectRedisKey(key: string) {
  if (!currentConn.value) return
  redisLoading.value = true
  try {
    redisValue.value = await workbenchApi.redisValue(currentConn.value.id, key)
    redisTTL.value = redisValue.value?.ttl ?? -1
    const v = redisValue.value?.value
    const t = redisValue.value?.type
    if (t === 'string') {
      redisStringEdit.value = typeof v === 'string' ? v : JSON.stringify(v, null, 2)
    } else if (t === 'hash' && v && typeof v === 'object') {
      Object.keys(redisHashEdit).forEach(k => delete (redisHashEdit as any)[k])
      Object.entries(v as any).forEach(([k, val]) => { (redisHashEdit as any)[k] = String(val) })
    } else if (t === 'list' && Array.isArray(v)) {
      redisListEdit.value = [...(v as string[])]
    } else if (t === 'set' && Array.isArray(v)) {
      redisSetEdit.value = [...(v as string[])]
    } else if (t === 'zset' && Array.isArray(v)) {
      redisZSetEdit.value = (v as any[]).map((it: any) => ({ member: it.member, score: it.score }))
    }
    redisView.value = 'value'
  } finally {
    redisLoading.value = false
  }
}
const prettyIsJSON = computed(() => {
  const v = redisValue.value?.value
  if (typeof v === 'string') {
    try {
      JSON.parse(v)
      return true
    } catch {
      return false
    }
  }
  return typeof v === 'object'
})
const displayedRedisValue = computed(() => {
  const v = redisValue.value?.value
  if (v === null || v === undefined) return '(nil)'
  if (!prettyToggle.value) {
    return typeof v === 'string' ? v : JSON.stringify(v)
  }
  if (typeof v === 'string') {
    try {
      return JSON.stringify(JSON.parse(v), null, 2)
    } catch {
      return v
    }
  }
  try {
    return JSON.stringify(v, null, 2)
  } catch {
    return String(v)
  }
})
async function copyRedisValue() {
  try {
    await navigator.clipboard.writeText(displayedRedisValue.value)
    ElMessage.success('已复制')
  } catch {
    ElMessage.warning('复制失败')
  }
}
async function deleteRedisKey(key: string) {
  if (!key) return
  if (isReadonly.value) {
    ElMessage.warning('只读角色不可删除')
    return
  }
  try {
    await ElMessageBox.confirm(`确认删除键 "${key}"？此操作不可恢复`, '删除确认', { type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消' })
  } catch {
    return
  }
  if (!currentConn.value) return
  try {
    await workbenchApi.redisDeleteKey(currentConn.value.id, key)
    ElMessage.success('已删除')
    if ((redisValue.value as any)?.key === key) redisView.value = 'keys'
    loadRedisKeys()
  } catch {
    ElMessage.error('删除失败')
  }
}
function deleteCurrentRedisKey() {
  const k = (redisValue.value as any)?.key || ''
  if (k) deleteRedisKey(k)
}
async function updateTTL() {
  if (!currentConn.value || !redisValue.value) return
  if (isReadonly.value) {
    ElMessage.warning('只读角色不可修改')
    return
  }
  const k = (redisValue.value as any).key || ''
  if (!k) return
  try {
    await workbenchApi.redisUpdateTTL(currentConn.value.id, k, redisTTL.value)
    ElMessage.success('TTL 已更新')
    if (redisValue.value) redisValue.value.ttl = redisTTL.value
  } catch {
    ElMessage.error('更新失败')
  }
}

async function saveRedisString() {
  if (!currentConn.value || !redisValue.value) return
  if (isReadonly.value) { ElMessage.warning('只读角色不可修改'); return }
  const k = (redisValue.value as any).key || ''
  try {
    await workbenchApi.redisUpdateValue(currentConn.value.id, k, redisStringEdit.value)
    ElMessage.success('已保存')
    redisValue.value.value = redisStringEdit.value
  } catch { ElMessage.error('保存失败') }
}
function addHashField() { newHashField.show = true; newHashField.field = ''; newHashField.value = '' }
async function confirmAddHashField() {
  if (!newHashField.field.trim()) { ElMessage.warning('字段名必填'); return }
  await saveHashField(newHashField.field.trim(), newHashField.value)
  newHashField.show = false
}
async function saveHashField(field: string, val?: string) {
  if (!currentConn.value || !redisValue.value) return
  if (isReadonly.value) { ElMessage.warning('只读角色不可修改'); return }
  const k = (redisValue.value as any).key || ''
  const v = val ?? (redisHashEdit as any)[field]
  try {
    await workbenchApi.redisHashSet(currentConn.value.id, k, field, v)
    ElMessage.success(`字段 ${field} 已保存`)
    ;(redisHashEdit as any)[field] = v
    if (redisValue.value?.value && typeof redisValue.value.value === 'object') {
      ;(redisValue.value.value as any)[field] = v
    }
  } catch { ElMessage.error('保存失败') }
}
async function deleteHashField(field: string) {
  if (!currentConn.value || !redisValue.value) return
  if (isReadonly.value) { ElMessage.warning('只读角色不可删除'); return }
  const k = (redisValue.value as any).key || ''
  try {
    await workbenchApi.redisHashDel(currentConn.value.id, k, field)
    ElMessage.success(`字段 ${field} 已删除`)
    delete (redisHashEdit as any)[field]
    if (redisValue.value?.value && typeof redisValue.value.value === 'object') {
      delete (redisValue.value.value as any)[field]
    }
  } catch { ElMessage.error('删除失败') }
}
async function redisListPush(dir: 'left' | 'right') {
  if (!currentConn.value || !redisValue.value) return
  if (!newListValue.value) { ElMessage.warning('值不能为空'); return }
  const k = (redisValue.value as any).key || ''
  try {
    await workbenchApi.redisListPush(currentConn.value.id, k, newListValue.value, dir)
    ElMessage.success(`${dir === 'left' ? 'LPUSH' : 'RPUSH'} 成功`)
    if (dir === 'left') redisListEdit.value.unshift(newListValue.value)
    else redisListEdit.value.push(newListValue.value)
    newListValue.value = ''
  } catch { ElMessage.error('操作失败') }
}
async function redisListPop(dir: 'left' | 'right') {
  if (!currentConn.value || !redisValue.value) return
  const k = (redisValue.value as any).key || ''
  try {
    const res = await workbenchApi.redisListPop(currentConn.value.id, k, dir) as any
    if (res?.value !== null && res?.value !== undefined) {
      ElMessage.success(`弹出：${res.value}`)
      if (dir === 'left') redisListEdit.value.shift()
      else redisListEdit.value.pop()
    } else {
      ElMessage.info('列表为空')
    }
  } catch { ElMessage.error('操作失败') }
}
async function addSetMember() {
  if (!currentConn.value || !redisValue.value) return
  if (!newSetMember.value) { ElMessage.warning('成员不能为空'); return }
  const k = (redisValue.value as any).key || ''
  try {
    await workbenchApi.redisSetAdd(currentConn.value.id, k, newSetMember.value)
    ElMessage.success('已添加')
    if (!redisSetEdit.value.includes(newSetMember.value)) redisSetEdit.value.push(newSetMember.value)
    newSetMember.value = ''
  } catch { ElMessage.error('添加失败') }
}
async function removeSetMember(member: string) {
  if (!currentConn.value || !redisValue.value) return
  const k = (redisValue.value as any).key || ''
  try {
    await workbenchApi.redisSetRemove(currentConn.value.id, k, member)
    ElMessage.success('已移除')
    redisSetEdit.value = redisSetEdit.value.filter(m => m !== member)
  } catch { ElMessage.error('移除失败') }
}
async function addZSetMember() {
  if (!currentConn.value || !redisValue.value) return
  if (!newZSetMember.member) { ElMessage.warning('成员不能为空'); return }
  const k = (redisValue.value as any).key || ''
  try {
    await workbenchApi.redisZSetAdd(currentConn.value.id, k, newZSetMember.member, Number(newZSetMember.score) || 0)
    ElMessage.success('已添加')
    const existing = redisZSetEdit.value.find(z => z.member === newZSetMember.member)
    if (existing) existing.score = Number(newZSetMember.score) || 0
    else redisZSetEdit.value.push({ member: newZSetMember.member, score: Number(newZSetMember.score) || 0 })
    redisZSetEdit.value.sort((a,b) => a.score - b.score)
    newZSetMember.member = ''; newZSetMember.score = 0
  } catch { ElMessage.error('添加失败') }
}
async function saveZSetMember(z: { member: string; score: number }) {
  if (!currentConn.value || !redisValue.value) return
  const k = (redisValue.value as any).key || ''
  try {
    await workbenchApi.redisZSetAdd(currentConn.value.id, k, z.member, Number(z.score) || 0)
    ElMessage.success(`成员 ${z.member} 分数已更新`)
    redisZSetEdit.value.sort((a,b) => a.score - b.score)
  } catch { ElMessage.error('更新失败') }
}
async function removeZSetMember(member: string) {
  if (!currentConn.value || !redisValue.value) return
  const k = (redisValue.value as any).key || ''
  try {
    await workbenchApi.redisZSetRemove(currentConn.value.id, k, member)
    ElMessage.success('已移除')
    redisZSetEdit.value = redisZSetEdit.value.filter(z => z.member !== member)
  } catch { ElMessage.error('移除失败') }
}
function openNewKeyDialog() {
  newKeyForm.key = ''
  newKeyForm.type = 'string'
  newKeyForm.value = ''
  newKeyForm.ttl = -1
  newKeyDialogOpen.value = true
}
async function createNewKey() {
  if (!currentConn.value) return
  if (!newKeyForm.key.trim()) { ElMessage.warning('Key 不能为空'); return }
  try {
    let val: any = newKeyForm.value
    if (newKeyForm.type === 'hash' || newKeyForm.type === 'set' || newKeyForm.type === 'list' || newKeyForm.type === 'zset') {
      try { val = JSON.parse(newKeyForm.value || '{}') } catch { /* keep string */ }
      if (newKeyForm.type === 'hash' && typeof val === 'string') val = { field: val }
      if (newKeyForm.type === 'set' && typeof val === 'string') val = [val]
      if (newKeyForm.type === 'list' && typeof val === 'string') val = [val]
      if (newKeyForm.type === 'zset' && typeof val === 'string') val = [{ member: val, score: 0 }]
    }
    await workbenchApi.redisCreateKey(currentConn.value.id, { key: newKeyForm.key.trim(), type: newKeyForm.type, value: val, ttl: newKeyForm.ttl })
    ElMessage.success('已创建')
    newKeyDialogOpen.value = false
    loadRedisKeys()
  } catch (e: any) {
    ElMessage.error(e?.message || '创建失败')
  }
}
function toggleRedisKey(key: string) {
  if (redisSelectedKeys.has(key)) redisSelectedKeys.delete(key)
  else redisSelectedKeys.add(key)
}
function toggleAllRedis() {
  if (isAllRedisSelected.value) {
    redisSelectedKeys.clear()
  } else {
    redisKeys.value.forEach(k => redisSelectedKeys.add(k.key))
  }
}
async function batchDeleteKeys() {
  if (!currentConn.value) return
  if (isReadonly.value) { ElMessage.warning('只读角色不可删除'); return }
  if (!redisSelectedKeys.size) return
  try {
    await ElMessageBox.confirm(`确认批量删除 ${redisSelectedKeys.size} 个键？此操作不可恢复`, '批量删除', { type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消' })
  } catch { return }
  let success = 0
  for (const k of Array.from(redisSelectedKeys)) {
    try { await workbenchApi.redisDeleteKey(currentConn.value.id, k); success++ } catch {}
  }
  ElMessage.success(`已删除 ${success} 个`)
  redisSelectedKeys.clear()
  loadRedisKeys()
}
async function batchUpdateTTL() {
  if (!currentConn.value) return
  if (!redisSelectedKeys.size) return
  let success = 0
  for (const k of Array.from(redisSelectedKeys)) {
    try { await workbenchApi.redisUpdateTTL(currentConn.value.id, k, redisBatchTTL.value); success++ } catch {}
  }
  ElMessage.success(`已更新 ${success} 个键 TTL 为 ${redisBatchTTL.value}`)
  loadRedisKeys()
}
function exportSelectedKeys() {
  if (!redisSelectedKeys.size) { ElMessage.warning('未选择键'); return }
  const data = Array.from(redisSelectedKeys).map(k => {
    const found = redisKeys.value.find(x => x.key === k)
    return { key: k, type: found?.type || 'unknown', ttl: found?.ttl ?? -1 }
  })
  const blob = new Blob([JSON.stringify(data, null, 2)], { type: 'application/json' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = `redis_keys_${new Date().toISOString().slice(0,10)}.json`
  a.click()
  URL.revokeObjectURL(url)
}
function onRedisTreeToggle(node: any) {
  if (redisTreeExpanded.has(node.path)) redisTreeExpanded.delete(node.path)
  else redisTreeExpanded.add(node.path)
}
function onRedisTreeSelect(key: string) {
  inspectRedisKey(key)
}
async function beginTransaction() {
  if (!currentConn.value) { ElMessage.warning('请先选择数据源'); return }
  if (currentConn.value.type === 'redis') { ElMessage.info('Redis 不支持事务'); return }
  if (isReadonly.value) { ElMessage.warning('只读角色不可开启事务'); return }
  try {
    const res = await workbenchApi.beginTransaction(currentConn.value.id, currentTab.value.database) as any
    transactionActive.value = true
    transactionId.value = res.transaction_id || `tx_${Date.now()}`
    transactionStartedAt.value = new Date().toISOString()
    transactionQueries.value = 0
    ElMessage.success(`事务已开启：${transactionId.value}`)
    log(`BEGIN ${transactionId.value}`, 'success')
  } catch (e: any) { ElMessage.error(e?.message || '开启事务失败') }
}
async function commitTransaction() {
  if (!currentConn.value || !transactionActive.value) return
  try {
    await workbenchApi.commitTransaction(currentConn.value.id, transactionId.value)
    ElMessage.success('事务已提交')
    log(`COMMIT ${transactionId.value}`, 'success')
    transactionActive.value = false
    transactionId.value = ''
    transactionStartedAt.value = ''
    transactionQueries.value = 0
  } catch (e: any) { ElMessage.error(e?.message || '提交失败') }
}
async function rollbackTransaction() {
  if (!currentConn.value || !transactionActive.value) return
  try {
    await workbenchApi.rollbackTransaction(currentConn.value.id, transactionId.value)
    ElMessage.success('事务已回滚')
    log(`ROLLBACK ${transactionId.value}`, 'info')
    transactionActive.value = false
    transactionId.value = ''
    transactionStartedAt.value = ''
    transactionQueries.value = 0
  } catch (e: any) { ElMessage.error(e?.message || '回滚失败') }
}
async function loadTransactionStatus() {
  if (!currentConn.value) return
  try {
    const res = await workbenchApi.transactionStatus(currentConn.value.id) as any
    transactionActive.value = !!res.active
    if (res.transaction_id) transactionId.value = res.transaction_id
    if (res.started_at) transactionStartedAt.value = res.started_at
    if (res.queries !== undefined) transactionQueries.value = res.queries
    if (!res.active) { transactionId.value = ''; transactionStartedAt.value = ''; transactionQueries.value = 0 }
  } catch {}
}

function onParamValuesUpdate(v: Record<string, string>) {
  paramValues.value = v
}
function onParamApply(values: Record<string, string>, finalSql: string) {
  paramValues.value = values
  paramDrawerOpen.value = false
  // 直接执行替换后 SQL
  // 临时替换执行，执行完恢复？我们直接用 finalSql 执行，不改原 SQL
  // 为了让用户看到替换结果，我们把 finalSql 存到一个临时变量并调用 runQueryWithSql
  runQueryWithSql(finalSql)
}
async function runQueryWithSql(sqlOverride: string) {
  if (!currentConn.value) { ElMessage.warning('请先在左侧选择数据源'); return }
  if (currentConn.value.type === 'redis') { ElMessage.info('Redis 不支持 SQL'); return }
  let sql = sqlOverride.trim()
  if (!sql) { ElMessage.warning('SQL 为空'); return }
  sql = applyAutoLimit(sql)
  if (currentConn.value.environment === 'prod' && isDangerousSQL(sql)) {
    try {
      await ElMessageBox.confirm(`生产环境 ${currentConn.value.name} 即将执行写操作：\n${sql.slice(0,200)}\n\n确认继续？`, '生产环境二次确认', { type: 'warning' })
    } catch { return }
  }
  running.value = true
  abortController.value = new AbortController()
  resetGrid()
  viewMode.value = 'sql'
  resultTab.value = 'result'
  log(`开始执行（参数化）：${sql.replace(/\s+/g,' ').slice(0,80)}`)
  try {
    const res = await workbenchApi.execute(currentConn.value.id, sql, currentTab.value.database, abortController.value.signal, paramValues.value)
    lastDuration.value = res.duration_ms
    if (res.kind === 'query') {
      grid.columns = res.columns ?? []
      grid.rows = res.rows ?? []
      grid.truncated = Boolean(res.truncated)
      log(`查询成功，返回 ${grid.rows.length} 行，耗时 ${res.duration_ms} ms`, 'success')
    } else {
      writeResult.value = res
      log(`执行成功，影响 ${res.affected_rows ?? 0} 行，耗时 ${res.duration_ms} ms`, 'success')
    }
  } catch (err: any) {
    if (err?.name === 'CanceledError' || err?.code === 'ERR_CANCELED') log('查询已取消','info')
    else { log(err instanceof Error ? err.message : '执行失败','error'); resultTab.value = 'message' }
  } finally {
    running.value = false
    abortController.value = null
    if (transactionActive.value) transactionQueries.value++
    if (resultTab.value === 'history') loadHistory()
  }
}
function onRestoreSnapshot(snap: any) {
  grid.columns = snap.columns
  grid.rows = snap.rows
  lastDuration.value = snap.duration_ms
  resultTab.value = 'result'
  ElMessage.success(`已恢复快照：${snap.name}`)
}

function addTab() {
  const t = newTab()
  tabs.value.push(t)
  activeTab.value = t.id
}
function closeTab(id: number) {
  const idx = tabs.value.findIndex((t) => t.id === id)
  if (idx === -1) return
  tabs.value.splice(idx, 1)
  if (tabs.value.length === 0) {
    tabs.value.push(newTab())
  }
  if (activeTab.value === id) {
    const neighbor = tabs.value[Math.max(0, idx - 1)] ?? tabs.value[0]!
    activeTab.value = neighbor.id
  }
}

function onGenerateSelect(payload: { conn: ConnectionItem; database: string; schema: string; table: TableInfo }) {
  const sch = payload.schema ? `${payload.schema}.` : ''
  const qualified = payload.conn.type === 'postgres' ? `${payload.database}.${sch}${payload.table.name}` : `${payload.database}.${payload.table.name}`
  const sql = `SELECT * FROM ${qualified} LIMIT 100;`
  const tab = currentTab.value
  const monaco = monacoRefs[tab.id]
  if (monaco) {
    monaco.insertText(sql)
  } else {
    tab.sql = tab.sql ? `${tab.sql}\n${sql}` : sql
  }
  currentTab.value.database = payload.database
  ElMessage.success(`已生成 SELECT：${payload.table.name}`)
}

function onInsertTableName(name: string) {
  const tab = currentTab.value
  const monaco = monacoRefs[tab.id]
  if (monaco) monaco.insertText(name)
  else tab.sql = tab.sql ? `${tab.sql} ${name}` : name
}

function onAiInsert(sql: string) {
  const tab = currentTab.value
  const monaco = monacoRefs[tab.id]
  if (monaco) {
    monaco.insertText(sql)
  } else {
    tab.sql += (tab.sql ? '\n' : '') + sql
  }
  ElMessage.success('已插入到编辑器')
}

function formatTime(s: string): string {
  const d = new Date(s)
  const today = new Date()
  const sameDay = d.toDateString() === today.toDateString()
  return d.toLocaleTimeString('zh-CN', { hour12: false, ...(sameDay ? {} : { month: '2-digit', day: '2-digit' }) })
}

onMounted(async () => {
  const preselect = Number(route.query.connection)
  await treeRef.value?.whenLoaded()
  if (preselect > 0) {
    const conn = treeRef.value?.findConnection(preselect)
    if (conn) onSelectConnection(conn)
  }
})
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
