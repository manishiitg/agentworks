package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
	"github.com/spf13/cobra"
)

type terminalStatus struct {
	Attached bool   `json:"attached"`
	Crew     string `json:"crew_name"`
	TeamOn   bool   `json:"team_on"`
	InTeam   bool   `json:"in_team"`
	Online   *int   `json:"online"`
	Unread   *int   `json:"unread"`
}

func terminalStatusLine(s terminalStatus) string {
	if !s.Attached {
		return ""
	}
	if s.Crew == "" {
		return "AgentWorks · connected"
	}
	name := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, s.Crew)
	name = strings.Join(strings.Fields(name), " ")
	if name == "" {
		return ""
	}
	runes := []rune(name)
	if len(runes) > 120 {
		name = string(runes[:120]) + "…"
	}
	line := "AgentWorks · Crew " + name
	if !s.TeamOn {
		return line
	}
	if !s.InTeam {
		return line + " · Not shared"
	}
	line += " · Shared with team"
	if s.Online != nil && *s.Online >= 0 {
		line += fmt.Sprintf(" · %d online", *s.Online)
	}
	if s.Unread != nil && *s.Unread >= 0 {
		line += fmt.Sprintf(" · %d new", *s.Unread)
	}
	return line
}

// Exact own-session detection only: no session, owner or path overrides.
func pluginTerminalStatusCommand(o *options) *cobra.Command {
	return &cobra.Command{Use: "terminal-status PROVIDER", Short: "Read this native session's quiet Crew status", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if o.getenv("AGENTWORKS_TERMINAL_STATUS") == "0" {
			return nil
		}
		if args[0] != "claude" && args[0] != "codex" {
			return nil
		}
		root, err := agentworksclient.DefaultLocalBridgeRoot()
		if err != nil {
			return nil
		}
		own, _, err := ownAgentSession(cmd.Context(), root, o)
		if err != nil || own.provider != args[0] || own.session == "" || own.host == "" {
			return nil
		}
		line := readPluginTerminalStatus(cmd.Context(), root, own.provider, own.session, own.host)
		if line != "" {
			_, err = fmt.Fprintln(o.stdout, line)
		}
		return err
	}}
}

func readPluginTerminalStatus(ctx context.Context, root, provider, session, host string) string {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	out, err := agentworksclient.CallLocalBridge(ctx, root, agentworksclient.LocalBridgeRequest{Action: "terminal_status", Provider: provider, SessionID: session, HostKey: host})
	if err != nil || !out.Attached || out.Error != "" {
		return ""
	}
	var status terminalStatus
	if json.Unmarshal(out.Result, &status) != nil {
		return ""
	}
	return terminalStatusLine(status)
}

func withTerminalStatus(output map[string]any, line string) map[string]any {
	if line != "" {
		output["systemMessage"] = line
	}
	return output
}

// teamPane is the Team pane's data for this exact attached session (#3357):
// only the fields the server's read-only team_pane projection returns.
type teamPane struct {
	Attached bool `json:"attached"`
	// Failed means the local bridge or server could not answer; the pane says it could not reach AgentWorks
	// instead of claiming the chat is not connected (an older running bridge lacks team_pane, #3357).
	Failed bool   `json:"failed,omitempty"`
	Crew   string `json:"crew_name,omitempty"`
	TeamOn bool   `json:"team_on"`
	InTeam bool   `json:"in_team"`
	Needs  []struct {
		Line    string   `json:"line"`
		Ref     string   `json:"ref,omitempty"`
		Agent   string   `json:"agent"`
		Person  string   `json:"person,omitempty"`
		Mine    bool     `json:"mine,omitempty"`
		Buttons []string `json:"buttons"`
		At      string   `json:"at"`
	} `json:"needs"`
	Asks []struct {
		Title   string `json:"title"`
		State   string `json:"state"`
		Ref     string `json:"ref,omitempty"`
		AskedBy string `json:"asked_by,omitempty"`
		Last    string `json:"last,omitempty"`
		At      string `json:"at"`
	} `json:"asks"`
	Kit []struct {
		ID      string   `json:"id"`
		Title   string   `json:"title"`
		Purpose string   `json:"purpose,omitempty"`
		State   string   `json:"state"`
		Steps   []string `json:"steps"`
		Done    int      `json:"done"`
		Keys    []string `json:"keys,omitempty"`
	} `json:"kit"`
	Agents []teamPaneAgent `json:"agents"`
	// Agent is set only when the pane opened one agent (--agent): its row and, when
	// its chat is shared Live, its last few messages; Note says why there are none.
	Agent *struct {
		teamPaneAgent
		Messages []struct {
			Role string `json:"role"`
			Text string `json:"text"`
		} `json:"messages"`
		Note string `json:"note,omitempty"`
	} `json:"agent,omitempty"`
}

