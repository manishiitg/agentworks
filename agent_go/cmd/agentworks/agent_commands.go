package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
	"github.com/pelletier/go-toml/v2"
	"github.com/spf13/cobra"
	_ "modernc.org/sqlite"
)

const personCommandHelp = "AgentWorks: /agentworks share publishes this chat's agent to the network (everyone on the server can find and message it; share @LOGIN for a network joined with --also), /agentworks unshare takes it back; /agentworks attach [NAME] [--on LOGIN] connects or reconnects this chat (--on moves it to another saved login); /agentworks attach [NAME] --also LOGIN adds this chat to another network too (send there with NAME@LOGIN); /agentworks detach stops delivery (--from LOGIN leaves only that network); /agentworks auto off|on turns off or on automatic reconnect of a new chat in this folder; /agentworks agents shows available agents. Type the command yourself."

// Reuse the exact session's saved identity and parent login, even when the
// default CLI login has changed. The person's attach refreshes the process fence.
func prepareAttachment(ctx context.Context, root string, o *options, req *agentworksclient.LocalBridgeRequest, cwd string) error {
	req.WorkspaceDir = cwd
	req.LabelName = req.AgentName
	req.ReuseIdentity = req.AgentName == ""
	req.Setup = true
	if err := ensureLocalBridge(ctx, root, o); err != nil {
		return err
	}
	status := *req
	status.Action = "status"
	current, err := agentworksclient.CallLocalBridge(ctx, root, status)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return errStillConnecting
		}
		return err
	}
	// A new process cannot inspect an active lease, but attach can recover it by
	// exact provider/session in the bridge, without granting it delivery first.
	if req.AgentName == "" && current.Attachment.AgentName != "" {
		req.AgentName = current.Attachment.AgentName
	}
	if req.AgentName == "" && req.Provider != "pi" && o.switchPath == "" {
		// A bare attach typed by the person comes back as the folder's most recent
		// earlier agent that nothing holds, so peers keep the address they know.
		free, why := autoAttachScan(ctx, root, *req, cwd)
		if len(free) > 0 {
			o.reconnectAlt, o.reconnectCwd = free[1:], cwd
			o.takeReconnect(req, free[0])
			return nil
		}
		o.newAgentWhy = why
	}
	if req.AgentName == "" {
		req.AgentName, _, err = proposedAgentIdentity(req.Provider, req.SessionID, cwd)
		if err != nil {
			return err
		}
	}
	if o.switchPath != "" {
		// The person chose another saved login for this exact chat.
		req.ConfigPath, req.SwitchConnection = o.switchPath, true
		return nil
	}
	req.ConfigPath, err = o.path()
	if err != nil {
		return err
	}
	req.ConfigPath, err = filepath.Abs(req.ConfigPath)
	return err
}

// takeReconnect points a bare attach at an earlier agent and words the one line
// that tells the person, naming the login when it is not the current default.
func (o *options) takeReconnect(req *agentworksclient.LocalBridgeRequest, r autoAttachRecord) {
	req.LabelName = r.LabelName
	req.AgentName, req.Purpose, req.ConfigPath, req.ReuseIdentity = r.Name, r.Purpose, r.ConfigPath, false
	login := ""
	if def, err := o.path(); err == nil {
		if abs, e := filepath.Abs(def); e != nil || abs != r.ConfigPath {
			if cfg, e := agentworksclient.LoadConfig(r.ConfigPath); e == nil {
				login = strings.TrimSuffix(strings.TrimPrefix(loginLabel(cfg.Server), " (login: "), ")")
			}
		}
	}
	o.reconnectNote = "Reconnected as agent:" + r.Name
	if login != "" {
		o.reconnectNote += " on " + login
	}
	o.reconnectNote += ". Type /agentworks attach NEW-NAME for a new agent."
}

// bridgeAttach sends the attach. When a bare attach lost a race for the earlier
// agent it chose (another chat took the name meanwhile), it moves on to the next
// free one, and finally to a new automatic name, instead of failing.
func bridgeAttach(ctx context.Context, root string, o *options, req *agentworksclient.LocalBridgeRequest) (agentworksclient.LocalBridgeReply, error) {
	out, err := bridgeCall(ctx, root, o, *req)
	for err != nil && req.Action == "attach" && o.reconnectNote != "" && strings.Contains(err.Error(), "is attached to another chat") {
		if len(o.reconnectAlt) > 0 {
			o.takeReconnect(req, o.reconnectAlt[0])
			o.reconnectAlt = o.reconnectAlt[1:]
		} else {
			name, _, e := proposedAgentIdentity(req.Provider, req.SessionID, o.reconnectCwd)
			cfg, e2 := o.path()
			if e != nil || e2 != nil {
				return out, err
			}
			abs, _ := filepath.Abs(cfg)
			req.AgentName, req.Purpose, req.ConfigPath, req.ReuseIdentity = name, "", abs, true
			o.reconnectNote = ""
		}
		out, err = bridgeCall(ctx, root, o, *req)
	}
	return out, err
}

func pluginAgents(ctx context.Context, root string, o *options, req agentworksclient.LocalBridgeRequest) error {
	req.Action = "peers"
	peers, err := bridgeCall(ctx, root, o, req)
	if err != nil {
		return pluginBlock(o, err.Error())
	}
	if !peers.Attached {
		return pluginBlock(o, "This chat is not attached. Type /agentworks attach first.")
	}
	local, _ := bridgeCall(ctx, root, o, agentworksclient.LocalBridgeRequest{Action: "list"})
	return pluginBlock(o, agentRoster(peers.Result, local)+networkRosters(peers, local))
}

func agentsCommand(o *options) *cobra.Command {
	var query string
	cmd := &cobra.Command{Use: "agents", Aliases: []string{"agent"}, Args: cobra.NoArgs, Short: "Show agents visible through your authorized connections", RunE: func(cmd *cobra.Command, _ []string) error {
		root, err := agentworksclient.DefaultLocalBridgeRoot()
		if err != nil {
			return err
		}
		local, _ := agentworksclient.CallLocalBridge(cmd.Context(), root, agentworksclient.LocalBridgeRequest{Action: "list"})
		if o.serverURL == "" && o.configPath == "" && o.getenv("AGENTWORKS_CONFIG") == "" {
			dirs, _ := agentworksclient.CallLocalBridge(cmd.Context(), root, agentworksclient.LocalBridgeRequest{Action: "directories", Query: query})
			if len(dirs.Directories) > 0 {
				if o.jsonOutput {
					return o.output(map[string]any{"directories": dirs.Directories, "local": local.AgentStates})
				}
				for _, d := range dirs.Directories {
					fmt.Fprintln(o.stdout, d.Server+loginLabel(d.Server))
					fmt.Fprintln(o.stdout, agentRoster(d.Agents, local))
				}
				return nil
			}
		}
		client, err := o.client()
		if err != nil {
			return err
		}
		raw, err := client.Call(cmd.Context(), "messages", map[string]any{"action": "list_agents", "query": query})
		if err != nil {
			return err
		}
		if o.jsonOutput {
			return o.output(map[string]any{"agents": json.RawMessage(raw), "local": local.AgentStates})
		}
		_, err = fmt.Fprintln(o.stdout, agentRoster(raw, local))
		return err
	}}
	cmd.Flags().StringVarP(&query, "query", "q", "", "Find agents by name or purpose")
	return cmd
}

