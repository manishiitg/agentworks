import { describe, expect, it } from 'vitest'
import { selectableProducts } from './selectableProducts'

const all = ['agentworks', 'code', 'dominion', 'sparkquill', 'video-studio', 'work']

describe('products an admin can grant', () => {
  it('offers only the deployment’s main products', () => {
    expect(selectableProducts(all, ['code'])).toEqual(['code'])
    expect(selectableProducts(all, ['agentworks', 'work', 'code'])).toEqual(['agentworks', 'code', 'work'])
  })
  it('never offers the dedicated products on a shared deployment, even when enabled', () => {
    expect(selectableProducts(all, ['agentworks', 'work', 'code', 'dominion', 'sparkquill', 'video-studio'])).toEqual(['agentworks', 'code', 'work'])
  })
  it('a dedicated product deployment still offers its own product', () => {
    expect(selectableProducts(all, ['dominion'])).toEqual(['dominion'])
    expect(selectableProducts(all, ['video-studio'])).toEqual(['video-studio'])
  })
  it('offers CapLayer only when the server and deployment enable it', () => {
    expect(selectableProducts([...all, 'mcp-gateway'], ['agentworks', 'work', 'mcp-gateway'])).toEqual(['agentworks', 'work', 'mcp-gateway'])
    expect(selectableProducts(all, ['mcp-gateway'])).toEqual([])
  })
  it('offers nothing the server does not host', () => {
    expect(selectableProducts(['code'], ['agentworks', 'code'])).toEqual(['code'])
    expect(selectableProducts([], ['code'])).toEqual([])
  })
  it('offers Relays only when hosted and enabled, including beside Goals and Vault', () => {
    expect(selectableProducts(['agentworks', 'relays', 'mcp-gateway'], ['agentworks', 'relays', 'mcp-gateway'])).toEqual(['agentworks', 'relays', 'mcp-gateway'])
    expect(selectableProducts(['agentworks', 'relays'], ['agentworks'])).toEqual(['agentworks'])
    expect(selectableProducts(['agentworks'], ['agentworks', 'relays'])).toEqual(['agentworks'])
  })
})
