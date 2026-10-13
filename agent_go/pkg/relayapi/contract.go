// Package relayapi defines the trusted in-process Relay product boundary.
// Host capabilities are live callbacks. They must not be serialized or replaced
// with a user-selected endpoint, identity, environment or workspace root.
package relayapi

import (
	"context"
	"errors"
	"io/fs"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
)

const Version = 1
const RecoveryLeaseGracePeriod = 17 * time.Second

type DBOSConfig struct {
	RunID, ReleaseHash, PythonExecutable string
	Attempt                              int
	Authorize                            func(context.Context) error
}
type DBOSPrototype = DBOSConfig
type InterruptedError struct{ Cause error }

func (e *InterruptedError) Error() string { return "Relay process interrupted: " + e.Cause.Error() }
func (e *InterruptedError) Unwrap() error { return e.Cause }
func IsInterrupted(err error) bool        { var e *InterruptedError; return errors.As(err, &e) }

type Tool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Schema      map[string]interface{} `json:"schema"`
}
type MCP struct {
	Server string   `json:"server"`
	Tools  []string `json:"tools"`
}
type Call struct {
	AttemptID    string                 `json:"-"`
	Kind         string                 `json:"kind"`
	Server       string                 `json:"server"`
	Tool         string                 `json:"tool"`
	Arguments    map[string]interface{} `json:"arguments"`
	ID           string                 `json:"id"`
	Name         string                 `json:"name"`
	SystemPrompt string                 `json:"system_prompt"`
	Messages     []string               `json:"messages"`
	Model        interface{}            `json:"model"`
	Tools        []Tool                 `json:"tools"`
	Skills       []string               `json:"skills"`
	MCP          []MCP                  `json:"mcp"`
	OutputSchema map[string]interface{} `json:"output_schema"`
	MaxTurns     int                    `json:"max_turns"`
	Recovery     string                 `json:"recovery"`
}
type ToolCaller func(context.Context, string, map[string]interface{}) (string, error)
type AgentResult struct {
	Output          interface{}
	Tools           []map[string]interface{}
	Provider, Model string
}
type AgentCaller func(context.Context, Call, ToolCaller) (interface{}, error)
type Files interface {
	ReadWorkspaceFile(context.Context, workspace.ReadWorkspaceFileParams) (workspace.ReadFileResult, error)
	UpdateWorkspaceFile(context.Context, workspace.UpdateWorkspaceFileParams) (workspace.UpdateFileResult, error)
	ExecuteShellCommand(context.Context, workspace.ExecuteShellCommandParams) (workspace.ShellCommandResult, error)
}
type Config struct {
	Client              Files
	SourcePath, RunPath string
	Input               interface{}
	Variables           map[string]interface{}
	Env                 map[string]string
	CallAgent           AgentCaller
	Timeout             int
	DBOSPrototype       *DBOSPrototype
	DBOS                *DBOSConfig
}
type Release struct {
	Version         string    `json:"version"`
	Error           string    `json:"error,omitempty"`
	SourceWorkspace string    `json:"source_workspace,omitempty"`
	Hash            string    `json:"hash"`
	PublishedAt     time.Time `json:"published_at"`
	Functions       []string  `json:"functions"`
	OutputStep      string    `json:"output_step_id"`
	FileCount       int       `json:"file_count"`
	Files           []string  `json:"files"`
}

var ErrVersionNotFound = errors.New("Relay version not found")

// PublishSpec is supplied only after host manifest/function/source validation.
type PublishSpec struct {
	Functions                      []string
	OutputStep                     string
	RequiredFiles, RequiredScripts []string
}

// ReleasePorts retain host workspace scoping, validation and publication locks.
// No secret store, user directory, raw filesystem or database handle is exposed.
type ReleasePorts struct {
	Read              func(context.Context, string) (string, bool, error)
	ListFiles         func(context.Context, string) ([]string, error)
	ListFolders       func(context.Context, string) ([]string, error)
	CreateFolder      func(context.Context, string) error
	Write             func(context.Context, string, string) error
	Prepare           func(context.Context, string) (PublishSpec, error)
	ValidatePublished func(context.Context, string, *Release) error
	Published         func(string)
	LockPublication   func(string) func()
}
type Releases interface {
	Read(context.Context, ReleasePorts, string, string) (*Release, string, error)
	Active(context.Context, ReleasePorts, string) (*Release, string, error)
	Resolve(context.Context, ReleasePorts, string, string) (*Release, string, error)
	List(context.Context, ReleasePorts, string) ([]Release, error)
	Verify(context.Context, ReleasePorts, *Release, string) error
	Publish(context.Context, ReleasePorts, string) (*Release, error)
}

// Factory is reviewed first-party code compiled with the host, not a sandbox.
type Factory struct {
	ID              string
	Version         int
	Files           fs.FS
	SourceInspector string
	Run             func(context.Context, Config) error
	Releases        Releases
}
