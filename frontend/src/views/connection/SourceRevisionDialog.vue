<script setup lang="ts">
import { onScopeDispose, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { sourceCatalogApi, type DefinitionRevision, type SourceDefinition } from '../../api/source-catalog'

const props = defineProps<{ source: SourceDefinition }>()
const emit = defineEmits<{ changed: [revision: { id: number; enabled: boolean }] }>()
const items = ref<DefinitionRevision[]>([]), total = ref(0), page = ref(1)
const loading = ref(false), busy = ref(false), loadFailed = ref(false)
let request = 0
let active = true
watch(() => props.source.id, () => {
  items.value = []
  total.value = 0
  void load(1)
}, { immediate: true })
onScopeDispose(() => { active = false; request++ })
async function load(next: number) {
  if (!active) return
  const seq = ++request, id = props.source.id
  loading.value = true; loadFailed.value = false
  try {
    const result = await sourceCatalogApi.revisions(id, next)
    if (seq !== request || props.source.id !== id) return
    items.value = result.items; total.value = result.total; page.value = result.page
  } catch { if (seq === request) { items.value = []; total.value = 0; loadFailed.value = true } }
  finally { if (seq === request) loading.value = false }
}
async function toggle(row: DefinitionRevision) {
  if (busy.value || row.binding_count === undefined) return
  const family = props.source.family_id
  busy.value = true
  try {
    if (row.enabled) {
      await ElMessageBox.confirm(`停用修订 ${row.revision} 将影响 ${row.binding_count} 个个人连接和 ${row.report_count ?? 0} 份正式报表。它们将无法发起新操作，已发送的操作可能继续完成。其他修订不受影响。`, '确认停用影响', {type: 'warning', confirmButtonText: '确认停用', cancelButtonText: '取消'})
    }
    if (!active || props.source.family_id !== family) return
    await sourceCatalogApi.status(row.id, !row.enabled, row.binding_count, row.report_count)
    ElMessage.success(row.enabled ? '该修订已停用' : '该修订已启用')
    emit('changed', { id: row.id, enabled: !row.enabled })
  } catch { /* Cancellation or API error; refresh includes changed impact counts. */ }
  finally { busy.value = false; if (active && props.source.family_id === family) await load(page.value) }
}
</script>

<template>
  <section class="min-w-0" aria-label="数据源修订记录">
    <p class="text-sm text-muted mb-3 break-words">每个修订独立启停，已有连接和正式报表保留各自的修订。</p>
    <div class="flex justify-between items-center gap-3 mb-4 shrink-0">
      <span v-if="!loading && !loadFailed" class="text-xs text-muted">共 {{ total }} 个可见修订<span v-if="source.my_revision_id"> · 已添加个人连接</span></span>
      <button class="ghost-button text-xs ui-control-target" :disabled="loading || busy" @click="load(page)">刷新修订记录</button>
    </div>
    <div data-loading-label="正在读取历史修订…" v-data-loading="loading" class="space-y-3" aria-label="历史修订列表" aria-live="polite">
      <article v-for="row in items" :key="row.id" class="ui-inset-card rounded-xl border border-stroke p-4 space-y-3" :data-revision="row.revision">
        <div class="flex flex-wrap items-center justify-between gap-2">
          <h3 class="font-medium">修订 {{ row.revision }} <span class="text-xs" :class="row.enabled ? 'text-success' : 'text-warning'">{{ row.enabled ? '可用' : '已停用' }}</span></h3>
          <span v-if="row.id === source.my_revision_id" class="text-xs text-accent">我的连接正在使用</span>
        </div>
        <p class="text-sm break-words">{{ row.name }}</p>
        <p class="text-xs text-muted break-all">{{ row.config.host }}{{ row.config.port ? ':' + row.config.port : '' }} / {{ row.config.database || '默认范围' }} · TLS {{ row.config.ssl_mode }}</p>
        <p class="text-xs text-muted">{{ new Date(row.created_at).toLocaleString('zh-CN', {hour12: false}) }}</p>
        <div v-if="row.binding_count !== undefined" class="flex flex-wrap items-center justify-between gap-3">
          <p class="text-sm" data-testid="binding-count">绑定 {{ row.binding_count }} 个个人连接 · 服务 {{ row.report_count ?? 0 }} 份正式报表</p>
          <button class="ghost-button text-xs ui-control-target" :disabled="busy || loading" @click="toggle(row)">{{ row.enabled ? '停用此修订' : '启用此修订' }}</button>
        </div>
        <details v-if="row.previous_revision" class="text-xs" :open="row.id === items[0]?.id">
          <summary class="cursor-pointer text-muted py-1">与上一可见修订 {{ row.previous_revision }} 比较 · {{ row.changes.length }} 项变化</summary>
          <p v-if="!row.changes.length" class="text-muted mt-2">连接目标及说明没有变化。</p>
          <div v-for="change in row.changes" :key="change.field" class="mt-3 space-y-1">
            <p class="text-muted">{{ change.label }}</p>
            <div class="grid grid-cols-1 sm:grid-cols-2 gap-2">
              <p class="rounded-lg bg-surface-muted p-2 break-all whitespace-pre-wrap"><span class="text-subtle">之前：</span>{{ change.before || '未设置' }}</p>
              <p class="rounded-lg bg-surface-muted p-2 break-all whitespace-pre-wrap"><span class="text-subtle">现在：</span>{{ change.after || '未设置' }}</p>
            </div>
          </div>
        </details>
        <p v-else class="text-xs text-muted">首次可见发布，没有更早的可见修订可供比较。</p>
      </article>
      <p v-if="!loading && !items.length" class="text-sm text-muted p-4">{{ loadFailed ? '修订记录加载失败，请刷新重试。' : '当前页没有可见修订。' }}</p>
    </div>
      <div v-if="!loading && !loadFailed && total > 10" class="flex flex-wrap justify-between items-center gap-2 mt-3">
        <span class="text-xs text-muted">第 {{ page }} / {{ Math.max(1, Math.ceil(total / 10)) }} 页</span>
        <div class="flex gap-2"><button class="ghost-button ui-control-target" :disabled="page <= 1 || loading || busy" @click="load(page - 1)">上一页</button><button class="ghost-button ui-control-target" :disabled="page * 10 >= total || loading || busy" @click="load(page + 1)">下一页</button></div>
      </div>
  </section>
</template>
