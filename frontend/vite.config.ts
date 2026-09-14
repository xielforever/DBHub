import { defineConfig, loadEnv } from 'vite'
import vue from '@vitejs/plugin-vue'
import tailwindcss from '@tailwindcss/vite'

// https://vite.dev/config/
export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), '')
  // 容器内默认指向 backend 服务；本地裸机运行时回退到 localhost
  const apiTarget = env.VITE_API_PROXY_TARGET || 'http://localhost:8080'
  const port = Number(env.VITE_PORT) || 5173

  return {
    plugins: [vue(), tailwindcss()],
    server: {
      host: true,
      port,
      // 容器/沙箱预览环境会通过任意主机名反代访问，开发环境放行全部主机
      allowedHosts: true,
      proxy: {
        '/api': {
          target: apiTarget,
          changeOrigin: true,
        },
      },
    },
  }
})
