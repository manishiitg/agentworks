package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
	"github.com/pelletier/go-toml/v2"
	"github.com/spf13/cobra"
	_ "modernc.org/sqlite"
)

const personCommandHelp = "AgentWorks: /agentworks attach [NAME] connects or reconnects this chat; /agentworks detach stops delivery; /agentworks agents shows available agents. Type the command yourself."

// Reuse the exact session's saved identity and parent login, even when the
// default CLI login has changed. The person's attach refreshes the process fence.
func prepareAttachment(ctx context.Context, root string, o *options, req *agentworksclient.LocalBridgeRequest, cwd string) error {
	req.ReuseIdentity = req.AgentName == ""
	req.Setup = true
	if err := ensureLocalBridge(ctx, root, o); err != nil {
		return err
	}
	status := *req
	status.Action = "status"
	current, err := agentworksclient.CallLocalBridge(ctx, root, status)
	if err != nil {
		return err
	}
	// A new process cannot inspect an active lease, but attach can recover it by
	// exact provider/session in the bridge, without granting it delivery first.
	if req.AgentName == "" && current.Attachment.AgentName != "" {
		req.AgentName = current.Attachment.AgentName
	}
	if req.AgentName == "" {
		req.AgentName, _, err = proposedAgentIdentity(req.Provider, req.SessionID, cwd)
		if err != nil {
			return err
		}
	}
	req.ConfigPath, err = o.path()
	if err != nil {
		return err
	}
	req.ConfigPath, err = filepath.Abs(req.ConfigPath)
	return err
}

func pluginAgents(ctx context.Context, root string, o *options, req agentworksclient.LocalBridgeRequest) error {
	req.Action = "peers"
	peers, err := agentworksclient.CallLocalBridge(ctx, root, req)
	if err != nil {
		return pluginBlock(o, err.Error())
	}
	if !peers.Attached {
		return pluginBlock(o, "This chat is not attached. Type /agentworks attach first.")
	}
	local, _ := agentworksclient.CallLocalBridge(ctx, root, agentworksclient.LocalBridgeRequest{Action: "list"})
	return pluginBlock(o, agentRoster(peers.Result, local))
}

func agentsCommand(o *options) *cobra.Command {
	return &cobra.Command{Use: "agents", Aliases: []string{"agent"}, Args: cobra.NoArgs, Short: "Show agents visible through your authorized connections", RunE: func(cmd *cobra.Command, _ []string) error {
		root, err := agentworksclient.DefaultLocalBridgeRoot()
		if err != nil {
			return err
		}
		local, _ := agentworksclient.CallLocalBridge(cmd.Context(), root, agentworksclient.LocalBridgeRequest{Action: "list"})
		if o.serverURL == "" && o.configPath == "" && o.getenv("AGENTWORKS_CONFIG") == "" {
			dirs, _ := agentworksclient.CallLocalBridge(cmd.Context(), root, agentworksclient.LocalBridgeRequest{Action: "directories"})
			if len(dirs.Directories) > 0 {
				if o.jsonOutput {
					return o.output(map[string]any{"directories": dirs.Directories, "local": local.AgentStates})
				}
				for _, d := range dirs.Directories {
					fmt.Fprintln(o.stdout, d.Server)
					fmt.Fprintln(o.stdout, agentRoster(d.Agents, local))
				}
				return nil
			}
		}
		client, err := o.client()
		if err != nil {
			return err
		}
		raw, err := client.Call(cmd.Context(), "messages", map[string]any{"action": "list_agents"})
		if err != nil {
			return err
		}
		if o.jsonOutput {
			return o.output(map[string]any{"agents": json.RawMessage(raw), "local": local.AgentStates})
		}
		_, err = fmt.Fprintln(o.stdout, agentRoster(raw, local))
		return err
	}}
}

