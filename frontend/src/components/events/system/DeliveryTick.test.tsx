// @vitest-environment happy-dom
import { describe, expect, it } from 'vitest'
import { renderToStaticMarkup } from 'react-dom/server'
import { ChatCompactingContext, DeliveryTick } from './DeliveryTick'

// #3795: a send held while the chat compacts says why, from the server's phase text.
describe('DeliveryTick while the chat compacts', () => {
  it('shows the compaction reason on a pending bubble', () => {
    const html = renderToStaticMarkup(
      <ChatCompactingContext.Provider value="Compacting this chat">
        <DeliveryTick metadata={{ provisional: true }} />
      </ChatCompactingContext.Provider>,
    )
    expect(html).toContain('Waiting for compaction…')
    expect(html).toContain('data-held="compacting"')
    expect(html).toContain('Compacting this chat. Your message sends when it&#x27;s done.')
  })

  it('keeps plain Sending… when nothing compacts, and never relabels a sent message', () => {
    expect(renderToStaticMarkup(<DeliveryTick metadata={{ provisional: true }} />)).toContain('Sending…')
    const sent = renderToStaticMarkup(
      <ChatCompactingContext.Provider value="Compacting this chat">
        <DeliveryTick metadata={{ delivery_status: 'sent_to_cli' }} />
      </ChatCompactingContext.Provider>,
    )
    expect(sent).toContain('Sent to agent')
    expect(sent).not.toContain('Waiting for compaction')
  })
})
