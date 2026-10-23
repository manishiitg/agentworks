import { defineConfig } from 'vite'
import tailwind from '@tailwindcss/vite'
import { fileURLToPath } from 'node:url'
import { copyFile, mkdir } from 'node:fs/promises'
import path from 'node:path'

const name = 'dashboard-react'

export default defineConfig({
  plugins: [tailwind(), {
    name: 'native-dashboard-runtime',
    async closeBundle() {
      const frontend = fileURLToPath(new URL('..', import.meta.url))
      for (const root of ['../agent_go/static', '../agent_go/cmd/server/static']) {
        const target = path.resolve(frontend, root)
        await mkdir(target, { recursive: true })
        for (const asset of [name + '.js', name + '.css']) await copyFile(path.join(frontend, 'dist', asset), path.join(target, asset))
      }
    },
  }],
  css: { postcss: { plugins: [] } },
  define: { 'process.env.NODE_ENV': '"production"' },
  build: {
    outDir: fileURLToPath(new URL('../dist', import.meta.url)),
    emptyOutDir: false,
    lib: { entry: 'src/tsx-runtime.tsx', name: 'DashboardReact', formats: ['iife'], fileName: () => name + '.js', cssFileName: name },
    rollupOptions: { output: { inlineDynamicImports: true } },
  },
})
