package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
)

// serverLogins is one server and the saved logins that sign in to it, in the
// order agentworks logins lists them.
type serverLogins struct {
	server string
	logins []agentworksclient.LoginInfo
}

func (g serverLogins) names() string {
	names := make([]string, 0, len(g.logins))
	for _, l := range g.logins {
		names = append(names, l.Name)
	}
	return strings.Join(names, ", ")
}

// savedServers groups every saved login by the server it signs in to, so a
// server reached by several logins is listed once.
func savedServers() []serverLogins {
	logins, _ := agentworksclient.ListLogins() // agentworks logins reports loading errors
	var groups []serverLogins
	index := map[string]int{}
	for _, l := range logins {
		server, err := agentworksclient.ValidateServer(l.Server)
		if err != nil || l.Path == "" {
			continue
		}
		i, ok := index[server]
		if !ok {
			i = len(groups)
			index[server] = i
			groups = append(groups, serverLogins{server: server})
		}
		groups[i].logins = append(groups[i].logins, l)
	}
	return groups
}

func loginClient(l agentworksclient.LoginInfo) (*agentworksclient.Client, error) {
	cfg, err := agentworksclient.LoadConfig(l.Path)
	if err != nil {
		return nil, err
	}
	client, err := agentworksclient.New(cfg.Server, cfg.Token)
	if err != nil {
		return nil, err
	}
	if cfg.RefreshToken != "" {
		client.WithTokenProvider(agentworksclient.ConfigTokenProvider(cfg.Server, l.Path))
	}
	return client, nil
}

// eachServer calls fn once per saved server, in parallel, trying that server's
// logins in order until one answers. It returns the first answer per server, or
// the last error when no login there worked.
func eachServer(ctx context.Context, groups []serverLogins, fn func(context.Context, *agentworksclient.Client) (json.RawMessage, error)) ([]json.RawMessage, []error) {
	results := make([]json.RawMessage, len(groups))
	errs := make([]error, len(groups))
	var wg sync.WaitGroup
	for i, g := range groups {
		wg.Add(1)
		go func(i int, g serverLogins) {
			defer wg.Done()
			for _, l := range g.logins {
				client, err := loginClient(l)
				if err == nil {
					call, cancel := context.WithTimeout(ctx, 15*time.Second)
					results[i], err = fn(call, client)
					cancel()
				}
				if err == nil {
					errs[i] = nil
					return
				}
				errs[i] = err
			}
		}(i, g)
	}
	wg.Wait()
	return results, errs
}

// agentDirectories lists agents once per server: every saved login's server
// first, then any server only an attached chat reaches. A server that cannot be
// listed keeps its section with the reason, never silently disappears.
func agentDirectories(ctx context.Context, query string, fromChats []agentworksclient.LocalAgentDirectory) []agentworksclient.LocalAgentDirectory {
	groups := savedServers()
	results, errs := eachServer(ctx, groups, func(ctx context.Context, c *agentworksclient.Client) (json.RawMessage, error) {
		return c.Call(ctx, "messages", map[string]any{"action": "list_agents", "query": query})
	})
	chats := map[string]agentworksclient.LocalAgentDirectory{}
	for _, d := range fromChats {
		if server, err := agentworksclient.ValidateServer(d.Server); err == nil {
			chats[server] = d
		}
	}
	var out []agentworksclient.LocalAgentDirectory
	covered := map[string]bool{}
	for i, g := range groups {
		covered[g.server] = true
		d := agentworksclient.LocalAgentDirectory{Server: g.server, Agents: results[i]}
		if errs[i] != nil {
			if chat, ok := chats[g.server]; ok && chat.Error == "" {
				d = chat // a chat's own connection still works here
			} else {
				d.Error = errs[i].Error()
			}
		}
		out = append(out, d)
	}
	for server, d := range chats {
		if !covered[server] {
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Server < out[j].Server })
	return out
}

// attachedWhere names the server a chat is connected to and the saved logins
// for it, so the person and the agent can tell where it landed.
func attachedWhere(server string) string {
	host := serverHost(server)
	if host == "" {
		return ""
	}
	return " on " + host + loginLabel(server)
}

// crewLoginHint tells a chat that could not join a Crew which saved login can
// see it, so it can move there instead of guessing between servers.
func crewLoginHint(ctx context.Context, project, ownServer string) string {
	project = strings.TrimSpace(project)
	if project == "" {
		return ""
	}
	groups := savedServers()
	_, errs := eachServer(ctx, groups, func(ctx context.Context, c *agentworksclient.Client) (json.RawMessage, error) {
		return c.Call(ctx, "workspace", map[string]any{"action": "get", "workspace_id": project})
	})
	own, _ := agentworksclient.ValidateServer(ownServer)
	for i, g := range groups {
		if errs[i] != nil {
			continue
		}
		login := g.logins[0].Name
		if g.server == own {
			return ""
		}
		where := "this chat is not connected yet"
		if host := serverHost(own); host != "" {
			where = "this chat is connected to " + host
		}
		return fmt.Sprintf("This project is on %s (login: %s), but %s. Move this chat there with: agentworks attach --on %s (or type /agentworks attach --on %s in the chat), then join again.", serverHost(g.server), g.names(), where, login, login)
	}
	return "None of your saved logins can see this project. Check the project ID, or sign in to the server it is on with agentworks login --server URL --name NAME."
}
