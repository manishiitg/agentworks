package main

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
)

// A chat lives on one network (its own login's server). The person can add it to
// another network they have a saved login for with --also: the chat then also
// receives messages there and can send there with NAME@LOGIN. Nothing joins
// another network on its own.

// joinNetwork adds the chat in req to the network of the saved login.
func joinNetwork(ctx context.Context, root string, o *options, req agentworksclient.LocalBridgeRequest, login, name string) (string, error) {
	path, err := o.loginPath(login)
	if err != nil {
		return "", err
	}
	if path, err = filepath.Abs(path); err != nil {
		return "", err
	}
	if err = ensureLocalBridge(ctx, root, o); err != nil {
		return "", err
	}
	status := req
	status.Action = "status"
	own, _ := agentworksclient.CallLocalBridge(ctx, root, status)
	req.Action, req.Also, req.ConfigPath, req.AgentName = "attach", true, path, name
	out, err := bridgeCall(ctx, root, o, req)
	if err != nil {
		return "", err
	}
	if !out.Attached {
		return "", fmt.Errorf("network %s did not confirm this chat", login)
	}
	ownName := "this chat's own network"
	if host := serverHost(own.OwnServer); host != "" {
		ownName = host
	}
	return fmt.Sprintf("This chat also joined network %s as agent:%s. It can now receive messages from %s, and anything you or the agent send there leaves %s. Send there with NAME@%s; leave with /agentworks detach --from %s.",
		login, out.Attachment.AgentName, login, ownName, login, login), nil
}

// rejoinNetworks reconnects the extra networks of a chat that just came back online.
func rejoinNetworks(ctx context.Context, root string, req agentworksclient.LocalBridgeRequest) {
	status := req
	status.Action = "status"
	out, err := agentworksclient.CallLocalBridge(ctx, root, status)
	if err != nil || !out.Attached {
		return
	}
	for _, n := range out.Networks {
		if n.Online {
			continue
		}
		again := req
		again.Action, again.Also, again.ConfigPath, again.AgentName = "attach", true, n.ConfigPath, n.AgentName
		_, _ = agentworksclient.CallLocalBridge(ctx, root, again)
	}
}

// networkRosters lists the agents of each extra network after the chat's own.
func networkRosters(out agentworksclient.LocalBridgeReply, local agentworksclient.LocalBridgeReply) string {
	var b strings.Builder
	for _, d := range out.Directories {
		fmt.Fprintf(&b, "\n\nOn network %s (send with NAME@%s):\n%s", d.Server, d.Server, agentRoster(d.Agents, local))
	}
	return b.String()
}

func serverHost(server string) string {
	if u, err := url.Parse(server); err == nil && u.Host != "" {
		return u.Host
	}
	return ""
}

// attachWords splits the person's attach words: [NAME] [--on LOGIN | --also LOGIN].
func attachWords(rest []string) (name, on, also string, ok bool) {
	if n := len(rest); n >= 2 && (rest[n-2] == "--on" || rest[n-2] == "--also") {
		if rest[n-2] == "--on" {
			on = rest[n-1]
		} else {
			also = rest[n-1]
		}
		rest = rest[:n-2]
	}
	if len(rest) > 1 || len(rest) == 1 && strings.HasPrefix(rest[0], "--") || strings.HasPrefix(on, "--") || strings.HasPrefix(also, "--") {
		return "", "", "", false
	}
	if len(rest) == 1 {
		name = rest[0]
	}
	return name, on, also, true
}

// personShareCommand runs a typed share or unshare. @LOGIN acts on a network the
// chat joined with --also.
func personShareCommand(ctx context.Context, root string, o *options, req agentworksclient.LocalBridgeRequest, words []string) string {
	if usage := parseShareWords(req.Action, words[2:], &req); usage != "" {
		return usage
	}
	if req.Network != "" {
		if _, err := o.loginPath(req.Network); err != nil {
			return "No saved login named " + req.Network + "."
		}
	}
	req.Action += "_person"
	done, err := bridgeCall(ctx, root, o, req)
	if err != nil {
		return err.Error()
	}
	if !done.Attached && len(done.Result) == 0 {
		return "This chat is not connected to AgentWorks, so nothing was shared. Type /agentworks attach in this chat first, then try again."
	}
	return shareResultText(done.Result)
}