// Only server-owned names/purposes are shown, with control characters removed.
// Match local observations by agent ID, never by name across accounts/servers.
func agentRoster(raw json.RawMessage, local agentworksclient.LocalBridgeReply) string {
	var data struct {
		Agents []struct {
			Name      string `json:"agent_name"`
			Label     string `json:"label"`
			ID        string `json:"agent_id"`
			Provider  string `json:"provider"`
			Purpose   string `json:"purpose"`
			Online    bool   `json:"online"`
			Presence  string `json:"presence"`
			Published bool   `json:"published"`
		} `json:"agents"`
		Hint   string `json:"hint"`
		Hosted []struct {
			Name    string `json:"agent_name"`
			Display string `json:"display"`
			Why     string `json:"reachable_because"`
		} `json:"chats_inside_agentworks"`
	}
	if json.Unmarshal(raw, &data) != nil {
		return "Agent roster unavailable."
	}
	sort.SliceStable(data.Agents, func(i, j int) bool {
		if data.Agents[i].Online != data.Agents[j].Online {
			return data.Agents[i].Online
		}
		return data.Agents[i].Name < data.Agents[j].Name
	})
	clean := func(s string) string {
		s = strings.Map(func(r rune) rune {
			if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
				return ' '
			}
			return r
		}, s)
		r := []rune(s)
		if len(r) > 100 {
			r = r[:100]
		}
		return string(r)
	}
	var b strings.Builder
	b.WriteString("AgentWorks agents (online first):\n")
	for i, a := range data.Agents {
		if i >= 30 {
			fmt.Fprintf(&b, "… %d more; use agentworks agents --query NAME_OR_PURPOSE or --json.\n", len(data.Agents)-i)
			break
		}
		presence, route := a.Presence, "Inbox"
		if presence == "" {
			presence = "Status unavailable"
		}
		if a.Online {
			route = "Live chat"
		}
		detail := ""
		for _, s := range local.AgentStates {
			if s.AgentID == a.ID {
				route = "This computer"
				detail = fmt.Sprintf("; %d waiting", s.Pending)
				if s.LastError != "" {
					detail += "; " + clean(s.LastError)
				}
				break
			}
		}
		shared := ""
		if a.Published {
			shared = " | published to the network"
		}
		fmt.Fprintf(&b, "%s | agent:%s | %s | %s | %s%s | %s%s\n", clean(a.Label), clean(a.Name), clean(a.Provider), presence, route, detail, clean(a.Purpose), shared)
	}
	if len(data.Agents) == 0 {
		b.WriteString("No registered agents.\n")
	}
	if data.Hint != "" {
		b.WriteString(clean(data.Hint) + "\n")
	}
	if len(data.Hosted) > 0 {
		b.WriteString("\nChats inside AgentWorks (they can't receive messages yet):\n")
		for _, h := range data.Hosted {
			fmt.Fprintf(&b, "agent:%s | %s | %s\n", clean(h.Name), clean(h.Display), clean(h.Why))
		}
	}
	return b.String()
}

type liveAgentSession struct{ provider, session, host, remote, cwd, name string }

func attachPluginCommand(o *options) *cobra.Command {
	var provider, session, remote, name, on, also string
	var all, yes bool
	var pid int
	cmd := &cobra.Command{Use: "attach [NAME]", Args: cobra.MaximumNArgs(1), Short: "Install, attach or reconnect a live coding-agent chat", Long: "Attach this chat, or reconnect it on the login it already uses. With --on LOGIN the chat moves to that saved login (see agentworks logins); the move keeps the chat's name unless you give NAME, and if the new login cannot be reached the chat stays where it was. With --all --on LOGIN every attached live chat is moved after you retype the listed count (add --yes to skip the prompt). A coding agent may run all of these for you when you ask it to.", RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 {
			name = args[0]
		}
		if all && on == "" {
			return errors.New("--all moves chats to another login: add --on LOGIN")
		}
		if all && name != "" {
			return errors.New("NAME selects one identity; use --all without NAME")
		}
		if on != "" {
			path, e := o.loginPath(on)
			if e != nil {
				return e
			}
			o.switchPath = path
		}
		root, err := agentworksclient.DefaultLocalBridgeRoot()
		if err != nil {
			return err
		}
		own, fromAgent, err := ownAgentSession(cmd.Context(), root, o)
		if err != nil {
			return err
		}
		if also != "" {
			if !fromAgent || on != "" || provider != "" || session != "" || remote != "" || pid != 0 {
				return errors.New("--also works from inside the chat itself, alone: ask the agent in that chat, or type /agentworks attach --also LOGIN there")
			}
			line, e := joinNetwork(cmd.Context(), root, o, agentworksclient.LocalBridgeRequest{Provider: own.provider, SessionID: own.session, HostKey: own.host}, also, name)
			if e != nil {
				return e
			}
			_, err = fmt.Fprintln(o.stdout, line)
			return err
		}
		if fromAgent {
			if on == "" {
				if provider != "" || session != "" || remote != "" || pid != 0 {
					return errors.New("own-session attach does not accept provider, session, process or endpoint overrides")
				}
				return attachLiveAgentWithSetup(cmd.Context(), o, root, own, name, false)
			}
			// An agent may move chats to a saved login (owner choice for an open
			// system): its own chat, another attached chat by provider and session,
			// or every attached chat, without the person's retype prompt.
			if remote != "" || pid != 0 {
				return errors.New("a move does not accept process or endpoint overrides")
			}
			o.attachedOnly = true
			if all || provider != "" || session != "" {
				sessions, e := discoverLiveAgents(cmd.Context(), root, o)
				if e != nil {
					return e
				}
				if all {
					return moveAttachedChats(cmd.Context(), o, root, sessions, on, true)
				}
				for _, s := range sessions {
					if s.provider == provider && s.session == session {
						if s.name == "" {
							return errors.New("that chat is not attached; only attached chats can be moved")
						}
						return attachLiveAgentWithSetup(cmd.Context(), o, root, s, name, false)
					}
				}
				return errors.New("no live attached chat has that provider and session")
			}
			return attachLiveAgentWithSetup(cmd.Context(), o, root, own, name, false)
		}
		if err := personAgentTerminal(); err != nil {
			return err
		}
		// Explicit technical overrides remain hidden for adapter diagnostics.
		var chosen liveAgentSession
		var selected []liveAgentSession
		// A move only concerns chats that already have a lease; do not probe every thread.
		o.attachedOnly = all
		if provider != "" && session != "" {
			chosen = liveAgentSession{provider: provider, session: session, remote: remote}
			if pid > 1 {
				chosen.host, err = providerPIDHostKey(provider, pid)
			} else {
				chosen.host, err = providerHostKey(provider)
			}
			if err != nil {
				return err
			}
		} else {
			if _, e := exec.LookPath("muse"); e == nil {
				fmt.Fprintln(o.stderr, museDiscoveryHint)
			}
			sessions, e := discoverLiveAgents(cmd.Context(), root, o)
			if e != nil {
				return e
			}
			if len(sessions) == 0 {
				installed := false
				providers := []string{"codex", "claude", "muse", "pi"}
				if os.Getenv("AGENTWORKS_BRIDGE_ROOT") != "" {
					providers = []string{"pi"}
				}
				for _, p := range providers {
					if providerAvailable(p) {
						if e := installAgentPlugin(cmd.Context(), o, p); e != nil {
							return e
						}
						installed = true
					}
				}
				if !installed {
					return errors.New("no supported coding CLI found (on PATH, or for Codex the Codex app on macOS); install Codex, Claude Code, Muse or Pi first")
				}
				fmt.Fprintln(o.stdout, "AgentWorks plugin ready. Reload your coding chat, review its hooks, then type /agentworks attach. No chat has been attached.")
				return nil
			}
			if all {
				return moveAttachedChats(cmd.Context(), o, root, sessions, on, yes)
			}
			for i, s := range sessions {
				fmt.Fprintf(o.stderr, "%d. %s %s (%s)\n", i+1, s.provider, s.session, s.name)
			}
			fmt.Fprintf(o.stderr, "Choose a chat number, or type all %d to attach the listed chats (cancel to stop): ", len(sessions))
			var answer, count string
			scanned, _ := fmt.Fscanln(o.stdin, &answer, &count)
			if scanned == 0 {
				fmt.Fprintln(o.stdout, "Attachment cancelled. No chat was changed.")
				return nil
			}
			if answer == "all" {
				if count != strconv.Itoa(len(sessions)) {
					return errors.New("attach-all cancelled: retype the listed chat count after all")
				}
				if name != "" && len(sessions) > 1 {
					return errors.New("NAME selects one identity; choose a single chat, or attach all without NAME")
				}
				selected = sessions
			} else {
				if count != "" {
					return errors.New("choose one chat number or all with the listed count")
				}
				n, e := strconv.Atoi(answer)
				if e != nil || n < 1 || n > len(sessions) {
					return errors.New("attachment cancelled")
				}
				chosen = sessions[n-1]
			}
		}
		if len(selected) == 0 {
			selected = []liveAgentSession{chosen}
		}
		for _, s := range selected {
			if err = attachLiveAgent(cmd.Context(), o, root, s, name); err != nil {
				return err
			}
		}
		return nil
	}}
	cmd.Flags().StringVar(&provider, "provider", "", "Exact provider override")
	cmd.Flags().StringVar(&session, "session", "", "Exact session override")
	cmd.Flags().StringVar(&remote, "native-remote", "", "Exact native endpoint override")
	cmd.Flags().IntVar(&pid, "native-pid", 0, "Exact process override")
	cmd.Flags().StringVar(&on, "on", "", "Move this chat to a saved login (see agentworks logins)")
	cmd.Flags().StringVar(&also, "also", "", "Also join this chat to the network of another saved login (only when the person asks)")
	cmd.Flags().BoolVar(&yes, "yes", false, "With --all --on, move without the retype-the-count prompt")
	cmd.Flags().BoolVar(&all, "all", false, "With --on, move every attached live chat after a counted confirmation")
	cmd.Flags().StringVar(&name, "name", "", "Name override")
	for _, flag := range []string{"provider", "session", "native-remote", "native-pid", "name"} {
		_ = cmd.Flags().MarkHidden(flag)
	}
	return cmd
}

