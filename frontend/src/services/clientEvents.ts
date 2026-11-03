import api from './api'

export type ClientEventKind = 'network_offline' | 'network_online' | 'page_hidden' | 'page_visible' | 'reconnect_attempt' | 'reconnect_gave_up' | 'send_stuck' | 'file_open_failed' | 'mac_off_notice' | 'app_reloaded'

/** Tells the server's log what this device saw on a chat's connection (PLAT-856-509): IDs and short codes only,
 * never message text. The server decides nothing from it; a lost report is fine. */
export function reportClientEvent(kind: ClientEventKind, ids: { project?: string; conversation?: string; submission?: string; detail?: string } = {}) {
  void api.post('/api/client-events', { kind, ...ids }).catch(() => {})
}
