import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import { TanStackRouterVite } from '@tanstack/router-plugin/vite'
import path from 'path'

// https://vite.dev/config/
export default defineConfig({
  base: '/developer/',
  plugins: [
    // Generates src/routeTree.gen.ts (gitignored). That is why the build
    // script runs `vite build` before `tsc -b`.
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
      '/api/processes': {
        target: 'http://localhost:8090',
        changeOrigin: true,
      },
    },
  },
})

// Made with Bob