// moveAttachedChats moves every live chat that is already attached to the login
// the person named. It lists the chats first and asks the person to retype the
// count, reports each chat on its own, and never stops at the first failure.
func moveAttachedChats(ctx context.Context, o *options, root string, sessions []liveAgentSession, on string, skipPrompt bool) error {
	var movable []liveAgentSession
	for _, s := range sessions {
		if s.name == "" {
			fmt.Fprintf(o.stderr, "skipped %s %s: not attached\n", s.provider, s.session)
			continue
		}
		status, err := agentworksclient.CallLocalBridge(ctx, root, agentworksclient.LocalBridgeRequest{Action: "status", Provider: s.provider, SessionID: s.session, HostKey: s.host})
		if err != nil || !status.Attached || status.Offline {
			fmt.Fprintf(o.stderr, "skipped %s %s: offline or ended\n", s.provider, s.session)
			continue
		}
		movable = append(movable, s)
	}
	if len(movable) == 0 {
		fmt.Fprintln(o.stdout, "No attached live chats to move. Nothing was changed.")
		return nil
	}
	for i, s := range movable {
		fmt.Fprintf(o.stderr, "%d. %s %s (agent:%s)\n", i+1, s.provider, s.session, s.name)
	}
	if !skipPrompt {
		fmt.Fprintf(o.stderr, "Move these %d chats to the login %q? Retype %d to confirm (anything else cancels): ", len(movable), on, len(movable))
		var answer string
		_, _ = fmt.Fscanln(o.stdin, &answer)
		if answer != strconv.Itoa(len(movable)) {
			fmt.Fprintln(o.stdout, "Cancelled. No chat was changed.")
			return nil
		}
	}
	failed, incomplete := 0, 0
	for _, s := range movable {
		// Existing chats retain their approved delivery setup when changing login.
		err := attachLiveAgentWithSetup(ctx, o, root, s, "", false)
		var partial attachedButIncomplete
		switch {
		case errors.As(err, &partial):
			incomplete++
			fmt.Fprintf(o.stdout, "moved %s %s, but delivery setup failed: %v (re-run attach in that chat)\n", s.provider, s.session, err)
		case err != nil:
			failed++
			fmt.Fprintf(o.stdout, "FAILED %s %s: %v (not moved; check where it is with agentworks agents)\n", s.provider, s.session, err)
		default:
			fmt.Fprintf(o.stdout, "moved %s %s\n", s.provider, s.session)
		}
	}
	fmt.Fprintf(o.stdout, "%d of %d chats moved (%d with delivery setup to redo).\n", len(movable)-failed, len(movable), incomplete)
	if failed > 0 || incomplete > 0 {
		return fmt.Errorf("%d chats were not moved and %d need delivery setup", failed, incomplete)
	}
	return nil
}

func attachLiveAgent(ctx context.Context, o *options, root string, chosen liveAgentSession, name string) error {
	return attachLiveAgentWithSetup(ctx, o, root, chosen, name, true)
}

// attachedButIncomplete marks a failure after the bridge already committed the
// attachment, so a caller never says the chat stayed where it was.
type attachedButIncomplete struct{ err error }

func (e attachedButIncomplete) Error() string { return e.err.Error() }
func (e attachedButIncomplete) Unwrap() error { return e.err }

