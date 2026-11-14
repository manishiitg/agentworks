export type TeamLiveAgent = { last_message?: string; last_message_at?: string; private?: boolean; id: string; owner_id: string; tab: string; chat_key: string; name: string; person: string; label: string; activity: string; doing?: string; waiting?: string }
export type TeamLivePerson = { user_id: string; name: string; online: boolean; line: string }
/** One Crew's Team, as the server words it for the viewer (never the viewer's own rows). The Monitor's Team view
 * gathers these from every Crew in the person's menu; the left menu shows only the person's own agents (PLAT-746-926). */
export type TeamLiveView = { network?: string; network_id?: string; agents: TeamLiveAgent[]; idle: TeamLiveAgent[]; people?: TeamLivePerson[]; summary: string }
