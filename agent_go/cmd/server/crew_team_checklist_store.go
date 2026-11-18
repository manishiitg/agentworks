package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
)

// PLAT-746-1045: instructions remain ordinary Team Markdown files, with the
// existing file history/sync. This record stores only their reference and the
// checklist. It does not replace the Crew's always-on team-setup.json rules.
const crewChecklistFile = "checklists.json"
const crewChecklistMaxBytes = 4 << 20

type crewChecklistInstruction struct {
	Path     string `json:"path"`
	Revision uint64 `json:"revision"`
}
type crewChecklistItem struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// crewChecklistKey is one thing a Todo needs a person to have: a key (Name), a personal sign-in (Provider), or, with
// Kind "mcp" or "skill", a tool or skill (Name) installed in that person's own app. Names only, never values. Keys and
// sign-ins are read from the Vault; an MCP or skill lives in the person's own app, so their own agent reports it.
type crewChecklistKey struct {
	Name     string `json:"name"`
	Provider string `json:"provider,omitempty"`
	Kind     string `json:"kind,omitempty"`
}

// crewChecklistInstallable is a Kind that the person's own agent reports (setup_checklist installed).
func crewChecklistInstallable(key crewChecklistKey) bool {
	return key.Kind == "mcp" || key.Kind == "skill"
}

type crewChecklistProgress struct {
	SetupRevision       uint64          `json:"setup_revision"`
	InstructionRevision uint64          `json:"instruction_revision"`
	Done                map[string]bool `json:"done"`
	// Installed is what this person's own agent reported for the MCP and skill names the Todo lists (true: installed).
	Installed map[string]bool `json:"installed,omitempty"`
	AgentID   string          `json:"agent_id"`
	CheckedAt time.Time       `json:"checked_at"`
}
type crewChecklist struct {
	ID          string                   `json:"id"`
	Title       string                   `json:"title"`
	Revision    uint64                   `json:"revision"`
	Instruction crewChecklistInstruction `json:"instruction"`
	Items       []crewChecklistItem      `json:"items"`
	Keys        []crewChecklistKey       `json:"keys,omitempty"`
	// Assignees are the Team people this is for (owner, 16 Nov: a Todo can be assigned to people); none means
	// everyone. Only they see it as theirs, may Apply it or have progress recorded.
	Assignees []string                         `json:"assignees,omitempty"`
	SharedBy  string                           `json:"shared_by"`
	UpdatedBy string                           `json:"updated_by"`
	UpdatedAt time.Time                        `json:"updated_at"`
	Progress  map[string]crewChecklistProgress `json:"progress,omitempty"`
}
type crewChecklistStore struct {
	Setups map[string]crewChecklist `json:"setups"`
}

// Constructed only after the native session or the MCP attachment is verified.
// Neither the screen nor a JSON argument can supply these unexported fields.
type crewChecklistActor struct{ person, agent string }

func crewChecklistTeam(claims *UserClaims, project string, write bool) (crewTeam, error) {
	if !crewTeamClaims(claims, project, write) {
		return crewTeam{}, fmt.Errorf("Use your own account to open Team setups")
	}
	team, err := readCrewTeam(project)
	if err != nil || !crewTeamAccountActive(claims.UserID) || (!crewTeamReachOpen(team, claims.UserID) && !crewTeamOwnerReach(team, claims.UserID) && !(team.On && crewTeamOwnerReach(team, team.OwnerID) && crewTeamHomeOwnsSetup(team, claims.UserID))) {
		return crewTeam{}, fmt.Errorf("Join this Team to open its setups")
	}
	return team, nil
}

func readCrewChecklists(project string) (crewChecklistStore, error) {
	out := crewChecklistStore{Setups: map[string]crewChecklist{}}
	dir, err := crewTeamDir(project)
	if err != nil {
		return out, err
	}
	file, err := os.Open(filepath.Join(dir, crewChecklistFile))
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, crewChecklistMaxBytes+1))
	if err != nil {
		return out, err
	}
	if len(raw) > crewChecklistMaxBytes {
		return out, fmt.Errorf("Team setups are too large")
	}
	if err = json.Unmarshal(raw, &out); err != nil {
		return out, err
	}
	if out.Setups == nil {
		out.Setups = map[string]crewChecklist{}
	}
	return out, nil
}
func saveCrewChecklists(project string, data crewChecklistStore) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if len(raw) > crewChecklistMaxBytes {
		return fmt.Errorf("Team setups are too large")
	}
	dir, err := crewTeamDir(project)
	if err != nil {
		return err
	}
	return fsutil.WriteJSONAtomic(filepath.Join(dir, crewChecklistFile), data, 0600)
}

