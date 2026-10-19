/** Unknown versions are not evidence of an outdated installation. */
export function extensionVersionOlder(installed?: string, latest?: string): boolean {
  if (!installed || !latest) return false
  const left = installed.split('.').map(Number), right = latest.split('.').map(Number)
  for (let i = 0; i < Math.max(left.length, right.length); i++) {
    const x = left[i] || 0, y = right[i] || 0
    if (x !== y) return x < y
  }
  return false
}
