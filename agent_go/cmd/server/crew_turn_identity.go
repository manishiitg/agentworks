package server

// Crew execution and Vault lending use the normal project owner policy. There
// is no standby session identity or member override (PLAT-746-1024).
func crewTurnSlot(folder, session, user string) (slot, person string, err error) {
	slot, person = crewOwnerSlot(folder)
	return slot, person, nil
}

func (api *StreamingAPI) turnSecretsOwner(session, userID, workspacePath string) string {
	return api.projectSecretsOwner(userID, workspacePath)
}
