<script setup lang="ts">
import BrandLogo from '../../components/base/BrandLogo.vue'
import { computed, ref } from 'vue'
import { ChartNoAxesCombined, Database, FolderSearch, Plus, ShieldCheck } from 'lucide-vue-next'
import EditorialNavigation from '../../components/base/EditorialNavigation.vue'
import AppBackdrop from '../../components/base/AppBackdrop.vue'
import EChart from '../../components/base/EChart.vue'
import PageHeader from '../../components/base/PageHeader.vue'
import PageState from '../../components/base/PageState.vue'
import ThemeSwitcher from '../../components/base/ThemeSwitcher.vue'
import WorkspaceSwitcher from '../../components/base/WorkspaceSwitcher.vue'
import NotificationIndicator from '../../components/base/NotificationIndicator.vue'
import CardActionMenu from '../../components/base/CardActionMenu.vue'
import SqlMonaco from '../query/components/SqlMonaco.vue'
import type { EChartsCoreOption } from 'echarts/core'

const mode = ref('content')
const previewGroups = [{ name: '报表', items: [{ id: 'content', label: '报表目录示例', path: '/content' }] }, { name: '查询', items: [{ id: 'development', label: 'SQL 与图表示例', path: '/development' }] }, { name: '管理', items: [{ id: 'components', label: '组件与状态示例', path: '/components' }] }]
const dialogOpen = ref(false)
const selectedRange = ref('today')
const selectedDate = ref('2026-10-26')
const fieldValue = ref('经营日报')
const sql = ref('SELECT region, revenue\nFROM sales_daily\nWHERE report_date = :report_date\nORDER BY revenue DESC;')
const keyword = ref('')
const buttonDemoClicks = ref(0)
const buttonDemoBusy = ref(false)
const lastCardAction = ref('尚未选择')
const sampleCardActions = [
  { id: 'edit', label: '编辑示例' },
  { id: 'draft', label: '创建草稿示例' },
  { id: 'remove', label: '删除示例', danger: true },
  { id: 'disabled', label: '不可用菜单示例', disabled: true },
]
const previewSpace = ref(1)
const previewSpaceLoading = ref(false)
const previewSpaces = [
  { id: 1, name: '销售运营空间' },
  { id: 2, name: '跨区域数据治理与业务协作工作空间' },
]
const notificationOpen = ref(false)
const previewUnread = ref(3)
const notificationFailed = ref(false)
const notificationLoading = ref(false)
function notificationSample(value: 'unread' | 'empty' | 'loading' | 'failed') {
  previewUnread.value = value === 'unread' ? 3 : 0
  notificationFailed.value = value === 'failed'
  notificationLoading.value = value === 'loading'
}
const sampleRows = [
  { name: '经营日报', owner: '销售运营组', status: '已发布', tone: 'success', time: '2026-10-26 09:30' },
  { name: '门店经营分析', owner: '运营分析组', status: '已发布', tone: 'success', time: '2026-10-26 09:15' },
  { name: '库存周转周报', owner: '我的开发', status: '待审批', tone: 'warning', time: '2026-10-25 17:40' },
  { name: '营销活动复盘', owner: '我的开发', status: '草稿', tone: '', time: '2026-10-25 16:20' },
]
const rows = computed(() => sampleRows.filter(row => row.name.includes(keyword.value)))
const chartOption: EChartsCoreOption = {
  tooltip: { trigger: 'axis', backgroundColor: 'var(--db-surface-overlay)', borderColor: 'var(--db-divider)', textStyle: { color: 'var(--db-text-primary)' } },
  grid: { left: 20, right: 16, top: 32, bottom: 12, containLabel: true },
  xAxis: { type: 'category', data: ['华东', '华南', '华北', '西南'], axisLabel: { color: 'var(--db-text-secondary)' }, axisLine: { lineStyle: { color: 'var(--db-divider)' } } },
  yAxis: { type: 'value', name: '营收（元）', nameTextStyle: { color: 'var(--db-text-secondary)' }, axisLabel: { color: 'var(--db-text-secondary)' }, splitLine: { lineStyle: { color: 'var(--db-chart-grid)' } } },
  series: [{ name: '营收', type: 'bar', data: [36400, 28800, 33200, 30240], itemStyle: { color: 'var(--db-chart-1)', borderRadius: [6, 6, 0, 0] } }],
}
</script>

