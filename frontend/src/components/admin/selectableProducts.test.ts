import { describe, expect, it } from 'vitest'
import { selectableProducts } from './selectableProducts'

const all = ['agentworks', 'code', 'example-app', 'work']

describe('products an admin can grant', () => {
  it('offers only the deployment’s main products', () => {
    expect(selectableProducts(all, ['code'])).toEqual(['code'])
    expect(selectableProducts(all, ['agentworks', 'work', 'code'])).toEqual(['agentworks', 'code', 'work'])
  })
  it('never offers the dedicated products on a shared deployment, even when enabled', () => {
    expect(selectableProducts(all, ['agentworks', 'work', 'code', 'example-app'])).toEqual(['agentworks', 'code', 'work'])
  })
  it('a dedicated product deployment still offers its own product', () => {
    expect(selectableProducts(all, ['example-app'])).toEqual(['example-app'])
  })
  it('never renders per-person controls for core account services', () => {
    expect(selectableProducts([...all, 'mcp-gateway'], ['agentworks', 'work', 'mcp-gateway'])).toEqual(['agentworks', 'work'])
    expect(selectableProducts(['mcp-gateway', 'knowledgebase', 'bullpen'], ['mcp-gateway', 'knowledgebase', 'bullpen'])).toEqual([])
  })
  it('offers nothing the server does not host', () => {
    expect(selectableProducts(['code'], ['agentworks', 'code'])).toEqual(['code'])
    expect(selectableProducts([], ['code'])).toEqual([])
  })
  it('offers Relays only when hosted and enabled, including beside Goals and Vault', () => {
    expect(selectableProducts(['agentworks', 'relays', 'mcp-gateway'], ['agentworks', 'relays', 'mcp-gateway'])).toEqual(['agentworks', 'relays'])
    expect(selectableProducts(['agentworks', 'relays'], ['agentworks'])).toEqual(['agentworks'])
    expect(selectableProducts(['agentworks'], ['agentworks', 'relays'])).toEqual(['agentworks'])
  })
  it('keeps Brain out of the editable list alongside the main products', () => {
    expect(selectableProducts(['agentworks', 'knowledgebase'], ['agentworks', 'knowledgebase'])).toEqual(['agentworks'])
  })
})
