<template>
  <el-select :model-value="modelValue" multiple filterable remote :remote-method="search" :loading="loading" class="w-full" :aria-label="label" :placeholder="label" @update:model-value="emit('update:modelValue', $event)">
    <el-option v-for="u in options" :key="u.id" :value="u.id" :label="u.username" />
  </el-select>
</template>
<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { adminApi, type UserItem } from '../api/admin'
const props = withDefaults(defineProps<{modelValue: number[]; label?: string}>(), {label: '搜索并选择用户'})
const emit = defineEmits<{ 'update:modelValue': [value: number[]] }>()
const users = ref<UserItem[]>([]), loading = ref(false)
const names = new Map<number, string>()
const options = computed(() => [...props.modelValue.map(id => ({id, username: names.get(id) || `用户 #${id}`})), ...users.value.filter(u => !props.modelValue.includes(u.id))])
let generation = 0
async function search(keyword: string) {
  const current = ++generation; loading.value = true
  try { const r = await adminApi.users({keyword, page: 1, page_size: 50}); if (generation === current) {users.value = r.items.filter(u => u.is_active); for (const u of r.items) names.set(u.id, u.username)} }
  catch { /* API displays the failure */ } finally {if (generation === current) loading.value = false}
}
onMounted(() => {void search('')})
</script>
