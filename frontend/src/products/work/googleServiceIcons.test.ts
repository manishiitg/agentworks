import { expect, it } from 'vitest'
import { brandSlugFor } from '../../components/connectors/brandSlug'

// The Google Workspace card shows each service's own mark (never the "G"
// monogram fallback); People has no mark of its own and uses Google's.
it('resolves a brand mark for every Google Workspace service', () => {
  expect(brandSlugFor('google')).toBe('google')
  const expected: Record<string, string> = {
    GoogleCalendar: 'googlecalendar', GoogleChat: 'googlechat', GoogleDocs: 'googledocs', GoogleDrive: 'googledrive',
    GoogleGmail: 'gmail', GooglePeople: 'google', GoogleSheets: 'googlesheets', GoogleSlides: 'googleslides',
  }
  for (const [catalog, slug] of Object.entries(expected)) expect(brandSlugFor(catalog)).toBe(slug)
})