// Only server-owned names/purposes are shown, with control characters removed.
// Match local observations by agent ID, never by name across accounts/servers.
func agentRoster(raw json.RawMessage, local agentworksclient.LocalBridgeReply) string {
	var data struct {
		Agents []struct {
			Name     string `json:"agent_name"`
			ID       string `json:"agent_id"`
			Provider string `json:"provider"`
			Purpose  string `json:"purpose"`
			Online   bool   `json:"online"`
		} `json:"agents"`
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
			fmt.Fprintf(&b, "… %d more; use agentworks agents --json.\n", len(data.Agents)-i)
			break
		}
		presence, route := "offline", "stored"
		if a.Online {
			presence, route = "online", "server stream; local route unknown"
		}
		detail := ""
		for _, s := range local.AgentStates {
			if s.AgentID == a.ID {
				if !s.Online {
					presence = "offline"
				}
				route = s.Route
				detail = fmt.Sprintf("; %d pending; last: %s", s.Pending, clean(s.LastDelivery))
				if s.LastError != "" {
					detail += "; " + clean(s.LastError)
				}
				break
			}
		}
		fmt.Fprintf(&b, "agent:%s | %s | %s | %s%s | %s\n", clean(a.Name), clean(a.Provider), presence, route, detail, clean(a.Purpose))
	}
	if len(data.Agents) == 0 {
		b.WriteString("No registered agents.\n")
	}
	return b.String()
}

type liveAgentSession struct{ provider, session, host, remote, cwd, name string }

func attachPluginCommand(o *options) *cobra.Command {
	var provider, session, remote, name string
	var pid int
	cmd := &cobra.Command{Use: "attach [NAME]", Args: cobra.MaximumNArgs(1), Short: "Install, attach or reconnect a live coding-agent chat", RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 {
			name = args[0]
		}
		root, err := agentworksclient.DefaultLocalBridgeRoot()
		if err != nil {
			return err
		}
		own, fromAgent, err := ownAgentSession(cmd.Context(), root, o)
		if err != nil {
			return err
		}
		if fromAgent {
			if provider != "" || session != "" || remote != "" || pid != 0 {
				return errors.New("own-session attach does not accept provider, session, process or endpoint overrides")
			}
			return attachLiveAgentWithSetup(cmd.Context(), o, root, own, name, false)
		}
		if err := personAgentTerminal(); err != nil {
			return err
		}
		// Explicit technical overrides remain hidden for adapter diagnostics.
		var chosen liveAgentSession
		var selected []liveAgentSession
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
					if _, e := exec.LookPath(p); e == nil {
						if e = installAgentPlugin(cmd.Context(), o, p); e != nil {
							return e
						}
						installed = true
					}
				}
				if !installed {
					return errors.New("no supported coding CLI found on PATH; install Codex, Claude Code, Muse or Pi first")
				}
				fmt.Fprintln(o.stdout, "AgentWorks plugin ready. Reload your coding chat, review its hooks, then type /agentworks attach. No chat has been attached.")
				return nil
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
	cmd.Flags().StringVar(&name, "name", "", "Name override")
	for _, flag := range []string{"provider", "session", "native-remote", "native-pid", "name"} {
		_ = cmd.Flags().MarkHidden(flag)
	}
	return cmd
}

