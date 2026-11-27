<script setup lang="ts">
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import PageCategoryTabs from '../../components/base/PageCategoryTabs.vue'
import PageHeader from '../../components/base/PageHeader.vue'
import PageState from '../../components/base/PageState.vue'
import { useUserStore } from '../../stores/user'
import { handlingCenterCategories } from '../../utils/editorial-navigation'
import BusinessOperations from '../business/BusinessOperations.vue'
import TicketView from '../ticket/TicketView.vue'
import TasksView from './TasksView.vue'

const route = useRoute()
const router = useRouter()
const user = useUserStore()
const categories = computed(() => handlingCenterCategories(user.visibleMenus))
const authorizedCategories = computed(() => handlingCenterCategories(user.handlingMenus))
const category = computed(() => {
  const requested = route.query.section
  if (requested === undefined) return categories.value[0]
  return authorizedCategories.value.find(item => item.id === requested)
})
const tabCategory = computed(() => categories.value.find(item => item.id === category.value?.id))
const hiddenCategoryView = computed(() => category.value && !tabCategory.value ? categoryViews[category.value.id] : undefined)
const categoryViews = { todo: TasksView, business: BusinessOperations, tickets: TicketView }

async function selectSection(id: string) {
  if (id === category.value?.id || !categories.value.some(item => item.id === id)) return
  await router.push({ path: '/handling', query: { workspace_id: String(user.workspaceId), section: id } })
}
</script>

<template>
  <section class="ui-page-stack">
    <PageHeader title="办理中心" />
    <PageCategoryTabs label="办理分类" :categories="categories" :active-id="tabCategory?.id" @select="selectSection">
      <component v-if="tabCategory" :is="categoryViews[tabCategory.id]" embedded />
      <template #unavailable>
        <PageState v-if="!hiddenCategoryView" title="此办理分类不可用"
          :description="categories.length ? '请选择当前可用的分类。' : '尚未获得办理中心入口的许可。'" />
      </template>
    </PageCategoryTabs>
    <component v-if="hiddenCategoryView" :is="hiddenCategoryView" embedded />
  </section>
</template>
