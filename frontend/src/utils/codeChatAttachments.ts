/** Local-chat attachments live on the server, separately from the laptop project. */
export function codeChatAttachmentFolder(workspace: string, sessionId: string): string {
  const key = btoa(String.fromCharCode(...new TextEncoder().encode(sessionId))).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
  return `${workspace.replace(/\/$/, '')}/uploads/chats/${key}`
}
export function codeChatAttachmentPaths(workspace: string, sessionId: string, files: { path: string; type: string }[]): string[] {
  if (!workspace || !sessionId) return []
  const folder = codeChatAttachmentFolder(workspace, sessionId)
  return [...new Set(files.filter(file => {
    const filename = file.path.slice(folder.length + 1)
    return file.type === 'file' && file.path.startsWith(`${folder}/`) && filename.length > 0 &&
      !/[\/\\]/.test(filename) && filename !== '.' && filename !== '..'
  }).map(file => file.path))]
}
export const localAttachmentAccept = 'image/png,image/jpeg,image/webp,image/gif,image/bmp,image/svg+xml,image/x-icon,text/*,.txt,.md,.csv,.json,.jsonl,.yaml,.yml,.xml,.html,.css,.js,.jsx,.ts,.tsx,.go,.py,.sh,.sql,.log,.toml,.rs,.java,.c,.cpp,.h,.ini'
export function supportedLocalAttachment(file: File): boolean {
  return /\.(png|jpe?g|webp|gif|bmp|svg|ico|txt|md|csv|jsonl?|ya?ml|xml|html|css|jsx?|tsx?|go|py|sh|sql|log|toml|rs|java|c|cpp|h|ini)$/i.test(file.name)
}
