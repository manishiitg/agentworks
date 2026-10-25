const defaultCdpPort = 9222
export const chromeCdpInstallerUrl =
  'https://raw.githubusercontent.com/manishiitg/coding-agent-loop/main/scripts/install-chrome-cdp-macOS.sh'
export const chromeCdpZipUrl =
  'https://raw.githubusercontent.com/manishiitg/coding-agent-loop/main/agent_go/cmd/server/embed_downloads/Chrome-CDP-macOS.zip'

function safeCdpPort(port: number): number {
  return Number.isFinite(port) && port >= 1 && port <= 65535 ? Math.trunc(port) : defaultCdpPort
}

function shellQuote(value: string): string {
  return `'${value.replace(/'/g, `'\\''`)}'`
}

export function chromeCdpLaunchCommand(port: number, platform?: string): string {
  const resolvedPort = safeCdpPort(port)
  // Chrome requires a distinct user-data-dir for each concurrent CDP process.
  // Keep the legacy default profile path for 9222 and make additional ports
  // deterministic so they represent independent login identities.
  const userDataDir = resolvedPort === defaultCdpPort
    ? '$HOME/.chrome-cdp-profile'
    : `$HOME/.chrome-cdp-profile-${resolvedPort}`
  const args = `--remote-debugging-port=${resolvedPort} --user-data-dir="${userDataDir}" --no-first-run --no-default-browser-check`

  if (platform?.includes('Mac')) {
    return `/Applications/Google\\ Chrome.app/Contents/MacOS/Google\\ Chrome ${args}`
  }

  return `google-chrome ${args}`
}

export function chromeCdpVerifyCommand(port: number): string {
  return `curl http://127.0.0.1:${safeCdpPort(port)}/json/version`
}

export function chromeCdpInstallCommand(port = defaultCdpPort): string {
  const resolvedPort = safeCdpPort(port)
  const portArgs = resolvedPort === defaultCdpPort ? '' : ` -s -- --port ${resolvedPort}`
  return `curl -fsSL ${shellQuote(chromeCdpInstallerUrl)} | bash${portArgs}`
}
