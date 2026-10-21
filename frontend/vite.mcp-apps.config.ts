import path from 'path'
import { fileURLToPath } from 'url'
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

const root = path.dirname(fileURLToPath(import.meta.url))
const entry = process.env.MCP_APP_ENTRY === 'document' ? 'document' : 'main'
const name = entry === 'document' ? 'mcp-dashboard-document.js' : 'mcp-dashboard.js'

export default defineConfig({
  plugins: [react()],
  publicDir: false,
  resolve: { alias: { '@': path.resolve(root, 'src') } },
  define: { 'process.env.NODE_ENV': '"production"', 'import.meta.env.DEV': 'false' },
  build: {
    outDir: path.resolve(root, 'dist'), emptyOutDir: false, minify: true, sourcemap: false,
    lib: { entry: path.resolve(root, `src/mcp-apps/${entry}.ts`), name: entry === 'main' ? 'AgentWorksDashboard' : 'AgentWorksDashboardDocument', formats: ['iife'], fileName: () => name },
    rollupOptions: { output: { inlineDynamicImports: true } },
  },
})
