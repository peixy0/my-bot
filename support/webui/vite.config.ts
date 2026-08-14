import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: './src/test/setup.ts',
    css: true
  },
  server: {
    proxy: {
      '/api/bot': {
        target: 'http://localhost:8017',
        ws: true,
        rewriteWsOrigin: true,
        changeOrigin: true,
        secure: false
      }
    }
  }
})
