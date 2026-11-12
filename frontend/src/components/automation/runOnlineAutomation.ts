// Run online for one automation (owner, 13 Nov): a schedule, webhook or watcher of a Crew on this Mac moves to the
// online agent of the agent it starts. The Crew chat asks that agent's Run online with it; the server decides the rest
// (which agent it starts, whether it can move, the login) in manage_crew_sync run_online.
export const RUN_ONLINE_AUTOMATION_HINT = 'Moves this to the online agent of the agent it starts, so it runs on the server even when your Mac is off. It stops here.'

export const runOnlineAutomationAsk = (name: string) =>
  `Run the automation "${name}" online: use manage_crew_sync run_online with agent set to the agent it starts (by name from agents) and automations=["${name}"]. Also pick the folders and files that agent works with and its other automations, and write a short handover note first.`
