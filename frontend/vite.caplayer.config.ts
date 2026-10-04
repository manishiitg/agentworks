import path from 'path'
import { fileURLToPath } from 'url'
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

const root = path.dirname(fileURLToPath(import.meta.url))
const backend = process.env.MCP_AGENT_SERVER_URL || 'http://127.0.0.1:8000'
const gateway = process.env.CAPLAYER_BACKEND_URL || 'http://127.0.0.1:8080'

export default defineConfig({
  plugins: [react()],
  resolve: { alias: { '@': path.resolve(root, 'src') } },
  server: { host: '127.0.0.1', proxy: { '/api': backend, '/mcp': gateway } },
  build: {
    outDir: 'dist-caplayer',
    rollupOptions: { input: path.resolve(root, 'caplayer.html') },
  },
})
