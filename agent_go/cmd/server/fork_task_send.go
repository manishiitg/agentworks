package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"
)

// agentSendHolder keeps this chat's own send_message tool, so fork_project_chat can send a fork its first task
// through exactly the path messages action=ask takes (same admission, the "From" label, the reply back to this chat).
type agentSendHolder struct {
	send func(context.Context, map[string]interface{}) (string, error)
}

// agentSendCaptureRegistrar registers every tool unchanged and keeps send_message's executor in holder.
type agentSendCaptureRegistrar struct {
	definitionToolRegistrar
	holder *agentSendHolder
}

func (r agentSendCaptureRegistrar) RegisterCustomTool(name, description string, params map[string]interface{}, execute func(context.Context, map[string]interface{}) (string, error), category string) error {
	if name == "send_message" && r.holder != nil {
		r.holder.send = execute
	}
	return r.definitionToolRegistrar.RegisterCustomTool(name, description, params, execute, category)
}

func (r agentSendCaptureRegistrar) RegisterCustomToolWithTimeout(name, description string, params map[string]interface{}, execute func(context.Context, map[string]interface{}) (string, error), timeout time.Duration, category string) error {
	if name == "send_message" && r.holder != nil {
		r.holder.send = execute
	}
	return r.definitionToolRegistrar.RegisterCustomToolWithTimeout(name, description, params, execute, timeout, category)
}

// spawnUnder and spawnBeside are where a new fork or helper sits in the person's left menu (owner, 22 Nov): under the
// agent that made it, as its helper, or beside it, as its own agent at the same level.
const (
	spawnUnder  = "under"
	spawnBeside = "beside"
)

func spawnPlace(args map[string]interface{}) (string, error) {
	switch place := strings.TrimSpace(stringToolArg(args, "place")); place {
	case "", spawnUnder:
		return spawnUnder, nil
	case spawnBeside:
		return spawnBeside, nil
	default:
		return "", fmt.Errorf("place is under or beside")
	}
}

// placeBesideMaker files a new agent at the maker's own level: under the maker's parent, or at the top in the
// maker's group. Layout only; a failure is logged and the agent stays under the maker.
func placeBesideMaker(ctx context.Context, userID string, project codeChatProject, key string) {
	store := defaultProductConversationRegistryStore()
	parent := project.Self.forkedFromKey
	if _, err := store.setChatContextOptions(ctx, userID, project.ProfileID, key, chatContextChange{ForkedFrom: &parent}, userID); err != nil {
		log.Printf("[CHAT_FORK] %s: could not place it beside %s: %v", key, project.Self.Key, err)
		return
	}
	if parent != "" {
		return
	}
	if groups, err := store.agentGroups(ctx, userID, project.ProfileID, project.ProjectID); err == nil && groups[project.Self.Key] != "" {
		if _, err := store.editAgentGroups(ctx, userID, project.ProfileID, project.ProjectID, agentGroupEdit{Op: "move", Key: key, Group: groups[project.Self.Key]}); err != nil {
			log.Printf("[CHAT_FORK] %s: could not put it in %s's group: %v", key, project.Self.Key, err)
		}
	}
}

// sendForkTask sends a new fork its first task (see sendStartTask); forkResult is the fork tool's JSON, returned with
// what happened to the task added.
func (api *StreamingAPI) sendForkTask(ctx context.Context, userID string, project codeChatProject, messages *agentSendHolder, task string, forkResult []byte) string {
	out := map[string]interface{}{}
	if err := json.Unmarshal(forkResult, &out); err != nil {
		return string(forkResult)
	}
	key, _ := out["conversation_key"].(string)
	for field, value := range api.sendStartTask(ctx, userID, project, messages, key, task) {
		out[field] = value
	}
	raw, _ := json.Marshal(out)
	return string(raw)
}

