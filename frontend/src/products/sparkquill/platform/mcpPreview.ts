/** MCP links identify family content; the normal login/PIN flow still applies. */
export function isSparkQuillPreviewPath(path: string): boolean {
  if (!/^(activities|materials|inbox|reports|memory)\//.test(path) || /[\\:\u0000\r\n]/.test(path)) return false
  return path.split('/').every(part => part !== '' && part !== '..' && !part.startsWith('.') && !['family.json', 'product.json', 'current-activity.json', 'secrets', 'keys', 'db', 'chat_history'].includes(part.toLowerCase()))
}
