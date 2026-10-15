import { expect, it, vi } from 'vitest'
import type { ReportDataApi } from '../components/workflow/reportWidgets/reportEmbedContext'
import { captureReportData } from './exportSnapshot'

it('freezes each data read once and excludes signed media URLs from saved data', async () => {
  const query = vi.fn().mockResolvedValue([{ total: 96 }])
  const capture = captureReportData({ query, getText: vi.fn(), mediaUrl: vi.fn().mockResolvedValue('private-media-url') } as unknown as ReportDataApi, true)
  expect(await capture.api.query('SELECT total FROM totals')).toEqual([{ total: 96 }])
  await capture.api.query('SELECT total FROM totals')
  await capture.api.mediaUrl('db/assets/video.mp4')
  const snapshot = await capture.freeze()
  expect(query).toHaveBeenCalledOnce()
  expect(snapshot).toEqual([{ method: 'query', args: ['SELECT total FROM totals'], value: [{ total: 96 }] }])
  expect(JSON.stringify(snapshot)).not.toContain('private-media-url')
  await expect(capture.api.getText('db/new-data.json')).rejects.toThrow('Load this data before creating the animation')
})
