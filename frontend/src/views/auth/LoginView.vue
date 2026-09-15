<template>
  <main
    class="relative min-h-screen flex items-center justify-center px-4 py-10 overflow-hidden"
  >
    <!-- 装饰光斑 -->
    <div
      class="pointer-events-none absolute -top-32 -left-32 w-96 h-96 rounded-full blur-3xl opacity-40"
      style="background: radial-gradient(circle, #667eea 0%, transparent 70%)"
    />
    <div
      class="pointer-events-none absolute -bottom-40 -right-24 w-[28rem] h-[28rem] rounded-full blur-3xl opacity-30"
      style="background: radial-gradient(circle, #764ba2 0%, transparent 70%)"
    />

    <section
      class="glass-card w-full max-w-md p-6 sm:p-10 relative z-10 animate-fade-up"
      aria-label="登录卡片"
    >
      <!-- 品牌区 -->
      <header class="flex flex-col items-center text-center mb-8">
        <div
          class="w-14 h-14 rounded-2xl flex items-center justify-center mb-4 shadow-lg"
          style="background-image: var(--image-liquid-gradient)"
        >
          <ShieldCheck class="w-7 h-7 text-white" :stroke-width="2.2" />
        </div>
        <h1 class="text-2xl font-bold tracking-wide">欢迎回来</h1>
        <p class="text-sm text-white/50 mt-1.5">登录以管理您的数据库资产</p>
      </header>

      <form class="space-y-5" novalidate @submit.prevent="handleLogin">
        <div>
          <label for="username" class="block text-xs font-medium text-white/60 mb-1.5">用户名</label>
          <div class="relative">
            <User class="w-4 h-4 text-white/35 absolute left-3.5 top-1/2 -translate-y-1/2" />
            <input
              id="username"
              v-model.trim="form.username"
              type="text"
              autocomplete="username"
              class="glass-input pl-10"
              placeholder="请输入用户名"
              :disabled="loading"
            />
          </div>
        </div>

        <div>
          <label for="password" class="block text-xs font-medium text-white/60 mb-1.5">密码</label>
          <div class="relative">
            <Lock class="w-4 h-4 text-white/35 absolute left-3.5 top-1/2 -translate-y-1/2" />
            <input
              id="password"
              v-model="form.password"
              :type="passwordVisible ? 'text' : 'password'"
              autocomplete="current-password"
              class="glass-input pl-10 pr-10"
              placeholder="请输入密码"
              :disabled="loading"
            />
            <button
              type="button"
              class="absolute right-3 top-1/2 -translate-y-1/2 text-white/40 hover:text-white/80 transition-colors"
              :aria-label="passwordVisible ? '隐藏密码' : '显示密码'"
              @click="passwordVisible = !passwordVisible"
            >
              <Eye v-if="!passwordVisible" class="w-4 h-4" />
              <EyeOff v-else class="w-4 h-4" />
            </button>
          </div>
        </div>

        <div class="flex items-center justify-between text-sm">
          <label class="flex items-center gap-2 text-white/55 cursor-pointer select-none">
            <input v-model="remember" type="checkbox" class="accent-indigo-400 w-3.5 h-3.5" />
            记住我
          </label>
          <button type="button" class="text-white/45 hover:text-white/80 transition-colors">
            忘记密码？
          </button>
        </div>

        <button type="submit" class="liquid-button w-full text-base" :disabled="loading">
          <span v-if="!loading">登 录</span>
          <span v-else class="inline-flex items-center gap-2">
            <span class="w-4 h-4 border-2 border-white/40 border-t-white rounded-full animate-spin" />
            登录中...
          </span>
        </button>
      </form>

      <!-- 开发期默认账号提示 -->
      <p class="mt-6 text-center text-xs text-white/35 leading-relaxed">
        开发环境默认账号
        <code class="text-white/55">admin / admin123</code>
        ，生产环境请通过环境变量注入
      </p>

      <!-- 后端状态 -->
      <footer class="mt-6 pt-5 border-t border-white/10 flex items-center justify-center gap-2 text-xs">
        <span
          class="w-2 h-2 rounded-full"
          :class="healthOk ? 'bg-emerald-400 animate-pulse' : 'bg-amber-400'"
        />
        <span class="text-white/40">后端状态：</span>
        <span :class="healthOk ? 'text-emerald-300' : 'text-amber-300'">{{ healthText }}</span>
      </footer>
    </section>
  </main>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { Eye, EyeOff, Lock, ShieldCheck, User } from 'lucide-vue-next'
import { useUserStore } from '../../stores/user'

const router = useRouter()
const route = useRoute()
const userStore = useUserStore()

const form = reactive({ username: '', password: '' })
const loading = ref(false)
const remember = ref(true)
const passwordVisible = ref(false)
const healthOk = ref(false)
const healthText = ref('检测中...')

async function handleLogin() {
  if (!form.username || !form.password) {
    ElMessage.warning('请输入用户名和密码')
    return
  }
  loading.value = true
  try {
    await userStore.login(form.username, form.password)
    if (!remember.value) {
      localStorage.removeItem('dbhub_access_token')
      localStorage.removeItem('dbhub_refresh_token')
      localStorage.removeItem('dbhub_user')
    }
    ElMessage.success('登录成功')
    const redirect = typeof route.query.redirect === 'string' ? route.query.redirect : '/'
    router.replace(redirect)
  } catch {
    /* 错误提示已由响应拦截器统一处理 */
  } finally {
    loading.value = false
  }
}

onMounted(async () => {
  try {
    const resp = await fetch('/api/health')
    const body = await resp.json()
    healthOk.value = resp.ok && body.code === 0
    healthText.value = healthOk.value ? `正常 · ${body.data.version}` : '异常'
  } catch {
    healthOk.value = false
    healthText.value = '离线'
  }
})
</script>

<style scoped>
.animate-fade-up {
  animation: fadeUp 0.6s cubic-bezier(0.22, 1, 0.36, 1);
}
@keyframes fadeUp {
  from {
    opacity: 0;
    transform: translateY(24px);
  }
  to {
    opacity: 1;
    transform: translateY(0);
  }
}
</style>
