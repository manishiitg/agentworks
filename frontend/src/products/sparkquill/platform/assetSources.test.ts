import { describe, expect, it } from 'vitest'
import { rewriteAssetSourcesRelativeTo } from './assetSources'

describe('lesson media in isolated srcDoc previews', () => {
  const dir = 'activities/least-count'
  const rawUrl = (path: string) => `/raw/${path}?token=authenticated`

  it('routes video, poster, captions, audio and images through authenticated workspace reads', () => {
    const html = `<video src="lesson.mp4" poster="poster.jpg"><source src='alternate.webm'><track src="captions.vtt"></video><audio src="narration.mp3"></audio><img src="ruler.png">`
    const rewritten = rewriteAssetSourcesRelativeTo(html, dir, rawUrl)
    for (const file of ['lesson.mp4', 'poster.jpg', 'alternate.webm', 'captions.vtt', 'narration.mp3', 'ruler.png']) {
      expect(rewritten).toContain(rawUrl(`${dir}/${file}`))
    }
  })

  it('preserves remote, absolute, data, blob and empty sources', () => {
    const html = `<video src="blob:local-video" poster="https://example.com/poster.jpg"></video><img src="data:image/png;base64,AA"><img src="//example.com/image.png"><img src="/assets/image.png"><img src="#anchor"><img src="">`
    expect(rewriteAssetSourcesRelativeTo(html, dir, rawUrl)).toBe(html)
  })
})
