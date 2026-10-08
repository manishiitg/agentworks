package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
	"github.com/manishiitg/coding-agent-loop/workspace/localfiles"
	"github.com/spf13/cobra"
)

// `agentworks start` shares the folder you are standing in with your AgentWorks server (read and write, with shell
// commands) and keeps the connection open, in the background or in this terminal. `stop` ends it, `status` lists
// what is shared. The connection logic itself is runExecutor, shared with `executor connect`.

const shareAuthFailed = "sign-in"

// shareState is what a running share leaves on disk so stop, status and a second start can find it.
type shareState struct {
	PID       int       `json:"pid"`
	Device    string    `json:"device"`
	Alias     string    `json:"alias"`
	Folder    string    `json:"folder"`
	Server    string    `json:"server"`
	Started   time.Time `json:"started"`
	Connected bool      `json:"connected"`
	Error     string    `json:"error,omitempty"`
	Log       string    `json:"log,omitempty"`
}

// useShareConfig keeps the local connection's sign-in apart from remote MCP credentials, as the setup docs describe.
func (o *options) useShareConfig() error {
	if o.configPath != "" {
		return nil
	}
	def, err := agentworksclient.DefaultConfigPath()
	if err != nil {
		return err
	}
	o.configPath = filepath.Join(filepath.Dir(def), "executor.json")
	return nil
}

func (o *options) shareDir() (string, error) {
	config, err := o.path()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(filepath.Dir(config), "shares")
	return dir, os.MkdirAll(dir, 0o700)
}

func shareKey(folder string) string {
	sum := sha256.Sum256([]byte(folder))
	return hex.EncodeToString(sum[:])[:10]
}

// sanitizeID turns a host or folder name into a valid device/folder identifier.
func sanitizeID(name, fallback string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	id := strings.Trim(b.String(), "-_")
	if len(id) > 63 {
		id = strings.Trim(id[:63], "-_")
	}
	if id == "" || !localfiles.ValidID(id) {
		return fallback
	}
	return id
}

func defaultDeviceName() string {
	host, _ := os.Hostname()
	host, _, _ = strings.Cut(host, ".")
	return sanitizeID(host, "my-computer")
}

func writeShareState(path string, st shareState) {
	raw, err := json.Marshal(st)
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if os.WriteFile(tmp, raw, 0o600) == nil {
		_ = os.Rename(tmp, path)
	}
}

func readShareState(path string) (shareState, bool) {
	var st shareState
	raw, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(raw, &st) != nil {
		return st, false
	}
	return st, true
}

// runningShares lists the shares whose process is alive; stale files are removed.
func runningShares(dir string) map[string]shareState {
	found := map[string]shareState{}
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	for _, file := range files {
		st, ok := readShareState(file)
		if !ok || !processAlive(st.PID) {
			_ = os.Remove(file)
			continue
		}
		found[strings.TrimSuffix(filepath.Base(file), ".json")] = st
	}
	return found
}

func ask(in *bufio.Reader, out *os.File, question string, def bool) bool {
	hint := "[Y/n]"
	if !def {
		hint = "[y/N]"
	}
	fmt.Fprintf(out, "%s %s ", question, hint)
	line, _ := in.ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	case "n", "no":
		return false
	}
	return def
}

func shareWebURL(server, device, alias string) string {
	q := url.Values{"local_device": {device}, "local_folder": {alias}}
	return strings.TrimRight(server, "/") + "/?" + q.Encode()
}

func openWebsite(link string) { openLoginBrowser(link) }

type startFlags struct {
	device                 string
	blocked                []string
	downloads, debug       bool
	foreground, background bool
	open, noOpen           bool
}

func startCommand(o *options) *cobra.Command {
	var f startFlags
	cmd := &cobra.Command{
		Use:   "start",
		Args:  cobra.NoArgs,
		Short: "Share the current folder with your AgentWorks server (read and write, with shell commands)",
		Long: "Run this inside a project folder. It signs you in if needed, shares this folder with your AgentWorks server (the agent can edit " +
			"files and run commands here, inside a sandbox limited to this folder), and keeps the connection open. It asks whether to run in " +
			"the background (stop it with `agentworks stop`) or keep this terminal open (Ctrl-C stops), and whether to open the website. " +
			"The first time, add --server https://your-agentworks.example.",
		RunE: func(cmd *cobra.Command, _ []string) error { return runStart(cmd.Context(), o, f) },
	}
	cmd.Flags().StringVar(&f.device, "device", "", "Name for this computer (default: its host name)")
	cmd.Flags().StringArrayVar(&f.blocked, "block", nil, "Relative path the agent must never read or change, e.g. .env (repeatable)")
	cmd.Flags().BoolVar(&f.downloads, "downloads", false, "Also allow reading and writing ~/Downloads (explicit opt-in)")
	cmd.Flags().BoolVar(&f.foreground, "foreground", false, "Keep this terminal open instead of running in the background (Ctrl-C stops)")
	cmd.Flags().BoolVar(&f.background, "background", false, "Run in the background without asking")
	cmd.Flags().BoolVar(&f.open, "open", false, "Open the website when connected, without asking")
	cmd.Flags().BoolVar(&f.noOpen, "no-open", false, "Do not open the website")
	cmd.Flags().BoolVar(&f.debug, "debug", false, "Stay in this terminal, print diagnostics and log every file and command request the server sends")
	return cmd
}

