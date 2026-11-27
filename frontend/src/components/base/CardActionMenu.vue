<script setup lang="ts">
import { MoreHorizontal } from 'lucide-vue-next'

const props = defineProps<{
  label: string
  items: readonly { id: string; label: string; disabled?: boolean; danger?: boolean }[]
}>()
const emit = defineEmits<{ select: [id: string] }>()

function select(command: unknown) {
  const item = props.items.find(item => item.id === command)
  if (item && !item.disabled) emit('select', item.id)
}
</script>

<template>
  <el-dropdown v-if="items.length" trigger="click" @command="select">
    <button type="button" class="ghost-button ui-icon-button" :aria-label="label" :title="label">
      <MoreHorizontal class="w-4 h-4" aria-hidden="true" />
    </button>
    <template #dropdown>
      <el-dropdown-menu>
        <el-dropdown-item v-for="item in items" :key="item.id" :command="item.id" :disabled="item.disabled">
          <span :class="item.danger && !item.disabled ? 'text-danger' : undefined">{{ item.label }}</span>
        </el-dropdown-item>
      </el-dropdown-menu>
    </template>
  </el-dropdown>
</template>
