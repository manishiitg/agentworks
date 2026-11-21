package server

import "github.com/manishiitg/coding-agent-loop/workspace/crewroot"

// A paired Mac may transfer a conversation copy; this is not a message transport.
func crewTeamMacPairAdmitted(project, user, pair string) bool {
	if user == "" || pair == "" {
		return false
	}
	if ownerOfCrewProjectID(project) == user {
		b, err := crewroot.ReadSync(project)
		return err == nil && b != nil && b.Mode == "relay" && b.Side == "server" && b.Editing == "local" && b.PairID == pair
	}
	t, err := readCrewTeam(project)
	if err != nil || ownerOfCrewProjectID(project) != t.OwnerID {
		return false
	}
	recorded, ok := t.memberMac(user)
	return ok && recorded == pair
}
