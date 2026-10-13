// Package brainapi is the in-process contract between the public platform and a trusted Brain engine.
// It contains no content engine, storage implementation, credentials or HTTP authentication.
package brainapi

import (
	"context"
	"time"
)

const Version = 1

type Config struct {
	Root string
	// Backend is operator-selected: builtin (default) or private.
	Backend string `json:"-"`
	// LiveRoot is the folder holding Brain's notes as plain files (a Git working folder); default <Root>/live.
	LiveRoot       string
	OrganizationID string
	BackupRemote   string
	BackupBranch   string
	// Operator-only opt-in for Git hosts on a private network. Never a tool argument.
	AllowPrivateBackup bool
	// Host-derived AES key; never persisted in KB configuration or Git.
	BackupEncryptionKey string `json:"-"`
	// SecretResolver reads a platform (global) secret by name; backup credentials may reference one instead of
	// holding a token of their own. Set by the server; nil means no secret can be referenced.
	SecretResolver func(name string) (string, bool) `json:"-"`
}

type Cap struct {
	FolderPath string `json:"folder_path"`
	Role       string `json:"role"`
}

type Principal struct {
	IdentityID    string                      `json:"identity_id"`
	IsAdmin       bool                        `json:"is_admin"`
	AccessOnly    bool                        `json:"access_only"`
	Caps          *[]Cap                      `json:"caps,omitempty"`
	Recheck       func(context.Context) error `json:"-"`
	BindingPolicy *BindingPolicy              `json:"-"`
	// Engine-owned idempotency metadata; never decoded from a caller request.
	RequestTool      string         `json:"-"`
	RequestArguments map[string]any `json:"-"`
}

type Error struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	Retryable  bool   `json:"retryable,omitempty"`
	RetryAfter *int   `json:"retry_after,omitempty"`
	Details    any    `json:"details,omitempty"`
}

type ToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
	Mutates     bool           `json:"mutates"`
}

type Identity struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	Disabled bool   `json:"disabled"`
}

type Entry struct {
	ID              string   `json:"entry_id"`
	FolderID        string   `json:"folder_id"`
	FolderPath      string   `json:"folder_path"`
	Path            string   `json:"path"`
	Filename        string   `json:"filename"`
	Type            string   `json:"type"`
	Title           string   `json:"title"`
	Description     string   `json:"description"`
	Tags            []string `json:"tags"`
	Sequence        uint64   `json:"sequence"`
	ContentSequence uint64   `json:"content_sequence"`
	Fingerprint     string   `json:"fingerprint"`
	Version         string   `json:"version"`
	CreatedAt       string   `json:"created_at"`
	UpdatedAt       string   `json:"updated_at"`
	CreatedBy       string   `json:"created_by"`
	UpdatedBy       string   `json:"updated_by"`
	// Binary marks content that is not text (an image, a PDF, an archive). It is stored and read whole, as base64 over
	// the tools; line and section reads, diffs and search apply to text only.
	Binary bool `json:"binary,omitempty"`
}

type Deletion struct {
	ID         string `json:"deletion_id"`
	EntryID    string `json:"entry_id"`
	FolderID   string `json:"folder_id"`
	FolderPath string `json:"folder_path"`
	Path       string `json:"path"`
	Sequence   uint64 `json:"sequence"`
	Actor      string `json:"actor"`
	CreatedAt  string `json:"created_at"`
}

type Binding struct {
	Alias    string `json:"alias"`
	FolderID string `json:"folder_id"`
	Access   string `json:"access"`
}

type BindingPolicy struct {
	Bindings []Binding
	Audience []string
	// Admins are the audience members who are installation administrators: they can read every folder.
	Admins []string
	// ReadAll is the project's "Read" Brain access: read-only on any folder the principal can read, and only where every
	// output reader can read it too. It never allows a write and is exclusive with Bindings.
	ReadAll bool
	// WriteAll is "Read & write": read and write wherever the principal itself may, so the project's agents organize
	// folders themselves. The principal's own role and the output audience still bound every folder.
	WriteAll bool
}

type Snapshot struct {
	EntryID     string `json:"entry_id,omitempty"`
	DeletionID  string `json:"deletion_id,omitempty"`
	Path        string `json:"path"`
	FolderPath  string `json:"folder_path"`
	Version     string `json:"version,omitempty"`
	Sequence    uint64 `json:"content_sequence"`
	Fingerprint string `json:"fingerprint,omitempty"`
	Content     []byte `json:"-"`
}

type Receipt struct {
	Branch         string     `json:"branch,omitempty"`
	ID             string     `json:"receipt_id"`
	IdentityID     string     `json:"-"`
	OrganizationID string     `json:"-"`
	Base           string     `json:"base_commit_id"`
	Commit         string     `json:"prepared_commit_id"`
	Selections     []Snapshot `json:"selections"`
	CreatedAt      string     `json:"created_at"`
	ExpiresAt      string     `json:"expires_at"`
	State          string     `json:"state"`
	PushedAt       string     `json:"pushed_at,omitempty"`
	LastError      string     `json:"last_backup_error,omitempty"`
}

type AutoPushStatus struct {
	At     time.Time `json:"at"`
	OK     bool      `json:"ok"`
	Pushed string    `json:"pushed,omitempty"` // the commit now on the remote branch
	Error  string    `json:"error,omitempty"`
}

type GitFolder struct {
	Path      string
	Remote    string
	Branch    string
	Username  string
	PATSecret string
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// Engine runs with a host-established principal. Recheck callbacks retain live token,
// account and project restrictions; nil and empty Caps must never be conflated.
// Factories are trusted first-party code compiled into the same process, not plugins
// loaded from a path or HTTP services. The host owns selection, draining and writer leases.
type Engine interface {
	Close() error
	AutoPush(ctx context.Context, quiet time.Duration, now time.Time) (bool, error)
	BackupConfigured() (bool, error)
	Call(ctx context.Context, p Principal, tool string, args map[string]any) (any, error)
	CallTool(ctx context.Context, p Principal, tool string, args map[string]any) (any, error)
	CanEditWholeBrain(p Principal) bool
	CanWriteSomewhere(p Principal) bool
	EnsureDefaultFolder(ctx context.Context, admin Principal, name, role string, users []Identity) error
	EnsureGitRepository(ctx context.Context) (GitFolder, error)
	GitFolder() (GitFolder, error)
	GitToken(f GitFolder) string
	IdentityActive(ctx context.Context, id string) bool
	LastAutoPush() *AutoPushStatus
	ReconcileBackup(ctx context.Context, p Principal) (any, error)
	RequireIdentityFolderRole(ctx context.Context, id, folderID, role string) error
	ReserveIntegrationRequest(ctx context.Context, p Principal, tool string, args map[string]any) (string, error)
	ResolveBinding(ctx context.Context, p Principal, b Binding, audience []string) (string, error)
	RunGit(ctx context.Context, p Principal, a map[string]any, run func(context.Context, string) (any, error)) (any, error)
	SeenCommit(identity string) string
	SetSeenCommit(identity, commit string) error
	SyncDisk(ctx context.Context, actor string) error
	SyncPlatformIdentities(ctx context.Context, users []Identity) error
	ValidateCaps(ctx context.Context, p Principal, id string, caps *[]Cap) error
	ValidateServiceTokenIdentity(ctx context.Context, p Principal, id string) error
	ValidateTokenIdentity(ctx context.Context, p Principal, id string) error
}

// Factory declares the exact host-compatible tool contract before opening any state.
type Factory struct {
	Version int
	Catalog []ToolDefinition
	Open    func(Config) (Engine, error)
}