func attachLiveAgentWithSetup(ctx context.Context, o *options, root string, chosen liveAgentSession, name string, installPlugin bool) error {
	var err error
	if chosen.provider == "pi" {
		root, err = piBridgeRoot()
		if err != nil {
			return err
		}
		record, e := piSessionForHost(root, chosen.host, chosen.session)
		if e != nil || record.Mode != "tui" {
			return errors.New("no exact Pi TUI SDK session")
		}
		chosen.remote = ""
	}
	if _, err = uuid.Parse(chosen.session); err != nil {
		return errors.New("no exact live session UUID")
	}
	if chosen.host == "" {
		return errors.New("cannot verify the provider process")
	}
	if installPlugin && chosen.provider != "pi" {
		if err = installAgentPlugin(ctx, o, chosen.provider); err != nil {
			return err
		}
	}
	if chosen.remote == "" {
		chosen.remote = discoverNativeRemote(chosen.provider, chosen.session, chosen.host)
	}
	req := agentworksclient.LocalBridgeRequest{Action: "attach", Provider: chosen.provider, SessionID: chosen.session, HostKey: chosen.host, NativeRemote: chosen.remote, AgentName: name}
	if err = prepareAttachment(ctx, root, o, &req, chosen.cwd); err != nil {
		return err
	}
	// The calling agent receives the roster in this command's output. A native
	// setup turn is only needed for a person command consumed by an adapter.
	if !installPlugin {
		req.Setup = false
		req.OwnAttach = chosen.provider == "codex"
	}
	out, err := bridgeAttach(ctx, root, o, &req)
	if err != nil {
		return err
	}
	if chosen.provider == "pi" && out.Attached {
		if err = activatePiSession(ctx, root, chosen.host, chosen.session, out.Attachment.ID); err != nil {
			return attachedButIncomplete{err}
		}
		req.Action, req.AttachmentID = "status", out.Attachment.ID
		out, err = agentworksclient.CallLocalBridge(ctx, root, req)
		if err != nil {
			return attachedButIncomplete{err}
		}
	}
	if o.jsonOutput {
		return o.output(out)
	}
	if out.Warning != "" {
		fmt.Fprintln(o.stderr, "Warning:", out.Warning)
	}
	if !installPlugin && !out.NativeDelivery && o.switchPath == "" {
		fmt.Fprintf(o.stdout, "AgentWorks attached as agent:%s. Native delivery is unavailable; use the CLI inbox for recovery. No plugin was installed.\n", out.Attachment.AgentName)
		if help := pluginInstallHelp(chosen.provider); help != "" {
			fmt.Fprintf(o.stdout, "For live delivery the person installs the %s plugin: %s\n", chosen.provider, help)
		}
	} else {
		fmt.Fprintln(o.stdout, pluginAttachmentStatus(out))
	}
	if o.reconnectNote != "" {
		fmt.Fprintln(o.stdout, o.reconnectNote)
	} else if o.newAgentWhy != "" {
		fmt.Fprintln(o.stdout, o.newAgentWhy)
	}
	// A move already reports each chat; avoid repeating the whole directory.
	if o.switchPath != "" {
		return nil
	}
	req.Action = "peers"
	peers, e := agentworksclient.CallLocalBridge(ctx, root, req)
	if e == nil {
		local, _ := agentworksclient.CallLocalBridge(ctx, root, agentworksclient.LocalBridgeRequest{Action: "list"})
		fmt.Fprintln(o.stdout, agentRoster(peers.Result, local))
	}
	if !installPlugin {
		fmt.Fprintf(o.stdout, "Describe your responsibilities with agentworks bridge message purpose --provider %s --purpose TEXT.\n", chosen.provider)
	}
	return nil
}

func detachAgentCommand(o *options) *cobra.Command {
	var from string
	cmd := &cobra.Command{Use: "detach", Args: cobra.NoArgs, Short: "Stop delivery to an attached local chat", RunE: func(cmd *cobra.Command, _ []string) error {
		if from != "" {
			if _, err := o.loginPath(from); err != nil {
				return err
			}
		}
		root, err := agentworksclient.DefaultLocalBridgeRoot()
		if err != nil {
			return err
		}
		if os.Getenv("AGENTWORKS_BRIDGE_ROOT") != "" {
			root, err = piBridgeRoot()
			if err != nil {
				return err
			}
		}
		own, fromAgent, err := ownAgentSession(cmd.Context(), root, o)
		if err != nil {
			return err
		}
		if fromAgent {
			request := agentworksclient.LocalBridgeRequest{Action: "detach_person", Provider: own.provider, SessionID: own.session, HostKey: own.host, Network: from}
			if own.provider == "codex" {
				request.Action = "detach_own"
			}
			if own.provider == "pi" || own.provider == "codex" || own.provider == "cursor" {
				current, e := bridgeCall(cmd.Context(), root, o, agentworksclient.LocalBridgeRequest{Action: "status", Provider: own.provider, SessionID: own.session, HostKey: own.host})
				if e != nil {
					return e
				}
				request.AttachmentID = current.Attachment.ID
			}
			if _, err = bridgeCall(cmd.Context(), root, o, request); err != nil {
				return err
			}
			if from != "" {
				fmt.Fprintf(o.stdout, "This chat left network %s.\n", from)
			} else {
				fmt.Fprintln(o.stdout, "Detached this chat from AgentWorks.")
			}
			return nil
		}
		if err := personAgentTerminal(); err != nil {
			return err
		}
		out, err := bridgeCall(cmd.Context(), root, o, agentworksclient.LocalBridgeRequest{Action: "list"})
		if err != nil {
			return err
		}
		sessions := localAttachedChats(out)
		if len(sessions) == 0 {
			fmt.Fprintln(o.stdout, "No attached chats.")
			return nil
		}
		for i, s := range sessions {
			fmt.Fprintf(o.stderr, "%d. agent:%s (%s)\n", i+1, s.AgentName, s.Provider)
		}
		fmt.Fprint(o.stderr, "Choose the chat to detach (number, or cancel): ")
		var answer string
		if _, err = fmt.Fscanln(o.stdin, &answer); err != nil {
			return err
		}
		n, err := strconv.Atoi(answer)
		if err != nil || n < 1 || n > len(sessions) {
			return errors.New("detach cancelled")
		}
		a := sessions[n-1]
		request := agentworksclient.LocalBridgeRequest{Action: "detach_person", Provider: a.Provider, SessionID: a.SessionID, Network: from}
		if a.Provider == "pi" {
			request.AttachmentID = a.ID
		}
		_, err = bridgeCall(cmd.Context(), root, o, request)
		if err != nil {
			return err
		}
		if from != "" {
			fmt.Fprintf(o.stdout, "agent:%s left network %s.\n", a.AgentName, from)
		} else {
			fmt.Fprintf(o.stdout, "Detached agent:%s.\n", a.AgentName)
		}
		return nil
	}}
	cmd.Flags().StringVar(&from, "from", "", "Leave only this extra network, keeping the chat's own connection")
	return cmd
}

