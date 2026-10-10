import path from 'node:path'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import { defineConfig } from 'vite'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
  server: {
    proxy: {
      // 开发代理：前端直连 Go 服务（guo serve，GUO_ADDR 默认 :8080）
      '/api': {
        target: process.env.GUO_DEV_API ?? 'http://localhost:8080',
        changeOrigin: true,
      },
    },
  },
})
