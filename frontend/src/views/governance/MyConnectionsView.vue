<script setup lang="ts">
import { computed } from 'vue'
import PageCategoryTabs from '../../components/base/PageCategoryTabs.vue'
import PageHeader from '../../components/base/PageHeader.vue'
import PageState from '../../components/base/PageState.vue'
import { usePageCategory } from '../../composables/usePageCategory'
import { pageTitles } from '../../navigation'
import { useUserStore } from '../../stores/user'
import ConnectionView from '../connection/ConnectionView.vue'
import ProxyView from '../proxy/ProxyView.vue'
import SourceTasksView from '../connection/SourceTasksView.vue'
const user = useUserStore()
const categories = [
  { id: 'connections', name: '个人连接', action: 'connection.page' },
  { id: 'proxies', name: '我的代理', action: 'connection.proxy.manage' },
  { id: 'tasks', name: '我的连接任务', action: 'connection.task.view' },
]
const tabs = computed(() => categories.filter(item => user.can(item.action)))
const { category, selectCategory } = usePageCategory(tabs)
</script>

<template>
  <section class="ui-page-stack">
    <PageHeader :title="pageTitles.connections" />
    <PageCategoryTabs label="我的连接分类" :categories="tabs" :active-id="category?.id" @select="selectCategory">
      <ConnectionView v-if="category?.id === 'connections'" embedded />
      <ProxyView v-else-if="category?.id === 'proxies'" embedded />
      <SourceTasksView v-else-if="category?.id === 'tasks'" embedded />
      <template #unavailable>
        <PageState title="此连接分类不可用"
          :description="tabs.length ? '请选择当前可用的分类。' : '尚未获得个人连接分类的许可。'" />
      </template>
    </PageCategoryTabs>
  </section>
</template>
