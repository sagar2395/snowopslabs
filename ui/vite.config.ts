import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: 'dist',
    emptyOutDir: true,
  },
  server: {
    port: 5173,
    proxy: {
      // Proxy API calls to the running labctl server so `npm run dev` works
      // against a live cluster without CORS issues.
      '/api': {
        target: 'http://localhost:3939',
        changeOrigin: true,
        ws: true,            // proxy WebSocket upgrades too
      },
    },
  },
})
