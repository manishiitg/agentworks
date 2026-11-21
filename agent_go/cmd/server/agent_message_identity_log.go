package server

import (
	"encoding/json"
	"log"
)

// Temporary identity diagnostics for a real delivery/read refusal. Read only the
// service-owned committed mailbox image; never open its SQLite file elsewhere.
// Deliberately excludes paths, labels, bodies, tool arguments and error details.
func logAgentMessageIdentityRefusal(inbox, operation, check string, caller *triggerLinkCaller, user string) {
	type endpoint struct {
		User         string `json:"user_id"`
		Kind         string `json:"kind"`
		ID           string `json:"id"`
		Profile      string `json:"profile"`
		ChatKey      string `json:"chat_key"`
		Session      string `json:"session"`
		TeamAgentID  string `json:"team_agent_id"`
		NamedAgentID string `json:"named_agent_id"`
	}
	safe := func(e agentMessageEndpoint) endpoint {
		return endpoint{e.UserID, e.Kind, e.ID, e.Profile, e.ChatKey, e.Session, e.TeamAgentID, e.NamedAgentID}
	}
	mb, err := currentAgentMailbox()
	if err != nil {
		return
	}
	for _, c := range mb.current().store.Conversations {
		if c.ID != inbox {
			continue
		}
		fields := map[string]any{"endpoints": [2]endpoint{safe(c.Endpoints[0]), safe(c.Endpoints[1])}}
		if caller != nil {
			fields["caller"] = safe(callerMessageEndpoint(user, *caller))
		}
		data, _ := json.Marshal(fields)
		log.Printf("[AGENT_MESSAGE_IDENTITY] inbox=%s operation=%s check=%s identities=%s", inbox, operation, check, data)
		return
	}
	fields := map[string]any{"endpoints": []endpoint{}}
	if caller != nil {
		fields["caller"] = safe(callerMessageEndpoint(user, *caller))
	}
	data, _ := json.Marshal(fields)
	log.Printf("[AGENT_MESSAGE_IDENTITY] inbox=%s operation=%s check=%s identities=%s", inbox, operation, check+"_inbox_missing", data)

}
