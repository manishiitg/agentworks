// Package localfiles is the file and shell protocol for an outbound laptop executor.
// Absolute roots are omitted from grant metadata; command output can contain local paths.
package localfiles

import (
	"fmt"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
	"regexp"
	"strings"
)

const Version = 1
const MaxMessageBytes = 16 << 20

var safeID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

func ValidID(id string) bool { return safeID.MatchString(id) }

type Resource struct {
	ID string `json:"id"`
	// Capabilities distinguish older CLIs. New executors always support shell;
	// writable folders also support guarded patches. These are not opt-in flags.
	Patch     bool           `json:"patch,omitempty"`
	Shell     bool           `json:"shell,omitempty"`
	Writable  bool           `json:"writable"`
	Downloads bool           `json:"downloads,omitempty"`
	Guard     wf.FolderGuard `json:"guard"`
}
type Hello struct {
	Version  int    `json:"version"`
	DeviceID string `json:"device_id"`
	// CLIVersion is the build the CLI was made from (a source revision, or "dev"); the website compares it with the server's
	// current one and asks the person to update. Empty from a CLI older than this field.
	CLIVersion string `json:"cli_version,omitempty"`
	// CLIBuild identifies the source the CLI is built from; unlike CLIVersion it changes only when the CLI does.
	CLIBuild  string     `json:"cli_build,omitempty"`
	Resources []Resource `json:"resources"`
}

func (h Hello) Validate() error {
	if h.Version != Version || !ValidID(h.DeviceID) || len(h.Resources) == 0 || len(h.Resources) > 32 || len(h.CLIVersion) > 80 || len(h.CLIBuild) > 80 || strings.ContainsAny(h.CLIBuild, "\r\n\x00") || strings.ContainsAny(h.CLIVersion, "\r\n\x00") {
		return fmt.Errorf("invalid executor handshake")
	}
	seen := map[string]bool{}
	for _, r := range h.Resources {
		if !ValidID(r.ID) || seen[r.ID] {
			return fmt.Errorf("invalid or duplicate folder alias")
		}
		if err := r.Guard.Validate(); err != nil {
			return err
		}
		if !r.Writable && len(r.Guard.WritePaths) > 0 {
			return fmt.Errorf("read-only resource has write grants")
		}
		seen[r.ID] = true
	}
	for _, r := range h.Resources {
		if r.Downloads {
			valid := false
			for _, companion := range h.Resources {
				if companion.ID == "downloads" && companion.Writable && !companion.Downloads && r.ID != "downloads" {
					valid = true
				}
			}
			if !valid {
				return fmt.Errorf("Downloads access requires its separate writable folder grant")
			}
		}
	}
	return nil
}

type Request struct {
	Identity         wf.EditIdentity `json:"identity"`
	ID               string          `json:"id"`
	ResourceID       string          `json:"resource_id"`
	Operation        string          `json:"operation"`
	Path             string          `json:"path"`
	Command          string          `json:"command,omitempty"`
	TimeoutSeconds   int             `json:"timeout_seconds,omitempty"`
	Content          string          `json:"content,omitempty"`
	ExpectedRevision string          `json:"expected_revision,omitempty"`
	RequestID        string          `json:"request_id,omitempty"`
}
type Response struct {
	Patches []wf.WriteReceipt `json:"patches,omitempty"`
	Shell   *ShellResult      `json:"shell,omitempty"`
	Code    string            `json:"code,omitempty"`
	ID      string            `json:"id"`
	Status  int               `json:"status"`
	Error   string            `json:"error,omitempty"`
	File    *wf.File          `json:"file,omitempty"`
	Entries []wf.Entry        `json:"entries,omitempty"`
	Receipt *wf.WriteReceipt  `json:"receipt,omitempty"`
}

const DefaultShellTimeout = 60
const MaxShellTimeout = 300
const MaxShellCommandBytes = 64 << 10
const MaxShellOutputBytes = 1 << 20

type ShellResult struct {
	RequestID string          `json:"request_id"`
	Identity  wf.EditIdentity `json:"identity"`
	Stdout    string          `json:"stdout"`
	Stderr    string          `json:"stderr"`
	ExitCode  int             `json:"exit_code"`
	TimedOut  bool            `json:"timed_out"`
	Truncated bool            `json:"truncated"`
}

func (r Request) ShellTimeout() int {
	if r.TimeoutSeconds == 0 {
		return DefaultShellTimeout
	}
	return r.TimeoutSeconds
}

func (r Request) ValidateShell() error {
	if len(r.Command) == 0 || len(r.Command) > MaxShellCommandBytes || r.ShellTimeout() < 1 || r.ShellTimeout() > MaxShellTimeout || len(r.RequestID) == 0 || len(r.RequestID) > 128 {
		return fmt.Errorf("shell requires a bounded command, request_id and timeout_seconds between 1 and 300")
	}
	return nil
}