func attachLiveAgent(ctx context.Context, o *options, root string, chosen liveAgentSession, name string) error {
	return attachLiveAgentWithSetup(ctx, o, root, chosen, name, true)
}

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
	out, err := agentworksclient.CallLocalBridge(ctx, root, req)
	if err != nil {
		return err
	}
	if chosen.provider == "pi" && out.Attached {
		if err = activatePiSession(ctx, root, chosen.host, chosen.session, out.Attachment.ID); err != nil {
			return err
		}
		req.Action, req.AttachmentID = "status", out.Attachment.ID
		out, err = agentworksclient.CallLocalBridge(ctx, root, req)
		if err != nil {
			return err
		}
	}
	if o.jsonOutput {
		return o.output(out)
	}
	if !installPlugin && !out.NativeDelivery {
		fmt.Fprintf(o.stdout, "AgentWorks attached as agent:%s. Native delivery is unavailable; use the CLI inbox for recovery. No plugin was installed.\n", out.Attachment.AgentName)
	} else {
		fmt.Fprintln(o.stdout, pluginAttachmentStatus(out))
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
	return &cobra.Command{Use: "detach", Args: cobra.NoArgs, Short: "Stop delivery to an attached local chat", RunE: func(cmd *cobra.Command, _ []string) error {
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
			request := agentworksclient.LocalBridgeRequest{Action: "detach_person", Provider: own.provider, SessionID: own.session, HostKey: own.host}
			if own.provider == "codex" {
				request.Action = "detach_own"
			}
			if own.provider == "pi" || own.provider == "codex" || own.provider == "cursor" {
				current, e := agentworksclient.CallLocalBridge(cmd.Context(), root, agentworksclient.LocalBridgeRequest{Action: "status", Provider: own.provider, SessionID: own.session, HostKey: own.host})
				if e != nil {
					return e
				}
				request.AttachmentID = current.Attachment.ID
			}
			if _, err = agentworksclient.CallLocalBridge(cmd.Context(), root, request); err != nil {
				return err
			}
			fmt.Fprintln(o.stdout, "Detached this chat from AgentWorks.")
			return nil
		}
		if err := personAgentTerminal(); err != nil {
			return err
		}
		out, err := agentworksclient.CallLocalBridge(cmd.Context(), root, agentworksclient.LocalBridgeRequest{Action: "list"})
		if err != nil {
			return err
		}
		if len(out.Sessions) == 0 {
			fmt.Fprintln(o.stdout, "No attached chats.")
			return nil
		}
		for i, s := range out.Sessions {
			fmt.Fprintf(o.stderr, "%d. agent:%s (%s)\n", i+1, s.AgentName, s.Provider)
		}
		fmt.Fprint(o.stderr, "Choose the chat to detach (number, or cancel): ")
		var answer string
		if _, err = fmt.Fscanln(o.stdin, &answer); err != nil {
			return err
		}
		n, err := strconv.Atoi(answer)
		if err != nil || n < 1 || n > len(out.Sessions) {
			return errors.New("detach cancelled")
		}
		a := out.Sessions[n-1]
		request := agentworksclient.LocalBridgeRequest{Action: "detach_person", Provider: a.Provider, SessionID: a.SessionID}
		if a.Provider == "pi" {
			request.AttachmentID = a.ID
		}
		_, err = agentworksclient.CallLocalBridge(cmd.Context(), root, request)
		if err != nil {
			return err
		}
		fmt.Fprintf(o.stdout, "Detached agent:%s.\n", a.AgentName)
		return nil
	}}
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
	// Existing cache + installation registry are required; a downloaded cache on
	// its own is not an installed plugin. Never enable/trust hooks programmatically.
	registry := filepath.Join(home, ".claude", "plugins", "installed_plugins.json")
	marker := "agentworks@agentworks"
	if provider == "codex" {
		registry = filepath.Join(home, ".codex", "plugins", "installed_plugins.json")
		marker = "agentworks-codex"
	}
	if provider == "codex" {
		var cfg struct {
			Plugins map[string]struct {
				Enabled bool `toml:"enabled"`
			} `toml:"plugins"`
		}
		if b, e := os.ReadFile(filepath.Join(home, ".codex", "config.toml")); e == nil && toml.Unmarshal(b, &cfg) == nil {
			if _, exists := cfg.Plugins["agentworks-codex@agentworks"]; exists {
				return nil
			}
		}
	}
	if b, e := os.ReadFile(registry); e == nil && json.Valid(b) && strings.Contains(string(b), marker) {
		return nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	fmt.Fprintf(o.stderr, "Install the AgentWorks %s plugin in %s? Review provider trust prompts afterward. Type yes: ", provider, cwd)
	var answer string
	if _, err = fmt.Fscanln(o.stdin, &answer); err != nil || answer != "yes" {
		return errors.New("plugin installation cancelled")
	}
	var steps [][]string
	if provider == "claude" {
		steps = [][]string{{"plugin", "marketplace", "add", "manishiitg/agentworks", "--scope", "project"}, {"plugin", "install", "agentworks@agentworks", "--scope", "project"}}
	} else {
		steps = [][]string{{"plugin", "marketplace", "add", "manishiitg/agentworks"}, {"plugin", "add", "agentworks-codex@agentworks"}}
	}
	for _, args := range steps {
		c := exec.CommandContext(ctx, provider, args...)
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
	fmt.Fprintf(o.stderr, "Install the AgentWorks muse plugin in %s? Review provider trust prompts afterward. Type yes: ", cwd)
	var answer string
	if _, err = fmt.Fscanln(o.stdin, &answer); err != nil || answer != "yes" {
		return errors.New("plugin installation cancelled")
	}
	for _, args := range [][]string{
		{"plugins", "marketplace", "add", "agentworks", "https://github.com/manishiitg/agentworks.git"},
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

func providerHostAlive(provider, host string) bool {
	if provider == "pi" {
		if host == "" {
			return false
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, "ps", "-axo", "pid=", "-o", "comm=", "-o", "args=", "-o", "lstart=")
		command.Env = append(os.Environ(), "LC_ALL=C", "TZ=UTC")
		out, err := command.Output()
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
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ps", "-axo", "pid=", "-o", providerProcessField(provider), "-o", "lstart=").Output()
	if err != nil {
		return true
	} // Permission failure is not evidence of death.
	for _, line := range strings.Split(string(out), "\n") {
		p := strings.Fields(line)
		if len(p) < 7 || !providerProcessMatches(provider, p[1]) {
			continue
		}
		sum := sha256.Sum256([]byte(p[0] + " " + strings.Join(p[len(p)-5:], " ")))
		if hex.EncodeToString(sum[:]) == host {
			return true
		}
	}
	return false
}

func discoverLiveAgents(ctx context.Context, root string, o *options) ([]liveAgentSession, error) {
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
	var found []liveAgentSession
	names := map[string]string{}
	for _, s := range local.Sessions {
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
	var desktopHost string
	ps, e := exec.CommandContext(ctx, "ps", "-axo", "pid=,args=").Output()
	if e == nil {
		for _, line := range strings.Split(string(ps), "\n") {
			p := strings.Fields(line)
			if len(p) < 2 || !codexDesktopHost(line) || !strings.Contains(line, "app-server") {
				continue
			}
			pid, _ := strconv.Atoi(p[0])
			desktopHost, e = providerPIDHostKey("codex", pid)
			if e == nil {
				break
			}
		}
	}
	if desktopHost != "" && validateCodexRemote(remote) == nil {
		candidates := map[string]string{}
		for _, s := range local.Sessions {
			if s.Provider == "codex" {
				candidates[s.SessionID] = ""
			}
		}
		codexDir := o.getenv("CODEX_HOME")
		if codexDir == "" {
			codexDir = filepath.Join(home, ".codex")
		}
		// mode=ro prevents this observer creating or mutating the desktop catalog.
		db, e := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(codexDir, "state_5.sqlite"))+"?mode=ro")
		if e == nil {
			rows, e := db.QueryContext(ctx, "SELECT id,cwd FROM threads WHERE archived=0 ORDER BY updated_at DESC")
			if e == nil {
				for rows.Next() {
					var id, cwd string
					if rows.Scan(&id, &cwd) == nil {
						candidates[id] = cwd
					}
				}
				rows.Close()
			}
			db.Close()
		}
		probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		conn, call, e := openCodexDesktop(probeCtx, remote)
		if e == nil {
			defer conn.Close()
			ids := make([]string, 0, len(candidates))
			for id := range candidates {
				ids = append(ids, id)
			}
			sort.Strings(ids)
			for _, id := range ids {
				if _, e = uuid.Parse(id); e != nil {
					continue
				}
				reply, e := call("thread-owner-discovery", map[string]any{"hostId": "local", "conversationId": id}, 1, "")
				if e != nil {
					fmt.Fprintln(o.stderr, "Desktop discovery did not complete. Open the intended chat and type /agentworks attach if it is missing.")
					break
				}
				var owner string
				var support struct {
					Untrusted bool `json:"supportsUntrustedAppInput"`
				}
				_ = json.Unmarshal(reply["handledByClientId"], &owner)
				_ = json.Unmarshal(reply["result"], &support)
				if owner != "" && support.Untrusted {
					found = append(found, liveAgentSession{provider: "codex", session: id, host: desktopHost, remote: remote, cwd: candidates[id], name: names["codex:"+id]})
				}
			}
		}
	}
	found = append(found, discoverPiLiveAgents(root, local)...)

	sort.Slice(found, func(i, j int) bool { return found[i].provider+found[i].session < found[j].provider+found[j].session })
	return found, nil
}

// stop pauses delivery without deleting attachment identities or revoking the
// person's child connections. start/update restore only existing approvals.
func stopAgentBridge(ctx context.Context) error {
	root, err := agentworksclient.DefaultLocalBridgeRoot()
	if err != nil {
		return err
	}
	if _, err = agentworksclient.CallLocalBridge(ctx, root, agentworksclient.LocalBridgeRequest{Action: "ping"}); err != nil {
		return nil
	}
	_, _ = agentworksclient.CallLocalBridge(ctx, root, agentworksclient.LocalBridgeRequest{Action: "shutdown"})
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
	if _, err = agentworksclient.CallLocalBridge(ctx, root, agentworksclient.LocalBridgeRequest{Action: "ping"}); err == nil {
		return nil
	}
	if executable == "" {
		executable, err = os.Executable()
		if err != nil {
			return err
		}
	}
	c := exec.Command(executable, "bridge", "serve")
	detach(c)
	if err = c.Start(); err != nil {
		return err
	}
	go func() { _ = c.Wait() }()
	wait, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	for {
		if _, err = agentworksclient.CallLocalBridge(wait, root, agentworksclient.LocalBridgeRequest{Action: "ping"}); err == nil {
			fmt.Fprintln(o.stderr, "AgentWorks messaging reconnected for existing live sessions.")
			return nil
		}
		select {
		case <-wait.Done():
			return errors.New("CLI ready, but delivery did not reconnect; run agentworks attach in your chat")
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// A PTY alone is not person approval. Reject model-launched terminals even if
// the model allocates a PTY; approval comes from the person's in-chat hook.
func personAgentTerminal() error {
	if !stdinIsTerminal() {
		return errors.New("attach/detach is a person command; type /agentworks attach or /agentworks detach yourself in the chat")
	}
	pid := os.Getppid()
	for i := 0; i < 32 && pid > 1; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		out, err := exec.CommandContext(ctx, "ps", "-p", strconv.Itoa(pid), "-o", "ppid=", "-o", "comm=", "-o", "args=").Output()
		cancel()
		if err != nil {
			return errors.New("cannot verify the terminal process; type the person command in the chat instead")
		}
		fields := strings.Fields(string(out))
		if len(fields) < 2 {
			return errors.New("cannot verify the terminal process")
		}
		for _, f := range fields[1:] {
			if isCodingAgentProcess(f) {
				return errors.New("coding-agent context cannot attach or detach chats through this terminal, including a PTY. Use another terminal or type /agentworks attach or /agentworks detach yourself in the chat")
			}
		}
		pid, err = strconv.Atoi(fields[0])
		if err != nil {
			return err
		}
	}
	if pid > 1 {
		return errors.New("cannot verify the terminal ancestry; type the person command in the chat instead")
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
