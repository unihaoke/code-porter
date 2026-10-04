import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import path from 'node:path'
import { fileURLToPath, URL } from 'node:url'

const root = path.resolve(__dirname, 'src/renderer')

// 渲染进程（Vue 界面）构建配置。
// Electron 主进程是独立的一套 tsc 构建（tsconfig.main.json）。
export default defineConfig({
  root,
  plugins: [vue()],
  // Electron 通过 file:// 加载页面，必须用相对路径。
  base: './',
  resolve: {
    alias: {
      '@': path.resolve(root, 'src'),
      '@shared': path.resolve(__dirname, 'src/shared')
    }
  },
  build: {
    // outDir 相对 root，最终产物在 client/dist/electron/。
    outDir: path.resolve(__dirname, 'dist/electron'),
    emptyOutDir: true,
    chunkSizeWarningLimit: 1024
  },
  server: {
    port: 5273,
    strictPort: true
  }
})
