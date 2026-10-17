<template>
  <div v-if="diagnostic" class="space-y-2 text-xs leading-relaxed" data-testid="connection-diagnosis">
    <p class="text-white/65 break-words">{{ diagnostic.hint }}</p>
    <button v-if="canUpdateCredentials && diagnostic.action === 'update_credentials'" class="ghost-button text-xs" @click="emit('recover')">更新我的凭据</button>
  </div>
</template>

<script setup lang="ts">
import type { ConnectionDiagnostic } from '../../api/datasource'
defineProps<{diagnostic?: ConnectionDiagnostic; canUpdateCredentials?: boolean}>()
const emit = defineEmits<{recover: []}>()
</script>
