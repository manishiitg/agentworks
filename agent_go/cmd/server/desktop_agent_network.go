package server

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Stable across title changes and login renewal; different chats/devices never
// silently compete for an address. The directory carries the readable label.
func desktopAppAgentName(device, conversation string) string {
	sum := sha256.Sum256([]byte(device + "\x00" + conversation))
	return fmt.Sprintf("app-%x", sum[:16])
}

type desktopAppAgentRegistration struct {
	Name       string `json:"agent_name"`
	ID         string `json:"agent_id"`
	Attachment string `json:"attachment_id"`
}

func registerDesktopAppAgent(ctx context.Context, owner string, connection desktopConnection, record ProductConversationRecord) (desktopAppAgentRegistration, error) {
	var out desktopAppAgentRegistration
	device := desktopMachineID()
	if device == "" || record.ConversationID == "" {
		return out, fmt.Errorf("this app chat has no stable identity")
	}
	args := map[string]any{"action": "register", "agent_name": desktopAppAgentName(device, record.ConversationID), "device_id": device, "conversation_id": record.ConversationID, "label": record.Title, "purpose": cleanAgentPurpose(record.Description)}
	err := crewTeamRemoteJSON(ctx, owner, connection, http.MethodPost, "/api/external/v1/agents/app", args, &out)
	if err == nil && (out.ID == "" || out.Name != args["agent_name"] || out.Attachment == "") {
		err = fmt.Errorf("the server did not register this app chat")
	}
	return out, err
}

// All saved sign-ins are independent networks. Address qualification preserves
// the exact connection; neither Crew pairing nor a name chooses an account.
func desktopAgentNetworkRows(ctx context.Context, owner string) ([]map[string]interface{}, string) {
	if !IsLocalMode() {
		return nil, ""
	}
	connections, err := readDesktopConnections(owner)
	if err != nil {
		return nil, "Your saved network connections are unavailable."
	}
	rows := []map[string]interface{}{}
	note := ""
	for _, connection := range connections {
		var directory struct {
			Agents []map[string]interface{} `json:"agents"`
		}
		read, done := context.WithTimeout(ctx, 8*time.Second)
		err := crewTeamRemoteJSON(read, owner, connection, http.MethodGet, "/api/agent-directory?view=message", nil, &directory)
		done()
		if err != nil {
			note = "Some network agents could not be loaded. Try again."
			continue
		}
		for _, row := range directory.Agents {
			target, _ := row["target"].(string)
			if target == "" {
				target, _ = row["id"].(string)
			}
			if !strings.HasPrefix(target, "agent:") || (row["agent_id"] == nil && row["scope"] != "everyone") {
				continue
			}
			copy := make(map[string]interface{}, len(row)+2)
			for key, value := range row {
				copy[key] = value
			}
			address := "network:" + connection.ID + ":" + target
			copy["id"], copy["target"], copy["network"] = address, address, connection.Server
			copy["reach"] = "messages action=send(target=<this exact id>, message=…); read replies using the returned inbox_id"
			rows = append(rows, copy)
		}
	}
	return rows, note
}

func (api *StreamingAPI) desktopAgentNetworkMessage(ctx context.Context, owner string, caller triggerLinkCaller, raw string, in desktopAgentNetworkRequest) (map[string]any, error) {
	connectionID, address, ok := splitDesktopAgentNetworkAddress(raw)
	if !IsLocalMode() || !ok || caller.Chat == nil {
		return nil, fmt.Errorf("use an agent address from your network directory")
	}
	// A shared Run turn must not borrow the owner's saved login. The local
	// current chat, not an agent-supplied agent_name, selects the named sender.
	source := api.agentMessageCallerRole(callerMessageEndpoint(owner, caller))
	if source.FunctionCallID != "" {
		return nil, fmt.Errorf("this turn cannot use the person's AgentBridge login")
	}
	api.lastQueryMu.RLock()
	prior, known := api.lastQueryRequests[caller.Chat.SessionID]
	api.lastQueryMu.RUnlock()
	restricted := !known || source.Mode != "builder" || source.Guest != "" || desktopAppTurnRestricted(prior, owner)
	if restricted && strings.HasPrefix(address, "inbox-") && (prior.appAgentReplyTo == "" || prior.appAgentReplyTo != raw) {
		return nil, fmt.Errorf("another person's request may only reply in its admitted AgentBridge conversation")
	}
	connection, err := desktopConnectionFor(owner, connectionID)
	if err != nil {
		return nil, err
	}
	records, err := desktopAppAgentRecords(ctx, owner)
	if err != nil {
		return nil, err
	}
	var exact *ProductConversationRecord
	for _, record := range records {
		if record.SessionID == caller.Chat.SessionID && record.ConversationKey == caller.Chat.Key {
			copy := record
			exact = &copy
			break
		}
	}
	if exact == nil {
		return nil, fmt.Errorf("this app chat was removed or replaced")
	}
	registration, err := registerDesktopAppAgent(ctx, owner, connection, *exact)
	if err != nil {
		return nil, err
	}
	args := map[string]any{"action": "send", "agent_name": registration.Name, "attachment_id": registration.Attachment, "message": in.Message, "submission_id": in.SubmissionID, "non_owner_turn": restricted}
	if strings.HasPrefix(address, "inbox-") {
		args["inbox_id"] = address
	} else {
		args["target"] = address
		args["public_only"] = restricted // server-derived turn provenance, never a tool argument
	}
	if in.Read {
		args["action"], args["after"], args["limit"] = "read", in.After, in.Limit
	}
	out := map[string]any{}
	if err := crewTeamRemoteJSON(ctx, owner, connection, http.MethodPost, "/api/external/v1/agents/app", args, &out); err != nil {
		return nil, err
	}
	qualifyDesktopAgentReplyAddresses(out, connectionID)
	return out, nil
}

// A published agent's visitor may receive a reply, but cannot use the owner's
// saved login to reach that owner's private agents. JSON cannot set the fence.
func desktopAppTurnMayAddress(prior QueryRequest, address string) bool {
	if !desktopAppTurnRestricted(prior, "") {
		return true
	}
	return prior.appAgentReplyTo != "" && prior.appAgentReplyTo == address
}

func desktopAppTurnRestricted(prior QueryRequest, owner string) bool {
	return strings.TrimSpace(prior.SharedAgentSender) != "" || prior.AgentMessageFromOtherPerson || prior.PinRunMode || prior.CrewGuestCaller != "" || prior.BotPlatform != "" || (prior.AskedBy != "" && prior.AskedBy != owner)
}

func publicAgentDirectoryOnly(out map[string]interface{}) {
	rows, _ := out["agents"].([]map[string]interface{})
	kept := make([]map[string]interface{}, 0, len(rows))
	for _, row := range rows {
		if row["published"] == true || row["scope"] == "everyone" {
			kept = append(kept, row)
		}
	}
	out["agents"] = kept
	out["notice"] = "This turn can use published agents and reply to its original sender; your private agents are unavailable."
}
