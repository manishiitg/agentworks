package server

// Only the local project's owner supplies discovery defaults. The registry's
// saved choice wins later; no creation/history timestamp changes privacy.
func crewTeamJoinAgentDefaults(user, project, workspace string) func(ProductConversationRecord) *bool {
	state := readCrewTeamState(project)
	if !IsLocalMode() || ownerOfCrewProjectID(project) != user || state.MemberOf == "" {
		return func(ProductConversationRecord) *bool { return nil }
	}
	return func(ProductConversationRecord) *bool {
		inTeam := crewTeamDefaultInTeam(nil)
		return &inTeam
	}
}
