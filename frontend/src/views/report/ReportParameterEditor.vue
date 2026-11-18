<script setup lang="ts">
import type { ReportParameter } from './reportParameters'
const props = defineProps<{ modelValue: ReportParameter[]; disabled?: boolean; title?: string }>()
const emit = defineEmits<{ 'update:modelValue': [parameters: ReportParameter[]] }>()
const types = { text: '文本', integer: '整数', decimal: '金额／小数', date: '日期', datetime: '日期时间', boolean: '是／否', enum: '枚举' }
function patch(index: number, fields: Partial<ReportParameter>) { emit('update:modelValue', props.modelValue.map((p, i) => i === index ? { ...p, ...fields } : p)) }
function changeType(index: number, type: ReportParameter['type']) { patch(index, { type, default: undefined, default_relative_days: undefined, options: undefined, min: undefined, max: undefined }) }
</script>

<template>
  <details class="glass-panel p-3 space-y-3" :open="modelValue.length > 0">
    <summary class="text-sm cursor-pointer">{{ title || '报表参数' }}（{{ modelValue.length }}/20）</summary>
    <p class="text-xs text-muted">在 SQL 中使用 :参数名。日期、编号和枚举值按声明校验，参数定义会随版本送审。</p>
    <div v-for="(p, index) in modelValue" :key="index" class="ui-inset-card border border-stroke rounded-lg p-3 space-y-2">
      <div class="grid sm:grid-cols-3 gap-2">
        <label class="text-xs">参数名<input :value="p.name" :aria-label="`参数${index + 1}名称`" class="glass-input block w-full" maxlength="48" :disabled="disabled" @input="patch(index, { name: ($event.target as HTMLInputElement).value })" /></label>
        <label class="text-xs">显示名称<input :value="p.label" :aria-label="`参数${index + 1}显示名称`" class="glass-input block w-full" maxlength="80" :disabled="disabled" @input="patch(index, { label: ($event.target as HTMLInputElement).value })" /></label>
        <label class="text-xs">类型<select :value="p.type" :aria-label="`参数${index + 1}类型`" class="glass-input block w-full" :disabled="disabled" @change="changeType(index, ($event.target as HTMLSelectElement).value as ReportParameter['type'])"><option v-for="(label, type) in types" :key="type" :value="type">{{ label }}</option></select></label>
      </div>
      <div class="flex flex-wrap items-center gap-3 text-xs">
        <label class="flex gap-2"><input type="checkbox" :checked="p.required" :disabled="disabled" @change="patch(index, { required: ($event.target as HTMLInputElement).checked })" />必填</label>
        <label v-if="p.type !== 'date' || p.default_relative_days === undefined">默认值<input :value="p.default ?? ''" :aria-label="`参数${index + 1}默认值`" class="glass-input ml-2" :disabled="disabled" @input="patch(index, { default: ($event.target as HTMLInputElement).value || undefined })" /></label>
        <template v-if="p.type === 'date'"><label class="flex gap-2"><input type="checkbox" :checked="p.default_relative_days !== undefined" :disabled="disabled" @change="patch(index, { default: undefined, default_relative_days: ($event.target as HTMLInputElement).checked ? 0 : undefined, timezone: Intl.DateTimeFormat().resolvedOptions().timeZone })" />默认相对今天</label><label v-if="p.default_relative_days !== undefined">偏移天数<input type="number" :value="p.default_relative_days" :aria-label="`参数${index + 1}相对天数`" class="glass-input w-24 ml-2" :disabled="disabled" @input="patch(index, { default_relative_days: Number(($event.target as HTMLInputElement).value) })" /></label></template>
        <template v-if="['integer', 'decimal'].includes(p.type)"><label>最小值<input type="number" :value="p.min" :aria-label="`参数${index + 1}最小值`" class="glass-input w-24 ml-2" :disabled="disabled" @input="patch(index, { min: ($event.target as HTMLInputElement).value === '' ? undefined : Number(($event.target as HTMLInputElement).value) })" /></label><label>最大值<input type="number" :value="p.max" :aria-label="`参数${index + 1}最大值`" class="glass-input w-24 ml-2" :disabled="disabled" @input="patch(index, { max: ($event.target as HTMLInputElement).value === '' ? undefined : Number(($event.target as HTMLInputElement).value) })" /></label></template>
        <button type="button" class="ghost-button text-danger ui-control-target" :disabled="disabled" :aria-label="`移除参数${index + 1}`" @click="emit('update:modelValue', modelValue.filter((_, i) => i !== index))">移除</button>
      </div>
      <label v-if="p.type === 'enum'" class="block text-xs">枚举选项（每行 值=显示名称）<textarea :value="(p.options || []).map(option => `${option.value}=${option.label}`).join('\n')" :aria-label="`参数${index + 1}选项`" class="glass-input block w-full mt-1" rows="3" :disabled="disabled" @change="patch(index, { options: ($event.target as HTMLTextAreaElement).value.split('\n').filter(Boolean).map(line => { const split = line.indexOf('='); return { value: split < 0 ? line : line.slice(0, split), label: split < 0 ? line : line.slice(split + 1) } }) })" /></label>
    </div>
    <button type="button" class="ghost-button text-xs ui-control-target" :disabled="disabled || modelValue.length >= 20" @click="emit('update:modelValue', [...modelValue, { name: '', label: '', type: 'text' }])">添加参数</button>
  </details>
</template>
