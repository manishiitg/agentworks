import { test } from 'node:test'
import assert from 'node:assert/strict'
import { attachLiveBrowser } from '../index.mjs'

test('rejects credentials in a URL before opening a browser connection', async () => {
  await assert.rejects(attachLiveBrowser({}, { apiURL: 'https://user:secret@example.test/s/run', token: 'test' }), /without embedded credentials/)
})
test('requires a run binding rather than a caller-selected workspace', async () => {
  const old = process.env.MCP_SESSION_ID
  delete process.env.MCP_SESSION_ID
  try { await assert.rejects(attachLiveBrowser({}, { apiURL: 'http://localhost:12345', token: 'test' }), /session-scoped/) }
  finally { if (old !== undefined) process.env.MCP_SESSION_ID = old }
})
