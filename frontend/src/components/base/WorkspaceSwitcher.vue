<script setup lang="ts">
import { computed, useId } from 'vue'
import { Building2, LoaderCircle } from 'lucide-vue-next'

const props = withDefaults(defineProps<{
  options: ReadonlyArray<{ id: number; name: string }>
  modelValue?: number
  label?: string
  contextLabel?: string
  loading?: boolean
  disabled?: boolean
}>(), { label: '当前工作空间', contextLabel: '', loading: false, disabled: false })
const emit = defineEmits<{ change: [id: number] }>()
const id = `workspace-${useId()}`
const selectedName = computed(() => props.options.find(option => option.id === props.modelValue)?.name || '选择工作空间')
const title = computed(() => `${props.label}：${props.loading ? '正在切换到' : ''}${selectedName.value}${props.contextLabel ? `；${props.contextLabel}` : ''}`)

function change(value: unknown) {
  if (typeof value === 'number' && props.options.some(option => option.id === value)) emit('change', value)
}
</script>

<template>
  <div class="workspace-switcher" :title="title" :aria-busy="loading">
    <label :for="id" class="sr-only">{{ label }}</label>
    <el-select :id="id" :model-value="modelValue" :aria-label="label" :title="title"
      :disabled="disabled || loading || !options.length" :loading="loading" :fit-input-width="false"
      placeholder="选择工作空间" popper-class="workspace-switcher__popper" @change="change">
      <template #prefix><LoaderCircle v-if="loading" class="workspace-switcher__icon animate-spin" aria-hidden="true" /><Building2 v-else class="workspace-switcher__icon" aria-hidden="true" /></template>
      <el-option v-for="option in options" :key="option.id" :value="option.id" :label="option.name" :title="option.name" />
    </el-select>
    <span v-if="loading" class="sr-only" role="status">正在切换工作空间</span>
  </div>
</template>
