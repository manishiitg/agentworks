package crewroot

import (
	"encoding/json"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/workspace/publication"
	"github.com/manishiitg/coding-agent-loop/workspace/workspaceref"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
)

// SyncBinding is platform state, never a file supplied by the working tree.
// IDs on different servers are deliberately not treated as interchangeable.
type SyncBinding struct {
	SchedulesPaused bool   `json:"schedules_paused,omitempty"`
	ArchiveOnly     bool   `json:"archive_only,omitempty"`
	Mode            string `json:"mode,omitempty"`
	SplitFiles      bool   `json:"split_files,omitempty"`
	PairID          string `json:"pair_id"`
	Side            string `json:"side"`
	Editing         string `json:"editing"`
	ConnectionID    string `json:"connection_id,omitempty"`
	PeerProjectID   string `json:"peer_project_id"`
	PeerServer      string `json:"peer_server,omitempty"`
	UpdatedAt       string `json:"updated_at,omitempty"`
	PendingAction   string `json:"pending_action,omitempty"`
	LastTransfer    string `json:"last_transfer,omitempty"`
	MacName         string `json:"mac_name,omitempty"`
	MacID           string `json:"mac_id,omitempty"` // the Mac's saved sign-in to this server, so the website groups a Mac's Crews
	LastCompletedAt string `json:"last_completed_at,omitempty"`
}

var syncSettingsMu sync.Mutex

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
	syncSettingsMu.Lock()
	defer syncSettingsMu.Unlock()
	return writeSync(id, binding)
}

// ReplaceSync writes the binding as given, for the one-off move of older pairs to relay pairs.
func ReplaceSync(id string, binding SyncBinding) error {
	syncSettingsMu.Lock()
	defer syncSettingsMu.Unlock()
	return writeSync(id, binding)
}

func writeSync(id string, binding SyncBinding) error {
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
	if Within(roots.Private, physical) || roots.syncRuntimePath(physical) || roots.teamCopyPath(physical) {
		return nil
	}
	return fmt.Errorf("this Crew is moving online; open its online copy when the move finishes")
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
			if locked && !Within(roots.Private, physical) && !roots.syncRuntimePath(physical) && !roots.teamCopyPath(physical) {
				if physical == roots.Resources {
					for _, runtime := range SyncRuntimeDirectories {
						ensureRuntimeDirectory(filepath.Join(roots.Resources, runtime))
						if filepath.IsAbs(grant) {
							out = append(out, filepath.Join(roots.Resources, runtime))
						} else {
							out = append(out, roots.Virtual+"/"+runtime+"/")
						}
					}
				}
				continue
			}
		}
		out = append(out, grant)
	}
	return out
}

func SyncContentAllowed(key string, directory bool) bool {
	area, rel, ok := strings.Cut(key, "/")
	if !ok || (area != "files" && area != "resources" && area != "knowledge") {
		return false
	}
	if _, err := publication.CleanPath(rel); err != nil {
		return false
	}
	if area == "knowledge" {
		return (rel == "knowledgebase" || strings.HasPrefix(rel, "knowledgebase/")) && !publication.ExcludedFromSync(rel, directory)
	}
	// Team members' working copies are their own, never shared or synced (crew_team_member_copy.go).
	if first := strings.SplitN(rel, "/", 2)[0]; area != "knowledge" && first == TeamCopiesFolder {
		return false
	}
	if area == "resources" {
		first := strings.SplitN(rel, "/", 2)[0]
		switch first {
		case "_files", "tool_output_folder", ".calls", "runs", "planning", "knowledgebase", "conversation", "costs", "human_inputs",
			"product.json", "workflow.json", "functions.json", "schedules.json", "webhooks.json", "product-schedules.json", "schedule-runs.json":
			return false
		}
		if rel == "triggers/deliveries" || strings.HasPrefix(rel, "triggers/deliveries/") || rel == "db/db.sqlite" || strings.HasPrefix(rel, "db/db.sqlite-") {
			return false
		}
	}
	// Git's ignore/attribute files are ordinary project content, never .git state.
	if path.Base(rel) == ".gitignore" || path.Base(rel) == ".gitattributes" {
		return true
	}
	return !publication.ExcludedFromSync(rel, directory)
}

// TeamCopiesFolder holds each Team member's working copy of a Crew on the server, at team-copies/<member>/.
const TeamCopiesFolder = "team-copies"

// TeamCopyHidden: a Files listing never shows team-copies/; a person sees only their own copy, when the listing is
// opened inside it (their Files root). entry and listing are workspace paths, user the caller's id.
func TeamCopyHidden(entry, listing, user string) bool {
	parts := strings.Split(strings.Trim(filepath.ToSlash(entry), "/"), "/")
	at := 2
	if len(parts) > 2 && parts[2] == FilesSegment {
		at = 3
	}
	if len(parts) <= at || !strings.EqualFold(parts[0], "Crew") || parts[at] != TeamCopiesFolder {
		return false
	}
	if user == "" {
		return true
	}
	// The folder is named by the path-safe form of the id (crewTeamMemberCopyRoot), so the match uses it too.
	own := strings.Join(parts[:at+1], "/") + "/" + workspaceref.SanitizeUserID(user)
	inside := func(p string) bool {
		p = strings.Trim(filepath.ToSlash(p), "/")
		return p == own || strings.HasPrefix(p, own+"/")
	}
	return !inside(entry) || !inside(listing)
}

// TeamSetupFile is the Team's copy of the Crew setup (crew_team_setup.go) at the top of the Crew and of each member's
// copy. Team sync keeps it; the setup is edited in its normal places, so a Files listing never shows it.
const TeamSetupFile = "team-setup.json"

// TeamSetupHidden: entry (a workspace path) is the Team setup file at a Crew's top or at the top of a member's copy.
func TeamSetupHidden(entry string) bool {
	parts := strings.Split(strings.Trim(filepath.ToSlash(entry), "/"), "/")
	if len(parts) < 3 || !strings.EqualFold(parts[0], "Crew") || parts[len(parts)-1] != TeamSetupFile {
		return false
	}
	rest := parts[2:]
	if rest[0] == FilesSegment {
		rest = rest[1:]
	}
	return len(rest) == 1 || len(rest) == 3 && rest[0] == TeamCopiesFolder
}

// ensureRuntimeDirectory makes a runtime folder before it is granted: a relay copy of a Mac Crew has never run here, so
// its db/, assets/… did not exist, and the slot launcher made each missing write grant as an empty file, which then
// showed as unopenable files in Files (Showcase, 5 Nov). An empty file left that way becomes the folder again.
func ensureRuntimeDirectory(dir string) {
	if info, err := os.Lstat(dir); err == nil && info.Mode().IsRegular() && info.Size() == 0 {
		_ = os.Remove(dir)
	}
	_ = os.Mkdir(dir, 0o770)
}

var SyncRuntimeDirectories = []string{"db", "assets", "reports", "tool_output_folder", ".calls", "shared", "outputs"}

// teamCopyPath: members' copies (team-copies/<member>/) are synced by Team sync, never by crew sync, so the Crew's
// editing location does not lock them; a member writes their own copy while the owner's Mac edits the Crew.
func (r Roots) teamCopyPath(physical string) bool {
	return r.Files != "" && Within(filepath.Join(r.Files, TeamCopiesFolder), physical)
}

func (r Roots) syncRuntimePath(physical string) bool {
	for _, name := range SyncRuntimeDirectories {
		if Within(filepath.Join(r.Resources, name), physical) {
			return true
		}
	}
	return false
}