func runStart(ctx context.Context, o *options, f startFlags) error {
	if err := o.useShareConfig(); err != nil {
		return err
	}
	folder, err := os.Getwd()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(folder); err == nil {
		folder = resolved
	}
	if home, _ := os.UserHomeDir(); folder == "/" || folder == home {
		return errors.New("run `agentworks start` inside a project folder, not your home folder or /")
	}
	device := f.device
	if device == "" {
		device = defaultDeviceName()
	}
	if !localfiles.ValidID(device) {
		return errors.New("--device needs letters, numbers, dashes or underscores")
	}
	alias := sanitizeID(filepath.Base(folder), "project")
	dir, err := o.shareDir()
	if err != nil {
		return err
	}
	key := shareKey(folder)
	statePath := filepath.Join(dir, key+".json")

	cfg, cfgPath, err := o.connection(false)
	if err != nil {
		return errors.New("first time: run `agentworks start --server https://your-agentworks.example` (the website's Code settings show the exact command)")
	}
	login := func() error {
		fmt.Fprintln(o.stderr, "Signing in (this approval is only for sharing local folders)...")
		return o.browserLogin(ctx, cfg, cfgPath, []string{"devices:connect"}, false)
	}
	if cfg.Token == "" {
		if err := login(); err != nil {
			return err
		}
	}

	if st, ok := runningShares(dir)[key]; ok {
		fmt.Fprintf(o.stderr, "%s is already shared as %s/%s (process %d). Use `agentworks stop` to end it.\n", folder, st.Device, st.Alias, st.PID)
		return nil
	}

	in := bufio.NewReader(o.stdin)
	interactive := stdinIsTerminal() && !o.jsonOutput
	foreground := f.foreground || f.debug
	if !foreground && !f.background && interactive {
		fmt.Fprintf(o.stderr, "Sharing %s as %s/%s with read and write access and shell commands.\n", folder, device, alias)
		foreground = !ask(in, os.Stderr, "Run in the background? (No keeps this terminal open; Ctrl-C stops.)", true)
	}
	openWeb := f.open
	if !f.open && !f.noOpen && interactive {
		openWeb = ask(in, os.Stderr, "Open your Code chat in the browser once connected?", true)
	}

	if f.debug {
		printDebug(ctx, o, cfg, cfgPath, folder, device, alias, statePath)
	}
	if foreground {
		return runShareForeground(ctx, o, f, folder, device, alias, cfg.Server, statePath, openWeb)
	}
	return runShareBackground(ctx, o, f, folder, device, alias, cfg.Server, dir, key, statePath, openWeb, login)
}

func shareParams(f startFlags, device, alias, folder string) executorParams {
	return executorParams{deviceID: device, writeFolders: []string{alias + "=" + folder}, blocked: f.blocked, downloads: f.downloads}
}

func runShareForeground(ctx context.Context, o *options, f startFlags, folder, device, alias, server, statePath string, openWeb bool) error {
	st := shareState{PID: os.Getpid(), Device: device, Alias: alias, Folder: folder, Server: server, Started: time.Now()}
	writeShareState(statePath, st)
	defer os.Remove(statePath)
	p := shareParams(f, device, alias, folder)
	var opened bool
	p.connected = func() {
		st.Connected = true
		writeShareState(statePath, st)
		fmt.Fprintf(o.stderr, "Connected. In the website, open a Code chat and switch it to Local: %s\n", shareWebURL(server, device, alias))
		if openWeb && !opened {
			opened = true
			openWebsite(shareWebURL(server, device, alias))
		}
	}
	if f.debug {
		p.trace = debugTrace(o)
	}
	return runExecutor(ctx, o, p)
}

