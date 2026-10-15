<template>
 <div class="flex flex-col h-full min-h-0 p-4 gap-3">
  <div class="flex items-center gap-2"><Sparkles class="w-4 h-4 text-emerald-300" /><p class="text-sm flex-1">AI 优化建议</p><button class="liquid-button text-xs" :disabled="!configured || !sql || loading" @click="analyze">{{ loading ? '分析中…' : 'AI 分析' }}</button></div>
  <p class="text-xs text-white/45">{{ configured ? '发送当前 SQL 与执行计划获取建议；建议不会自动执行。' : '模型服务尚未配置，可稍后在云环境设置。' }}</p>
  <pre v-if="content" class="glass-card p-4 text-xs text-white/75 whitespace-pre-wrap overflow-auto">{{ content }}</pre>
  <button v-if="rewrite" class="ghost-button text-xs self-start" @click="emit('apply-rewrite', rewrite)">插入建议 SQL</button>
 </div>
</template>
<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { Sparkles } from 'lucide-vue-next'
import { ElMessage } from 'element-plus'
import { advancedApi } from '../../../api/advanced'
const props = defineProps<{sql: string; explainColumns: string[]; explainRows: unknown[][]; duration: number | null; tableColumns?: string[]; dialect?: string; hasExplain?: boolean}>()
const emit = defineEmits<{(e: 'apply-rewrite', sql: string): void; (e: 'apply-index', index: unknown): void}>()
const content = ref(''), loading = ref(false), configured = ref(false)
const rewrite = computed(() => content.value.match(/```sql\s*([\s\S]*?)```/i)?.[1]?.trim() || '')
onMounted(async () => { try { configured.value = (await advancedApi.aiStatus()).configured } catch {} })
async function analyze() {loading.value = true; try { const res = await advancedApi.ai({prompt: '根据提供的 SQL 和实际 EXPLAIN 计划给出优化建议，未知信息请说明，不要虚构收益或评分', sql: props.sql, dialect: props.dialect, explain: props.hasExplain ? JSON.stringify({columns: props.explainColumns, rows: props.explainRows}).slice(0,10000) : undefined, mode: 'optimize'}); content.value = res.content } catch(e: unknown) { ElMessage.error(e instanceof Error ? e.message : '分析失败') } finally { loading.value = false }}
</script>
