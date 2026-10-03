/** srcDoc previews resolve local media against the lesson's workspace folder. */
export function rewriteAssetSourcesRelativeTo(html: string, dir: string, rawUrl: (path: string) => string): string {
  return html.replace(/\b(src|poster)\s*=\s*(["'])(.*?)\2/gi, (whole, attribute: string, quote: string, ref: string) => {
    if (/^[a-z][a-z0-9+.-]*:/i.test(ref) || ref.startsWith('/') || ref.startsWith('#') || !ref) return whole
    const resolved = dir ? `${dir}/${ref}` : ref
    return `${attribute}=${quote}${rawUrl(resolved)}${quote}`
  })
}
