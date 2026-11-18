<script setup lang="ts">
import { computed } from 'vue'
import { useRoute, useRouter, type LocationQueryRaw } from 'vue-router'
import PageCategoryTabs from '../../components/base/PageCategoryTabs.vue'
import PageHeader from '../../components/base/PageHeader.vue'
import PageState from '../../components/base/PageState.vue'
import { usePageCategory } from '../../composables/usePageCategory'
import { useUserStore } from '../../stores/user'
import AssetView from '../asset/AssetView.vue'
import MetricCatalog from '../business/MetricCatalog.vue'

const user = useUserStore()
const route = useRoute()
const router = useRouter()
const categories = computed(() => {
  const items = [{ id: 'metadata', name: '元数据' }]
  if (user.can('report.page') || user.can('catalog.metadata')) items.push({ id: 'metrics', name: '指标与数据集' })
  return items
})
const { category } = usePageCategory(categories, { defaultId: 'metadata' })

function selectCategory(id: string) {
  if (id === category.value?.id || !categories.value.some(item => item.id === id)) return
  const query: LocationQueryRaw = { ...route.query, tab: id }
  for (const key of ['q', 'page', 'connection_id', 'database', 'schema', 'table', 'asset_connection', 'asset_database', 'asset_schema', 'type', 'sensitivity', 'no_owner', 'starred']) delete query[key]
  void router.push({ path: route.path, query, hash: route.hash })
}
</script>

<template>
  <section class="ui-page-stack min-w-0">
    <PageHeader title="数据目录" />
    <PageCategoryTabs label="数据目录分类" :categories="categories" :active-id="category?.id" @select="selectCategory">
      <MetricCatalog v-if="category?.id === 'metrics'" embedded />
      <AssetView v-else-if="user.can('catalog.metadata')" embedded />
      <PageState v-else title="未获得元数据读取许可"
        description="查看表结构或预览数据，需要相应的元数据或数据预览权限。" />
      <template #unavailable>
        <PageState title="此目录分类不可用" description="请选择当前可用的分类。" />
      </template>
    </PageCategoryTabs>
  </section>
</template>
