<script setup lang="ts">
import LoadingState from '../../../components/base/LoadingState.vue'
import { ref, computed, onMounted } from 'vue'
import { Sparkles } from 'lucide-vue-next'
import { ElMessage } from 'element-plus'
import { advancedApi } from '../../../api/advanced'
const props = defineProps<{sql: string; explainColumns: string[]; explainRows: unknown[][]; duration: number | null; tableColumns?: string[]; dialect?: string; hasExplain?: boolean}>()
const emit = defineEmits<{(e: 'apply-rewrite', sql: string): void; (e: 'apply-index', index: unknown): void}>()
const statusLoading = ref(true)
const content = ref(''), loading = ref(false), configured = ref(false)
const rewrite = computed(() => content.value.match(/```sql\s*([\s\S]*?)```/i)?.[1]?.trim() || '')
onMounted(async () => { try { configured.value = (await advancedApi.aiStatus()).configured } catch {} finally { statusLoading.value = false } })
async function analyze() {loading.value = true; try { const res = await advancedApi.ai({prompt: '根据提供的 SQL 和实际 EXPLAIN 计划给出优化建议，未知信息请说明，不要虚构收益或评分', sql: props.sql, dialect: props.dialect, explain: props.hasExplain ? JSON.stringify({columns: props.explainColumns, rows: props.explainRows}).slice(0,10000) : undefined, mode: 'optimize'}); content.value = res.content } catch(e: unknown) { ElMessage.error(e instanceof Error ? e.message : '分析失败') } finally { loading.value = false }}
</script>

<template>
 <div class="flex flex-col h-full min-h-0 p-4 gap-3">
  <div class="flex items-center gap-2"><Sparkles class="w-4 h-4 text-success" /><p class="text-sm flex-1">AI 优化建议</p><button class="liquid-button text-xs ui-control-target" :disabled="!configured || !sql || loading" @click="analyze">{{ loading ? '分析中…' : 'AI 分析' }}</button></div>
  <p class="text-xs text-muted">{{ statusLoading ? '正在确认模型服务…' : configured ? '发送当前 SQL 与执行计划获取建议；建议不会自动执行。' : '模型服务尚未配置，可稍后在云环境设置。' }}</p>
  <LoadingState v-if="loading" variant="detail" label="正在生成查询优化建议…" /><pre v-else-if="content" class="glass-card p-4 text-xs text-content whitespace-pre-wrap overflow-auto">{{ content }}</pre>
  <button v-if="rewrite && !loading" class="ghost-button text-xs self-start ui-control-target" @click="emit('apply-rewrite', rewrite)">插入建议 SQL</button>
 </div>
</template>