// Current shared-file identity, never a second instruction body or an arbitrary
// filesystem path. A removed folder or narrowed connection takes effect now.
func crewChecklistInstructionVersion(claims *UserClaims, team crewTeam, rel string, state crewTeamFileState) (uint64, error) {
	if rel == "" || len(rel) > 1024 || path.Clean(rel) != rel || strings.HasPrefix(rel, "/") || strings.HasPrefix(rel, "../") || strings.ContainsAny(rel, "\\\x00") || !strings.EqualFold(path.Ext(rel), ".md") || !team.Folder.holds(rel) || !crewTeamAllowed(rel, state.onPurpose()) {
		return 0, fmt.Errorf("Choose a Markdown instruction file in this Team's shared files")
	}
	if claims.AccessToken != nil && claims.AccessToken.FileGuard != nil && !claims.AccessToken.FileGuard.Allows(rel, false) {
		return 0, fmt.Errorf("This connection cannot read that instruction file")
	}
	version, found := state.Versions[rel]
	if !found || version.File == nil {
		return 0, fmt.Errorf("Save the instructions in Team files first")
	}
	if version.File.Size > 64<<10 {
		return 0, fmt.Errorf("Use an instruction document smaller than 64 KB")
	}
	return version.Revision, nil
}

func crewChecklistName(value string, limit int) bool {
	return value != "" && utf8.RuneCountInString(value) <= limit && !strings.ContainsAny(value, "\r\n\x00")
}

// crewChecklistFor reports whether a Todo is for this person: everyone's when it names nobody.
func crewChecklistFor(setup crewChecklist, person string) bool {
	return len(setup.Assignees) == 0 || slices.Contains(setup.Assignees, person)
}

func validateCrewChecklist(input *crewChecklist, previous *crewChecklist, team crewTeam) error {
	input.Title = strings.TrimSpace(input.Title)
	if !crewChecklistName(input.Title, 160) || len(input.Items) == 0 || len(input.Items) > 32 || len(input.Keys) > 32 {
		return fmt.Errorf("Give a short title and 1 to 32 checklist items")
	}
	existing := map[string]bool{}
	if previous != nil {
		for _, item := range previous.Items {
			existing[item.ID] = true
		}
	}
	seen := map[string]bool{}
	for i := range input.Items {
		item := &input.Items[i]
		item.Label = strings.TrimSpace(item.Label)
		if !crewChecklistName(item.Label, 240) {
			return fmt.Errorf("Use short checklist items")
		}
		if item.ID == "" {
			item.ID = uuid.NewString()
		} else if !existing[item.ID] {
			return fmt.Errorf("Use this setup's current checklist item IDs")
		}
		if seen[item.ID] {
			return fmt.Errorf("Checklist items must have different IDs")
		}
		seen[item.ID] = true
	}
	seen = map[string]bool{}
	if len(input.Assignees) > 64 {
		return fmt.Errorf("Assign a setup step to up to 64 people, or leave it for everyone")
	}
	for _, person := range input.Assignees {
		if seen[person] || !team.member(person) {
			return fmt.Errorf("Assign a setup step only to people in this Team (see people), once each")
		}
		seen[person] = true
	}
	seen = map[string]bool{}
	installable := map[string]bool{}
	for _, key := range input.Keys {
		badKind := key.Kind != "" && key.Kind != "mcp" && key.Kind != "skill"
		if badKind || (key.Name == "" && key.Provider == "") || (key.Name != "" && !crewChecklistName(key.Name, 128)) || len(key.Provider) > 128 || strings.ContainsAny(key.Name, " =:/\\\t") || strings.ContainsAny(key.Provider, "\r\n\x00") || seen[key.Name+"\x00"+key.Provider+"\x00"+key.Kind] || (crewChecklistInstallable(key) && (key.Name == "" || key.Provider != "" || installable[key.Name])) {
			return fmt.Errorf("List required key names, sign-in providers, MCP names and skill names only, never values")
		}
		seen[key.Name+"\x00"+key.Provider+"\x00"+key.Kind] = true
		if crewChecklistInstallable(key) {
			installable[key.Name] = true
		}
	}
	return nil
}

