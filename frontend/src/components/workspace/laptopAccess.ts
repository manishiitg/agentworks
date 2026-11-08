export function laptopSettingsEndpoint(profile?: string, project?: string): string {
  return profile && project ? `/api/agent-profiles/${encodeURIComponent(profile)}/projects/${encodeURIComponent(project)}/laptop` : ''
}

export function laptopFoldersEndpoint(endpoint: string, device: string, path: string): string {
  const [base, search] = endpoint.split('?')
  const query = new URLSearchParams(search)
  query.set('device_id', device)
  query.set('path', path)
  return `${base}/folders?${query}`
}