func runShareBackground(ctx context.Context, o *options, f startFlags, folder, device, alias, server, dir, key, statePath string, openWeb bool, login func() error) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	logPath := filepath.Join(dir, key+".log")
	for attempt := 0; ; attempt++ {
		logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			return err
		}
		_ = os.Remove(statePath)
		args := []string{"share-serve", "--config", o.configPath, "--server", server, "--device", device, "--alias", alias, "--folder", folder, "--state", statePath, "--log", logPath}
		for _, b := range f.blocked {
			args = append(args, "--block", b)
		}
		if f.downloads {
			args = append(args, "--downloads")
		}
		child := exec.Command(exe, args...)
		child.Stdout, child.Stderr = logFile, logFile
		detach(child)
		if err := child.Start(); err != nil {
			logFile.Close()
			return err
		}
		logFile.Close()
		exited := make(chan struct{})
		go func() { _ = child.Wait(); close(exited) }()
		deadline := time.After(30 * time.Second)
		poll := time.NewTicker(200 * time.Millisecond)
		defer poll.Stop()
	wait:
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-exited:
				st, _ := readShareState(statePath)
				if st.Error == shareAuthFailed && attempt == 0 {
					fmt.Fprintln(o.stderr, "The saved sign-in is no longer valid.")
					if err := login(); err != nil {
						return err
					}
					break wait
				}
				return fmt.Errorf("sharing stopped right away: %s\nDetails: %s", firstNonEmpty(st.Error, "see the log"), logPath)
			case <-deadline:
				return fmt.Errorf("did not connect within 30 seconds; run `agentworks start --debug` to see why (log: %s)", logPath)
			case <-poll.C:
				if st, ok := readShareState(statePath); ok && st.Connected {
					fmt.Fprintf(o.stderr, "Sharing %s as %s/%s in the background (process %d). Stop it with `agentworks stop`.\n", folder, device, alias, st.PID)
					fmt.Fprintf(o.stderr, "In the website, open a Code chat and switch it to Local: %s\n", shareWebURL(server, device, alias))
					if openWeb {
						openWebsite(shareWebURL(server, device, alias))
					}
					return nil
				}
			}
		}
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// serveShareCommand is the background process `start` launches; it is not meant to be run by hand.
func serveShareCommand(o *options) *cobra.Command {
	var f startFlags
	var alias, folder, statePath, logPath string
	cmd := &cobra.Command{Use: "share-serve", Hidden: true, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		o.stderr = os.Stderr
		server := o.serverURL
		st := shareState{PID: os.Getpid(), Device: f.device, Alias: alias, Folder: folder, Server: server, Started: time.Now(), Log: logPath}
		writeShareState(statePath, st)
		p := shareParams(f, f.device, alias, folder)
		p.connected = func() { st.Connected = true; writeShareState(statePath, st) }
		err := runExecutor(cmd.Context(), o, p)
		if err != nil {
			st.Connected = false
			st.Error = err.Error()
			var apiErr *agentworksclient.APIError
			if errors.As(err, &apiErr) && (apiErr.Status == 401 || apiErr.Status == 403) {
				st.Error = shareAuthFailed
			}
			writeShareState(statePath, st)
			return err
		}
		_ = os.Remove(statePath)
		return nil
	}}
	cmd.Flags().StringVar(&f.device, "device", "", "")
	cmd.Flags().StringVar(&alias, "alias", "", "")
	cmd.Flags().StringVar(&folder, "folder", "", "")
	cmd.Flags().StringVar(&statePath, "state", "", "")
	cmd.Flags().StringVar(&logPath, "log", "", "")
	cmd.Flags().StringArrayVar(&f.blocked, "block", nil, "")
	cmd.Flags().BoolVar(&f.downloads, "downloads", false, "")
	return cmd
}

func stopCommand(o *options) *cobra.Command {
	var all bool
	cmd := &cobra.Command{Use: "stop", Args: cobra.NoArgs, Short: "Stop sharing the current folder (or every shared folder with --all)", RunE: func(cmd *cobra.Command, _ []string) error {
		if err := o.useShareConfig(); err != nil {
			return err
		}
		dir, err := o.shareDir()
		if err != nil {
			return err
		}
		shares := runningShares(dir)
		if len(shares) == 0 {
			fmt.Fprintln(o.stderr, "Nothing is being shared.")
			return nil
		}
		targets := map[string]shareState{}
		if all {
			targets = shares
		} else {
			folder, _ := os.Getwd()
			if resolved, err := filepath.EvalSymlinks(folder); err == nil {
				folder = resolved
			}
			if st, ok := shares[shareKey(folder)]; ok {
				targets[shareKey(folder)] = st
			} else if len(shares) == 1 {
				for k, st := range shares {
					targets[k] = st
				}
			} else {
				fmt.Fprintln(o.stderr, "This folder is not shared. Shared folders:")
				listShares(o, shares)
				fmt.Fprintln(o.stderr, "Run `agentworks stop` inside one of them, or `agentworks stop --all`.")
				return errors.New("no share for this folder")
			}
		}
		for key, st := range targets {
			terminateProcess(st.PID, false)
			for i := 0; i < 40 && processAlive(st.PID); i++ {
				time.Sleep(200 * time.Millisecond)
			}
			if processAlive(st.PID) {
				terminateProcess(st.PID, true)
			}
			_ = os.Remove(filepath.Join(dir, key+".json"))
			fmt.Fprintf(o.stderr, "Stopped sharing %s (%s/%s).\n", st.Folder, st.Device, st.Alias)
		}
		return nil
	}}
	cmd.Flags().BoolVar(&all, "all", false, "Stop every shared folder")
	return cmd
}

