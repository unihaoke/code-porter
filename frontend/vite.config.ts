import { fileURLToPath, URL } from 'node:url'
import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// 开发态直连本机的 codeporter-gateway（默认 :9022）。
const GATEWAY = process.env.VITE_GATEWAY_TARGET ?? 'http://127.0.0.1:9022'

export default defineConfig({
  plugins: [vue()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  server: {
    port: 5173,
    proxy: {
      '/api': { target: GATEWAY, changeOrigin: true },
      '/v1': { target: GATEWAY, changeOrigin: true },
      '/webhook': { target: GATEWAY, changeOrigin: true },
      '/healthz': { target: GATEWAY, changeOrigin: true },
    },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    chunkSizeWarningLimit: 800,
  },
})