func installAgentPlugin(ctx context.Context, o *options, provider string) error {
	if provider == "pi" {
		return installPiPlugin(ctx, o)
	}
	if !messagingPluginProvider(provider) {
		return errors.New("this shared installer supports Codex, Claude Code, Muse and Pi")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	if provider == "muse" {
		return installMusePlugin(ctx, o)
	}
	if provider == "codex" {
		if e := codexHomeError(); e != nil {
			return e
		}
	}
	// Existing cache + installation registry are required; a downloaded cache on
	// its own is not an installed plugin. Never enable/trust hooks programmatically.
	registry := filepath.Join(home, ".claude", "plugins", "installed_plugins.json")
	marker := "agentworks@agentworks"
	if provider == "codex" {
		registry = filepath.Join(codexHomeDir(home), "plugins", "installed_plugins.json")
		marker = "agentworks-codex"
	}
	if provider == "codex" {
		var cfg struct {
			Plugins map[string]struct {
				Enabled bool `toml:"enabled"`
			} `toml:"plugins"`
		}
		if b, e := os.ReadFile(filepath.Join(codexHomeDir(home), "config.toml")); e == nil && toml.Unmarshal(b, &cfg) == nil {
			if _, exists := cfg.Plugins["agentworks-codex@agentworks"]; exists {
				return nil
			}
		}
	}
	// A project-scoped Claude install elsewhere is not an every-project setup.
	if b, e := os.ReadFile(registry); e == nil && json.Valid(b) && strings.Contains(string(b), marker) && !(o.setupAll && provider == "claude") {
		return nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	if !o.setupAll {
		fmt.Fprintf(o.stderr, "Install the AgentWorks %s plugin in %s? Review provider trust prompts afterward. Type yes: ", provider, cwd)
		var answer string
		if _, err = fmt.Fscanln(o.stdin, &answer); err != nil || answer != "yes" {
			return errors.New("plugin installation cancelled")
		}
	}
	var steps [][]string
	if provider == "claude" {
		scope := "project"
		if o.setupAll {
			scope = "user"
		}
		steps = [][]string{{"plugin", "marketplace", "add", "manishiitg/agentworks-plugins", "--scope", scope}, {"plugin", "install", "agentworks@agentworks", "--scope", scope}}
	} else {
		steps = [][]string{{"plugin", "marketplace", "add", "manishiitg/agentworks-plugins"}, {"plugin", "add", "agentworks-codex@agentworks"}}
	}
	for _, args := range steps {
		c := exec.CommandContext(ctx, providerExecutable(provider), args...)
		c.Stdin = o.stdin
		result, e := c.CombinedOutput()
		if e != nil && !strings.Contains(strings.ToLower(string(result)), "already") {
			return fmt.Errorf("%s plugin setup failed: %s", provider, strings.TrimSpace(string(result)))
		}
		fmt.Fprint(o.stderr, string(result))
	}
	return nil
}

func installMusePlugin(ctx context.Context, o *options) error {
	// Use the provider's installation registry, not its download cache. Listing
	// never enables hooks or changes the person's approval decisions.
	result, err := exec.CommandContext(ctx, "muse", "plugins", "list", "--json").Output()
	if err != nil {
		return fmt.Errorf("cannot inspect Muse plugin installation: %w", err)
	}
	var installed struct {
		Plugins []struct {
			Record struct {
				ID string `json:"id"`
			} `json:"record"`
			Valid bool `json:"valid"`
		} `json:"plugins"`
	}
	if err = json.Unmarshal(result, &installed); err != nil {
		return fmt.Errorf("cannot read Muse plugin installation: %w", err)
	}
	for _, p := range installed.Plugins {
		if p.Record.ID == "agentworks-muse" && p.Valid {
			return nil
		}
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	if !o.setupAll {
		fmt.Fprintf(o.stderr, "Install the AgentWorks muse plugin in %s? Review provider trust prompts afterward. Type yes: ", cwd)
		var answer string
		if _, err = fmt.Fscanln(o.stdin, &answer); err != nil || answer != "yes" {
			return errors.New("plugin installation cancelled")
		}
	}
	for _, args := range [][]string{
		{"plugins", "marketplace", "add", "agentworks", "https://github.com/manishiitg/agentworks-plugins.git"},
		{"plugins", "install", "agentworks-muse@agentworks"},
	} {
		c := exec.CommandContext(ctx, "muse", args...)
		c.Stdin = o.stdin
		output, e := c.CombinedOutput()
		if e != nil && !strings.Contains(strings.ToLower(string(output)), "already") {
			return fmt.Errorf("muse plugin setup failed: %s", strings.TrimSpace(string(output)))
		}
		fmt.Fprint(o.stderr, string(output))
	}
	fmt.Fprintln(o.stderr, "Muse package installed. Inspect it with muse plugins inspect agentworks-muse --json, approve its hooks and MCP yourself, then restart Muse and type /agentworks attach. Installation does not approve or attach a chat.")
	return nil
}

// runPS lists processes with the given ps arguments.
func runPS(env []string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "ps", args...)
	if env != nil {
		command.Env = append(os.Environ(), env...)
	}
	return command.Output()
}

// psSnapshots lets the local bridge ask "is this chat's process alive?" for every attached chat
// with one process listing instead of one per chat: with twenty chats, a listing each held the
// bridge for tens of seconds (an attach then timed out in its hook). A snapshot is reused for two seconds.
var psSnapshots struct {
	sync.Mutex
	at  time.Time
	key string
	out []byte
	err error
}

func cachedPS(env []string, args ...string) ([]byte, error) {
	psSnapshots.Lock()
	defer psSnapshots.Unlock()
	key := strings.Join(env, " ") + "|" + strings.Join(args, " ")
	if psSnapshots.key == key && time.Since(psSnapshots.at) < 2*time.Second {
		return psSnapshots.out, psSnapshots.err
	}
	out, err := runPS(env, args...)
	psSnapshots.key, psSnapshots.at, psSnapshots.out, psSnapshots.err = key, time.Now(), out, err
	return out, err
}

func providerHostAlive(provider, host string) bool {
	return providerHostAliveWith(provider, host, runPS)
}

// providerHostAliveCached is the local bridge's check: many chats, one process listing.
func providerHostAliveCached(provider, host string) bool {
	return providerHostAliveWith(provider, host, cachedPS)
}

func providerHostAliveWith(provider, host string, ps func(env []string, args ...string) ([]byte, error)) bool {
	if provider == "pi" {
		if host == "" {
			return false
		}
		out, err := ps([]string{"LC_ALL=C", "TZ=UTC"}, "-axo", "pid=", "-o", "comm=", "-o", "args=", "-o", "lstart=")
		if err != nil {
			return true
		}
		for _, line := range strings.Split(string(out), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 8 || !piProcessFields(fields[1:len(fields)-5]) {
				continue
			}
			sum := sha256.Sum256([]byte(fields[0] + " " + strings.Join(fields[len(fields)-5:], " ")))
			if hex.EncodeToString(sum[:]) == host {
				return true
			}
		}
		return false
	}
	if host == "" {
		return false
	}
	if strings.HasPrefix(host, codexDesktopHostPrefix) {
		// A key derived from the desktop's own connection: alive while its socket answers.
		remote, err := codexDesktopSocket()
		if err != nil {
			return false
		}
		conn, err := net.DialTimeout("unix", strings.TrimPrefix(remote, "desktop+unix://"), time.Second)
		if err != nil {
			return false
		}
		_ = conn.Close()
		return true
	}
	if runtime.GOOS == "windows" {
		return winHostAlive(provider, host)
	}
	out, err := ps(nil, "-axo", "pid=", "-o", "lstart=", "-o", providerProcessField(provider))
	if err != nil {
		return true
	} // Permission failure is not evidence of death.
	for _, line := range strings.Split(string(out), "\n") {
		p := strings.Fields(line)
		if len(p) < 7 || !providerProcessMatches(provider, strings.Join(p[6:], " ")) {
			continue
		}
		sum := sha256.Sum256([]byte(p[0] + " " + strings.Join(p[1:6], " ")))
		if hex.EncodeToString(sum[:]) == host {
			return true
		}
	}
	return false
}

// The helper has one local chat projection. Joined networks are connections of
// that same chat; discovery and a move use its primary saved identity once.
func localAttachedChats(local agentworksclient.LocalBridgeReply) []agentworksclient.AgentAttachment {
	chats := make([]agentworksclient.AgentAttachment, 0, len(local.AgentStates))
	for _, state := range local.AgentStates {
		if state.Network == "" {
			chats = append(chats, state.AgentAttachment)
		}
	}
	return chats
}

func discoverLiveAgents(ctx context.Context, root string, o *options, only ...agentworksclient.AgentAttachment) ([]liveAgentSession, error) {
	if os.Getenv("AGENTWORKS_BRIDGE_ROOT") != "" {
		configured, err := piBridgeRoot()
		if err != nil {
			return nil, err
		}
		local, _ := agentworksclient.CallLocalBridge(ctx, configured, agentworksclient.LocalBridgeRequest{Action: "list"})
		return discoverPiLiveAgents(configured, local), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	local, _ := agentworksclient.CallLocalBridge(ctx, root, agentworksclient.LocalBridgeRequest{Action: "list"})
	if len(only) > 0 {
		wanted := map[string]bool{}
		for _, saved := range only {
			wanted[saved.Provider+":"+saved.SessionID] = true
		}
		selected := local.AgentStates[:0]
		for _, state := range local.AgentStates {
			if state.Network == "" && wanted[state.Provider+":"+state.SessionID] {
				selected = append(selected, state)
			}
		}
		local.AgentStates = selected
	}
	var found []liveAgentSession
	names := map[string]string{}
	for _, s := range localAttachedChats(local) {
		names[s.Provider+":"+s.SessionID] = s.AgentName
	}
	for _, s := range discoverMuseSessions(root) {
		s.name = names["muse:"+s.session]
		found = append(found, s)
	}
	files, _ := filepath.Glob(filepath.Join(home, ".claude", "sessions", "*.json"))
	for _, file := range files {
		b, e := os.ReadFile(file)
		if e != nil || len(b) > 64<<10 {
			continue
		}
		var r claudeSessionRecord
		if json.Unmarshal(b, &r) != nil {
			continue
		}
		remote := "claude+session://" + file
		if _, _, e = nativeClaudeSession(remote, r.SessionID); e != nil {
			continue
		}
		host, e := providerPIDHostKey("claude", r.PID)
		if e != nil {
			continue
		}
		found = append(found, liveAgentSession{provider: "claude", session: r.SessionID, host: host, remote: remote, name: names["claude:"+r.SessionID]})
	}
	// The database is only a candidate catalog. Every candidate must be confirmed
	// by a running desktop owner supporting untrusted input before it is selectable.
	remote := "desktop+unix://" + filepath.Join(home, ".codex", "ipc", "ipc.sock")
	desktopHost, _ := codexDesktopProcessHost(ctx, remote)
	if desktopHost != "" && validateCodexRemote(remote) == nil {
		candidates := codexProbeCandidates(localAttachedChats(local))
		leased := map[string]bool{}
		for id := range candidates {
			leased[id] = true
		}
		var recent []string // catalog threads, newest first
		codexDir := o.getenv("CODEX_HOME")
		if codexDir == "" {
			codexDir = filepath.Join(home, ".codex")
		}
		// mode=ro prevents this observer creating or mutating the desktop catalog.
		db, e := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(codexDir, "state_5.sqlite"))+"?mode=ro")
		if e == nil {
			catalog := map[string]string{}
			rows, e := db.QueryContext(ctx, "SELECT id,cwd FROM threads WHERE archived=0 ORDER BY updated_at DESC")
			if e == nil {
				for rows.Next() {
					var id, cwd string
					if rows.Scan(&id, &cwd) == nil {
						catalog[id] = cwd
						recent = append(recent, id)
					}
				}
				rows.Close()
			}
			db.Close()
			mergeCodexCatalog(candidates, catalog, o.attachedOnly)
		}
		probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		found = append(found, discoverCodexDesktopSessions(probeCtx, remote, desktopHost, codexProbeOrder(leased, candidates, recent), candidates, names)...)
	}
	if observations, err := cursorLiveObservations(root); err == nil {
		for _, observation := range observations {
			found = append(found, liveAgentSession{provider: "cursor", session: observation.Session, host: observation.Host, cwd: observation.Cwd, name: names["cursor:"+observation.Session]})
		}
	}
	found = append(found, discoverPiLiveAgents(root, local)...)

	sort.Slice(found, func(i, j int) bool { return found[i].provider+found[i].session < found[j].provider+found[j].session })
	return found, nil
}

// A closed catalog thread may never answer owner discovery. Probe independently
// so that it cannot delay a live chat or turn an ordinary update into an attach
// request. These are bounded read-only native metadata queries, not inbox polls.
func discoverCodexDesktopSessions(ctx context.Context, remote, host string, ids []string, folders, names map[string]string) []liveAgentSession {
	jobs := make(chan string)
	var mu sync.Mutex
	var found []liveAgentSession
	var workers sync.WaitGroup
	for i := 0; i < 8; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for id := range jobs {
				if _, err := uuid.Parse(id); err != nil || ctx.Err() != nil {
					continue
				}
				probe, cancel := context.WithTimeout(ctx, time.Second)
				conn, call, err := openCodexDesktop(probe, remote)
				if err != nil {
					cancel()
					continue
				}
				reply, err := call("thread-owner-discovery", map[string]any{"hostId": "local", "conversationId": id}, 1, "")
				conn.Close()
				cancel()
				if err != nil {
					continue
				}
				var owner string
				var support struct {
					Untrusted bool `json:"supportsUntrustedAppInput"`
				}
				_ = json.Unmarshal(reply["handledByClientId"], &owner)
				_ = json.Unmarshal(reply["result"], &support)
				if owner != "" && support.Untrusted {
					mu.Lock()
					found = append(found, liveAgentSession{provider: "codex", session: id, host: host, remote: remote, cwd: folders[id], name: names["codex:"+id]})
					mu.Unlock()
				}
			}
		}()
	}
	for _, id := range ids {
		select {
		case jobs <- id:
		case <-ctx.Done():
			close(jobs)
			workers.Wait()
			return found
		}
	}
	close(jobs)
	workers.Wait()
	return found
}

