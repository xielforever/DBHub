<template>
  <div class="p-8 min-h-screen flex flex-col items-center justify-center space-y-12">
    <!-- Header Section -->
    <header class="text-center space-y-4 animate-fade-in">
      <h1 class="text-6xl font-bold bg-clip-text text-transparent bg-gradient-to-r from-blue-400 to-purple-500 drop-shadow-sm">
        DBHub
      </h1>
      <p class="text-gray-300 text-xl font-light tracking-wide">
        高端 / 安全 / 高效的数据管理平台
      </p>
    </header>

    <!-- Glassmorphism Cards Container -->
    <div class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-8 w-full max-w-6xl">
      <!-- Database Card -->
      <div class="glass-card p-8 flex flex-col space-y-6">
        <div class="w-14 h-14 bg-blue-500/20 rounded-2xl flex items-center justify-center">
          <el-icon class="text-3xl text-blue-400"><DataLine /></el-icon>
        </div>
        <div>
          <h3 class="text-2xl font-semibold mb-2 text-white">数据库管理</h3>
          <p class="text-gray-400 leading-relaxed text-sm">
            支持 MySQL, PostgreSQL, Redis 等主流数据库,为您提供极致的性能体验。
          </p>
        </div>
        <button class="liquid-button mt-auto">立即连接</button>
      </div>

      <!-- Security Card -->
      <div class="glass-card p-8 flex flex-col space-y-6">
        <div class="w-14 h-14 bg-purple-500/20 rounded-2xl flex items-center justify-center">
          <el-icon class="text-3xl text-purple-400"><Lock /></el-icon>
        </div>
        <div>
          <h3 class="text-2xl font-semibold mb-2 text-white">安全与审计</h3>
          <p class="text-gray-400 leading-relaxed text-sm">
            内置全方位的 RBAC 访问控制系统与操作审计,确保您的数据资产安全无虞。
          </p>
        </div>
        <button class="liquid-button mt-auto bg-gradient-to-r from-purple-500 to-pink-500">查看日志</button>
      </div>

      <!-- Analytics Card -->
      <div class="glass-card p-8 flex flex-col space-y-6">
        <div class="w-14 h-14 bg-indigo-500/20 rounded-2xl flex items-center justify-center">
          <el-icon class="text-3xl text-indigo-400"><TrendCharts /></el-icon>
        </div>
        <div>
          <h3 class="text-2xl font-semibold mb-2 text-white">智能可视化</h3>
          <p class="text-gray-400 leading-relaxed text-sm">
            集成 ECharts,将海量原始数据转化为直观的业务洞察,助力高效决策。
          </p>
        </div>
        <button class="liquid-button mt-auto bg-gradient-to-r from-indigo-500 to-blue-500">分析报表</button>
      </div>
    </div>

    <!-- Backend Status (Example of Interaction) -->
    <footer class="glass-card px-6 py-4 flex items-center space-x-4">
      <div class="flex items-center space-x-2">
        <div class="w-2 h-2 bg-green-500 rounded-full animate-pulse"></div>
        <span class="text-sm font-medium text-gray-300">后端状态:</span>
      </div>
      <span class="text-sm text-green-400 font-mono">{{ backendStatus }}</span>
    </footer>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { DataLine, Lock, TrendCharts } from '@element-plus/icons-vue'

const backendStatus = ref('正在连接...')

onMounted(async () => {
  try {
    const response = await fetch('/api/health')
    if (response.ok) {
      backendStatus.value = await response.text()
    } else {
      backendStatus.value = '连接异常'
    }
  } catch (error) {
    backendStatus.value = '服务器离线'
  }
})
</script>

<style scoped>
.animate-fade-in {
  animation: fadeIn 1s ease-out;
}

@keyframes fadeIn {
  from { opacity: 0; transform: translateY(-20px); }
  to { opacity: 1; transform: translateY(0); }
}
</style>
