package main

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
	"github.com/spf13/cobra"
)

// `agentworks debug` writes everything support needs to look at a connection problem into one text file and shows it in the
// file manager, so a person can drop it into Slack. Tokens and passwords are removed; folder paths, workspace names and the
// recent file and command activity are kept (they are what explains a failure), so the person should glance at it first.

func debugCommand(o *options) *cobra.Command {
	var toTerminal, noReveal bool
	cmd := &cobra.Command{
		Use:   "debug",
		Args:  cobra.NoArgs,
		Short: "Collect diagnostics into one file you can send to support (no tokens or passwords)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			report := redactDebug(buildDebugReport(cmd.Context(), o))
			if toTerminal {
				fmt.Fprint(o.stdout, report)
				return nil
			}
			path, err := writeDebugReport(report)
			if err != nil {
				return err
			}
			fmt.Fprintf(o.stderr, "Wrote %s\n", path)
			fmt.Fprintln(o.stderr, "Send this file in Slack. It contains no tokens or passwords, but it does show your folder paths, workspace names and recent file and command activity: have a look first.")
			if !noReveal {
				revealFile(path)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&toTerminal, "print", false, "Print the report instead of writing a file")
	cmd.Flags().BoolVar(&noReveal, "no-open", false, "Do not show the file in the file manager")
	return cmd
}

func writeDebugReport(report string) (string, error) {
	dir := os.TempDir()
	if home, err := os.UserHomeDir(); err == nil {
		if info, err := os.Stat(filepath.Join(home, "Downloads")); err == nil && info.IsDir() {
			dir = filepath.Join(home, "Downloads")
		}
	}
	path := filepath.Join(dir, "agentworks-debug-"+time.Now().Format("20060102-150405")+".txt")
	return path, os.WriteFile(path, []byte(report), 0o600)
}

// revealFile shows the file in the system file manager.
func revealFile(path string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", "-R", path)
	case "windows":
		cmd = exec.Command("explorer", "/select,"+path)
	default:
		cmd = exec.Command("xdg-open", filepath.Dir(path))
	}
	if err := cmd.Start(); err != nil {
		return
	}
	go func() { _ = cmd.Wait() }()
}

func buildDebugReport(ctx context.Context, o *options) string {
	var b strings.Builder
	section := func(title string) { fmt.Fprintf(&b, "\n== %s ==\n", title) }
	line := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }

	fmt.Fprintf(&b, "AgentWorks CLI debug report\n")
	line("time:      %s (%s)", time.Now().Format(time.RFC3339), time.Now().UTC().Format("15:04 UTC"))
	line("cli:       %s", cliVersion)
	line("system:    %s/%s, %s", runtime.GOOS, runtime.GOARCH, runtime.Version())

	// Two logins exist: the one the agents' messaging and tools use (config.json, or the one named with --config),
	// and the one folder sharing uses (executor.json). Report both, the way each is really used.
	section("sign-in and server")
	servers := map[string]bool{}
	debugLogin := func(title, path string) {
		if server := debugLoginSection(&b, title, path); server != "" {
			servers[server] = true
		}
	}
	if path, err := o.path(); err == nil {
		debugLogin("agent login (messaging and tools):", path)
	} else {
		line("agent login: %v", err)
	}
	_ = o.useShareConfig()
	sharePath, shareErr := o.path()
	if shareErr == nil {
		debugLogin("sharing login (agentworks start):", sharePath)
	}
	names := make([]string, 0, len(servers))
	for server := range servers {
		names = append(names, server)
	}
	sort.Strings(names)
	for _, server := range names {
		if latest, ok := latestCLIRelease(ctx, server); ok {
			state := "up to date"
			if cliVersion == "dev" {
				state = "a development build"
			} else if !cliIsCurrent(cliVersion, cliBuild, latest.Version, latest.CLIBuild) {
				state = "OUT OF DATE: run `agentworks update`"
			}
			line("cli build: %s, the server offers %s (%s)", shortVersion(cliBuild), shortVersion(latest.CLIBuild), state)
		}
		debugNetwork(ctx, &b, server)
	}

	debugBridge(ctx, &b)

	section("proxy settings")
	for _, name := range []string{"HTTPS_PROXY", "HTTP_PROXY", "ALL_PROXY", "NO_PROXY"} {
		value := os.Getenv(name)
		if value == "" {
			value = os.Getenv(strings.ToLower(name))
		}
		if value == "" {
			line("%s: not set", name)
		} else {
			line("%s: %s", name, value)
		}
	}

	section("command sandbox")
	if err := configureLocalShellSandbox(); err != nil {
		line("NOT available: %v", err)
	} else {
		line("available (or not needed on this system)")
	}

	section("shared folders")
	dir, err := o.shareDir()
	if err != nil {
		line("shares folder: %v", err)
	} else {
		line("folder:    %s", dir)
		shares := runningShares(dir)
		if len(shares) == 0 {
			line("running:   nothing is being shared")
		}
		keys := make([]string, 0, len(shares))
		for k := range shares {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			st := shares[k]
			line("running:   %s as %s/%s, workspace %q, connected=%v, process %d, since %s", st.Folder, st.Device, st.Alias, st.Workspace, st.Connected, st.PID, st.Started.Format(time.RFC3339))
			if st.Error != "" {
				line("           last error: %s", st.Error)
			}
		}
		if raw, err := os.ReadFile(filepath.Join(dir, "workspaces.json")); err == nil {
			line("remembered workspaces: %s", strings.TrimSpace(string(raw)))
		}
		if raw, err := os.ReadFile(filepath.Join(dir, "start-preferences.json")); err == nil {
			line("saved answers: %s", strings.TrimSpace(string(raw)))
		}
		logs, _ := filepath.Glob(filepath.Join(dir, "*.log"))
		sort.Strings(logs)
		for _, path := range logs {
			section("recent activity: " + filepath.Base(path))
			for _, l := range tailLines(path, 200) {
				b.WriteString(l + "\n")
			}
		}
		crashes, _ := filepath.Glob(filepath.Join(dir, "*.crash"))
		for _, path := range crashes {
			if info, err := os.Stat(path); err == nil && info.Size() > 0 {
				section("output outside the log: " + filepath.Base(path))
				for _, l := range tailLines(path, 100) {
					b.WriteString(l + "\n")
				}
			}
		}
		if len(logs) == 0 {
			section("recent activity")
			line("no log yet: start sharing with `agentworks start`, reproduce the problem, then run `agentworks debug` again")
		}
	}
	return b.String()
}

