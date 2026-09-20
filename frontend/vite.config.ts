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
          // 诊断：记录经预览网关到达 vite 的认证头是否被剥离
          configure: (proxy) => {
            proxy.on('proxyReq', (_proxyReq, req) => {
              if (process.env.PROXY_AUTH_DEBUG === '1') {
                // eslint-disable-next-line no-console
                console.log(
                  `[proxy] ${req.method} ${req.url} ` +
                    `authorization=${req.headers.authorization ? 'Y' : 'N'} ` +
                    `x-access-token=${req.headers['x-access-token'] ? 'Y' : 'N'} ` +
                    `cookie-token=${(req.headers.cookie || '').includes('dbhub_access_token') ? 'Y' : 'N'}`,
                )
              }
            })
          },
        },
      },
    },
  }
})
