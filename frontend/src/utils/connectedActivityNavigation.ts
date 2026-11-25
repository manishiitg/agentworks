import { openOnServerWebsite } from './openOnServerWebsite'

/** Work on a server opens on that server's website (#3779): the Mac app shows only this Mac's projects. */
export function openConnectedActivity(connection: string, crewProjectId?: string): void {
  void openOnServerWebsite(connection, crewProjectId ? `/projects/${encodeURIComponent(crewProjectId)}` : '/')
}