<template>
  <AppBackdrop />
  <div class="iam-shell editorial-shell">
    <div class="min-w-0 flex-1">
      <header class="editorial-masthead">
        <BrandLogo />
        <EditorialNavigation :groups="previewGroups" :active-path="'/' + mode" @navigate="mode = $event.slice(1)" />
        <div class="editorial-masthead__actions" aria-label="工作空间与账户操作">
          <WorkspaceSwitcher :options="previewSpaces" :model-value="previewSpace" :loading="previewSpaceLoading" context-label="设计预览，仅切换示例名称" @change="previewSpace = $event" />
          <NotificationIndicator :unread="previewUnread" :failed="notificationFailed" :loading="notificationLoading" @click="notificationOpen = true" />
          <ThemeSwitcher compact /><span class="app-topbar__user">示例数据</span>
        </div>
      </header>
      <main class="app-content editorial-content space-y-6">
        <PageHeader :title="mode === 'content' ? '报表目录' : mode === 'components' ? '组件与状态' : 'SQL 与图表'" description="此页使用示例内容，不连接业务数据。" eyebrow="A2 数据编辑室">
          <template #actions><button type="button" class="liquid-button ui-control-target" @click="dialogOpen = true"><Plus class="w-4 h-4" aria-hidden="true" />查看浮层样式</button></template>
        </PageHeader>
        <template v-if="mode === 'content'">
          <section class="editorial-summary-strip" aria-label="数据摘要示例">
            <article class="glass-card space-y-3 ui-card-padding"><div class="flex items-center gap-2 text-accent"><ChartNoAxesCombined class="w-4 h-4" aria-hidden="true" /><h2 class="text-sm font-medium">经营日报</h2></div><p class="text-3xl font-semibold tabular-nums">128,640.00 <span class="text-xs font-normal text-muted">元</span></p><p class="text-xs text-muted">今日营收 · 09:30 更新</p></article>
            <article class="glass-card space-y-3 ui-card-padding"><div class="flex items-center gap-2 text-accent"><Database class="w-4 h-4" aria-hidden="true" /><h2 class="text-sm font-medium">门店经营分析</h2></div><p class="text-3xl font-semibold tabular-nums">36 <span class="text-xs font-normal text-muted">家门店</span></p><p class="text-xs text-muted">本期覆盖范围 · 09:15 更新</p></article>
            <article class="glass-card space-y-3 ui-card-padding"><div class="flex items-center gap-2 text-accent"><FolderSearch class="w-4 h-4" aria-hidden="true" /><h2 class="text-sm font-medium">区域销售趋势</h2></div><p class="text-3xl font-semibold tabular-nums">4 <span class="text-xs font-normal text-muted">个区域</span></p><p class="text-xs text-muted">华东、华南、华北、西南</p></article>
          </section>
          <div class="grid grid-cols-1 xl:grid-cols-3 gap-5">
            <section class="glass-panel overflow-hidden xl:col-span-2" aria-label="示例报表列表">
              <div class="flex flex-wrap items-center justify-between gap-3 p-5"><h2 class="font-medium">内容列表</h2><label class="min-w-0 w-full sm:w-56"><span class="sr-only">搜索示例报表</span><input v-model="keyword" type="search" class="glass-input" placeholder="搜索报表名称" /></label></div>
              <div class="overflow-x-auto"><table class="w-full text-sm"><thead class="bg-surface-inset text-muted text-xs"><tr><th scope="col" class="text-left font-medium p-4">名称 / 归属</th><th scope="col" class="text-left font-medium p-4">状态</th><th scope="col" class="text-left font-medium p-4">操作</th></tr></thead><tbody><tr v-for="row in rows" :key="row.name" class="border-t border-divider"><td class="p-4"><p>{{ row.name }}</p><p class="text-xs text-muted mt-1">{{ row.owner }}</p></td><td class="p-4"><span class="status-badge" :class="row.tone ? `status-badge--${row.tone}` : ''">{{ row.status }}</span></td><td class="p-4"><button class="ghost-button text-xs !px-3 ui-control-target" @click="dialogOpen = true">查看</button></td></tr><tr v-if="!rows.length"><td colspan="3" class="p-8 text-center text-muted">没有匹配的报表</td></tr></tbody></table></div>
              <p class="text-xs text-muted p-4 border-t border-divider">共 {{ rows.length }} 项示例内容</p>
            </section>
            <section class="glass-card ui-card-padding" aria-label="示例数据图表"><div class="flex items-center justify-between gap-3"><h2 class="font-medium">经营日报</h2><span class="status-badge status-badge--success">已发布</span></div><p class="text-xs text-muted mt-4">区域营收 · 元</p><EChart :option="chartOption" height="280px" aria-label="示例区域营收：华东36400元、华南28800元、华北33200元、西南30240元" /><p class="text-xs text-muted border-t border-divider pt-4">示例业务时间：2026-10-26 09:30</p></section>
          </div>
        </template>
        <template v-else-if="mode === 'components'">
          <section class="glass-panel space-y-4 ui-card-padding" aria-label="按钮点击反馈样板">
            <h2 class="font-medium">按钮点击反馈</h2><p class="text-sm text-muted">按下时明度轻微变化，松开恢复；位置保持稳定。以下按钮只改变本页示例计数。</p>
            <div class="flex flex-wrap items-center gap-3">
              <button type="button" class="liquid-button" aria-label="演示主按钮" @click="buttonDemoClicks++">主要操作</button>
              <button type="button" class="ghost-button" aria-label="演示次按钮" @click="buttonDemoClicks++">次要操作</button>
              <button type="button" class="ghost-button text-danger" aria-label="演示危险按钮" @click="buttonDemoClicks++">危险操作</button>
              <button type="button" class="ghost-button !p-0 ui-icon-button" aria-label="演示图标按钮" @click="buttonDemoClicks++"><Plus class="w-5 h-5" aria-hidden="true" /></button>
              <el-button type="primary" aria-label="演示基础组件按钮" @click="buttonDemoClicks++">基础组件</el-button>
              <a href="#inset-card-demo" class="nav-item" aria-label="查看内嵌数据卡示例">查看内嵌示例</a>
              <button type="button" class="ghost-button" disabled>不可用示例</button><el-button loading>处理中示例</el-button>
            </div>
            <div class="flex flex-wrap items-center gap-3"><button type="button" class="liquid-button min-w-32" :disabled="buttonDemoBusy" :aria-busy="buttonDemoBusy" @click="buttonDemoBusy = true; buttonDemoClicks++">{{ buttonDemoBusy ? '示例处理中…' : '演示提交' }}</button><button type="button" class="ghost-button" :disabled="!buttonDemoBusy" @click="buttonDemoBusy = false">恢复示例</button></div>
            <p role="status" aria-live="polite" aria-atomic="true" class="text-sm text-muted">本页示例：已触发 {{ buttonDemoClicks }} 次</p>
          </section>
          <section class="glass-panel ui-card-padding space-y-3" aria-label="密度与卡片操作样板">
            <h2 class="font-medium">桌面密度与卡片操作</h2>
            <p class="text-sm text-muted">桌面使用紧凑控件，窄屏及触屏扩大点击范围。以下操作只改变示例计数。</p>
            <div class="ui-card-actions max-w-sm">
              <button class="ghost-button ui-card-actions__primary" @click="buttonDemoClicks++">查看示例</button>
              <CardActionMenu label="示例卡片的更多操作" :items="sampleCardActions" @select="lastCardAction = $event; buttonDemoClicks++" />
            </div>
            <p role="status" class="text-xs text-muted">最近菜单操作：{{ lastCardAction }}</p>
          </section>
          <section id="inset-card-demo" class="glass-panel space-y-4 ui-card-padding" aria-label="内嵌数据卡样板">
            <h2 class="font-medium">内嵌数据卡</h2><p class="text-sm text-muted">修订、摘要和编辑分组保持原位，通过边缘和阴影反馈；内容加载不影响仍可操作的按钮。</p>
            <div class="grid grid-cols-1 lg:grid-cols-3 gap-4">
              <article class="ui-inset-card p-4 space-y-2" aria-label="修订记录样例"><h3 class="font-medium">修订记录 · 示例</h3><p class="text-sm text-muted">数据表面保持稳定，无二次磨砂。</p></article>
              <article class="ui-inset-card ui-inset-card--warning bg-warning-soft p-4 space-y-2" aria-label="语义色内嵌卡样例"><h3 class="font-medium text-warning">待复核 · 示例</h3><p class="text-sm text-muted">状态颜色和文字含义保持。</p></article>
              <section class="ui-inset-card p-4 space-y-2" aria-busy="true" aria-label="内容加载与按钮样例"><h3 class="font-medium">内容载入中 · 示例</h3><button type="button" class="ghost-button" @click="buttonDemoClicks++">取消载入示例</button><p class="text-xs text-muted">仅更新本页示例计数。</p></section>
            </div>
          </section>
          <section class="glass-card space-y-3 ui-card-padding" aria-label="通知状态样板">
            <h2 class="text-lg font-medium">通知圆点与状态</h2><p class="text-sm text-muted">右上方铃铛使用同一组件。下列操作只改变本页示例，不调用业务API。</p>
            <div class="flex flex-wrap gap-2"><button class="ghost-button" @click="notificationSample('unread')">有未读</button><button class="ghost-button" @click="notificationSample('empty')">无未读</button><button class="ghost-button" @click="notificationSample('loading')">加载中</button><button class="ghost-button" @click="notificationSample('failed')">读取失败</button></div>
            <p class="text-sm text-muted">工作空间切换状态</p><div class="flex flex-wrap gap-2"><button class="ghost-button" @click="previewSpaceLoading = true">演示工作空间切换中</button><button class="ghost-button" @click="previewSpaceLoading = false">结束切换示例</button></div>
          </section>
          <section class="glass-card space-y-5 ui-card-padding" aria-label="表单与操作状态">
            <h2 class="text-lg font-medium">表单与操作</h2>
            <div class="grid grid-cols-1 md:grid-cols-3 gap-4"><label class="text-sm">报表名称<input v-model="fieldValue" class="glass-input mt-2" /></label><label class="text-sm">业务范围<el-select v-model="selectedRange" class="w-full mt-2" aria-label="示例业务范围"><el-option label="今日" value="today" /><el-option label="本周" value="week" /><el-option label="本月" value="month" /></el-select></label><label class="text-sm">业务日期<el-date-picker v-model="selectedDate" value-format="YYYY-MM-DD" class="!w-full mt-2" aria-label="示例业务日期" /></label></div>
            <div class="grid grid-cols-1 sm:grid-cols-2 gap-4"><label class="text-sm">只读字段<input class="glass-input mt-2" value="销售运营组" readonly /></label><label class="text-sm">禁用字段<input class="glass-input mt-2" value="当前不可修改" disabled /></label></div>
            <label class="block text-sm max-w-md">校验状态<input class="glass-input mt-2" value="" placeholder="请输入名称" aria-invalid="true" aria-describedby="preview-field-error" /><span id="preview-field-error" class="block text-danger text-xs mt-2">请填写报表名称。</span></label>
            <div class="flex flex-wrap gap-3"><button class="liquid-button ui-control-target" @click="dialogOpen = true">主要操作</button><button class="ghost-button ui-control-target" @click="dialogOpen = true">次要操作</button><button class="ghost-button text-danger ui-control-target" @click="dialogOpen = true">危险操作样式</button><button class="liquid-button ui-control-target" disabled>提交中…</button><button class="ghost-button ui-control-target" disabled>暂不可用</button></div>
          </section>
          <div class="grid grid-cols-1 lg:grid-cols-2 gap-5"><PageState title="暂无匹配内容" description="调整当前筛选条件后再次查看。"><template #actions><button class="ghost-button ui-control-target" @click="mode = 'content'">返回内容列表</button></template></PageState><PageState tone="danger" title="暂时无法读取数据" description="这是失败状态的样式示例。保留当前任务，并提供明确的恢复入口。"><template #actions><button class="liquid-button ui-control-target" @click="dialogOpen = true">重试样式</button></template></PageState></div>
          <div class="grid grid-cols-1 lg:grid-cols-3 gap-5">
            <button type="button" class="glass-card text-left space-y-2 ui-card-padding" aria-label="可点击的报表卡片示例" @click="dialogOpen = true"><span class="block font-medium">可点击的报表入口</span><span class="block text-sm text-muted">悬浮上移8px、轻微放大，配合高光与弥散投影；支持键盘打开。</span></button>
            <button type="button" class="glass-card text-left space-y-2 ui-card-padding" disabled aria-label="禁用的整卡入口"><span class="block font-medium">禁用的整卡入口</span><span class="block text-sm text-muted">禁用时保持原位，不呈现可点击反馈。</span></button>
            <section class="glass-panel ui-surface-static space-y-2 ui-card-padding" aria-label="固定数据表面例外"><h2 class="font-medium">固定数据表面例外</h2><p class="text-sm text-muted">嵌入式工作区外框固定，内部控件保留自己的焦点反馈。</p><label class="sr-only" for="preview-static-input">固定表面内的只读字段</label><input id="preview-static-input" class="glass-input" value="稳定的编辑区域" readonly /></section>
          </div>
          <section class="glass-panel space-y-4 ui-card-padding" aria-label="嵌套卡片反馈样板">
            <h2 class="font-medium">嵌套层级反馈</h2><p class="text-sm text-muted">进入内部卡片时，只有当前卡片抬升、显示高光，外层保持稳定。</p>
            <article class="glass-card max-w-lg space-y-3 ui-card-padding" aria-label="嵌套内容卡片"><h3 class="font-medium">经营摘要 · 示例</h3><p class="text-sm text-muted">普通卡片上移6px并轻微放大，内部按钮仍可独立操作。</p><button class="ghost-button" @click="dialogOpen = true">查看示例详情</button></article>
          </section>
        </template>
        <template v-else>
          <div class="grid grid-cols-1 xl:grid-cols-2 gap-5"><section class="glass-panel overflow-hidden" aria-label="编辑器主题预览"><div class="flex items-center justify-between gap-3 p-5 border-b border-divider"><h2 class="font-medium">SQL 编辑器</h2><span class="text-xs text-muted">仅本页示例</span></div><div class="h-80"><SqlMonaco v-model="sql" /></div></section><section class="glass-panel ui-card-padding"><h2 class="font-medium mb-4">数据图表</h2><EChart :option="chartOption" height="320px" aria-label="示例区域营收比较，单位元" /></section></div>
          <p class="text-xs text-muted">切换主题时，编辑内容、选区与撤销历史保持；图表沿用同一数据。</p>
        </template>
        <p class="flex items-center gap-2 text-xs text-muted"><ShieldCheck class="w-4 h-4" aria-hidden="true" />所有内容均为示例，预览不调用业务 API。</p>
      </main>
    </div>
    <el-dialog v-model="dialogOpen" title="经营日报 · 浮层样式" width="min(600px, calc(100vw - 24px))" class="glass-dialog">
      <div class="space-y-5"><p class="text-muted">示例数据 · 2026-10-26 09:30</p><p class="text-3xl font-semibold tabular-nums">128,640.00 <span class="text-sm font-normal text-muted">元</span></p><EChart :option="chartOption" height="240px" aria-label="浮层中的示例区域营收" /><p class="text-sm text-muted">日期、下拉及对话框共同响应当前主题。</p></div>
      <template #footer><button class="ghost-button ui-control-target" @click="dialogOpen = false">关闭预览</button></template>
    </el-dialog>
    <el-drawer v-model="notificationOpen" title="通知预览 · 示例数据" size="min(400px, calc(100vw - 24px))">
      <div class="space-y-4"><p class="text-sm text-muted">仅用于查看圆点和通知样式，不读取真实通知，打开不会自动已读。</p><article class="glass-card p-4 space-y-2"><h2 class="font-medium">经营日报已更新 · 示例</h2><p class="text-sm text-muted">查看最新一期内容，实际时效由报表页面说明。</p><span class="status-badge">{{ previewUnread ? '未读示例' : '已读示例' }}</span></article><button class="ghost-button" @click="notificationSample('empty')">将本页示例标为已读</button></div>
    </el-drawer>
  </div>
</template>