// debugNetwork checks the three things that break a connection: name lookup, plain HTTPS, and the WebSocket upgrade a
// proxy or firewall may block.
func debugNetwork(ctx context.Context, b *strings.Builder, server string) {
	u, err := url.Parse(server)
	if err != nil || u.Host == "" {
		fmt.Fprintf(b, "network:   server address is not valid: %q\n", server)
		return
	}
	host := u.Hostname()
	began := time.Now()
	addrs, err := net.DefaultResolver.LookupHost(ctx, host)
	if err != nil {
		fmt.Fprintf(b, "dns:       %s FAILED: %v\n", host, err)
	} else {
		fmt.Fprintf(b, "dns:       %s -> %s (%s)\n", host, strings.Join(addrs, ", "), time.Since(began).Round(time.Millisecond))
	}
	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(reqCtx, http.MethodGet, strings.TrimRight(server, "/")+"/api/health", nil)
	began = time.Now()
	if resp, err := http.DefaultClient.Do(req); err != nil {
		fmt.Fprintf(b, "https:     FAILED: %v\n", err)
	} else {
		resp.Body.Close()
		fmt.Fprintf(b, "https:     /api/health -> HTTP %d in %s\n", resp.StatusCode, time.Since(began).Round(time.Millisecond))
	}
	// A connection attempt without credentials must be answered (401/403): that proves the WebSocket path is open end to end.
	address := strings.Replace(strings.Replace(strings.TrimRight(server, "/"), "https://", "wss://", 1), "http://", "ws://", 1) + "/api/external/v1/devices/connect"
	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	began = time.Now()
	conn, resp, err := dialer.DialContext(ctx, address, nil)
	switch {
	case conn != nil:
		conn.Close()
		fmt.Fprintf(b, "websocket: connected without credentials (unexpected) in %s\n", time.Since(began).Round(time.Millisecond))
	case resp != nil:
		resp.Body.Close()
		fmt.Fprintf(b, "websocket: server answered HTTP %d in %s (a 401 or 403 here is normal: the path is open)\n", resp.StatusCode, time.Since(began).Round(time.Millisecond))
	default:
		fmt.Fprintf(b, "websocket: FAILED (a proxy or firewall may block WebSockets): %v\n", err)
	}
}

func tailLines(path string, n int) []string {
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()
	if info, err := file.Stat(); err == nil && info.Size() > 64<<10 {
		_, _ = file.Seek(info.Size()-64<<10, 0)
	}
	var lines []string
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
		if len(lines) > n {
			lines = lines[1:]
		}
	}
	return lines
}

var debugSecretPatterns = []struct {
	re   *regexp.Regexp
	with string
}{
	{regexp.MustCompile(`aw_pat_[A-Za-z0-9_-]{8,}`), "[redacted token]"},
	{regexp.MustCompile(`cli_verify_[A-Za-z0-9]+`), "[redacted code]"},
	{regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._~+/=-]{8,}`), "Bearer [redacted]"},
	{regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{16,}`), "[redacted key]"},
	{regexp.MustCompile(`(?i)\b(authorization|token|refresh_token|secret|password|passwd|api[_-]?key)(["']?\s*[:=]\s*["']?)[^\s"',;]{6,}`), "${1}${2}[redacted]"},
	{regexp.MustCompile(`(?i)(https?://)[^/\s:@]+:[^/\s@]+@`), "${1}[redacted]@"},
	// A lease id is a fence for one chat's messages, and a session id names a chat: keep only enough to tell them apart.
	{regexp.MustCompile(`attach-[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}`), "attach-[id]"},
	{regexp.MustCompile(`\b([0-9a-f]{8})-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`), "${1}…"},
}

