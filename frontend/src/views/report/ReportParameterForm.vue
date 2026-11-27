<script setup lang="ts">
import { computed, nextTick, ref, useId } from 'vue'
import { reportParameterError, type ParameterValues, type ReportParameter } from './reportParameters'
const props = withDefaults(defineProps<{ definitions: ReportParameter[]; modelValue: ParameterValues; disabled?: boolean; title?: string;actions?:boolean }>(),{actions:true})
const emit = defineEmits<{ 'update:modelValue': [value: ParameterValues]; apply: []; reset: [] }>()
const fieldset = ref<HTMLFieldSetElement>()
const formId = useId()
const attempted = ref(false)
const errors = computed(() => {
  const messages = new Map<string, string>()
  for (const parameter of props.definitions) {
    const message = reportParameterError(parameter, props.modelValue[parameter.name])
    if (message) messages.set(parameter.name, message)
  }
  return messages
})
function fieldError(parameter: ReportParameter, index: number) {
  if (!attempted.value || !errors.value.has(parameter.name)) return {}
  return { 'aria-invalid': true, 'aria-describedby': `${formId}-error-${index}` }
}
async function validate() {
  attempted.value = errors.value.size > 0
  if (!attempted.value) return true
  await nextTick()
  fieldset.value?.querySelector<HTMLElement>('[aria-invalid="true"]')?.focus()
  return false
}
defineExpose({ validate })
function update(name: string, value: string | boolean | null) { emit('update:modelValue', { ...props.modelValue, [name]: value }) }
</script>

<template>
  <fieldset v-if="definitions.length" ref="fieldset" class="glass-panel p-3 space-y-3" :disabled="disabled">
    <legend class="px-1 text-sm font-medium">{{ title || '筛选条件' }}</legend>
    <div class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3">
      <label v-for="(p, index) in definitions" :key="p.name" class="text-xs text-muted space-y-1 min-w-0">
        <span>{{ p.label }}<span v-if="p.required" class="text-warning"> *</span></span>
        <select v-if="p.type === 'enum'" v-bind="fieldError(p, index)" :value="modelValue[p.name] ?? ''" :aria-label="p.label" :required="p.required" class="glass-input block w-full" @change="update(p.name, ($event.target as HTMLSelectElement).value || null)">
          <option value="">{{ p.required ? '请选择' : '全部' }}</option>
          <option v-for="option in p.options" :key="option.value" :value="option.value">{{ option.label }}</option>
        </select>
        <select v-else-if="p.type === 'boolean'" v-bind="fieldError(p, index)" :value="modelValue[p.name] === undefined || modelValue[p.name] === null ? '' : String(modelValue[p.name])" :aria-label="p.label" :required="p.required" class="glass-input block w-full" @change="update(p.name, ($event.target as HTMLSelectElement).value === '' ? null : ($event.target as HTMLSelectElement).value === 'true')"><option value="">全部</option><option value="true">是</option><option value="false">否</option></select>
        <input v-else v-bind="fieldError(p, index)" :type="p.type === 'date' ? 'date' : 'text'" :inputmode="['integer', 'decimal'].includes(p.type) ? 'decimal' : undefined" :value="modelValue[p.name] ?? ''" :aria-label="p.label" :required="p.required" :maxlength="p.type === 'text' ? undefined : p.max_length || 1024" :placeholder="p.type === 'datetime' ? '2026-10-20T08:00:00+08:00' : p.required ? '请输入' : '留空不限制'" class="glass-input block w-full" @input="update(p.name, ($event.target as HTMLInputElement).value || null)" />
        <span v-if="attempted && errors.has(p.name)" :id="`${formId}-error-${index}`" class="block text-xs text-danger">{{ errors.get(p.name) }}</span>
      </label>
    </div>
    <div v-if="actions!==false" class="flex flex-wrap gap-2"><button type="button" class="liquid-button text-xs ui-control-target" :disabled="disabled" @click="emit('apply')">应用筛选</button><button type="button" class="ghost-button text-xs ui-control-target" :disabled="disabled" @click="emit('reset')">恢复默认</button><slot /></div>
  </fieldset>
</template>
