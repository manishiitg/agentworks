// Package localfiles is the file-only protocol for an outbound laptop executor.
// Absolute roots and credentials never appear in messages to the server.
package localfiles

import (
	"fmt"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
	"regexp"
)

const Version = 1
const MaxMessageBytes = 16 << 20

var safeID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

func ValidID(id string) bool { return safeID.MatchString(id) }

type Resource struct {
	ID       string         `json:"id"`
	Writable bool           `json:"writable"`
	Guard    wf.FolderGuard `json:"guard"`
}
type Hello struct {
	Version   int        `json:"version"`
	DeviceID  string     `json:"device_id"`
	Resources []Resource `json:"resources"`
}

func (h Hello) Validate() error {
	if h.Version != Version || !ValidID(h.DeviceID) || len(h.Resources) == 0 || len(h.Resources) > 32 {
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
	return nil
}

type Request struct {
	Identity         wf.EditIdentity `json:"identity"`
	ID               string          `json:"id"`
	ResourceID       string          `json:"resource_id"`
	Operation        string          `json:"operation"`
	Path             string          `json:"path"`
	Content          string          `json:"content,omitempty"`
	ExpectedRevision string          `json:"expected_revision,omitempty"`
	RequestID        string          `json:"request_id,omitempty"`
}
type Response struct {
	Code    string           `json:"code,omitempty"`
	ID      string           `json:"id"`
	Status  int              `json:"status"`
	Error   string           `json:"error,omitempty"`
	File    *wf.File         `json:"file,omitempty"`
	Entries []wf.Entry       `json:"entries,omitempty"`
	Receipt *wf.WriteReceipt `json:"receipt,omitempty"`
}
