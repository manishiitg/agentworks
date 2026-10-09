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

	_ = o.useShareConfig()
	section("sign-in and server")
	cfg, cfgPath, err := o.connection(false)
	line("config:    %s", cfgPath)
	if err != nil {
		line("server:    not set (%v)", err)
	} else {
		line("server:    %s", cfg.Server)
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
		line("sign-in:   %s", signed)
		if latest, ok := latestCLIRelease(ctx, cfg.Server); ok {
			state := "up to date"
			if cliVersion == "dev" {
				state = "a development build"
			} else if !cliIsCurrent(cliVersion, cliBuild, latest.Version, latest.CLIBuild) {
				state = "OUT OF DATE: run `agentworks update`"
			}
			line("cli build:  %s, the server offers %s (%s)", shortVersion(cliBuild), shortVersion(latest.CLIBuild), state)
		}
		debugNetwork(ctx, &b, cfg.Server)
	}

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
