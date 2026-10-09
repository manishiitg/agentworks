import type { ReportDataApi } from './reportEmbedContext'

type QueryResponse = {
  success?: boolean
  error?: string
  data?: { rows?: Record<string, unknown>[] }
}

// Both the app and headless preview use the same payload and response contract.
export function createReportQuery(
  workspace: string,
  post: (body: { workspace: string; sql: string; params?: unknown[] }) => Promise<QueryResponse>,
): ReportDataApi['query'] {
  return async (sql, params) => {
    const result = await post({ workspace, sql, params })
    if (!result.success || !result.data) throw new Error(result.error || 'Query failed.')
    return result.data.rows ?? []
  }
}
