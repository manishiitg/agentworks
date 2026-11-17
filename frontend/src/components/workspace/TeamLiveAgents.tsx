import type { TeamNeed } from '../../products/work/TeamActivity'
import type { TeamPanelEvent } from '../../products/work/TeamPanel'
import type { AvatarFields } from '../ui/Avatar'
export type TeamLiveAgent = AvatarFields & { purpose?:string;examples?:string[];you?:boolean;device?:string;mac_name?:string;cli?: string; engine?: string; typing?: boolean; last_message?: string; last_message_at?: string; private?: boolean; id: string; owner_id: string; tab: string; chat_key: string; name: string; person: string; label: string; activity: string; doing?: string; waiting?: string }
export type TeamLivePerson = AvatarFields & { you?:boolean;device?:string;mac_name?:string; user_id: string; name: string; online: boolean; line: string }
/** One Crew's Team, as the server words it for the viewer; own identity/device metadata is owner-only. The Monitor's Team view
 * gathers these from every Crew in the person's menu; the left menu shows only the person's own agents (PLAT-746-926). */
export type TeamMonitorWork = { working:number;label?:string;lines:string[];more?:string;last_worked_at?:string;last_worked_label?:string }
export type TeamLiveView = { work?:TeamMonitorWork; project_id?:string;connection?:string;name?:string;needs?:TeamNeed[];recent?:TeamPanelEvent|null; network?: string; network_id?: string; agents: TeamLiveAgent[]; idle: TeamLiveAgent[]; people?: TeamLivePerson[]; summary: string }
