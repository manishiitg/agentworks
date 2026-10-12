package server

// crewAskModelSentence is the one sentence every Crew-facing description states about a free-text ask (856-59).
const crewAskModelSentence = "A non-owner's ask is read-only (it reads files, memory, skills and functions; it writes nothing and uses no MCP tools); to make a Crew do something, call one of its functions. The owner's own ask has full access. Only the owner changes a Crew."

// crewAskDeniedTools are the outward tools a guest's free-text ask never gets, beyond the reader deny list: they act on
// the world as the Crew's owner (Slack posts, Google writes). Functions the owner exposes are how a guest acts.
func crewAskDeniedTools() []string {
	return []string{"send_slack_message", "google_workspace_cli", "agent_browser", "schedule_message_wakeup"}
}

var crewAskDeniedToolSet = func() map[string]bool {
	set := map[string]bool{}
	for _, name := range crewAskDeniedTools() {
		set[name] = true
	}
	return set
}()
