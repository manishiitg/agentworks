// Markdown rendering for HTML reports, shared by the in-app Report tab
// (ReportViewer) and the headless preview page so both produce identical
// markup: the app's markdown engine + GFM, wrapped in .report-markdown, with
// workspace links/images rewritten to the report's own file channel.

import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { rewriteReportMarkdownReferences } from './reportMarkdownLinks'

export const REPORT_ALLOWED_ROOTS = ['db/', 'dashboard/', 'knowledgebase/', 'docs/', 'planning/', 'evaluation/', 'costs/', 'variables/']
const REPORT_ALLOWED_FILES = ['soul.md', 'workflow.json', 'MEMORY.md']

export function normalizeReportSource(path: string): string {
  return path.replace(/\\/g, '/').replace(/^\/+/, '').replace(/\/+/g, '/')
}

/** The workflow-relative paths a report may read: the durable store and the
 * other authored folders, never a run's scratch output. Returns '' when the
 * path is outside that set. */
export function allowedReportPath(path: string): string {
  const normalized = normalizeReportSource(path)
  if (!normalized || normalized.split('/').includes('..')) return ''
  return REPORT_ALLOWED_ROOTS.some(root => normalized.startsWith(root)) || REPORT_ALLOWED_FILES.includes(normalized)
    ? normalized
    : ''
}

/** Render markdown to the themed HTML string window.report.getHtml /
 * renderMarkdown hand back. `basePath` is the folder of the markdown FILE
 * being rendered (getHtml), so its own relative links/images resolve; a
 * markdown string from data has no folder and only workspace-root-relative
 * references resolve. */
export function renderReportMarkdown(markdown: string, basePath = ''): string {
  if (!markdown) return ''
  try {
    // Single-tilde strikethrough off: ~ means "approximately" in model text
    // (~2.37L), and pairing those into <del> spans strikes whole paragraphs.
    const rendered = renderToStaticMarkup(createElement(ReactMarkdown, { remarkPlugins: [[remarkGfm, { singleTilde: false }]] }, markdown))
    return `<div class="report-markdown">${rewriteReportMarkdownReferences(rendered, allowedReportPath, basePath)}</div>`
  } catch {
    return ''
  }
}

export function reportMarkdownBasePath(path: string): string {
  const allowed = allowedReportPath(path)
  return allowed.includes('/') ? allowed.slice(0, allowed.lastIndexOf('/')) : ''
}