func listShares(o *options, shares map[string]shareState) {
	keys := make([]string, 0, len(shares))
	for k := range shares {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		st := shares[k]
		state := "connecting"
		if st.Connected {
			state = "connected"
		}
		fmt.Fprintf(o.stderr, "  %s  %s/%s  %s  (process %d, since %s)\n", st.Folder, st.Device, st.Alias, state, st.PID, st.Started.Format("15:04"))
	}
}

func statusCommand(o *options) *cobra.Command {
	return &cobra.Command{Use: "status", Args: cobra.NoArgs, Short: "Show which folders are being shared", RunE: func(cmd *cobra.Command, _ []string) error {
		if err := o.useShareConfig(); err != nil {
			return err
		}
		dir, err := o.shareDir()
		if err != nil {
			return err
		}
		shares := runningShares(dir)
		if len(shares) == 0 {
			fmt.Fprintln(o.stderr, "Nothing is being shared. Run `agentworks start` inside a project folder.")
			return nil
		}
		listShares(o, shares)
		return nil
	}}
}

// printDebug prints what is needed to explain a failed or odd connection.
func printDebug(ctx context.Context, o *options, cfg agentworksclient.Config, cfgPath, folder, device, alias, statePath string) {
	w := o.stderr
	fmt.Fprintln(w, "--- agentworks start --debug ---")
	fmt.Fprintf(w, "cli:        %s (%s/%s)\n", cliVersion, runtime.GOOS, runtime.GOARCH)
	fmt.Fprintf(w, "server:     %s\n", cfg.Server)
	fmt.Fprintf(w, "config:     %s\n", cfgPath)
	signed := "not signed in"
	if cfg.Token != "" {
		signed = "signed in"
		if !cfg.ExpiresAt.IsZero() {
			signed += ", access token expires " + cfg.ExpiresAt.Format(time.RFC3339)
			if cfg.RefreshToken != "" {
				signed += " (renewed automatically)"
			}
		}
	}
	fmt.Fprintf(w, "sign-in:    %s\n", signed)
	fmt.Fprintf(w, "folder:     %s (shared as %s/%s, read and write, shell commands on)\n", folder, device, alias)
	fmt.Fprintf(w, "state file: %s\n", statePath)
	reqCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(reqCtx, http.MethodGet, strings.TrimRight(cfg.Server, "/")+"/api/health", nil)
	began := time.Now()
	if resp, err := http.DefaultClient.Do(req); err != nil {
		fmt.Fprintf(w, "server:     NOT reachable: %v\n", err)
	} else {
		resp.Body.Close()
		fmt.Fprintf(w, "server:     reachable, health %d in %s\n", resp.StatusCode, time.Since(began).Round(time.Millisecond))
	}
	if err := configureLocalShellSandbox(); err != nil {
		fmt.Fprintf(w, "sandbox:    NOT available: %v\n", err)
	} else {
		fmt.Fprintln(w, "sandbox:    ok (commands are limited to the shared folder)")
	}
	fmt.Fprintln(w, "Every request from the server is logged below. Ctrl-C stops.")
	fmt.Fprintln(w, "-------------------------------")
}

func debugTrace(o *options) func(request localfiles.Request, response localfiles.Response, took time.Duration) {
	return func(request localfiles.Request, response localfiles.Response, took time.Duration) {
		what := request.Path
		if request.Operation == "shell" {
			what = "$ " + request.Command
		}
		if len(what) > 160 {
			what = what[:160] + "…"
		}
		line := fmt.Sprintf("%s  %-8s %-12s %s  -> %d in %s", time.Now().Format("15:04:05"), request.Operation, request.ResourceID, what, response.Status, took.Round(time.Millisecond))
		if response.Error != "" {
			line += "  error: " + response.Error
		}
		if response.Shell != nil {
			line += fmt.Sprintf("  exit %d", response.Shell.ExitCode)
		}
		fmt.Fprintln(o.stderr, line)
	}
}