// sendStartTask sends a new fork or helper its first task as this chat's AgentBridge ask, so it starts working at once
// and its answer comes back here (owner, 22 Nov: a new agent waited until the person typed). It returns the fields to
// add to the tool result; a failed send keeps the new agent and says how to send the task by hand.
func (api *StreamingAPI) sendStartTask(ctx context.Context, userID string, project codeChatProject, messages *agentSendHolder, key, task string) map[string]interface{} {
	out := map[string]interface{}{}
	address := ""
	if key != "" {
		if _, err := api.registerHostedBridgeChat(ctx, userID, hostedBridgeBinding{Profile: project.ProfileID, Project: project.ProjectID, Chat: key}); err == nil {
			address = bridgeChatAddress(userID, project.ProfileID, project.ProjectID, key)
		} else {
			log.Printf("[CHAT_FORK] %s: task not sent, address unavailable: %v", key, err)
		}
	}
	if address != "" && messages != nil && messages.send != nil {
		receipt, err := messages.send(ctx, map[string]interface{}{"target": address, "message": task, "message_kind": "ask", "expects_reply": true})
		if err == nil {
			var sent map[string]interface{}
			if json.Unmarshal([]byte(receipt), &sent) == nil {
				out["task_inbox_id"] = sent["inbox_id"]
			}
			out["task_sent"] = true
			out["next"] = "It is working on the task; its answer comes back to you in that inbox. Tell the person it has started."
			log.Printf("[CHAT_FORK] %s: task sent as an ask", key)
			return out
		}
		log.Printf("[CHAT_FORK] %s: task not sent: %v", key, err)
		out["task_error"] = err.Error()
	}
	out["task_sent"] = false
	out["target"] = address
	out["next"] = "It was made but its task was not sent. Send it with messages action=ask (find it with messages action=list_agents)."
	return out
}

// spawnIdentity is the purpose and standing instructions a new agent is set up with in the same call that makes it
// (#3655), so the person does not have to paste them into the new tab by hand.
type spawnIdentity struct {
	purpose, instructions *string
}

func (s spawnIdentity) empty() bool { return s.purpose == nil && s.instructions == nil }

// spawnIdentityArgs reads and checks purpose and instructions before anything is made, so a refused value makes no tab.
func (api *StreamingAPI) spawnIdentityArgs(ctx context.Context, sessionID, userID string, args map[string]interface{}) (spawnIdentity, error) {
	var out spawnIdentity
	if value, ok := args["purpose"].(string); ok && strings.TrimSpace(value) != "" {
		if len([]rune(strings.Join(strings.Fields(value), " "))) > productAgentPurposeMaxRunes {
			return out, fmt.Errorf("the purpose is longer than %d characters; nothing was made", productAgentPurposeMaxRunes)
		}
		out.purpose = &value
	}
	if value, ok := args["instructions"].(string); ok && strings.TrimSpace(value) != "" {
		if len([]rune(strings.TrimSpace(value))) > productAgentInstructionsMaxRunes {
			return out, fmt.Errorf("the instructions are longer than %d characters; nothing was made", productAgentInstructionsMaxRunes)
		}
		// The same rule as set_my_agent_identity: only the agent owner's own turn writes what an agent always follows.
		api.lastQueryMu.RLock()
		turn, known := api.lastQueryRequests[sessionID]
		api.lastQueryMu.RUnlock()
		if _, _, _, _, other := askerIsNotAgentOwner(ctx, &turn, userID, userID); known && other {
			return out, fmt.Errorf("only this agent's own person sets a new agent's instructions, from their own chat; nothing was made")
		}
		out.instructions = &value
	}
	return out, nil
}

// saveSpawnIdentity writes the checked purpose and instructions on the new agent and reports what was saved.
func saveSpawnIdentity(ctx context.Context, userID string, project codeChatProject, key string, identity spawnIdentity, out map[string]interface{}) {
	if identity.empty() || key == "" {
		return
	}
	actor := "agent " + firstNonEmptyTrimmed(project.Self.Name, "its maker")
	store := defaultProductConversationRegistryStore()
	if identity.purpose != nil {
		saved, err := store.setAgentIdentity(ctx, userID, project.ProfileID, key, nil, identity.purpose, actor)
		log.Printf("[AGENT_IDENTITY] store=tab tool=spawn tab=%s set=purpose err=%v", key, err)
		if err != nil {
			out["purpose_error"] = err.Error()
		} else {
			out["purpose"] = saved.Purpose
		}
	}
	if identity.instructions != nil {
		saved, err := store.setAgentInstructions(ctx, userID, project.ProfileID, key, *identity.instructions, actor)
		log.Printf("[AGENT_IDENTITY] store=tab tool=spawn tab=%s set=instructions bytes=%d err=%v", key, len(saved.Instructions), err)
		if err != nil {
			out["instructions_error"] = err.Error()
		} else {
			out["instructions_saved"] = true
		}
	}
}

// saveSpawnIdentityJSON is saveSpawnIdentity for a fork, whose result is the fork handler's JSON body.
func saveSpawnIdentityJSON(ctx context.Context, userID string, project codeChatProject, identity spawnIdentity, result []byte) []byte {
	if identity.empty() {
		return result
	}
	out := map[string]interface{}{}
	if err := json.Unmarshal(result, &out); err != nil {
		return result
	}
	key, _ := out["conversation_key"].(string)
	saveSpawnIdentity(ctx, userID, project, key, identity, out)
	raw, err := json.Marshal(out)
	if err != nil {
		return result
	}
	return raw
}
