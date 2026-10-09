package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	Workspace string    `json:"workspace,omitempty"`
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

// shareWebURL opens the website on the Code workspace that uses this folder; it finds the workspace by its name.
func shareWebURL(server, device, alias, workspace string) string {
	q := url.Values{"local_device": {device}, "local_folder": {alias}, "local_workspace": {workspace}}
	return strings.TrimRight(server, "/") + "/?" + q.Encode()
}

func openWebsite(link string) { openLoginBrowser(link) }

type startFlags struct {
	device                 string
	blocked                []string
	downloads, debug       bool
	foreground, background bool
	askAgain               bool
	workspace              string
	noOpen                 bool
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
	cmd.Flags().StringVar(&f.workspace, "workspace", "", "The Code workspace (its name in Settings) that uses this folder; asked the first time, then remembered")
	cmd.Flags().BoolVar(&f.askAgain, "ask", false, "Ask the background and open-website questions again instead of using the saved answers")
	cmd.Flags().BoolVar(&f.noOpen, "no-open", false, "Do not open the website (for a machine with no browser)")
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
	pruneStaleLogs(dir, runningShares(dir))

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
	// Which Code workspace uses this folder is required, never guessed: given with --workspace, remembered per folder, or asked.
	linksPath := filepath.Join(dir, "workspaces.json")
	links := loadShareLinks(linksPath)
	workspace := strings.TrimSpace(f.workspace)
	if workspace == "" {
		workspace = links[folder]
	}
	if workspace == "" {
		if !interactive {
			return errors.New("agentworks start needs the Code workspace that uses this folder: add --workspace \"<name shown in Settings>\"")
		}
		fmt.Fprintf(o.stderr, "Which Code workspace should use %s? Type its name as shown in Settings (Workspace name): ", folder)
		line, _ := in.ReadString('\n')
		if workspace = strings.TrimSpace(line); workspace == "" {
			return errors.New("a workspace name is required")
		}
	}
	links[folder] = workspace
	saveShareLinks(linksPath, links)
	// The first run asks; the answers are remembered, so later runs just connect (--ask asks again).
	prefsPath := filepath.Join(dir, "start-preferences.json")
	prefs := loadStartPrefs(prefsPath)
	if f.askAgain {
		prefs = startPrefs{}
	}
	usedSaved := false
	foreground := f.foreground || f.debug
	switch {
	case foreground:
	case f.background:
	case prefs.Background != nil:
		foreground, usedSaved = !*prefs.Background, true
	case interactive:
		fmt.Fprintf(o.stderr, "Sharing %s as %s/%s with read and write access and shell commands.\n", folder, device, alias)
		bg := ask(in, os.Stderr, "Run in the background? (No keeps this terminal open and shows live activity; Ctrl-C stops.)", true)
		foreground, prefs.Background = !bg, &bg
	}
	// The website link is what applies this folder to the named workspace, so it is always opened (--no-open for a headless machine).
	openWeb := !f.noOpen
	if interactive {
		saveStartPrefs(prefsPath, prefs)
	}
	if usedSaved && interactive {
		fmt.Fprintln(o.stderr, "Using your saved choices. `agentworks start --ask` asks again.")
	}

	if f.debug {
		printDebug(ctx, o, cfg, cfgPath, folder, device, alias, statePath)
	}
	if foreground {
		return runShareForeground(ctx, o, f, folder, device, alias, workspace, cfg.Server, statePath, openWeb)
	}
	return runShareBackground(ctx, o, f, folder, device, alias, workspace, cfg.Server, dir, key, statePath, openWeb, login)
}

func shareParams(f startFlags, device, alias, folder string) executorParams {
	return executorParams{deviceID: device, writeFolders: []string{alias + "=" + folder}, blocked: f.blocked, downloads: f.downloads}
}

func runShareForeground(ctx context.Context, o *options, f startFlags, folder, device, alias, workspace, server, statePath string, openWeb bool) error {
	// A terminal that stays open also writes the same (size-limited) log a background share does, so `agentworks debug` has something to send.
	logPath := strings.TrimSuffix(statePath, ".json") + ".log"
	if rot, err := openRotatingLog(logPath, shareLogMaxBytes, shareLogKeep); err == nil {
		defer rot.Close()
		teed := *o
		teed.stderr = io.MultiWriter(o.stderr, rot)
		o = &teed
	}
	st := shareState{PID: os.Getpid(), Device: device, Alias: alias, Workspace: workspace, Folder: folder, Server: server, Started: time.Now(), Log: logPath}
	writeShareState(statePath, st)
	defer os.Remove(statePath)
	p := shareParams(f, device, alias, folder)
	var opened bool
	p.connected = func() {
		st.Connected = true
		writeShareState(statePath, st)
		if !opened {
			printShared(o, folder, device, alias, workspace, server, os.Getpid(), false)
		}
		if openWeb && !opened {
			opened = true
			openWebsite(shareWebURL(server, device, alias, workspace))
		}
	}
	p.trace = requestTrace(o) // a terminal that stays open shows what the server asks for, live
	return runExecutor(ctx, o, p)
}

