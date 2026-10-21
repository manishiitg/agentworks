import { defineConfig } from 'vite'
import tailwind from '@tailwindcss/vite'
import { fileURLToPath } from 'node:url'

export default defineConfig({
  plugins: [tailwind()],
  css: { postcss: { plugins: [] } },
  define: { 'process.env.NODE_ENV': '"production"' },
  build: {
    outDir: fileURLToPath(new URL('../dist', import.meta.url)),
    emptyOutDir: false,
    lib: { entry: 'src/main.tsx', name: 'DashboardJSON', formats: ['iife'], fileName: () => 'dashboard-json.js', cssFileName: 'dashboard-json' },
    rollupOptions: { output: { inlineDynamicImports: true } },
  },
})