// redactDebug removes tokens and passwords, and shortens the home folder, before a report leaves the machine.
func redactDebug(report string) string {
	for _, p := range debugSecretPatterns {
		report = p.re.ReplaceAllString(report, p.with)
	}
	if home, err := os.UserHomeDir(); err == nil && len(home) > 3 {
		report = strings.ReplaceAll(report, home, "~")
	}
	return report
}

// debugBridge reports the local messaging helper (AgentBridge): whether it runs, which chats it holds and how each is
// reached, the plugins found on this computer, and the helper's own log. Names and states only, no message text.
func debugBridge(ctx context.Context, b *strings.Builder) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	line := func(format string, args ...any) { fmt.Fprintf(b, format+"\n", args...) }
	fmt.Fprintf(b, "\n== local messaging helper ==\n")
	root, err := agentworksclient.DefaultLocalBridgeRoot()
	if err != nil {
		line("folder:    %v", err)
		return
	}
	line("folder:    %s", root)
	reply, err := agentworksclient.CallLocalBridge(ctx, root, agentworksclient.LocalBridgeRequest{Action: "ping"})
	if err != nil {
		line("helper:    not running (%v)", err)
	} else {
		state := "same build as this CLI"
		if reply.Version != "" && reply.Version != cliVersion {
			state = "ANOTHER build than this CLI (" + shortVersion(cliVersion) + "): run `agentworks update`"
		}
		line("helper:    running, build %s (%s)", shortVersion(reply.Version), state)
		if local, e := agentworksclient.CallLocalBridge(ctx, root, agentworksclient.LocalBridgeRequest{Action: "list"}); e == nil {
			line("chats:     %d held", len(local.AgentStates))
			for _, st := range local.AgentStates {
				online := "offline"
				if st.Online {
					online = "online"
				}
				text := fmt.Sprintf("  %s %s: %s, route %s, chat process %s, %d waiting", st.Provider, st.AgentName, online, st.Route, map[bool]string{true: "running", false: "not found"}[st.HostAlive], st.Pending)
				if st.LastError != "" {
					text += ", last error: " + st.LastError
				}
				line("%s", text)
			}
		}
	}
	if home, e := os.UserHomeDir(); e == nil {
		onPath := func(name string) bool { _, err := exec.LookPath(name); return err == nil }
		read := func(path string) string { raw, _ := os.ReadFile(path); return string(raw) }
		var found []string
		for _, u := range installedPluginUpdates(home, onPath, read) {
			found = append(found, u.Provider)
		}
		if len(found) == 0 {
			line("plugins:   none found for Claude, Codex, Muse, Cursor or Pi on this computer")
		} else {
			line("plugins:   found for %s (hook approval is shown inside each app: Settings > Hooks, or the app's plugin list)", strings.Join(found, ", "))
		}
	}
	debugHelperStartup(b, root)
	if lines := tailLines(filepath.Join(root, "bridge.log"), 400); len(lines) > 0 {
		fmt.Fprintf(b, "\n== local messaging helper log (recent, idle waits left out) ==\n")
		kept := lines[:0]
		for _, l := range lines {
			if !strings.Contains(l, `action "next"`) {
				kept = append(kept, l)
			}
		}
		if len(kept) > 60 {
			kept = kept[len(kept)-60:]
		}
		for _, l := range kept {
			fmt.Fprintln(b, l)
		}
	}
}

func debugHelperStartup(b *strings.Builder, root string) {
	fmt.Fprintln(b, "\n== helper startup (recent) ==")
	lines, err := helperStartupLines(filepath.Join(root, "startup.log"), 0, 20)
	if err != nil {
		fmt.Fprintln(b, shortStartupDetail("Startup log unavailable: "+err.Error()))
		return
	}
	if len(lines) == 0 {
		fmt.Fprintln(b, "No startup output recorded.")
	}
	for _, line := range lines {
		fmt.Fprintln(b, redactDebug(line))
	}
}

// debugLoginSection writes one saved login's state (never the credentials) and returns its server.
func debugLoginSection(b *strings.Builder, title, path string) string {
	fmt.Fprintf(b, "%s\n  config:  %s\n", title, path)
	if _, err := os.Stat(path); err != nil {
		fmt.Fprintf(b, "  state:   not set up (no saved login there)\n")
		return ""
	}
	cfg, err := agentworksclient.LoadConfig(path)
	if err != nil {
		fmt.Fprintf(b, "  state:   cannot be read (%v)\n", err)
		return ""
	}
	fmt.Fprintf(b, "  server:  %s\n", cfg.Server)
	signed := "NOT signed in"
	if cfg.Token != "" {
		signed = "signed in"
		if !cfg.ExpiresAt.IsZero() {
			if time.Now().After(cfg.ExpiresAt) {
				signed += fmt.Sprintf(", access token expired %s ago", time.Since(cfg.ExpiresAt).Round(time.Second))
			} else {
				signed += ", access token valid until " + cfg.ExpiresAt.Format(time.RFC3339)
			}
			if cfg.RefreshToken != "" {
				signed += " (renewed automatically)"
			}
		}
	}
	fmt.Fprintf(b, "  sign-in: %s\n", signed)
	return cfg.Server
}
