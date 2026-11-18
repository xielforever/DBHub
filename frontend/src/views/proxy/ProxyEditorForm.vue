<script setup lang="ts">
import { ref, useId } from 'vue'
import type { ProxyItem } from '../../api/proxy'

export interface ProxyDraft {
  name: string
  type: ProxyItem['type']
  host: string
  port: number | ''
  username: string
  password: string
  description: string
}

defineProps<{
  editing: boolean
  saving: boolean
  error: string
  protocols: readonly { value: ProxyItem['type']; label: string }[]
}>()
const draft = defineModel<ProxyDraft>({ required: true })
defineEmits<{ save: []; cancel: [] }>()
const id = useId()
const nameInput = ref<{ focus: () => void }>()
defineExpose({ focus: () => nameInput.value?.focus() })
</script>

<template>
  <form class="space-y-4" :aria-busy="saving" @submit.prevent="$emit('save')">
    <fieldset class="grid gap-3 sm:grid-cols-2" :disabled="saving">
      <div class="grid min-w-0 gap-2">
        <label :for="`${id}-name`" class="text-sm text-muted">代理名称</label>
        <el-input :id="`${id}-name`" ref="nameInput" v-model="draft.name" maxlength="128" :disabled="saving" />
      </div>
      <div class="grid min-w-0 gap-2">
        <label :for="`${id}-type`" class="text-sm text-muted">协议</label>
        <el-select :id="`${id}-type`" v-model="draft.type" class="w-full" :disabled="saving">
          <el-option v-for="item in protocols" :key="item.value" :label="item.label" :value="item.value" />
        </el-select>
      </div>
      <div class="grid min-w-0 gap-2">
        <label :for="`${id}-host`" class="text-sm text-muted">主机</label>
        <el-input :id="`${id}-host`" v-model="draft.host" :disabled="saving" />
      </div>
      <div class="grid min-w-0 gap-2">
        <label :for="`${id}-port`" class="text-sm text-muted">端口</label>
        <input :id="`${id}-port`" v-model.number="draft.port" type="number" min="1" max="65535" class="glass-input w-full" />
      </div>
      <div class="grid min-w-0 gap-2">
        <label :for="`${id}-username`" class="text-sm text-muted">用户名（可选）</label>
        <el-input :id="`${id}-username`" v-model="draft.username" autocomplete="off" :disabled="saving" />
      </div>
      <div class="grid min-w-0 gap-2">
        <label :for="`${id}-password`" class="text-sm text-muted">密码（可选）</label>
        <el-input :id="`${id}-password`" v-model="draft.password" type="password" show-password autocomplete="new-password"
          :placeholder="editing ? '留空保留已存密码' : undefined" :disabled="saving" />
      </div>
      <div class="grid min-w-0 gap-2 sm:col-span-2">
        <label :for="`${id}-description`" class="text-sm text-muted">说明</label>
        <el-input :id="`${id}-description`" v-model="draft.description" type="textarea" :rows="2" maxlength="512" :disabled="saving" />
      </div>
    </fieldset>
    <p v-if="error" role="alert" class="text-sm text-danger break-words">{{ error }}</p>
    <div class="flex flex-wrap justify-end gap-2">
      <button type="button" class="ghost-button ui-control-target" :disabled="saving" @click="$emit('cancel')">取消</button>
      <button type="submit" class="liquid-button ui-control-target" :disabled="saving">{{ saving ? '保存中…' : '保存' }}</button>
    </div>
  </form>
</template>