// stop pauses delivery without deleting attachment identities or revoking the
// person's child connections. start/update restore only existing approvals.
func stopAgentBridge(ctx context.Context, reason string) error {
	root, err := agentworksclient.DefaultLocalBridgeRoot()
	if err != nil {
		return err
	}
	return stopLocalBridgeAt(ctx, root, reason)
}

func stopLocalBridgeAt(ctx context.Context, root, reason string) error {
	var err error
	if _, err = agentworksclient.CallLocalBridge(ctx, root, agentworksclient.LocalBridgeRequest{Action: "ping"}); err != nil {
		return nil
	}
	reply, _ := agentworksclient.CallLocalBridge(ctx, root, agentworksclient.LocalBridgeRequest{Action: "shutdown", ShutdownReason: reason})
	if reply.Error != "" {
		return errors.New(reply.Error)
	}
	// The HTTP reply may race shutdown. Completion is observed through the socket.
	wait, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for {
		if _, e := agentworksclient.CallLocalBridge(wait, root, agentworksclient.LocalBridgeRequest{Action: "ping"}); e != nil {
			return nil
		}
		select {
		case <-wait.Done():
			return errors.New("delivery bridge did not stop")
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func restoreAgentBridge(ctx context.Context, o *options, executable string) error {
	root, err := agentworksclient.DefaultLocalBridgeRoot()
	if err != nil {
		return err
	}
	// Never turn a folder-sharing start into a first attachment or parent login.
	if _, err = os.Stat(filepath.Join(root, "sessions.json")); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if running, pingErr := agentworksclient.CallLocalBridge(ctx, root, agentworksclient.LocalBridgeRequest{Action: "ping"}); pingErr == nil {
		if executable != "" || running.Version == cliVersion {
			return nil
		}
		// Explicit start/update adopts the installed binary even when only the
		// helper is old. Ordinary hooks keep a responsive helper untouched.
		if err = stopLocalBridgeAt(ctx, root, "update"); err != nil {
			return err
		}
	}
	if executable == "" {
		executable, err = os.Executable()
		if err != nil {
			return err
		}
	}
	// The new helper waits for the old one to finish exiting (it holds the single-listener lock until then).
	wait, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	return ensureLocalBridgeWithExecutable(wait, root, o, executable)
}

// A PTY alone is not person approval. Reject model-launched terminals even if
// the model allocates a PTY; approval comes from the person's in-chat hook.
func personAgentTerminal() error {
	return personTerminal("attach/detach", "attach or detach chats", "type /agentworks attach or /agentworks detach yourself in the chat")
}

// personTerminal refuses unless this is a real terminal with no coding agent among its ancestors: what is the command,
// verb says what it would do, instead says what to do instead.
func personTerminal(command, verb, instead string) error {
	if !stdinIsTerminal() {
		return errors.New(command + " is a person command; " + instead)
	}
	if runtime.GOOS == "windows" {
		inside, err := winInsideCodingAgent()
		if err != nil {
			return errors.New("cannot verify the terminal process; " + instead)
		}
		if inside {
			return errors.New("coding-agent context cannot " + verb + " through this terminal, including a PTY. Use another terminal, or " + instead)
		}
		return nil
	}
	pid := os.Getppid()
	for i := 0; i < 32 && pid > 1; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		out, err := exec.CommandContext(ctx, "ps", "-p", strconv.Itoa(pid), "-o", "ppid=", "-o", "comm=", "-o", "args=").Output()
		cancel()
		if err != nil {
			return errors.New("cannot verify the terminal process; " + instead)
		}
		fields := strings.Fields(string(out))
		if len(fields) < 2 {
			return errors.New("cannot verify the terminal process")
		}
		for _, f := range fields[1:] {
			if isCodingAgentProcess(f) {
				return errors.New("coding-agent context cannot " + verb + " through this terminal, including a PTY. Use another terminal, or " + instead)
			}
		}
		pid, err = strconv.Atoi(fields[0])
		if err != nil {
			return err
		}
	}
	if pid > 1 {
		return errors.New("cannot verify the terminal ancestry; " + instead)
	}
	return nil
}

func isCodingAgentProcess(command string) bool {
	name := strings.ToLower(filepath.Base(command))
	switch name {
	case "codex", "claude", "cursor-agent", "cursor", "muse", "pi", "agy", "codexcli":
		return true
	}
	return strings.HasPrefix(name, "muse-bin-") || (strings.Contains(filepath.ToSlash(command), "/claude-code/") && name == "cli.js") || name == "pi.mjs" || (strings.Contains(filepath.ToSlash(command), "/pi-coding-agent/") && name == "cli.js")
}

// loginLabel names the saved logins that sign in to a server, so a roster says
// which login it came from. It never shows credentials or paths.
func loginLabel(server string) string {
	want, err := agentworksclient.ValidateServer(server)
	if err != nil {
		return ""
	}
	var names []string
	logins, _ := agentworksclient.ListLogins() // Cosmetic labels; the logins command reports loading errors.
	for _, l := range logins {
		if have, e := agentworksclient.ValidateServer(l.Server); e == nil && have == want {
			names = append(names, l.Name)
		}
	}
	if len(names) == 0 {
		return ""
	}
	return " (login: " + strings.Join(names, ", ") + ")"
}

// shareCommand publishes an agent to the network (everyone on the server can find and
// message it); the unshare form takes it back. With --agent it is the person's terminal
// command for one of their agents; without it, it acts on this chat's own attached agent,
// which a coding agent may do for its own exact session when the person asked.
func shareCommand(o *options, unshare bool) *cobra.Command {
	var agent string
	use, short := "share [@LOGIN]", "Publish an agent to the network (everyone on the server can find and message it)"
	if unshare {
		use, short = "unshare [@LOGIN]", "Take an agent back from the network (only you and your agents can reach it)"
	}
	cmd := &cobra.Command{Use: use, Args: cobra.MaximumNArgs(1), Short: short, Long: "An agent is connected (reachable only by you and your own agents) or published to the network (everyone on the server can find and message it). Messages from other people's agents arrive marked as untrusted. @LOGIN acts on a network this chat joined with --also.", RunE: func(cmd *cobra.Command, args []string) error {
		network := ""
		if len(args) == 1 {
			var ok bool
			if network, ok = strings.CutPrefix(args[0], "@"); !ok || network == "" {
				return errors.New("use @LOGIN to name another network, or no argument for this chat's own network")
			}
			if _, e := o.loginPath(network); e != nil {
				return fmt.Errorf("no saved login named %s", network)
			}
		}
		verb := "share"
		if unshare {
			verb = "unshare"
		}
		if agent != "" {
			if network != "" {
				return errors.New("@LOGIN works on this chat's own agent; run the command in the chat without --agent")
			}
			if err := personAgentTerminal(); err != nil {
				return err
			}
			client, err := o.client()
			if err != nil {
				return err
			}
			raw, err := client.ShareAgent(cmd.Context(), verb, agent)
			if err != nil {
				return err
			}
			return printShareResult(o, raw)
		}
		root, err := agentworksclient.DefaultLocalBridgeRoot()
		if err != nil {
			return err
		}
		own, fromAgent, err := ownAgentSession(cmd.Context(), root, o)
		if err != nil {
			return err
		}
		if !fromAgent {
			return fmt.Errorf("name the agent: agentworks %s --agent AGENT, or type /agentworks %s in the chat", verb, verb)
		}
		// @LOGIN: the network this chat joined with --also. Only the person's own
		// request, run in the chat (never a message tool).
		out, err := bridgeCall(cmd.Context(), root, o, agentworksclient.LocalBridgeRequest{Action: verb + "_own", Provider: own.provider, SessionID: own.session, HostKey: own.host, Network: network})
		if err != nil {
			return err
		}
		if o.jsonOutput {
			return o.output(out)
		}
		return printShareResult(o, out.Result)
	}}
	cmd.Flags().StringVar(&agent, "agent", "", "One of your registered agents (a person's terminal); omit to act on this chat's own agent")
	return cmd
}

// printShareResult shows a publish or unpublish result in plain words.
func printShareResult(o *options, raw json.RawMessage) error {
	if o.jsonOutput {
		_, err := fmt.Fprintln(o.stdout, string(raw))
		return err
	}
	_, err := fmt.Fprintln(o.stdout, shareResultText(raw))
	return err
}

func shareResultText(raw json.RawMessage) string {
	var data struct {
		State        string `json:"state"`
		Agent        string `json:"agent_name"`
		WasPublished bool   `json:"was_published"`
	}
	if json.Unmarshal(raw, &data) != nil {
		// Never report success without a result.
		return "AgentWorks sent back no result, so this may not have changed anything. Check with /agentworks agents."
	}
	switch data.State {
	case "published":
		return fmt.Sprintf("agent:%s is published to the network. Everyone on the server can find and message it; their messages arrive marked as untrusted.", data.Agent)
	case "unpublished":
		if !data.WasPublished {
			return fmt.Sprintf("agent:%s was not published.", data.Agent)
		}
		return fmt.Sprintf("agent:%s is no longer published. Only you and your own agents can reach it.", data.Agent)
	}
	return "AgentWorks sent back an unexpected result, so this may not have changed anything. Check with /agentworks agents."
}

// codexProbeCandidates are the Codex threads the bridge already has a lease for.
func codexProbeCandidates(sessions []agentworksclient.AgentAttachment) map[string]string {
	candidates := map[string]string{}
	for _, s := range sessions {
		if s.Provider == "codex" {
			candidates[s.SessionID] = ""
		}
	}
	return candidates
}

// codexProbeMax is how many threads without a lease are probed. A desktop catalog can hold
// thousands of threads (one owner's held twelve thousand, and probing them in id order used up
// the whole time limit before reaching an open chat); only recently used ones can be open.
const codexProbeMax = 150

// codexProbeOrder lists every leased thread first, then the newest catalog threads that are
// candidates, up to codexProbeMax of them.
func codexProbeOrder(leased map[string]bool, candidates map[string]string, recent []string) []string {
	ids := make([]string, 0, len(leased)+codexProbeMax)
	for id := range leased {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	extra := 0
	for _, id := range recent {
		if _, ok := candidates[id]; !ok || leased[id] {
			continue
		}
		if extra++; extra > codexProbeMax {
			break
		}
		ids = append(ids, id)
	}
	return ids
}

// mergeCodexCatalog adds the desktop's thread catalog to the candidates. A move
// (attachedOnly) probes only threads that already have a lease, so the catalog
// then only supplies their working directories instead of adding thousands of
// threads to probe.
func mergeCodexCatalog(candidates, catalog map[string]string, attachedOnly bool) {
	for id, cwd := range catalog {
		if _, leased := candidates[id]; leased || !attachedOnly {
			candidates[id] = cwd
		}
	}
}

// parseShareWords fills a bridge request from the words after share or unshare
// (an optional @LOGIN), and returns a one-line usage message when they are wrong.
func parseShareWords(action string, rest []string, req *agentworksclient.LocalBridgeRequest) string {
	if len(rest) > 1 {
		return "Use /agentworks " + action + " [@LOGIN]"
	}
	if len(rest) == 1 {
		if !strings.HasPrefix(rest[0], "@") || len(rest[0]) < 2 {
			return "Use /agentworks " + action + " [@LOGIN]"
		}
		req.Network = rest[0][1:]
	}
	return ""
}

// bridgeCall talks to the local helper and, when it is not running, starts it and
// tries once more. Person commands always start it; background hook events do so
// only when this computer has ever attached a chat, so a plugin that was never
// used does not leave a helper behind.
// errStillConnecting is what a hook shows when the bridge did not answer within the hook's time:
// the work usually finishes on its own, and trying again then finds it done.
var errStillConnecting = errors.New("AgentWorks is still connecting. Try again in a moment.")

// errStillAttaching is what an attach shows when it ran out of the hook's time: the attach keeps going in the
// helper and the next prompt says when it is done (it is not an error, but the hook can only report it as text).
var errStillAttaching = errors.New("AgentWorks is still connecting this chat. It keeps going; \"attached\" will be confirmed with your next message.")

func bridgeCall(ctx context.Context, root string, o *options, req agentworksclient.LocalBridgeRequest) (agentworksclient.LocalBridgeReply, error) {
	out, err := bridgeCallOnce(ctx, root, o, req)
	if err != nil && errors.Is(err, context.DeadlineExceeded) {
		if req.Action == "attach" || req.Action == "resume" {
			return out, errStillAttaching
		}
		return out, errStillConnecting
	}
	return out, namedLoginFailure(o, req, err)
}

// namedLoginFailure says which login file and server a failed sign-in belongs to, so a stale or unreachable login
// (for example AGENTWORKS_CONFIG pointing at a dead local server) is not an anonymous "connection refused". It never
// switches to another login: that could deliver to a different server or account without the person knowing.
func namedLoginFailure(o *options, req agentworksclient.LocalBridgeRequest, err error) error {
	if err == nil || !(strings.Contains(err.Error(), "refresh browser login") || strings.Contains(err.Error(), "run agentworks login")) {
		return err
	}
	path := req.ConfigPath
	if path == "" {
		if p, e := o.path(); e == nil {
			path = p
		}
	}
	if path == "" {
		return err
	}
	server := "unknown server"
	if cfg, e := agentworksclient.LoadConfig(path); e == nil && cfg.Server != "" {
		server = cfg.Server
	}
	fix := "Run agentworks login for that server, or point this chat at a working login."
	if o.getenv != nil && o.getenv("AGENTWORKS_CONFIG") != "" {
		fix = "AGENTWORKS_CONFIG selects this login; unset it or point it at a working login (agentworks logins lists them)."
	}
	return fmt.Errorf("The login %s (server %s) cannot be used: %w. %s", path, server, err, fix)
}

func bridgeCallOnce(ctx context.Context, root string, o *options, req agentworksclient.LocalBridgeRequest) (agentworksclient.LocalBridgeReply, error) {
	out, err := agentworksclient.CallLocalBridge(ctx, root, req)
	if err == nil || !(errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED)) {
		return out, err
	}
	background := req.Action == "status" || req.Action == "drain" || req.Action == "offline" || req.Action == "list" || req.Action == "ping"
	if background {
		if _, statErr := os.Stat(filepath.Join(root, "sessions.json")); statErr != nil {
			return out, err
		}
	}
	if ensureLocalBridge(ctx, root, o) != nil {
		return out, errors.New("The AgentWorks helper is not running and could not be started. Run agentworks update, then try again.")
	}
	return agentworksclient.CallLocalBridge(ctx, root, req)
}
