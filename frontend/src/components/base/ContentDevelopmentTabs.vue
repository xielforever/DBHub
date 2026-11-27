<script setup lang="ts">
import { computed } from 'vue'
import { useUserStore } from '../../stores/user'
import { contentDevelopmentEntries } from '../../utils/editorial-navigation'
import { featureAllowed } from '../../utils/permission'

defineProps<{ active: 'report' | 'dashboard' }>()
const user = useUserStore()
const entries = computed(() => contentDevelopmentEntries(user.visibleMenus,
  kind => featureAllowed(user, kind + '.design', ['admin', 'developer'].includes(user.role))))
</script>

<template>
  <nav v-if="entries.length > 1" class="flex flex-wrap gap-2" aria-label="内容开发类型">
    <RouterLink v-for="entry in entries" :key="entry.kind"
      :to="{ path: entry.path, query: { [entry.kind === 'report' ? 'view' : 'tab']: 'managed', workspace_id: user.workspaceId } }"
      class="ghost-button ui-control-target" :class="{ 'text-accent border-accent': active === entry.kind }"
      :aria-current="active === entry.kind ? 'page' : undefined">{{ entry.label }}</RouterLink>
  </nav>
</template>
