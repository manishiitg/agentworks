package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

const computerUseVersion = "1.0.2"

// Desktop authority belongs to the OS user's own local installation. Neither
// a hosted service nor a project/device/attached-agent credential inherits it.
func computerUseAllowed(c *UserClaims) bool {
	return c != nil && localProductInstallation() && IsLocalMode() && !IsMultiUserMode() && c.UserID == GetDefaultUserID() && personalLaptopSettingsClaims(c)
}

type computerUseSettings struct {
	Enabled bool `json:"enabled"`
}
type computerUseInput struct {
	Action  string   `json:"action"`
	Command string   `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`
	Headed  bool     `json:"headed,omitempty"`
}

var computerUseHost struct {
	sync.Mutex
	cancel context.CancelFunc
	root   string
}
var computerUseInstall sync.Mutex

func computerUseRoot() (string, error) {
	root, err := workflowCLIStateRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "computer-use"), nil
}
func computerUseKey(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:16])
}
func computerUseChatDirectory(root, session string) (string, error) {
	base, err := computerUseRoot()
	return filepath.Join(base, "chats", computerUseKey(agentProfileRuntimeWorkspace(GetDefaultUserID(), root)+":"+session)), err
}

// The final CLI confinement policy needs this exact private directory even on
// Linux, where ordinary absolute folder-guard paths intentionally remain closed.
func computerUseCaptureReadPolicy(base *llmtypes.CLISecurityPolicy, directory string) *llmtypes.CLISecurityPolicy {
	policy := llmtypes.CLISecurityPolicy{}
	if base != nil {
		policy = base.Clone()
	}
	policy.WorkspaceReadPaths = appendUniqueStrings(policy.WorkspaceReadPaths, directory)
	return &policy
}
func computerUseSettingsFile(root string) (string, error) {
	base, err := computerUseRoot()
	return filepath.Join(base, "projects", computerUseKey(agentProfileRuntimeWorkspace(GetDefaultUserID(), root))+".json"), err
}
func readComputerUse(root string) (computerUseSettings, error) {
	file, err := computerUseSettingsFile(root)
	if err != nil {
		return computerUseSettings{}, err
	}
	data, err := os.ReadFile(file)
	if os.IsNotExist(err) {
		return computerUseSettings{}, nil
	}
	var out computerUseSettings
	if err == nil {
		err = json.Unmarshal(data, &out)
	}
	return out, err
}
func saveComputerUse(root string, enabled bool) error {
	file, err := computerUseSettingsFile(root)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		return err
	}
	data, _ := json.Marshal(computerUseSettings{Enabled: enabled})
	return os.WriteFile(file, data, 0600)
}
func computerUseBinary() (string, error) {
	base, err := computerUseRoot()
	if err != nil {
		return "", err
	}
	arch := runtime.GOARCH
	if arch == "amd64" {
		arch = "x64"
	}
	suffix := ""
	platform := runtime.GOOS
	if platform == "windows" {
		platform = "win32"
		suffix = ".exe"
	}
	pack := filepath.Join(base, "runtime", "node_modules", "agent-desktop")
	data, err := os.ReadFile(filepath.Join(pack, "package.json"))
	var manifest struct {
		Version string `json:"version"`
	}
	if err != nil || json.Unmarshal(data, &manifest) != nil || manifest.Version != computerUseVersion {
		return "", errors.New("Install Computer use from this Crew's Computer use panel, or use action=install")
	}
	file := filepath.Join(pack, "bin", "agent-desktop-"+platform+"-"+arch+suffix)
	if info, err := os.Stat(file); err != nil || !info.Mode().IsRegular() {
		return "", errors.New("Computer use is not installed for this computer")
	}
	return file, nil
}

// Bound output without logging app text, screenshots, clipboard or typed values.
type computerUseOutput struct {
	bytes.Buffer
	exceeded bool
}

func (b *computerUseOutput) Write(p []byte) (int, error) {
	n := len(p)
	if b.Len()+n > 1<<20 {
		b.exceeded = true
		return n, nil
	}
	_, _ = b.Buffer.Write(p)
	return n, nil
}
func computerUseEnvironment(home, binary string) []string {
	env := []string{}
	for _, v := range os.Environ() {
		key, _, _ := strings.Cut(v, "=")
		// Native desktop IPC/display context only; never AI, platform or secret env.
		allowed := strings.HasPrefix(key, "LC_")
		switch key {
		case "PATH", "HOME", "USER", "LOGNAME", "LANG", "TMPDIR", "TMP", "TEMP",
			"SystemRoot", "WINDIR", "USERPROFILE", "LOCALAPPDATA", "APPDATA",
			"DISPLAY", "WAYLAND_DISPLAY", "XDG_RUNTIME_DIR", "DBUS_SESSION_BUS_ADDRESS", "XAUTHORITY":
			allowed = true
		}
		if allowed {
			env = append(env, v)
		}
	}
	env = append(env, "AGENT_DESKTOP_HOME="+home)
	if runtime.GOOS == "darwin" {
		arch := runtime.GOARCH
		if arch == "amd64" {
			arch = "x64"
		}
		env = append(env, "AGENT_DESKTOP_MACOS_HELPER_PATH="+filepath.Join(filepath.Dir(binary), "agent-desktop-macos-helper-darwin-"+arch))
	}
	return env
}

