<script setup lang="ts">
import { computed, onScopeDispose, ref, watch } from 'vue'
import { connectionApi, type ConnectionItem } from '../../../api/datasource'
import type { TicketPayload } from '../../../api/ticket'
import LoadingState from '../../../components/base/LoadingState.vue'

const props = defineProps<{
  modelValue: boolean
  prefill?: Partial<TicketPayload> | null
  create: (payload: TicketPayload) => Promise<boolean>
}>()
const emit = defineEmits<{ 'update:modelValue': [open: boolean] }>()
const connections = ref<ConnectionItem[]>([])
const loading = ref(false)
const error = ref('')
const saving = ref(false)
const form = ref<TicketPayload>(emptyForm())
const valid = computed(() => !!(form.value.title.trim() && form.value.sql.trim() && form.value.table.trim() && form.value.connection_id))
let optionsRequest: AbortController | undefined

function emptyForm(): TicketPayload {
  return { title: '', type: 'ddl', table: '', sql: '', risk: 'low', reason: '', database: '' }
}

async function loadConnections() {
  optionsRequest?.abort()
  const request = new AbortController()
  optionsRequest = request
  loading.value = true
  error.value = ''
  try {
    const result = await connectionApi.list(undefined, { signal: request.signal, silentError: true })
    if (!request.signal.aborted) connections.value = result.items.filter(item => item.type !== 'redis')
  } catch (failure) {
    if (!request.signal.aborted) error.value = failure instanceof Error ? failure.message : '目标数据源读取失败'
  } finally {
    if (!request.signal.aborted) loading.value = false
  }
}

watch(() => props.modelValue, (open) => {
  if (open) {
    form.value = { ...emptyForm(), ...props.prefill }
    connections.value = []
    void loadConnections()
  } else {
    optionsRequest?.abort()
  }
}, { immediate: true })
onScopeDispose(() => optionsRequest?.abort())

function close() {
  if (!saving.value) emit('update:modelValue', false)
}

async function submit() {
  if (!valid.value || saving.value || loading.value || error.value) return
  saving.value = true
  try {
    if (await props.create({ ...form.value })) emit('update:modelValue', false)
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <el-dialog :model-value="modelValue" title="新建变更工单" width="min(560px, 94vw)" class="glass-dialog"
    :close-on-click-modal="false" :close-on-press-escape="!saving" :show-close="!saving" @update:model-value="close">
    <form id="new-change-ticket" class="space-y-3" @submit.prevent="submit">
      <label class="block text-sm">
        目标数据源
        <select v-model="form.connection_id" required class="glass-input block w-full mt-1" :disabled="saving || loading || !!error">
          <option :value="undefined" disabled>选择执行目标</option>
          <option v-for="item in connections" :key="item.id" :value="item.id">{{ item.name }}</option>
        </select>
      </label>
      <LoadingState v-if="loading" label="正在读取目标数据源…" variant="inline" />
      <div v-else-if="error" class="flex flex-wrap gap-2 items-center" role="alert">
        <p class="text-sm text-danger">{{ error }}</p>
        <button type="button" class="ghost-button ui-control-target" @click="loadConnections">重试读取数据源</button>
      </div>
      <p v-else-if="!connections.length" class="text-sm text-muted">当前没有可选的个人 SQL 连接。</p>
      <label class="block text-sm">
        数据库（留空使用连接默认值）
        <input v-model.trim="form.database" class="glass-input block w-full mt-1" :disabled="saving" />
      </label>
      <label class="block text-sm">
        标题
        <input v-model.trim="form.title" required maxlength="128" class="glass-input block w-full mt-1" :disabled="saving" />
      </label>
      <div class="grid grid-cols-2 gap-3">
        <label class="text-sm">
          类型
          <select v-model="form.type" class="glass-input block w-full mt-1" :disabled="saving">
            <option value="ddl">DDL</option><option value="dml">DML</option><option value="index">索引</option>
          </select>
        </label>
        <label class="text-sm">
          风险
          <select v-model="form.risk" class="glass-input block w-full mt-1" :disabled="saving">
            <option value="low">低</option><option value="medium">中</option><option value="high">高</option>
          </select>
        </label>
      </div>
      <label class="block text-sm">
        目标表
        <input v-model.trim="form.table" required class="glass-input block w-full mt-1" :disabled="saving" />
      </label>
      <label class="block text-sm">
        变更 SQL
        <textarea v-model="form.sql" required rows="4" class="glass-input block w-full mt-1 font-mono" :disabled="saving" />
      </label>
      <label class="block text-sm">
        变更原因
        <textarea v-model.trim="form.reason" rows="2" class="glass-input block w-full mt-1" :disabled="saving" />
      </label>
    </form>
    <template #footer>
      <div class="flex justify-end gap-2">
        <button type="button" class="ghost-button ui-control-target" :disabled="saving" @click="close">取消</button>
        <button type="submit" form="new-change-ticket" class="liquid-button ui-control-target" :disabled="!valid || saving || loading || !!error">
          {{ saving ? '正在创建…' : '创建工单' }}
        </button>
      </div>
    </template>
  </el-dialog>
</template>