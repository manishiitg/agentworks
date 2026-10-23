import type { ReportDataApi } from '../components/workflow/reportWidgets/reportEmbedContext'
import type { ReportHostTheme } from '../components/workflow/reportWidgets/reportHostRuntime'

export interface StaticSnapshot {
  version: 1
  html: string
  data: { method: string; args: unknown[]; value?: unknown }[]
  as_of: string
  theme: ReportHostTheme
  live_url: string
}

/** Only authorized file/media bytes are exported, never their signed URLs. */
export function staticAssetDataApi(api: ReportDataApi): ReportDataApi {
  const result = { ...api }
  for (const method of ['fileUrl', 'mediaUrl'] as const) {
    result[method] = async path => {
      const url = await api[method](path)
      if (!url) return null
      const response = await fetch(url)
      if (!response.ok) throw new Error('A dashboard asset could not be included in the snapshot.')
      const blob = await response.blob()
      if (blob.size > 20 * 1024 * 1024) throw new Error('A dashboard asset exceeds the 20 MB static export limit. Link to its live dashboard instead.')
      return new Promise<string>((resolve, reject) => {
        const reader = new FileReader()
        reader.onload = () => resolve(String(reader.result))
        reader.onerror = () => reject(new Error('A dashboard asset could not be saved.'))
        reader.readAsDataURL(blob)
      })
    }
  }
  return result
}

/** Replay the actual calls made while rendering; no query extraction or database copy. */
export function staticDataApi(snapshot: StaticSnapshot, renderMarkdown: ReportDataApi['renderMarkdown']): ReportDataApi {
  const records = new Map(snapshot.data.map(entry => [JSON.stringify([entry.method, entry.args]), entry.value]))
  const unavailable = () => Promise.reject(new Error('This data is not included in the published snapshot. Open the live dashboard.'))
  const api = {
    workspacePath: '', renderMarkdown,
    openFile: () => { if (snapshot.live_url) window.open(snapshot.live_url, '_blank', 'noopener,noreferrer') },
    sendChatMessage: async () => { throw new Error('Open the live dashboard to ask its agent.') },
    updateField: async () => { throw new Error('Open the live dashboard to edit data.') },
    updateFields: async () => { throw new Error('Open the live dashboard to edit data.') },
  } as ReportDataApi
  for (const method of ['query', 'get', 'getText', 'getHtml', 'getRelayRuns', 'getCosts', 'run', 'fileUrl', 'mediaUrl']) {
    Object.assign(api, { [method]: (...args: unknown[]) => {
      const key = JSON.stringify([method, args])
      return records.has(key) ? Promise.resolve(structuredClone(records.get(key))) : unavailable()
    } })
  }
  return api
}

/** Relative runtime URLs work on domain roots and subfolders on any static host. */
export function staticIndex(snapshot: StaticSnapshot): string {
  const data = JSON.stringify(snapshot).replace(/</g, '\\u003c')
  return `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Dashboard</title></head><body><script id="agentworks-static-snapshot" type="application/json">${data}</script><script src="./report-preview.js"></script></body></html>`
}
