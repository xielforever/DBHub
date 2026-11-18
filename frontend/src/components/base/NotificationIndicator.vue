<script setup lang="ts">
import { computed } from 'vue'
import { Bell, CircleAlert } from 'lucide-vue-next'

const props = withDefaults(defineProps<{
  unread?: number
  known?: boolean
  loading?: boolean
  failed?: boolean
  qualified?: boolean
}>(), { unread: 0, known: true, loading: false, failed: false, qualified: true })
const emit = defineEmits<{ click: [event: MouseEvent] }>()
const label = computed(() => {
  if (!props.qualified) return '通知，选择获准工作空间后查看未读状态'
  if (props.failed) return '通知，未读状态暂不可用，打开通知查看'
  if (props.loading) return props.unread > 0 ? `通知，当前工作空间 ${props.unread} 条未读，正在更新` : '通知，正在读取未读状态'
  if (!props.known) return '通知，未读状态尚未确认，打开通知查看'
  return props.unread > 0 ? `通知，当前工作空间 ${props.unread} 条未读` : '通知，当前工作空间暂无未读'
})
</script>

<template>
  <button type="button" class="ghost-button notification-button" :aria-label="label" :title="label"
    :aria-busy="loading" @click="emit('click', $event)">
    <Bell class="w-5 h-5" aria-hidden="true" />
    <CircleAlert v-if="qualified && failed" class="notification-button__error" aria-hidden="true" />
    <span v-else-if="qualified && known && unread > 0" class="notification-button__dot" aria-hidden="true" />
  </button>
</template>
