import { createContext, useContext } from 'react'
import type { WorkflowCostsResponse } from '../../../services/api-types'

export interface ReportCostOptions { days?: number; before?: string }

export type ReportChatReceipt = { status: 'queued'; tabId: string; reused: boolean; queuedBehindRunningTurn: boolean }

export type ReportChatOptions = {
  /** Stable identity for this item/version/action. Deduplicates in this report view. */
  requestId?: string
}

// Live data access for HTML report documents. HTML renders its OWN visuals
// (charts/tables/branded CSS) — we just hand it the data. `query` runs read-only
// SQL against the workflow's db/db.sqlite (the primary data source); `get`/
// `getText` fetch any db/ knowledgebase/ docs file (markdown, text, assets) on
// demand. Exposed inside the iframe as `window.report`.
export interface ReportDataApi {
  workspacePath: string
  getCosts?: (options: ReportCostOptions) => Promise<WorkflowCostsResponse>
  // Sends directly from a report action through the human-decision chat queue.
  // Reuses an interactive automation chat, or creates one only if none exists.
  // Queued is not proof of execution or completion.
  sendChatMessage: (message: string, options?: ReportChatOptions) => Promise<ReportChatReceipt>
  query: (sql: string) => Promise<Record<string, unknown>[]>
  get: (path: string) => Promise<unknown>
  getText: (path: string) => Promise<string | null>
  // getHtml renders a markdown (db/ knowledgebase/ docs/) file to an HTML string
  // (the app's markdown engine + GFM), wrapped in <div class="report-markdown">,
  // so an HTML report can drop a rendered .md inline: el.innerHTML = await
  // window.report.getHtml(path). The iframe ships a default .report-markdown prose
  // style (theme-aware, overridable).
  getHtml: (path: string) => Promise<string | null>
  // renderMarkdown renders a markdown STRING (not a file) to the same themed HTML
  // (<div class="report-markdown">…</div>, app markdown engine + GFM). Synchronous.
  // Use for markdown held in data — a db/sql value, knowledgebase field, inline
  // text: el.innerHTML = window.report.renderMarkdown(row.notes_md).
  renderMarkdown: (md: string) => string
  // File access (parity with file widgets): fileUrl returns an authenticated
  // blob URL usable in <img src> / <a href> / <iframe src> for images, PDFs,
  // etc. For backwards compatibility, db/assets video/audio paths are routed
  // through the range-streaming media endpoint instead of becoming large Blobs.
  // openFile opens the file in the in-report preview modal. Both are scoped to
  // db/ knowledgebase/ docs/.
  fileUrl: (path: string) => Promise<string | null>
  /** Expiring authenticated range-streaming URL for db/assets video/audio. */
  mediaUrl: (path: string) => Promise<string | null>
  openFile: (path: string) => void
  // updateField writes exactly one cell; updateFields writes several columns on
  // the same row in one atomic call (a form submit). Both share the same
  // validation: table/column are checked against the live schema server-side
  // and the row is matched on that table's own primary key — there is no way
  // to pass raw SQL through either call. Rejects columns that identify or
  // timestamp the row (the primary key, any *_id column, created_at/
  // updated_at) and values that aren't a plain string/number/boolean/null.
  // updateFields applies every field or none (single transaction) and resolves
  // the confirmed old/new values keyed by column name; throws on validation
  // failure or if the row/any named column doesn't exist.
  updateField: (
    table: string,
    rowId: string | number,
    column: string,
    value: string | number | boolean | null,
  ) => Promise<{ oldValue: unknown; newValue: unknown }>
  updateFields: (
    table: string,
    rowId: string | number,
    fields: Record<string, string | number | boolean | null>,
  ) => Promise<{ oldValues: Record<string, unknown>; newValues: Record<string, unknown> }>
  // Runs one of the workflow's own scripts under code/ on the server (with the
  // workflow's MCP servers, secrets and a read-only DB snapshot) and resolves
  // the JSON it printed. Live data: every call runs the script again.
  run: (path: string, args?: unknown) => Promise<unknown>
}

export interface ReportRuntime {
  data: ReportDataApi
}

const ReportRuntimeContext = createContext<ReportRuntime | null>(null)

export const ReportEmbedProvider = ReportRuntimeContext.Provider

// HTML data injection: returns the live data API, or null outside a report.
export function useReportDataApi(): ReportDataApi | null {
  return useContext(ReportRuntimeContext)?.data ?? null
}