type teamPaneAgent struct {
	ID       string `json:"id"`
	Mine     bool   `json:"mine,omitempty"`
	Name     string `json:"name"`
	Person   string `json:"person,omitempty"`
	Kind     string `json:"kind"`
	Online   bool   `json:"online"`
	Activity string `json:"activity,omitempty"`
	Offline  string `json:"offline_line,omitempty"`
}

// The pane reads one JSON line; a chat that is not attached prints {"attached":false}.
func pluginTeamPaneCommand(o *options) *cobra.Command {
	var agent, fork string
	cmd := &cobra.Command{Use: "team-pane PROVIDER", Short: "Read this native session's Team pane", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		pane := teamPane{}
		if fork != "" {
			// Fork to my Mac: the server runs the existing self-fork on this person's own paired Mac.
			result := map[string]any{"forked": false, "error": "Couldn't reach AgentWorks."}
			if root, err := agentworksclient.DefaultLocalBridgeRoot(); err == nil && args[0] == "claude" {
				if own, _, err := ownAgentSession(cmd.Context(), root, o); err == nil && own.provider == args[0] && own.session != "" && own.host != "" {
					result = forkTeamPaneAgent(cmd.Context(), root, own.provider, own.session, own.host, fork)
				}
			}
			line, err := json.Marshal(result)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(o.stdout, string(line))
			return err
		}
		if args[0] == "claude" {
			if root, err := agentworksclient.DefaultLocalBridgeRoot(); err == nil {
				if own, _, err := ownAgentSession(cmd.Context(), root, o); err == nil && own.provider == args[0] && own.session != "" && own.host != "" {
					pane = readPluginTeamPane(cmd.Context(), root, own.provider, own.session, own.host, agent)
				}
			}
		}
		line, err := json.Marshal(pane)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(o.stdout, string(line))
		return err
	}}
	cmd.Flags().StringVar(&agent, "agent", "", "Open one Team agent by its ID from the pane list")
	cmd.Flags().StringVar(&fork, "fork", "", "Copy one teammate's agent (by its ID from the pane list) to this person's own Mac")
	return cmd
}

func forkTeamPaneAgent(ctx context.Context, root, provider, session, host, agent string) map[string]any {
	ctx, cancel := context.WithTimeout(ctx, 100*time.Second)
	defer cancel()
	out, err := agentworksclient.CallLocalBridge(ctx, root, agentworksclient.LocalBridgeRequest{Action: "team_sync", Provider: provider, SessionID: session, HostKey: host, Arguments: map[string]any{"team_action": "fork_from", "agent_key": agent}})
	switch {
	case err != nil || out.Error == "unknown local bridge action":
		return map[string]any{"forked": false, "error": "Couldn't reach AgentWorks."}
	case out.Error != "":
		return map[string]any{"forked": false, "error": out.Error}
	case !out.Attached:
		return map[string]any{"forked": false, "error": "Connect this chat to AgentWorks first."}
	}
	return map[string]any{"forked": true}
}

func readPluginTeamPane(ctx context.Context, root, provider, session, host, agent string) teamPane {
	// Opening a terminal chat reads it from its owner's Mac, which can take longer than the list.
	limit := 4 * time.Second
	request := agentworksclient.LocalBridgeRequest{Action: "team_pane", Provider: provider, SessionID: session, HostKey: host}
	if agent != "" {
		limit = 18 * time.Second
		request.Arguments = map[string]any{"query": agent}
	}
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	out, err := agentworksclient.CallLocalBridge(ctx, root, request)
	// An older running bridge answers "unknown local bridge action" without a session; that is not "not attached".
	if err != nil || (out.Attached && out.Error != "") || out.Error == "unknown local bridge action" {
		return teamPane{Failed: true}
	}
	if !out.Attached {
		return teamPane{}
	}
	var pane teamPane
	if json.Unmarshal(out.Result, &pane) != nil {
		return teamPane{Failed: true}
	}
	pane.Attached = true
	return pane
}
