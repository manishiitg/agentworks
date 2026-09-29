import { BRAND_MARKS } from './brandMarks'
import { RASTER_MARKS } from './rasterMarks'

/**
 * Server names that do not normalize cleanly onto their brand slug.
 * Keys are the normalized server name (lowercased, non-alphanumerics stripped).
 */
const SLUG_ALIASES: Record<string, string> = {
  mondaycom: 'monday',
  bitbucket: 'atlassian',
  awsknowledge: 'amazonaws',
  microsoftlearn: 'microsoft',
  googlegmail: 'gmail',
  googledrive: 'googledrive',
  googledocs: 'googledocs',
  googlesheets: 'googlesheets',
  googleslides: 'googleslides',
  googlecalendar: 'googlecalendar',
  googlechat: 'googlechat',
  googlepeople: 'google',
  prismapostgres: 'prisma',
}

/**
 * Vendors that publish a family of servers under one brand — "Cloudflare
 * Radar", "Cloudflare AutoRAG" and six more all carry the Cloudflare mark.
 * Matched by prefix so a new sibling server needs no alias entry.
 */
const SLUG_PREFIXES: string[] = ['cloudflare']

/**
 * Resolves an MCP server's display name to a `BRAND_MARKS` slug.
 *
 * Most names match once lowercased and stripped of punctuation ("PayPal" →
 * "paypal"). The rest are aliased above. A slug counts as resolved if either
 * map has it, since a brand with only a bitmap logo lives in `rasterMarks`
 * rather than `brandMarks`. Servers with no mark in either — Unstructured,
 * custom servers — resolve to undefined and fall back to the monogram tile.
 */
export function brandSlugFor(serverName: string): string | undefined {
  const normalized = serverName.toLowerCase().replace(/[^a-z0-9]/g, '')
  const prefix = SLUG_PREFIXES.find((p) => normalized.startsWith(p))
  const slug = SLUG_ALIASES[normalized] ?? prefix ?? normalized
  return slug in BRAND_MARKS || slug in RASTER_MARKS ? slug : undefined
}
