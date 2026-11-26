package upstream

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

// A local command connection (transport "stdio", auth scheme "Command") is an MCP server that this Vault starts as a
// program on its own computer, the way Claude Code, Codex and Cursor run a configured `command`. Only a Vault started
// with GATEWAY_ALLOW_LOCAL_COMMANDS=1 runs them: the Mac app and the source launcher in local mode set it; a server
// never does. The whole launch (program, arguments, environment, folder) is the connection's credential, so it sits in
// the encrypted credential store and is never returned by an API: arguments and environment often carry API keys.

const (
	CommandTransport  = "stdio"
	CommandAuthScheme = "Command"
)

// LocalCommand is the stored launch of a local command connection.
type LocalCommand struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Dir     string            `json:"dir,omitempty"`
}

var commandEnvName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ErrLocalCommandsOff is the refusal on a Vault that does not run programs (every server).
var ErrLocalCommandsOff = errors.New("this Vault does not run local programs: a command MCP server can be added only in the AgentWorks Mac app")

// ParseLocalCommand reads and checks a stored launch: an absolute program path, bounded arguments and environment.
func ParseLocalCommand(raw string) (LocalCommand, error) {
	var lc LocalCommand
	if len(raw) > 65536 {
		return lc, errors.New("local command is too large")
	}
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&lc); err != nil {
		return lc, errors.New("invalid local command")
	}
	if !filepath.IsAbs(lc.Command) || strings.ContainsRune(lc.Command, 0) {
		return lc, errors.New("local command must be an absolute program path")
	}
	if len(lc.Args) > 100 {
		return lc, errors.New("local command has too many arguments")
	}
	for _, arg := range lc.Args {
		if strings.ContainsRune(arg, 0) {
			return lc, errors.New("invalid local command argument")
		}
	}
	if len(lc.Env) > 100 {
		return lc, errors.New("local command has too many environment variables")
	}
	for name, value := range lc.Env {
		if !commandEnvName.MatchString(name) || strings.ContainsRune(value, 0) || hiddenCommandEnv(name) {
			return lc, fmt.Errorf("invalid environment variable %q", name)
		}
	}
	if lc.Dir != "" && !filepath.IsAbs(lc.Dir) {
		return lc, errors.New("local command folder must be an absolute path")
	}
	return lc, nil
}

// hiddenCommandEnv names Vault's own settings, which never reach a program it starts.
func hiddenCommandEnv(name string) bool {
	upper := strings.ToUpper(name)
	return strings.HasPrefix(upper, "GATEWAY_") || strings.HasPrefix(upper, "VAULT_") || strings.HasPrefix(upper, "CAPLAYER_")
}

// commandEnvironment is this process's environment without Vault's own settings, plus the connection's variables.
func commandEnvironment(lc LocalCommand) []string {
	env := make([]string, 0, len(lc.Env)+32)
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if _, override := lc.Env[name]; override || hiddenCommandEnv(name) {
			continue
		}
		env = append(env, entry)
	}
	for name, value := range lc.Env {
		env = append(env, name+"="+value)
	}
	return env
}

// redactCommand hides the connection's argument and environment values in a message.
func redactCommand(lc LocalCommand, text string) string {
	for _, value := range lc.Env {
		if len(value) >= 4 {
			text = strings.ReplaceAll(text, value, "[redacted]")
		}
	}
	for _, arg := range lc.Args {
		if len(arg) >= 8 {
			text = strings.ReplaceAll(text, arg, "[redacted]")
		}
	}
	return text
}

// stderrTail keeps the last bytes a program wrote to stderr, for the reason shown when it does not start.
type stderrTail struct {
	mu  sync.Mutex
	buf []byte
}

func (t *stderrTail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > 600 {
		t.buf = t.buf[len(t.buf)-600:]
	}
	return len(p), nil
}

func (t *stderrTail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}

func dialCommand(ctx context.Context, opts DialOptions) (*Client, error) {
	if !opts.AllowLocalCommands {
		return nil, ErrLocalCommandsOff
	}
	if opts.AuthScheme != CommandAuthScheme {
		return nil, errors.New("local command connection has no launch")
	}
	lc, err := ParseLocalCommand(opts.BearerToken)
	if err != nil {
		return nil, err
	}
	if info, err := os.Stat(lc.Command); err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
		return nil, fmt.Errorf("program %s is not installed on this computer", filepath.Base(lc.Command))
	}
	launch := func(_ context.Context, command string, _ []string, args []string) (*exec.Cmd, error) {
		cmd := exec.Command(command, args...)
		cmd.Env = commandEnvironment(lc)
		cmd.Dir = lc.Dir
		return cmd, nil
	}
	stderr := &stderrTail{}
	c, err := client.NewStdioMCPClientWithOptions(lc.Command, nil, lc.Args, transport.WithCommandFunc(launch), transport.WithCommandStderrWriter(stderr))
	if err != nil {
		return nil, errors.New(redactCommand(lc, fmt.Sprintf("could not start %s: %v", filepath.Base(lc.Command), err)))
	}
	initCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: "mcp-gateway", Version: "0.1.0"}
	if _, err := c.Initialize(initCtx, initReq); err != nil {
		c.Close()
		said := strings.Join(strings.Fields(stderr.String()), " ")
		text := fmt.Sprintf("%s did not start as an MCP server: %v", filepath.Base(lc.Command), err)
		if said != "" {
			text += " (it said: " + said + ")"
		}
		return nil, errors.New(redactCommand(lc, text))
	}
	return &Client{c: c, url: "command:" + filepath.Base(lc.Command), diagnostics: &refusalNote{}}, nil
}
