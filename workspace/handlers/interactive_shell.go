package handlers

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/manishiitg/coding-agent-loop/workspace/models"
	"github.com/manishiitg/coding-agent-loop/workspace/security"
	"github.com/manishiitg/coding-agent-loop/workspace/slots"
	"github.com/manishiitg/coding-agent-loop/workspace/utils"
	"github.com/spf13/viper"
)

// Interactive shells (Code's terminal). The workspace service starts a small tmux server inside the same sandbox
// /api/execute uses: Folder Guard via Landlock, the private /tmp, a private /dev/pts, the sanitized environment and the
// allow-listed extra env. Where slots are on, all of it runs as the caller's own Linux account (slots.WrapCommandFile:
// the request travels in a file so the terminal stays the command's stdin), and a person without a slot gets no
// shell. The service itself creates the namespaces, since it is the only binary the host's AppArmor userns exception
// covers; a tmux pane could not. The shell and everything it starts inherit the sandbox.
//
// The tmux socket lives in the shell's own folder: in the slot's run folder (group-shared with the service) where slots
// are on, else a shared shells folder. Nothing outside the sandbox ever attaches to it: an attached tmux client runs
// whatever the server tells it to (`detach-client -E`), so the attach client runs in the shell's own sandbox too
// (interactive_shell_attach.go).
//
// Only the agent server calls these routes (workspace token); it authorizes the user for the project first, passes
// the project's Folder Guard and stamps the user (X-User-ID).

const interactiveShellRoot = "/tmp/.agentworks-shells"

// interactiveShellPromptCommand is what bash runs before every prompt (PROMPT_COMMAND), so it holds whatever /etc/bash.bashrc or a
// profile sets. It gives the terminal a short prompt: just the current folder's name in bold, not bash's default
// user@host:/full/path (on a server a path with a user id and a project id in it, far wider than the screen). In a sandboxed terminal
// it also makes an empty `cd` (and `cd ~`) return to the folder the terminal started in: there the real "home" is a private folder
// inside the project (.sandbox-cache/home), which an empty cd used to land in with no hint where it was or how to get back.
// AGENTWORKS_START_DIR holds the starting folder. A terminal with the person's real home (unconfined, their own machine) keeps cd as is.
func interactiveShellPromptCommand(sandboxed bool) string {
	cmd := `PS1='\[\e[1m\]${PWD##*/}\[\e[0m\] \$ '`
	if sandboxed {
		cmd += `; cd() { if [ "$#" -eq 0 ] || [ "$1" = "$HOME" ]; then builtin cd -- "$AGENTWORKS_START_DIR"; else builtin cd "$@"; fi; }`
		// The private home has no ~/.bashrc, so nothing turned colours on: ls, grep and diff printed everything in the one text colour
		// (all green in the Homebrew scheme). Once per shell: GNU tools get --color=auto, BSD ls (a Mac) gets CLICOLOR.
		cmd += `; if [ -z "$AGENTWORKS_COLOURS" ]; then AGENTWORKS_COLOURS=1; if ls --color=auto -d . >/dev/null 2>&1; then alias ls='ls --color=auto' grep='grep --color=auto' egrep='egrep --color=auto' fgrep='fgrep --color=auto' diff='diff --color=auto'; else export CLICOLOR=1; fi; fi`
		// Ubuntu's "command not found" helper reads a database the sandbox cannot open and printed a Python crash report for any typo (or for
		// `nvm` before it is installed). Plain bash wording instead. An empty ~/.bashrc is created if missing, because installers such as nvm say "Profile not found" and
		// skip adding themselves without one. Then the person's own ~/.bashrc (in the private home, where `nvm` and
		// similar installers put themselves) is read once, last, so their settings win.
		cmd += `; if [ -z "$AGENTWORKS_RC" ]; then AGENTWORKS_RC=1; command_not_found_handle() { echo "bash: $1: command not found" >&2; return 127; }; [ -e "$HOME/.bashrc" ] || : > "$HOME/.bashrc" 2>/dev/null; [ -f "$HOME/.bashrc" ] && . "$HOME/.bashrc"; fi`
	}
	return cmd
}

const interactiveShellSession = "shell"

