<template>
  <div class="max-w-3xl space-y-5">
    <header>
      <h1 class="text-xl font-semibold">系统设置</h1>
      <p class="text-sm text-white/45 mt-1">账号偏好与安全配置</p>
    </header>

    <section class="glass-card p-6">
      <h2 class="font-medium mb-4">个人资料</h2>
      <div class="grid grid-cols-1 sm:grid-cols-2 gap-4">
        <div>
          <label for="settings-username" class="block text-xs text-white/55 mb-1.5">用户名</label>
          <input id="settings-username" class="glass-input" :value="userStore.username" disabled />
        </div>
        <div>
          <label for="settings-role" class="block text-xs text-white/55 mb-1.5">角色</label>
          <input id="settings-role" class="glass-input" :value="roleLabel" disabled />
        </div>
      </div>
    </section>

    <section class="glass-card p-6">
      <h2 class="font-medium mb-4">外观</h2>
      <div class="flex items-center justify-between">
        <div>
          <p class="text-sm">深色液态玻璃主题</p>
          <p class="text-xs text-white/40 mt-1">浅色主题与主题跟随系统将在后续版本提供</p>
        </div>
        <el-switch v-model="darkMode" disabled aria-label="深色液态玻璃主题开关" />
      </div>
    </section>

    <section class="glass-card p-6">
      <h2 class="font-medium mb-4">安全</h2>
      <ul class="space-y-3 text-sm text-white/65">
        <li class="flex items-center justify-between">
          <span>Access Token 有效期</span>
          <span class="text-white/45">15 分钟</span>
        </li>
        <li class="flex items-center justify-between">
          <span>Refresh Token 有效期</span>
          <span class="text-white/45">7 天</span>
        </li>
        <li class="flex items-center justify-between">
          <span>敏感凭据存储</span>
          <span class="text-emerald-300">AES-256-GCM 加密</span>
        </li>
      </ul>
    </section>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useUserStore } from '../../stores/user'

const userStore = useUserStore()
const darkMode = ref(true)
const roleLabels: Record<string, string> = {
  admin: '管理员',
  developer: '开发者',
  readonly: '只读用户',
}
const roleLabel = computed(() => roleLabels[userStore.role] ?? userStore.role ?? '-')
</script>
