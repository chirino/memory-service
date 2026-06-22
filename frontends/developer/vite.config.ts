import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import { TanStackRouterVite } from '@tanstack/router-plugin/vite'
import path from 'path'

// https://vite.dev/config/
export default defineConfig({
  base: '/developer/',
  plugins: [
    TanStackRouterVite(),
    react({
      babel: {
        plugins: [['babel-plugin-react-compiler']],
      },
    }),
  ],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
  server: {
    port: 3000,
    strictPort: true,
    proxy: {
      '/v1': {
        target: 'http://localhost:8082',
        changeOrigin: true,
      },
      '/admin': {
        target: 'http://localhost:8082',
        changeOrigin: true,
      },
    },
  },
})

// Made with Bob