// interactiveShellHistoryLines is how far back the terminal can be scrolled (tmux's history for the pane).
const interactiveShellHistoryLines = 50000

var interactiveShellID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,47}$`)

type InteractiveShellStartRequest struct {
	ShellID          string                    `json:"shell_id"`
	WorkingDirectory string                    `json:"working_directory"`
	FolderGuard      *models.FolderGuardConfig `json:"folder_guard"`
	ExtraEnv         map[string]string         `json:"extra_env,omitempty"`
	Cols             int                       `json:"cols,omitempty"`
	Rows             int                       `json:"rows,omitempty"`
	// Unconfined asks for the shell with the person's own rights and real home, no sandbox: what the coding agents get on a
	// person's own machine (the agent server sets it from the same switch). Honoured only where interactiveShellUnconfinedAllowed.
	Unconfined bool `json:"unconfined,omitempty"`
}

type InteractiveShellHandle struct {
	ShellID string `json:"shell_id"`
	Socket  string `json:"socket"`
	Session string `json:"session"`
	Running bool   `json:"running"`
}

type interactiveShellRef struct {
	ShellID string `json:"shell_id"`
	Cols    int    `json:"cols,omitempty"`
	Rows    int    `json:"rows,omitempty"`
}

// interactiveShellPaths returns the shell's folder and tmux socket. Where slots are on, the shell runs as the
// caller's slot account and its tmux files live in that slot's own run folder (group-shared with the service, so the
// service can reach the socket and clean up); elsewhere they are in the shared shells folder.
// interactiveShellRootPath is the shared shells folder by its real path. On a Mac /tmp is a link to /private/tmp: the strict
// sandbox grants the real folder and will not follow the link, so a socket named through /tmp is refused ("Operation not permitted").
func interactiveShellRootPath() string {
	parent := filepath.Dir(interactiveShellRoot)
	if real, err := filepath.EvalSymlinks(parent); err == nil {
		parent = real
	}
	return filepath.Join(parent, filepath.Base(interactiveShellRoot))
}

func interactiveShellPaths(id, slot string) (dir, socket string, err error) {
	if slot != "" {
		run, runErr := slots.RunDirFor(slot)
		if runErr != nil {
			return "", "", runErr
		}
		dir = filepath.Join(run, "shells", id)
	} else {
		dir = filepath.Join(interactiveShellRootPath(), id)
	}
	return dir, filepath.Join(dir, "tmux.sock"), nil
}

// interactiveShellSlot decides which account the caller's shell runs as. Where slots are on, a person without a
// slot gets no shell: it would otherwise run as the service account.
func interactiveShellSlot(c *gin.Context) (slot string, status int, message string) {
	slot, on, err := slots.For(getUserID(c))
	if on && err == nil && slot != "" {
		return slot, 0, ""
	}
	// Slots are available on this host (even only for some people): a person without one gets no shell, never one
	// as the service account.
	if slots.Enabled() || err != nil {
		return "", http.StatusForbidden, "the terminal needs a personal account on this server"
	}
	return "", 0, ""
}

// realTmux is the tmux binary itself, by full path: the service's PATH starts with the slot front-end shim, which must not see
// these -S calls, and the sandboxed command's own PATH is trimmed (Homebrew's tmux is not on it on a Mac).
func realTmux() string {
	if _, err := os.Stat("/usr/bin/tmux"); err == nil {
		return "/usr/bin/tmux"
	}
	if path, err := exec.LookPath("tmux"); err == nil {
		return path
	}
	return "tmux"
}

// interactiveShellTmuxQuietBindings turns off tmux's own menus and prefix commands (each ends with a separating " \\; ").
const interactiveShellTmuxQuietBindings = `set-option -g prefix None \; set-option -g prefix2 None \; unbind-key -a -T prefix \; ` +
	`unbind-key -T root MouseDown3Pane \; unbind-key -T root M-MouseDown3Pane \; unbind-key -T root MouseDown3Status \; ` +
	`unbind-key -T root MouseDown3StatusLeft \; unbind-key -T root MouseDown3StatusRight \; ` +
	`unbind-key -T root M-MouseDown3Status \; unbind-key -T root M-MouseDown3StatusLeft \; unbind-key -T root MouseDrag1Border \; `

// interactiveShellServerAccess lets the service account talk to a terminal that runs as a user's slot. tmux 3.3+ refuses every client of
// another user ("access not allowed") whatever the socket's file mode, so without it the service could not see, resize or stop the shell:
// each start removed the "dead" socket and started another server, and each stop removed the folder but left the server running, so a
// person's shells piled up (Excellence 2026-10-03: five servers for one terminal).
//
// The tmux server runs in the sandbox's user namespace, which maps only the slot's own id: every other user, the service included,
// arrives as the overflow user (nobody, 65534), so that is the user to allow. Who can reach the socket at all is still decided by its
// file mode: the slot and its group (the service), never another person's slot.
func interactiveShellServerAccess(slot string) string {
	if slot == "" {
		return ""
	}
	name := "nobody"
	if overflow, err := os.ReadFile("/proc/sys/kernel/overflowuid"); err == nil {
		if account, lookupErr := user.LookupId(strings.TrimSpace(string(overflow))); lookupErr == nil && account.Username != "" {
			name = account.Username
		}
	}
	// -w: write access, which kill-server and resize need (plain -a let the service list but not stop).
	return fmt.Sprintf(`server-access -a -w %s \; `, shellQuote(name))
}

func interactiveShellRunning(socket string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, realTmux(), "-S", socket, "has-session", "-t", interactiveShellSession).Run() == nil
}

// interactiveShellHostOS is the platform the workspace service runs on (a variable for tests).
var interactiveShellHostOS = runtime.GOOS

// interactiveShellUnconfinedAllowed says whether this workspace service may start a terminal without the sandbox: only a person's own
// Mac in native mode, the same rule the coding agents follow (macOS has no Landlock; Seatbelt confinement is PLAT-364 follow-up work).
// Never on Linux, so never on a server; never where slots (per-user accounts) are on. There is no switch to forget.
func interactiveShellUnconfinedAllowed() bool {
	return interactiveShellHostOS == "darwin" &&
		strings.EqualFold(strings.TrimSpace(os.Getenv("NATIVE_WORKSPACE")), "true") &&
		!slots.Enabled()
}

// unconfinedShellCommand runs command for a terminal that is not sandboxed, in dir, with the host environment (known secrets stripped).
func unconfinedShellCommand(ctx context.Context, command, dir string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)
	cmd.Dir = dir
	cmd.Env = security.BuildSafeEnvironment()
	return cmd
}

func clampShellSize(cols, rows int) (int, int) {
	if cols < 20 || cols > 500 {
		cols = 160
	}
	if rows < 5 || rows > 200 {
		rows = 48
	}
	return cols, rows
}

func shellError(c *gin.Context, status int, message string) {
	c.JSON(status, models.APIResponse[any]{Success: false, Message: message, Error: message})
}

// prepareInteractiveShellDir creates the shell's folder. For a slot it is group-writable and setgid (the run
// folder's group is the slot's), so the slot's tmux server can create its socket and the service can still remove it.
func prepareInteractiveShellDir(dir, slot string) error {
	if err := os.MkdirAll(filepath.Join(dir, "tmp"), 0o700); err != nil {
		return err
	}
	if slot == "" {
		return os.Chmod(interactiveShellRootPath(), 0o700)
	}
	for _, d := range []string{filepath.Dir(dir), dir, filepath.Join(dir, "tmp")} {
		if err := os.Chmod(d, 0o770|os.ModeSetgid); err != nil {
			return err
		}
	}
	return nil
}

// StartInteractiveShell starts (or returns the running) sandboxed shell.
func StartInteractiveShell(c *gin.Context) {
	var req InteractiveShellStartRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		shellError(c, http.StatusBadRequest, "Invalid request body")
		return
	}
	if !interactiveShellID.MatchString(req.ShellID) {
		shellError(c, http.StatusBadRequest, "shell_id must be 1-48 lowercase letters, digits or dashes")
		return
	}
	// A shell never runs without a Folder Guard: it would otherwise be an
	// unsandboxed login on the server.
	if req.FolderGuard == nil || !req.FolderGuard.Enabled || len(req.FolderGuard.WritePaths) == 0 {
		shellError(c, http.StatusBadRequest, "folder_guard with write paths is required")
		return
	}
	slot, status, message := interactiveShellSlot(c)
	if status != 0 {
		shellError(c, status, message)
		return
	}
	docsDir := viper.GetString("docs-dir")
	workingDir := docsDir
	if req.WorkingDirectory != "" {
		full := filepath.Join(docsDir, utils.SanitizeInputPath(req.WorkingDirectory, docsDir))
		if !utils.IsValidFilePath(full, docsDir) {
			shellError(c, http.StatusBadRequest, "Working directory must be within the workspace")
			return
		}
		if info, err := os.Stat(full); err != nil || !info.IsDir() {
			shellError(c, http.StatusBadRequest, "Working directory does not exist")
			return
		}
		workingDir = full
	}
	dir, socket, err := interactiveShellPaths(req.ShellID, slot)
	if err != nil {
		shellError(c, http.StatusInternalServerError, "Cannot find the shell folder: "+err.Error())
		return
	}
	extraEnv := map[string]string{}
	for k, v := range req.ExtraEnv {
		if isAllowedShellExtraEnvKey(k) {
			extraEnv[k] = v
		}
	}
	isolator := &security.Isolator{
		ReadPaths:         req.FolderGuard.ReadPaths,
		WritePaths:        append(append([]string{}, req.FolderGuard.WritePaths...), dir),
		BlockedPaths:      req.FolderGuard.BlockedPaths,
		BlockedWritePaths: req.FolderGuard.BlockedWritePaths,
		WorkDir:           workingDir,
		BaseDir:           docsDir,
		StrictAllowlist:   req.FolderGuard.StrictAllowlist,
		AllowNetwork:      !req.FolderGuard.DenyNetwork,
		BrowserSession:    req.FolderGuard.BrowserSession,
		AllowPTY:          true,
		Slot:              slot,
		Interactive:       true,
		ExtraEnv:          extraEnv,
	}
	// Attaches run in this same sandbox (interactive_shell_attach.go). The
	// agent server calls start before every attach, so a restarted service
	// learns the sandbox of a shell that outlived it.
	unconfined := req.Unconfined && interactiveShellUnconfinedAllowed()
	rememberInteractiveShell(req.ShellID, *isolator, unconfined)
	handle := InteractiveShellHandle{ShellID: req.ShellID, Socket: socket, Session: interactiveShellSession, Running: true}
	if interactiveShellRunning(socket) {
		c.JSON(http.StatusOK, models.APIResponse[InteractiveShellHandle]{Success: true, Data: handle})
		return
	}
	_ = os.RemoveAll(dir)
	if err := prepareInteractiveShellDir(dir, slot); err != nil {
		shellError(c, http.StatusInternalServerError, "Cannot prepare the shell folder")
		return
	}

	for _, wp := range req.FolderGuard.WritePaths {
		physical := wp
		if !filepath.IsAbs(physical) {
			physical = filepath.Join(docsDir, physical)
		}
		_ = os.MkdirAll(physical, 0o755)
	}
	cols, rows := clampShellSize(req.Cols, req.Rows)
	shell := "/bin/bash"
	if _, err := os.Stat(shell); err != nil {
		shell = "/bin/sh"
	}
	// tmux daemonizes: this command returns once the server is up, and the
	// server keeps the sandbox (and its private /tmp) alive for the shell.
	// TMPDIR points at the shell's own folder: the per-command scratch is
	// removed as soon as this start command returns.
	environment := fmt.Sprintf("TMPDIR=%s TERM=xterm-256color PROMPT_COMMAND=%s", shellQuote(filepath.Join(dir, "tmp")), shellQuote(interactiveShellPromptCommand(!unconfined)))
	if !unconfined {
		environment += fmt.Sprintf(" AGENTWORKS_START_DIR=%s", shellQuote(workingDir))
	}
	// Every sandboxed terminal, a user's slot account included, gets the project's private home: the slot used to keep the service
	// account's real HOME, so a login shell read /srv/agents/home/.profile ("Permission denied") and the terminal and the coding agent
	// (whose shell already uses this home) saw different installs (Excellence 2026-10-03).
	if !unconfined {
		if home := interactiveShellHome(docsDir, req.FolderGuard.WritePaths, workingDir); home != "" {
			environment += fmt.Sprintf(" HOME=%s XDG_CONFIG_HOME=%s", shellQuote(home), shellQuote(filepath.Join(home, ".config")))
		}
	}
	// The options are set before the session exists (start-server first, so they apply to its first pane): mouse reporting so the browser's
	// wheel scrolls tmux's own history (tmux redraws the screen itself, so the browser has no scrollback of its own: with the mouse off a wheel
	// did nothing, or cycled the shell's command history), a long history, and no tmux status bar.
	// tmux's own key and mouse commands are switched off (the browser terminal is one shell, not a tmux): its right-click menu (split, kill,
	// respawn) covered the browser's copy/paste menu, and the Ctrl-b prefix could split panes or open windows the page cannot show. The
	// wheel bindings, which scroll the history, stay.
	tmuxStart := fmt.Sprintf(`%s -f /dev/null -S %s start-server \; set-option -g history-limit %d \; set-option -g mouse on \; set-option -g status off \; %s%s new-session -d -s %s -x %d -y %d %s -l`,
		shellQuote(realTmux()), shellQuote(socket), interactiveShellHistoryLines, interactiveShellTmuxQuietBindings, interactiveShellServerAccess(slot), interactiveShellSession, cols, rows, shell)
	command := environment + " exec " + tmuxStart
	if slot != "" {
		// tmux makes its socket owner-only. The service reaches it (has-session, resize, stop) through the slot's group,
		// like the slot's own tmux socket, so the socket is opened to the group once the server is up.
		command = environment + " " + tmuxStart + fmt.Sprintf(" && chmod 0660 %s", shellQuote(socket))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var cmd *exec.Cmd
	if unconfined {
		cmd = unconfinedShellCommand(ctx, command, workingDir)
	} else {
		var cleanup func()
		var err error
		cmd, cleanup, err = isolator.ExecuteIsolated(ctx, command, nil)
		if err != nil {
			shellError(c, http.StatusInternalServerError, "Failed to set up the sandbox: "+err.Error())
			return
		}
		if cleanup != nil {
			defer cleanup()
		}
		if slot == "" {
			// Not run as a slot: the environment is the command's own, so the extra values go on it directly.
			for k, v := range extraEnv {
				cmd.Env = append(cmd.Env, k+"="+v)
			}
		}
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		shellError(c, http.StatusInternalServerError, "The shell did not start: "+strings.TrimSpace(string(out)))
		return
	}
	if !interactiveShellRunning(socket) {
		shellError(c, http.StatusInternalServerError, "The shell exited right after starting")
		return
	}
	c.JSON(http.StatusOK, models.APIResponse[InteractiveShellHandle]{Success: true, Data: handle})
}

// StopInteractiveShell ends the shell and everything it started.
func StopInteractiveShell(c *gin.Context) {
	var req interactiveShellRef
	if err := c.ShouldBindJSON(&req); err != nil || !interactiveShellID.MatchString(req.ShellID) {
		shellError(c, http.StatusBadRequest, "valid shell_id is required")
		return
	}
	slot, status, message := interactiveShellSlot(c)
	if status != 0 {
		shellError(c, status, message)
		return
	}
	dir, socket, err := interactiveShellPaths(req.ShellID, slot)
	if err != nil {
		shellError(c, http.StatusInternalServerError, "Cannot find the shell folder")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = exec.CommandContext(ctx, realTmux(), "-S", socket, "kill-server").Run()
	_ = os.RemoveAll(dir)
	forgetInteractiveShell(req.ShellID)
	c.JSON(http.StatusOK, models.APIResponse[InteractiveShellHandle]{Success: true, Data: InteractiveShellHandle{ShellID: req.ShellID, Socket: socket, Session: interactiveShellSession}})
}

// ResizeInteractiveShell sets the shell window size.
func ResizeInteractiveShell(c *gin.Context) {
	var req interactiveShellRef
	if err := c.ShouldBindJSON(&req); err != nil || !interactiveShellID.MatchString(req.ShellID) {
		shellError(c, http.StatusBadRequest, "valid shell_id is required")
		return
	}
	slot, status, message := interactiveShellSlot(c)
	if status != 0 {
		shellError(c, status, message)
		return
	}
	_, socket, err := interactiveShellPaths(req.ShellID, slot)
	if err != nil || !interactiveShellRunning(socket) {
		shellError(c, http.StatusNotFound, "The shell is not running")
		return
	}
	cols, rows := clampShellSize(req.Cols, req.Rows)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, realTmux(), "-S", socket, "resize-window", "-t", interactiveShellSession, "-x", strconv.Itoa(cols), "-y", strconv.Itoa(rows)).Run(); err != nil {
		shellError(c, http.StatusInternalServerError, "Resize failed")
		return
	}
	c.JSON(http.StatusOK, models.APIResponse[InteractiveShellHandle]{Success: true, Data: InteractiveShellHandle{ShellID: req.ShellID, Socket: socket, Session: interactiveShellSession, Running: true}})
}

// interactiveShellSweepRequest names the shells to keep; every other shell with the prefix is stopped.
type interactiveShellSweepRequest struct {
	Prefix string   `json:"prefix"`
	Keep   []string `json:"keep"`
}

// interactiveShellFolders lists every shell folder on this host: the shared shells folder and, where slots are on, each
// slot's own.
func interactiveShellFolders() []string {
	var folders []string
	roots := []string{interactiveShellRootPath()}
	if cfg, err := slots.LoadExecConfig(slots.ConfigPath()); err == nil && cfg.SlotRunRoot != "" {
		if slotDirs, err := filepath.Glob(filepath.Join(cfg.SlotRunRoot, "*", "shells")); err == nil {
			roots = append(roots, slotDirs...)
		}
	}
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				folders = append(folders, filepath.Join(root, entry.Name()))
			}
		}
	}
	return folders
}

// SweepInteractiveShells stops shells a previous agent server left running: at its start nothing tracks them. Only
// the agent server calls it, with the ids it does track.
func SweepInteractiveShells(c *gin.Context) {
	var req interactiveShellSweepRequest
	if err := c.ShouldBindJSON(&req); err != nil || !regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,19}$`).MatchString(req.Prefix) {
		shellError(c, http.StatusBadRequest, "a prefix of 1-20 lowercase letters, digits or dashes is required")
		return
	}
	keep := map[string]bool{}
	for _, id := range req.Keep {
		keep[id] = true
	}
	stopped := 0
	for _, folder := range interactiveShellFolders() {
		id := filepath.Base(folder)
		if !strings.HasPrefix(id, req.Prefix) || !interactiveShellID.MatchString(id) || keep[id] {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_ = exec.CommandContext(ctx, realTmux(), "-S", filepath.Join(folder, "tmux.sock"), "kill-server").Run()
		cancel()
		_ = os.RemoveAll(folder)
		forgetInteractiveShell(id)
		stopped++
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "stopped": stopped})
}

