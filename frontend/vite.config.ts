import path from "path"
import { fileURLToPath } from "url"
import fs from "fs"
import react from "@vitejs/plugin-react"
import { defineConfig } from "vite"

// ESM-safe __dirname: package.json has "type": "module" so the top-level
// CommonJS __dirname isn't defined when Vite loads this config.
const projectRoot = path.dirname(fileURLToPath(import.meta.url))
// Keep local browser API calls on the Vite origin; embedded browsers can block
// direct requests to a second loopback port even when that backend is healthy.
const backendUrl = process.env.MCP_AGENT_SERVER_URL || 'http://127.0.0.1:18743'
const backendProxy = {
  '/api': { target: backendUrl, changeOrigin: true, ws: true },
}

const isolatedRuntimeConfigPath = process.env.AGENTWORKS_RUNTIME_CONFIG_PATH

function isolatedRuntimeConfigMiddleware() {
  return (req: { url?: string }, res: {
    statusCode: number
    setHeader(name: string, value: string): void
    end(body?: string): void
  }, next: () => void) => {
    if (!isolatedRuntimeConfigPath || req.url?.split('?', 1)[0] !== '/runtime-config.js') {
      next()
      return
    }

    try {
      const contents = fs.readFileSync(isolatedRuntimeConfigPath, 'utf8')
      res.statusCode = 200
      res.setHeader('Content-Type', 'application/javascript; charset=utf-8')
      res.setHeader('Cache-Control', 'no-store')
      res.end(contents)
    } catch (error) {
      res.statusCode = 500
      res.setHeader('Content-Type', 'text/plain; charset=utf-8')
      res.end(`Unable to read isolated runtime config: ${String(error)}`)
    }
  }
}

const isolatedRuntimeConfigPlugin = {
  name: 'agentworks-isolated-runtime-config',
  configureServer(server: { middlewares: { use(handler: ReturnType<typeof isolatedRuntimeConfigMiddleware>): void } }) {
    server.middlewares.use(isolatedRuntimeConfigMiddleware())
  },
  configurePreviewServer(server: { middlewares: { use(handler: ReturnType<typeof isolatedRuntimeConfigMiddleware>): void } }) {
    server.middlewares.use(isolatedRuntimeConfigMiddleware())
  },
}

// https://vite.dev/config/
export default defineConfig({
  plugins: [isolatedRuntimeConfigPlugin, react()],
  resolve: {
    alias: {
      "@": path.resolve(projectRoot, "./src"),
    },
  },
  server: {
    port: 5173,
    host: '0.0.0.0',
    proxy: backendProxy,
  },
  preview: { proxy: backendProxy },
  build: {
    // Connector brand marks are decoration on one panel, so they must not ride
    // in the eager JS chunk. Vite inlines any asset under 4 kB as a base64 data
    // URI by default, which would push the five smallest PNGs into the bundle
    // the budget check measures. Emitting them as files instead costs a request
    // only when that panel is actually opened.
    assetsInlineLimit(filePath: string) {
      if (filePath.includes('components/connectors/marks/')) return false
      return undefined
    },
  },
})
