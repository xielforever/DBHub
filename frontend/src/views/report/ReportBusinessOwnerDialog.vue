<script setup lang="ts">
import { computed, onMounted, onScopeDispose, ref } from 'vue'
import { managedReportApi, type BusinessOwnerCandidate, type ManagedReport } from '../../api/managed-report'
import { reportCommandKeys } from './reportCommands'

const props = defineProps<{ report: ManagedReport; initial?: boolean }>()
const emit = defineEmits<{ close: []; saved: [] }>()
const title = computed(() => props.initial ? '选择业务主管并提交审批' : '更换业务负责人')
const candidates = ref<BusinessOwnerCandidate[]>([])
const selected = ref<number>()
const loading = ref(false)
const saving = ref(false)
const failure = ref('')
const controller = new AbortController()
const keys = reportCommandKeys()

async function load() {
  loading.value = true
  failure.value = ''
  try {
    const result = await managedReportApi.businessOwnerCandidates(props.report.id, controller.signal)
    if (!controller.signal.aborted) candidates.value = result.items ?? []
  } catch (error) {
    if (!controller.signal.aborted) failure.value = error instanceof Error ? error.message : '读取主管名单失败。'
  } finally {
    loading.value = false
  }
}

async function save() {
  const businessOwner = selected.value
  if (!businessOwner || saving.value || loading.value || businessOwner === props.report.business_owner_id) return
  saving.value = true
  failure.value = ''
  try {
    if (props.initial) {
      const payload = { version: props.report.draft_version, business_owner_id: businessOwner }
      await managedReportApi.submit(props.report.id, payload.version, keys.get('submit-owner', payload), businessOwner)
    } else {
      const payload = { expected_version: props.report.business_owner_version ?? 0, business_owner_id: businessOwner }
      await managedReportApi.changeBusinessOwner(props.report.id, { ...payload, request_id: keys.get('business-owner', payload) })
    }
    if (!controller.signal.aborted) emit('saved')
  } catch (error) {
    if (!controller.signal.aborted) failure.value = error instanceof Error ? error.message : '操作未完成，请核对当前状态。'
  } finally {
    saving.value = false
  }
}

onMounted(() => void load())
onScopeDispose(() => { controller.abort(); keys.clear() })
</script>

<template>
  <el-dialog :model-value="true" :title="title" append-to-body align-center class="glass-dialog"
    width="min(560px, calc(100vw - 24px))" :close-on-click-modal="false" :close-on-press-escape="!saving"
    :show-close="!saving" @close="emit('close')">
    <form class="space-y-4" @submit.prevent="save">
      <p class="break-words font-medium">{{ report.draft.name }}</p>
      <p v-if="report.business_owner_name" class="text-sm text-muted">当前业务负责人 {{ report.business_owner_name }}</p>
      <label class="block text-sm">{{ initial ? '业务主管' : '接任主管' }}
        <select v-model.number="selected" class="glass-input mt-1 block w-full" :disabled="loading || saving || !candidates.length">
          <option :value="undefined">请选择成员</option>
          <option v-for="candidate in candidates" :key="candidate.user_id" :value="candidate.user_id">{{ candidate.username }}</option>
        </select>
      </label>
      <p v-if="loading" role="status" class="text-sm text-muted">正在读取有效主管…</p>
      <p v-else-if="!candidates.length && !failure" class="text-sm text-muted">此空间暂无可选业务主管。</p>
      <p v-if="failure" role="alert" class="text-sm text-danger">{{ failure }}</p>
      <button v-if="failure && !candidates.length && !loading" type="button" class="ghost-button ui-control-target" @click="load">重试读取主管</button>
      <div class="flex flex-wrap justify-end gap-2 border-t border-stroke pt-3">
        <button type="button" class="ghost-button ui-control-target" :disabled="saving" @click="emit('close')">取消</button>
        <button type="submit" class="liquid-button ui-control-target" :disabled="!selected || saving || loading || selected === report.business_owner_id">
          {{ saving ? '保存中…' : initial ? '提交审批' : '保存负责人' }}
        </button>
      </div>
    </form>
  </el-dialog>
</template>
