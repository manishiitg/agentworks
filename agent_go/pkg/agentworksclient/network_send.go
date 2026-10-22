package agentworksclient

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

func namedSendTarget(target string) string {
	target = strings.TrimSpace(target)
	if strings.HasPrefix(target, "agent:") {
		return strings.TrimPrefix(target, "agent:")
	}
	if target != "" && !strings.ContainsAny(target, ":@") {
		return target
	}
	return ""
}

func sendNetworkName(s *localSession) string {
	if s.Network != "" {
		return s.Network
	}
	if s.client != nil {
		return s.client.baseURL
	}
	return "primary"
}

// Directory reads select a single authorized connection before any write. A
// refused or ambiguous send is never retried on another company's connection.
func (b *localBridge) sendAcrossNetworks(ctx context.Context, networks []*localSession, in LocalBridgeRequest, out LocalBridgeReply) LocalBridgeReply {
	name := namedSendTarget(in.Target)
	var matches []*localSession
	for _, network := range networks {
		if network.Offline || network.client == nil {
			return LocalBridgeReply{Error: fmt.Sprintf("Cannot locate agent %q while network %s is offline. Choose the intended joined network with network; no message was sent.", name, sendNetworkName(network))}
		}
		raw, err := network.client.Call(ctx, "messages", map[string]any{"action": "list_agents", "agent_name": network.Attachment.AgentName})
		if err != nil {
			return LocalBridgeReply{Error: fmt.Sprintf("Cannot locate agent %q on network %s: %v. Choose network explicitly; no message was sent.", name, sendNetworkName(network), err)}
		}
		var directory struct {
			Agents *[]struct {
				ID        string `json:"id"`
				AgentName string `json:"agent_name"`
			}
		}
		if json.Unmarshal(raw, &directory) != nil || directory.Agents == nil {
			return LocalBridgeReply{Error: "Invalid agent directory from network " + sendNetworkName(network) + "; no message was sent."}
		}
		for _, agent := range *directory.Agents {
			if strings.EqualFold(agent.ID, "agent:"+name) || agent.AgentName != "" && strings.EqualFold(agent.AgentName, name) {
				matches = append(matches, network)
				break
			}
		}
	}
	if len(matches) != 1 {
		var choices []string
		for _, network := range networks {
			choices = append(choices, sendNetworkName(network))
		}
		reason := "was not found"
		if len(matches) > 1 {
			reason = "appears on more than one network"
		}
		return LocalBridgeReply{Error: fmt.Sprintf("Agent %q %s in the joined directories (%s). Choose network explicitly; no message was sent.", name, reason, strings.Join(choices, ", "))}
	}
	in.Target = "agent:" + name
	return sendNetworkResult(b.mailboxCall(ctx, matches[0], in, out), matches[0])
}

func sendNetworkResult(out LocalBridgeReply, via *localSession) LocalBridgeReply {
	name := sendNetworkName(via)
	if out.Error != "" {
		out.Error = fmt.Sprintf("AgentWorks network %s refused the send: %s", name, out.Error)
		return out
	}
	var result map[string]any
	if json.Unmarshal(out.Result, &result) == nil && result != nil {
		result["network"] = name
		result["server"] = via.client.baseURL
		out.Result, _ = json.Marshal(result)
	}
	return out
}