var errCrewChecklistChanged = errors.New("This Team setup changed; open it again before continuing")

// The caller supplies shared-file references, not the text or secret material.
// The instruction ledger is read outside the Team-record mutex: never acquire
// file/history locks inside a Team-record write, or emit events while locked.
func saveCrewChecklist(claims *UserClaims, project string, input crewChecklist, expected uint64) (out crewChecklist, err error) {
	person := ""
	if claims != nil {
		person = claims.UserID
	}
	log.Printf("[TEAM_SETUP] project=%q setup=%q person=%q operation=share outcome=started", project, input.ID, person)
	defer func() { checklistOutcome(project, input.ID, person, "share", err) }()
	team, err := crewChecklistTeam(claims, project, true)
	if err != nil {
		return crewChecklist{}, err
	}
	state, err := readCrewTeamCurrentState(project)
	if err != nil {
		return crewChecklist{}, err
	}
	revision, err := crewChecklistInstructionVersion(claims, team, input.Instruction.Path, state)
	if err != nil {
		return crewChecklist{}, err
	}
	if revision != input.Instruction.Revision {
		return crewChecklist{}, errCrewChecklistChanged
	}
	dir, err := crewTeamDir(project)
	if err != nil {
		return crewChecklist{}, err
	}
	mu := crewTeamRecordMutex(dir)
	mu.Lock()
	defer mu.Unlock()
	fresh, err := crewChecklistTeam(claims, project, true)
	if err != nil {
		return crewChecklist{}, err
	}
	if fresh.Folder.ID != team.Folder.ID {
		return crewChecklist{}, errCrewChecklistChanged
	}
	if _, err = crewChecklistInstructionVersion(claims, fresh, input.Instruction.Path, state); err != nil {
		return crewChecklist{}, err
	}
	data, err := readCrewChecklists(project)
	if err != nil {
		return crewChecklist{}, err
	}
	var previous *crewChecklist
	if input.ID == "" {
		if expected != 0 || len(data.Setups) >= 100 {
			return crewChecklist{}, fmt.Errorf("Choose an existing setup to edit, or share fewer setups")
		}
		input.ID = uuid.NewString()
		input.SharedBy = claims.UserID
	} else {
		old, ok := data.Setups[input.ID]
		if !ok || old.Revision != expected {
			return crewChecklist{}, errCrewChecklistChanged
		}
		previous = &old
		input.SharedBy = old.SharedBy
	}
	if err = validateCrewChecklist(&input, previous, fresh); err != nil {
		return crewChecklist{}, err
	}
	input.Revision = expected + 1
	input.UpdatedBy = claims.UserID
	input.UpdatedAt = time.Now().UTC()
	// An edit requires each person's agent to re-check. Never carry completion
	// into different instructions/items, or accept client-supplied progress.
	input.Progress = map[string]crewChecklistProgress{}
	data.Setups[input.ID] = input
	if err = saveCrewChecklists(project, data); err != nil {
		return crewChecklist{}, err
	}
	return input, nil
}

// removeCrewChecklist takes a Todo off the Team (owner, 17 Nov: retire a Todo nobody should do any more). Only the
// Crew's owner or the person who shared it may remove it; everyone's progress on it goes with it.
func removeCrewChecklist(claims *UserClaims, project string, team crewTeam, id string, expected uint64) (err error) {
	log.Printf("[TEAM_SETUP] project=%q setup=%q person=%q operation=remove outcome=started", project, id, claims.UserID)
	defer func() { checklistOutcome(project, id, claims.UserID, "remove", err) }()
	dir, err := crewTeamDir(project)
	if err != nil {
		return err
	}
	mu := crewTeamRecordMutex(dir)
	mu.Lock()
	defer mu.Unlock()
	data, err := readCrewChecklists(project)
	if err != nil {
		return err
	}
	old, ok := data.Setups[id]
	if !ok || old.Revision != expected {
		return errCrewChecklistChanged
	}
	if claims.UserID != team.OwnerID && claims.UserID != old.SharedBy {
		return fmt.Errorf("Only the Team owner or the person who shared this setup can remove it")
	}
	delete(data.Setups, id)
	return saveCrewChecklists(project, data)
}

