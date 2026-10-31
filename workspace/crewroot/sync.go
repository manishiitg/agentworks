package crewroot

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SyncBinding is platform state, never a file supplied by the working tree.
// IDs on different servers are deliberately not treated as interchangeable.
type SyncBinding struct {
	PairID        string `json:"pair_id"`
	Side          string `json:"side"`
	Editing       string `json:"editing"`
	ConnectionID  string `json:"connection_id,omitempty"`
	PeerProjectID string `json:"peer_project_id"`
	PeerServer    string `json:"peer_server,omitempty"`
	UpdatedAt     string `json:"updated_at,omitempty"`
}

func syncPath(id string) (string, error) {
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, "/\\") {
		return "", fmt.Errorf("invalid Crew identity")
	}
	state, err := StateRoot()
	return filepath.Join(state, "crew-sync", id+".json"), err
}

func ReadSync(id string) (*SyncBinding, error) {
	p, err := syncPath(id)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var binding SyncBinding
	if json.Unmarshal(b, &binding) != nil || binding.PairID == "" || binding.PeerProjectID == "" ||
		(binding.Side != "local" && binding.Side != "server") || (binding.Editing != "local" && binding.Editing != "server") {
		return nil, fmt.Errorf("this Crew's sync settings are unavailable")
	}
	return &binding, nil
}

func WriteSync(id string, binding SyncBinding) error {
	p, err := syncPath(id)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return err
	}
	b, err := json.Marshal(binding)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(p), ".sync-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), p)
}

func (r Roots) SeparateFiles() bool { return r.Files != r.Resources }

// SourceLocked leaves the execution output area and platform records alone.
// This is an editing-location rule, not a new grant for Run callers.
func (r Roots) SourceLocked() (bool, error) {
	b, err := ReadSync(r.ID)
	return b != nil && b.Side != b.Editing, err
}

func CheckSyncWrite(docs, physical string) error {
	_, roots, found, err := Reverse(docs, physical)
	if err != nil || !found {
		return err
	}
	locked, err := roots.SourceLocked()
	if err != nil || !locked {
		return err
	}
	if Within(roots.Private, physical) || Within(filepath.Join(roots.Resources, "outputs"), physical) {
		return nil
	}
	return fmt.Errorf("project files are being edited on the other computer; use Work on this Mac or Work on server to bring editing here")
}

// SyncWriteGrants also handles physical aliases used by native CLIs. Dropping
// the broad grant matters: a narrower read grant cannot undo a parent write.
func SyncWriteGrants(docs string, writes []string) []string {
	var out []string
	for _, grant := range writes {
		physical := strings.TrimSuffix(grant, "/")
		var roots Roots
		var found bool
		var err error
		if filepath.IsAbs(physical) {
			_, roots, found, err = Reverse(docs, physical)
		} else {
			roots, found, err = Lookup(docs, physical)
			if found && err == nil {
				physical, _, err = Resolve(docs, physical)
			}
		}
		if found && err != nil {
			continue
		}
		if found {
			locked, e := roots.SourceLocked()
			if e != nil {
				continue
			}
			if locked && !Within(roots.Private, physical) && !Within(filepath.Join(roots.Resources, "outputs"), physical) {
				if physical == roots.Resources {
					if filepath.IsAbs(grant) {
						out = append(out, filepath.Join(roots.Resources, "outputs"))
					} else {
						out = append(out, roots.Virtual+"/outputs/")
					}
				}
				continue
			}
		}
		out = append(out, grant)
	}
	return out
}