var computerUseExecute = func(ctx context.Context, session string, args []string) (json.RawMessage, error) {
	binary, err := computerUseBinary()
	if err != nil {
		return nil, err
	}
	base, err := computerUseRoot()
	if err != nil {
		return nil, err
	}
	home := filepath.Join(base, "chats", computerUseKey(session))
	if err := os.MkdirAll(home, 0700); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Env = computerUseEnvironment(home, binary)
	var output computerUseOutput
	cmd.Stdout = &output
	// Do not forward native diagnostics, which may contain desktop content.
	err = cmd.Run()
	if output.exceeded {
		return nil, errors.New("Desktop response is too large; use a compact skeleton snapshot and inspect one region")
	}
	if json.Valid(output.Bytes()) {
		return append(json.RawMessage(nil), output.Bytes()...), nil
	}
	if err != nil {
		return nil, errors.New("Computer use could not complete this command; check permissions or read its command help")
	}
	// CLI help and compiled skills are text, unlike operational JSON responses.
	data, _ := json.Marshal(map[string]any{"ok": true, "text": output.String()})
	return data, nil
}

func installComputerUse(ctx context.Context) error {
	if !computerUseInstall.TryLock() {
		return errors.New("Computer use setup is already running")
	}
	defer computerUseInstall.Unlock()
	base, err := computerUseRoot()
	if err != nil {
		return err
	}
	dir := filepath.Join(base, "runtime")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	// Exact release, private destination, no package lifecycle scripts or global install.
	cmd := exec.CommandContext(ctx, "npm", "install", "--prefix", dir, "--ignore-scripts", "--no-audit", "--no-fund", "--no-save", "--package-lock=false", "agent-desktop@"+computerUseVersion)
	if err := cmd.Run(); err != nil {
		return errors.New("Computer use setup failed. Install Node.js with npm, then choose Install again")
	}
	_, err = computerUseBinary()
	return err
}

func computerUseArguments(in computerUseInput) ([]string, error) {
	const commands = " snapshot find get is list-apps list-windows list-displays list-surfaces click double-click triple-click right-click type set-value clear focus select toggle check uncheck expand collapse scroll scroll-to press hover drag mouse-move mouse-click mouse-wheel launch close-app focus-window resize-window move-window minimize maximize restore open-system-surface list-notifications dismiss-notification dismiss-all-notifications notification-action clipboard-get clipboard-set clipboard-clear wait screenshot "
	if !strings.Contains(commands, " "+in.Command+" ") || in.Command == "" {
		return nil, errors.New("Choose an agent-desktop command; use action=guide for its instructions")
	}
	if len(in.Args) > 64 {
		return nil, errors.New("Too many desktop arguments")
	}
	if in.Command == "screenshot" {
		for i := 0; i < len(in.Args); i += 2 {
			if i+1 >= len(in.Args) || in.Args[i] != "--app" && in.Args[i] != "--window-id" && in.Args[i] != "--screen" {
				return nil, errors.New("Screenshots accept --app, --window-id or --screen; AgentWorks keeps the image in private chat storage")
			}
		}
	}
	for _, arg := range in.Args {
		if len(arg) > 32768 || strings.ContainsRune(arg, 0) {
			return nil, errors.New("Desktop argument is too long")
		}
		flag, _, _ := strings.Cut(arg, "=")
		switch flag {
		case "--session", "--agent-id", "--trace", "--trace-strict", "--headed", "--screenshot", "--debug":
			return nil, errors.New("Chat session and output settings are managed by AgentWorks; use the headed field for physical input")
		}
	}
	args := []string{}
	if in.Headed {
		args = append(args, "--headed")
	}
	return append(append(args, in.Command), in.Args...), nil
}