func runShareBackground(ctx context.Context, o *options, f startFlags, folder, device, alias, workspace, server, dir, key, statePath string, openWeb bool, login func() error) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	logPath := filepath.Join(dir, key+".log")
	crashPath := filepath.Join(dir, key+".crash") // only what the child prints outside its log (a crash before the log opens)
	for attempt := 0; ; attempt++ {
		logFile, err := os.OpenFile(crashPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			return err
		}
		_ = os.Remove(statePath)
		args := []string{"share-serve", "--config", o.configPath, "--server", server, "--device", device, "--alias", alias, "--workspace", workspace, "--folder", folder, "--state", statePath, "--log", logPath}
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
					printShared(o, folder, device, alias, workspace, server, st.PID, true)
					if openWeb {
						openWebsite(shareWebURL(server, device, alias, workspace))
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
	var alias, workspace, folder, statePath, logPath string
	cmd := &cobra.Command{Use: "share-serve", Hidden: true, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		o.stderr = os.Stderr
		if logPath != "" {
			if rot, err := openRotatingLog(logPath, shareLogMaxBytes, shareLogKeep); err == nil {
				defer rot.Close()
				o.stderr = rot
			}
		}
		server := o.serverURL
		st := shareState{PID: os.Getpid(), Device: f.device, Alias: alias, Workspace: workspace, Folder: folder, Server: server, Started: time.Now(), Log: logPath}
		writeShareState(statePath, st)
		p := shareParams(f, f.device, alias, folder)
		p.connected = func() { st.Connected = true; writeShareState(statePath, st) }
		p.trace = requestTrace(o) // into the log: `agentworks watch` follows it
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
	cmd.Flags().StringVar(&workspace, "workspace", "", "")
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
		fmt.Fprintf(o.stderr, "  %s  %s/%s  workspace %q  %s  (process %d, since %s)\n", st.Folder, st.Device, st.Alias, st.Workspace, state, st.PID, st.Started.Format("15:04"))
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

func requestTrace(o *options) func(request localfiles.Request, response localfiles.Response, took time.Duration) {
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

// printShared is the one short summary printed once the folder is connected.
func printShared(o *options, folder, device, alias, workspace, server string, pid int, background bool) {
	w := o.stderr
	fmt.Fprintf(w, "\n✔ Sharing %s\n", folder)
	fmt.Fprintf(w, "  Computer: %s / %s · read and write · shell commands on\n", device, alias)
	fmt.Fprintf(w, "  Workspace: %s\n", workspace)
	fmt.Fprintf(w, "  Website:  %s\n", shareWebURL(server, device, alias, workspace))
	if background {
		fmt.Fprintf(w, "  Running in the background (process %d). Watch: agentworks watch · Status: agentworks status · Stop: agentworks stop\n\n", pid)
	} else {
		fmt.Fprintln(w, "  Live activity appears below. Ctrl-C stops sharing.")
		fmt.Fprintln(w)
	}
}

// startPrefs is the answer to the first-run question, so later runs do not ask.
type startPrefs struct {
	Background *bool `json:"background,omitempty"`
}

func loadStartPrefs(path string) startPrefs {
	var p startPrefs
	if raw, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(raw, &p)
	}
	return p
}

func saveStartPrefs(path string, p startPrefs) {
	if raw, err := json.Marshal(p); err == nil {
		_ = os.WriteFile(path, raw, 0o600)
	}
}

// watchCommand follows a background share's log: its connection state and every request the server makes.
func watchCommand(o *options) *cobra.Command {
	return &cobra.Command{Use: "watch", Args: cobra.NoArgs, Short: "Show live activity of the folder shared in the background (Ctrl-C leaves it running)", RunE: func(cmd *cobra.Command, _ []string) error {
		if err := o.useShareConfig(); err != nil {
			return err
		}
		dir, err := o.shareDir()
		if err != nil {
			return err
		}
		shares := runningShares(dir)
		folder, _ := os.Getwd()
		if resolved, err := filepath.EvalSymlinks(folder); err == nil {
			folder = resolved
		}
		st, ok := shares[shareKey(folder)]
		if !ok && len(shares) == 1 {
			for _, only := range shares {
				st, ok = only, true
			}
		}
		if !ok {
			fmt.Fprintln(o.stderr, "No folder is being shared in the background here. Run `agentworks start` first.")
			return errors.New("nothing to watch")
		}
		logPath := st.Log
		if logPath == "" {
			logPath = filepath.Join(dir, shareKey(st.Folder)+".log")
		}
		fmt.Fprintf(o.stderr, "Watching %s (%s/%s). Ctrl-C stops watching; sharing continues.\n", st.Folder, st.Device, st.Alias)
		file, err := os.Open(logPath)
		if err != nil {
			return err
		}
		defer func() { file.Close() }()
		reader := bufio.NewReader(file)
		if info, err := file.Stat(); err == nil && info.Size() > 4096 {
			_, _ = file.Seek(info.Size()-4096, 0)
			reader.Reset(file)
			_, _ = reader.ReadString('\n') // the first line after a mid-line seek may be partial
		}
		pending := ""
		for {
			line, err := reader.ReadString('\n')
			if err == nil {
				fmt.Fprint(o.stderr, pending+line)
				pending = ""
				continue
			}
			pending += line // a line still being written
			// The log was rotated (a new file at the same path): follow the new one.
			if opened, err1 := file.Stat(); err1 == nil {
				if current, err2 := os.Stat(logPath); err2 == nil && !os.SameFile(opened, current) {
					if next, err3 := os.Open(logPath); err3 == nil {
						file.Close()
						file = next
						reader = bufio.NewReader(file)
						pending = ""
						continue
					}
				}
			}
			select {
			case <-cmd.Context().Done():
				return nil
			case <-time.After(300 * time.Millisecond):
			}
			if !processAlive(st.PID) {
				fmt.Fprintln(o.stderr, "Sharing stopped.")
				return nil
			}
		}
	}}
}

// Which Code workspace uses which folder on this computer, remembered so only the first start asks.
func loadShareLinks(path string) map[string]string {
	links := map[string]string{}
	if raw, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(raw, &links)
	}
	return links
}

func saveShareLinks(path string, links map[string]string) {
	if raw, err := json.Marshal(links); err == nil {
		_ = os.WriteFile(path, raw, 0o600)
	}
}
