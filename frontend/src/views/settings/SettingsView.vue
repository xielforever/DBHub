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
      <h2 class="font-medium mb-4">修改密码</h2>
      <div class="grid grid-cols-1 sm:grid-cols-3 gap-4">
        <div>
          <label for="old-pwd" class="block text-xs text-white/55 mb-1.5">当前密码</label>
          <input id="old-pwd" v-model="pwd.oldPassword" type="password" autocomplete="current-password" class="glass-input" placeholder="当前密码" />
        </div>
        <div>
          <label for="new-pwd" class="block text-xs text-white/55 mb-1.5">新密码</label>
          <input id="new-pwd" v-model="pwd.newPassword" type="password" autocomplete="new-password" class="glass-input" placeholder="至少 8 个字符" />
        </div>
        <div class="flex items-end">
          <button class="liquid-button w-full sm:w-auto flex items-center justify-center gap-2" :disabled="changing" @click="changePassword">
            <KeyRound class="w-4 h-4" />{{ changing ? '提交中…' : '更新密码' }}
          </button>
        </div>
      </div>
      <p class="text-xs text-white/35 mt-3">修改成功后请使用新密码重新登录；其他已签发的 Token 在有效期内仍可用。</p>
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
        <li class="flex items-center justify-between">
          <span>密码哈希</span>
          <span class="text-emerald-300">PBKDF2-HMAC-SHA256 / 21 万次</span>
        </li>
      </ul>
    </section>
  </div>
</template>

<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { KeyRound } from 'lucide-vue-next'
import { useUserStore } from '../../stores/user'
import { adminApi } from '../../api/admin'

const userStore = useUserStore()
const darkMode = ref(true)
const changing = ref(false)
const pwd = reactive({ oldPassword: '', newPassword: '' })

const roleLabels: Record<string, string> = {
  admin: '管理员',
  developer: '开发者',
  readonly: '只读用户',
}
const roleLabel = computed(() => roleLabels[userStore.role] ?? userStore.role ?? '-')

async function changePassword() {
  if (!pwd.oldPassword || pwd.newPassword.length < 8) {
    ElMessage.warning('请填写当前密码，且新密码至少 8 个字符')
    return
  }
  if (pwd.newPassword === pwd.oldPassword) {
    ElMessage.warning('新密码不能与当前密码相同')
    return
  }
  changing.value = true
  try {
    await adminApi.changePassword(pwd.oldPassword, pwd.newPassword)
    ElMessage.success('密码已更新，请妥善保管')
    pwd.oldPassword = ''
    pwd.newPassword = ''
  } catch {
    /* 拦截器已提示 */
  } finally {
    changing.value = false
  }
}
</script>