// interactiveShellHome is the private home folder of a terminal that does not run as a slot: <project>/.sandbox-cache/home, inside the
// folder the shell may write. In native mode (a Mac or a single-user machine) the sandboxed command otherwise keeps the real HOME so
// host CLIs find their config, but the sandbox forbids reading it, so every tool that looks at ~ (bash's profile, git's config, a
// CLI's settings) prints "Operation not permitted". The servers already give the shell tool such a home.
func interactiveShellHome(docsDir string, writePaths []string, workingDir string) string {
	root := workingDir
	for _, wp := range writePaths {
		physical := wp
		if !filepath.IsAbs(physical) {
			physical = filepath.Join(docsDir, physical)
		}
		physical = filepath.Clean(physical)
		if workingDir == physical || strings.HasPrefix(workingDir+string(filepath.Separator), physical+string(filepath.Separator)) {
			root = physical
			break
		}
	}
	home := filepath.Join(root, ".sandbox-cache", "home")
	if err := os.MkdirAll(filepath.Join(home, ".config"), 0o700); err != nil {
		return ""
	}
	// The service creates the folders; a terminal running as a user's slot (another user, the project's group) must be able to use them.
	for _, dir := range []string{filepath.Dir(home), home, filepath.Join(home, ".config")} {
		_ = os.Chmod(dir, 0o770|os.ModeSetgid)
	}
	return home
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