func (api *StreamingAPI) computerUseAction(ctx context.Context, c *UserClaims, root, session string, in computerUseInput) (any, error) {
	if !computerUseAllowed(c) || !crewProjectOwnedByCaller(c.UserID, root) {
		return nil, errors.New("Computer use is available only in your own Crew running on this computer")
	}
	settings, err := readComputerUse(root)
	if err != nil {
		return nil, err
	}
	switch in.Action {
	case "enable", "disable", "stop":
		enabled := in.Action == "enable"
		if err := saveComputerUse(root, enabled); err != nil {
			return nil, err
		}
		if !enabled {
			computerUseHost.Lock()
			if computerUseHost.cancel != nil {
				computerUseHost.cancel()
			}
			computerUseHost.Unlock()
		}
		return map[string]any{"enabled": enabled}, nil
	case "install":
		if err := installComputerUse(ctx); err != nil {
			return nil, err
		}
		return map[string]any{"installed": true, "version": computerUseVersion}, nil
	case "status":
		_, installed := computerUseBinary()
		computerUseHost.Lock()
		active := computerUseHost.cancel != nil
		computerUseHost.Unlock()
		out := map[string]any{"enabled": settings.Enabled, "installed": installed == nil, "active": active, "version": computerUseVersion, "platform": runtime.GOOS}
		if installed == nil {
			permissions, err := computerUseExecute(ctx, "setup", []string{"permissions"})
			if err == nil {
				out["permissions"] = permissions
			}
		}
		return out, nil
	case "permissions":
		return computerUseExecute(ctx, "setup", []string{"permissions", "--guide"})
	case "guide":
		args := []string{"skills", "get", "desktop"}
		if in.Command != "" {
			args = []string{in.Command, "--help"}
			if in.Command == "platform" {
				args = []string{"skills", "get", "platform"}
			}
		}
		return computerUseExecute(ctx, session, args)
	case "run":
		if !settings.Enabled {
			return nil, errors.New("Computer use is off for this Crew. Enable it in Integrations → Computer use, or ask to enable it in chat")
		}
		args, err := computerUseArguments(in)
		if err != nil {
			return nil, err
		}
		computerUseHost.Lock()
		if computerUseHost.cancel != nil {
			computerUseHost.Unlock()
			return nil, errors.New("Your computer is busy with another desktop action. Wait for it to finish or choose Stop in Computer use")
		}
		settings, err = readComputerUse(root)
		if err != nil || !settings.Enabled {
			computerUseHost.Unlock()
			return nil, errors.New("Computer use was stopped. Ask to enable it again before continuing")
		}
		ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		computerUseHost.cancel, computerUseHost.root = cancel, root
		computerUseHost.Unlock()
		defer func() {
			cancel()
			computerUseHost.Lock()
			computerUseHost.cancel = nil
			computerUseHost.root = ""
			computerUseHost.Unlock()
		}()
		if in.Command == "screenshot" {
			dir, err := computerUseChatDirectory(root, session)
			if err != nil {
				return nil, err
			}
			if err := os.MkdirAll(dir, 0700); err != nil {
				return nil, err
			}
			file := filepath.Join(dir, "screenshot-"+time.Now().Format("20060102-150405.000000000")+".png")
			out, err := computerUseExecute(ctx, agentProfileRuntimeWorkspace(GetDefaultUserID(), root)+":"+session, append(args, file))
			if err == nil {
				_ = os.Chmod(file, 0600)
			}
			return out, err
		}
		return computerUseExecute(ctx, agentProfileRuntimeWorkspace(GetDefaultUserID(), root)+":"+session, args)
	default:
		return nil, errors.New("Choose status, install, permissions, enable, disable, stop, guide or run")
	}
}

func (api *StreamingAPI) handleCrewComputerUse(w http.ResponseWriter, r *http.Request) {
	if mux.Vars(r)["id"] != "work" || !computerUseAllowed(GetUserFromContext(r.Context())) {
		externalError(w, 403, "local_computer_required", "Open your own local Crew to use your computer")
		return
	}
	root, ok := api.laptopSettingsRoot(w, r)
	if !ok {
		return
	}
	in := computerUseInput{Action: "status"}
	if r.Method != http.MethodGet && json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536)).Decode(&in) != nil {
		externalError(w, 400, "invalid_request", "Choose a Computer use action")
		return
	}
	// This setup endpoint does not dispatch desktop actions. Those belong to an
	// admitted chat or personal MCP call, with their own private snapshot namespace.
	if in.Action == "run" {
		externalError(w, 400, "use_chat", "Ask this Crew to use your computer from chat")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	out, err := api.computerUseAction(ctx, GetUserFromContext(r.Context()), root, "setup", in)
	if err != nil {
		externalError(w, 400, "computer_use_unavailable", err.Error())
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	externalJSON(w, out)
}