func writeCrewChecklistProgress(claims *UserClaims, project, id string, actor crewChecklistActor, expected, instructionRevision uint64, done, installed map[string]bool) (out crewChecklistProgress, err error) {
	log.Printf("[TEAM_SETUP] project=%q setup=%q person=%q operation=checklist outcome=started", project, id, actor.person)
	defer func() { checklistOutcome(project, id, actor.person, "checklist", err) }()
	team, err := crewChecklistTeam(claims, project, true)
	if err != nil {
		return crewChecklistProgress{}, err
	}
	if actor.person != claims.UserID || actor.agent == "" || claims.UserID == "team-"+project {
		return crewChecklistProgress{}, fmt.Errorf("Update your checklist from your own agent")
	}
	state, err := readCrewTeamCurrentState(project)
	if err != nil {
		return crewChecklistProgress{}, err
	}
	dir, err := crewTeamDir(project)
	if err != nil {
		return crewChecklistProgress{}, err
	}
	mu := crewTeamRecordMutex(dir)
	mu.Lock()
	defer mu.Unlock()
	fresh, err := crewChecklistTeam(claims, project, true)
	if err != nil {
		return crewChecklistProgress{}, err
	}
	if fresh.Folder.ID != team.Folder.ID {
		return crewChecklistProgress{}, errCrewChecklistChanged
	}
	team = fresh
	data, err := readCrewChecklists(project)
	if err != nil {
		return crewChecklistProgress{}, err
	}
	setup, ok := data.Setups[id]
	if !ok || setup.Revision != expected {
		return crewChecklistProgress{}, errCrewChecklistChanged
	}
	if !crewChecklistFor(setup, claims.UserID) {
		return crewChecklistProgress{}, fmt.Errorf("This setup step is for other people, so there is nothing for you to check")
	}
	current, err := crewChecklistInstructionVersion(claims, team, setup.Instruction.Path, state)
	if err != nil {
		return crewChecklistProgress{}, err
	}
	if current != instructionRevision {
		return crewChecklistProgress{}, errCrewChecklistChanged
	}
	allowed := map[string]bool{}
	for _, item := range setup.Items {
		allowed[item.ID] = true
	}
	for item := range done {
		if !allowed[item] {
			return crewChecklistProgress{}, fmt.Errorf("Choose current checklist items")
		}
	}
	reportable := map[string]bool{}
	for _, key := range setup.Keys {
		if crewChecklistInstallable(key) {
			reportable[key.Name] = true
		}
	}
	for name := range installed {
		if !reportable[name] {
			return crewChecklistProgress{}, fmt.Errorf("Report only the MCPs and skills this setup step lists")
		}
	}
	progress := crewChecklistProgress{SetupRevision: expected, InstructionRevision: current, Done: map[string]bool{}, Installed: map[string]bool{}, AgentID: actor.agent, CheckedAt: time.Now().UTC()}
	if old, ok := setup.Progress[claims.UserID]; ok && old.SetupRevision == expected && old.InstructionRevision == current {
		for item, value := range old.Done {
			if allowed[item] {
				progress.Done[item] = value
			}
		}
		for name, value := range old.Installed {
			if reportable[name] {
				progress.Installed[name] = value
			}
		}
	}
	for item, value := range done {
		progress.Done[item] = value
	}
	for name, value := range installed {
		progress.Installed[name] = value
	}
	if setup.Progress == nil {
		setup.Progress = map[string]crewChecklistProgress{}
	}
	setup.Progress[claims.UserID] = progress
	data.Setups[id] = setup
	if err = saveCrewChecklists(project, data); err != nil {
		return crewChecklistProgress{}, err
	}
	return progress, nil
}

func crewChecklistDone(setup crewChecklist, progress crewChecklistProgress, current uint64) int {
	if current == 0 || progress.SetupRevision != setup.Revision || progress.InstructionRevision != current {
		return 0
	}
	count := 0
	for _, item := range setup.Items {
		if progress.Done[item.ID] {
			count++
		}
	}
	return count
}
func sortedCrewChecklists(data crewChecklistStore) []crewChecklist {
	out := make([]crewChecklist, 0, len(data.Setups))
	for _, setup := range data.Setups {
		out = append(out, setup)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Title == out[j].Title {
			return out[i].ID < out[j].ID
		}
		return out[i].Title < out[j].Title
	})
	return out
}

func checklistOutcome(project, id, person, operation string, err error) {
	outcome, reason := "done", ""
	if err != nil {
		outcome = "failed"
		reason = "refused_or_unavailable"
	}
	log.Printf("[TEAM_SETUP] project=%q setup=%q person=%q operation=%s outcome=%s reason=%s", project, id, person, operation, outcome, reason)
}
