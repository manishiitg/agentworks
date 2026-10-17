import type { ReportDocument } from './reportDocuments'

/** Distinguish reports with reused HTML titles without renaming stored metadata. */
export function reportDocumentLabel(document: ReportDocument, documents: readonly ReportDocument[]): string {
  const duplicates = documents.filter(other => other.title === document.title)
  if (duplicates.length < 2) return document.title
  const filename = document.path.split('/').pop() || document.title
  const sameFilename = duplicates.filter(other => other.path.split('/').pop() === filename)
  if (sameFilename.length > 1) return document.path.replace(/^db\/reports\//, '').replace(/\.html$/i, '')
  return filename.replace(/\.html$/i, '').replace(/[-_]+/g, ' ')
}
