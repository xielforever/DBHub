<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { connectionApi } from '../../../api/datasource'
import { workbenchApi } from '../../../api/workbench'
import { matchesScriptType, type ScriptTarget, type ScriptType } from '../../../api/workbench-scripts'
import { useUserStore } from '../../../stores/user'
import { useQueryTarget, type QueryTarget } from '../useQueryTarget'
import QueryTargetPicker from './QueryTargetPicker.vue'

const props = defineProps<{
  tabKey: number
  scriptType: ScriptType
  defaultTarget?: ScriptTarget
  actual: QueryTarget | null
  apply: (target: QueryTarget) => Promise<void>
}>()
const emit = defineEmits<{ close: [] }>()
const user = useUserStore()
const context = computed(() => `${user.workspaceId}:${user.identityEpoch()}:${user.permissionKey}:${props.tabKey}`)
const picker = useQueryTarget({
  sources: async signal => (await connectionApi.list(undefined, { signal, silentError: true })).items
    .filter(source => matchesScriptType(source, props.scriptType))
    .map(source => source.type === 'redis' && !source.database ? { ...source, database: '0' } : source),
  databases: async (id, signal) => (await workbenchApi.databases(id, signal, true)).items.map(item => item.name),
  context: () => context.value,
  blocked: () => '',
  navigate: target => props.apply(target),
  prefillSourceDatabase: false,
})
const { open, draft, draftSource, sources, databases, sourceLoading, databaseLoading, sourceError, databaseError, submitError, submitting, disabledReason } = picker
onMounted(() => {
  const saved = props.defaultTarget
  const actual = props.actual
  void picker.show(actual?.source.id ?? saved?.connection_id, actual?.database ?? saved?.database)
})

async function selectDatabase(database: string) {
  if (submitting.value) return
  if (props.scriptType !== 'redis') picker.chooseDatabase(database)
  if (draft.value?.database !== database) return
  if (await picker.confirm()) emit('close')
}
function close() {
  if (submitting.value) return
  picker.close()
  emit('close')
}
</script>

<template>
  <QueryTargetPicker :open="open" title="选择脚本数据源" :current-target="actual" direct-selection
    :draft-target="draft" :sources="sources" :databases="databases" :source-loading="sourceLoading"
    :database-loading="databaseLoading" :source-error="sourceError" :database-error="databaseError" :submit-error="submitError"
    :submitting="submitting" :disabled-reason="disabledReason"
    @update:open="close" @select-source="picker.chooseSource($event)" @select-database="selectDatabase"
    @retry-sources="picker.loadSources" @retry-databases="draftSource && picker.chooseSource(draftSource.id)" />
</template>
