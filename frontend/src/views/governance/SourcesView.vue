<script setup lang="ts">
import { computed } from 'vue'
import PageCategoryTabs from '../../components/base/PageCategoryTabs.vue'
import PageHeader from '../../components/base/PageHeader.vue'
import PageState from '../../components/base/PageState.vue'
import { usePageCategory } from '../../composables/usePageCategory'
import { useUserStore } from '../../stores/user'
import SourceCatalog from '../connection/SourceCatalog.vue'
import ReportAccountsPanel from '../report/ReportAccountsPanel.vue'
import ConnectorSettings from '../settings/ConnectorSettings.vue'
import SourceTasksView from '../connection/SourceTasksView.vue'
import RuntimeView from './RuntimeView.vue'
const user = useUserStore()
const categories = [
  { id: 'definitions', name: '数据源', action: 'source.definition.manage' },
  { id: 'accounts', name: '执行账号', action: 'source.account.manage' },
  { id: 'drivers', name: '驱动与模板', action: 'source.driver.manage' },
  { id: 'tasks', name: '空间任务', action: 'source.task.view' },
  { id: 'runtime', name: '运行监控', action: 'source.runtime.view' },
]
const tabs = computed(() => categories.filter(item => user.can(item.action)))
const { category, selectCategory } = usePageCategory(tabs)
</script>

<template>
  <section class="ui-page-stack">
    <PageHeader :title="category ? `数据管理 · ${category.name}` : '数据管理'"
      eyebrow="数据与运行" description="维护空间共用的数据来源、执行账号与运行信息。" />
    <PageCategoryTabs label="数据管理分类" :categories="tabs" :active-id="category?.id" @select="selectCategory">
      <SourceCatalog v-if="category?.id === 'definitions'" />
      <ReportAccountsPanel v-else-if="category?.id === 'accounts'" />
      <ConnectorSettings v-else-if="category?.id === 'drivers'" />
      <SourceTasksView v-else-if="category?.id === 'tasks'" embedded />
      <RuntimeView v-else-if="category?.id === 'runtime'" embedded />
      <template #unavailable>
        <PageState title="此数据管理分类不可用"
          :description="tabs.length ? '请选择当前可用的分类。' : '尚未获得数据管理分类的许可。'" />
      </template>
    </PageCategoryTabs>
  </section>
</template>
