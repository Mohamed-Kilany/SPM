import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import { fileURLToPath, URL } from 'node:url'

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
      // The API client uses relative `/api/v1` URLs in every environment.
      // This keeps local development same-origin so Authorization headers and
      // SSE work without CORS configuration.
      '/api': {
        target: 'http://localhost:8080',
        changeOrigin: false,
      },
    },
  },
  test: {
    environment: 'jsdom',
    globals: true,
    exclude: ['e2e/**', 'node_modules/**', 'dist/**'],
  },
})
