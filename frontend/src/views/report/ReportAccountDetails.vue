<script setup lang="ts">
import type { ReportAccount } from '../../api/managed-report'

defineProps<{
  account: ReportAccount
  busy: boolean
  error: string
  message: string
  embedded?: boolean
}>()
defineEmits<{ test: []; rotate: []; toggle: [] }>()
</script>

<template>
  <section class="min-w-0 space-y-4" aria-label="执行账号详情">
    <header class="flex flex-wrap items-baseline gap-2">
      <component :is="embedded ? 'h3' : 'h2'" class="font-editorial text-lg font-semibold break-all">{{ account.name }}</component>
      <span class="text-sm" :class="account.enabled ? 'text-success' : 'text-muted'">{{ account.enabled ? '已启用' : '已停用' }}</span>
    </header>
    <div class="flex flex-wrap gap-2">
      <button type="button" class="ghost-button ui-control-target" :disabled="busy || !account.enabled" @click="$emit('test')">测试只读连接</button>
      <button type="button" class="ghost-button ui-control-target" :disabled="busy" @click="$emit('rotate')">轮换密码</button>
      <button type="button" class="ghost-button ui-control-target" :disabled="busy" @click="$emit('toggle')">{{ account.enabled ? '停用' : '启用' }}</button>
    </div>
    <p v-if="message" role="status" class="text-sm text-success">{{ message }}</p>
    <p v-if="error" role="alert" class="text-sm text-danger break-all">{{ error }}</p>
    <dl class="account-details">
      <div><dt>用途</dt><dd>正式报表</dd></div>
      <div><dt>所属数据源</dt><dd>{{ account.source_name }}</dd></div>
      <div><dt>固定修订</dt><dd>#{{ account.definition_revision_id }}</dd></div>
      <div><dt>数据库用户名</dt><dd>{{ account.username }}</dd></div>
      <div><dt>当前服务</dt><dd>{{ account.report_count }} 份已发布报表</dd></div>
    </dl>
    <p class="text-sm text-muted">此账号只供获准的正式报表使用。轮换密码时保持数据库身份和数据源修订。</p>
  </section>
</template>

<style scoped>
.account-details > div {
  display: grid;
  grid-template-columns: minmax(7rem, 2fr) minmax(0, 3fr);
  gap: 12px;
  border-block-start: 1px solid var(--db-divider);
  padding-block: 8px;
  font-size: 14px;
}
.account-details dt { color: var(--db-text-secondary); }
.account-details dd { overflow-wrap: anywhere; }
</style>
