package sparkquillproduct

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"strings"
	"sync"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
)

// FamilyFile is the family's saved state, relative to the product workspace
// root (Chats/SparkQuill). It is the platform home of what the standalone
// app keeps in ~/.sunlit-learning/state.json.
const FamilyFile = "family.json"

// Child is the one child on this account.
type Child struct {
	Name     string `json:"name"`
	Grade    string `json:"grade"`
	Board    string `json:"board"`
	Language string `json:"language,omitempty"`
}

// FamilyState is family.json.
type FamilyState struct {
	// Engine is the family's chosen coding-agent runtime: one of the parent
	// profile's provider_options ids, written by onboarding/Settings and sent
	// as `engine` on every turn. It must round-trip through every Go-side
	// rewrite of family.json (set_child_profile etc.) or the choice is lost.
	Engine string `json:"engine,omitempty"`
	// Model is the legacy shared model. Settings now save a model and effort
	// for each role, with one platform account shared by the family.
	Model                 string   `json:"model,omitempty"`
	ParentModel           string   `json:"parent_model,omitempty"`
	ChildModel            string   `json:"child_model,omitempty"`
	ConnectionID          string   `json:"connection_id,omitempty"`
	ParentReasoningEffort string   `json:"parent_reasoning_effort,omitempty"`
	ChildReasoningEffort  string   `json:"child_reasoning_effort,omitempty"`
	Child                 *Child   `json:"child,omitempty"`
	ParentLabel           string   `json:"parent_label,omitempty"`
	PinHash               string   `json:"pin_hash,omitempty"`
	WatchSites            []string `json:"watch_sites,omitempty"`
}

// The family workspace layout. Tools, the inbox note and the prompt all read
// these names from here, so the prompt cannot describe folders that the code
// no longer uses.
const (
	MaterialsFolder = "materials"
	MemoryFolder    = "memory"
	ReportsFolder   = "reports"
	ArchiveFolder   = "archive"
	InboxFolder     = "inbox"
	// CurrentActivityFile points at the activity the parent last gave the
	// child ({"dir": "activities/<slug>"}); the app writes it on handoff.
	CurrentActivityFile = "current-activity.json"
)

// keySweeps remembers the family roots whose activity folders were already
// swept for stray answer keys in this process.
var keySweeps sync.Map

// WorkspaceLayout renders the "YOUR WORKSPACE" bullets of the parent prompt.
func WorkspaceLayout() string {
	return strings.Join([]string{
		"- " + ActivitiesFolder + "/<yyyy-mm-dd>-<slug>/ — every piece of child-facing content lives in its own activity folder: the content files, its activity.json manifest, and (once she starts) her own conversation and attempts/. She can read everything in it.",
		"- " + KeysFolder + "/<activity-slug>-KEY.md — the parent-only answer keys, one per activity, outside her reach. Never write a key anywhere else.",
		"- " + MaterialsFolder + "/<subject>/<topic>/ — school material the family uploaded; each file has a .meta.json alongside whose extracted_text already holds the full content.",
		"- " + MemoryFolder + "/child-profile.json — the child's grade/age/board, set via set_child_profile. Never write it by hand.",
		"- " + MemoryFolder + "/browser-notes.md — your own short cheat sheet for sites you browse with agent_browser; read it before a familiar site, keep it current, edit in place.",
		"- " + ReportsFolder + "/progress.html — the one living progress page: what she has, how she is doing, what to do next.",
		"- " + ArchiveFolder + "/ — activities the parent asked you to put away: move the whole folder here when they say so, never on your own. Still real evidence for reports; never handed to her again. Nothing is ever deleted unless the parent explicitly asks.",
		"- " + InboxFolder + "/ — uploads waiting to be filed (you are told above when there are any).",
		"- ../../chat_history/ — the platform's log of every conversation: your earlier chats with the parent and each activity's conversation with the tutor. Read-only; skills/recall-conversations/SKILL.md says how to find the right one.",
	}, "\n") + "\n"
}

// ParentPromptVariables computes the parent prompt's Product variables:
// who the child is, and the nudges for whatever the family has not told
// Quill yet.
func ParentPromptVariables(s FamilyState) map[string]string {
	name := "your child"
	who := name
	if s.Child != nil && strings.TrimSpace(s.Child.Name) != "" {
		name = strings.TrimSpace(s.Child.Name)
		who = name
	}
	var missing []string
	if s.Child == nil || strings.TrimSpace(s.Child.Name) == "" {
		missing = append(missing, "name")
	}
	if s.Child != nil && strings.TrimSpace(s.Child.Grade) != "" {
		who += ", Grade " + strings.TrimSpace(s.Child.Grade)
	} else {
		missing = append(missing, "grade")
	}
	if s.Child != nil && strings.TrimSpace(s.Child.Board) != "" {
		who += " (" + strings.TrimSpace(s.Child.Board) + ")"
	} else {
		missing = append(missing, "board (e.g. CBSE, ICSE, State Board)")
	}
	vars := map[string]string{
		"CHILD_NAME":         name,
		"CHILD_WHO":          who,
		"CHILD_INFO_NUDGE":   "",
		"PARENT_LABEL_NUDGE": "",
		"CONNECTOR_NOTE":     "",
		"INBOX_NOTE":         "",
		"WORKSPACE_LAYOUT":   WorkspaceLayout(),
	}
	if len(missing) > 0 {
		vars["CHILD_INFO_NUDGE"] = "IMPORTANT — you do not yet know the child's " + strings.Join(missing, ", ") +
			". Early in the conversation, warmly ask the parent for these, then save them with the set_child_profile tool. You need them to tailor material to the right grade and board.\n"
	}
	if strings.TrimSpace(s.ParentLabel) == "" {
		vars["PARENT_LABEL_NUDGE"] = "IMPORTANT — you don't yet know what to call the parent when you talk ABOUT them to " + name +
			" (\"your mom\" vs \"your dad\" vs a name). Early on, warmly ask once and save the answer with set_parent_label. Don't block other work on this.\n"
	}
	if sites := cleanSites(s.WatchSites); len(sites) > 0 {
		vars["CONNECTOR_NOTE"] = "The parent has asked you to keep an eye on these website(s): " + strings.Join(sites, ", ") +
			". Whenever they ask ANYTHING about them, call agent_browser(command=\"status\") FIRST, right then, before replying; never report \"no access\" without having just tried. Check memory/browser-notes.md before navigating and save what you learn there.\n"
	}
	return vars
}

// ChildPromptVariables computes the child prompt's Product variables for
// one activity.
func ChildPromptVariables(s FamilyState, activityDir string, manifest *ActivityManifest) map[string]string {
	name := "there"
	if s.Child != nil && strings.TrimSpace(s.Child.Name) != "" {
		name = strings.TrimSpace(s.Child.Name)
	}
	parent := strings.TrimSpace(s.ParentLabel)
	if parent == "" {
		parent = "parent"
	}
	vars := map[string]string{
		"CHILD_NAME":           name,
		"PARENT_LABEL":         parent,
		"GRADE_SUFFIX":         "",
		"GRADE_FOR_FORMATTING": "a school student",
		"ACTIVITY_DIR":         strings.Trim(strings.TrimSpace(activityDir), "/"),
		"ACTIVITY_TITLE":       "this activity",
		"ACTIVITY_GOAL":        "(none was written — help her with whatever the pages hold)",
		"ACTIVITY_PERSONA":     "warm, patient study buddy",
		"ACTIVITY_ITEMS":       "none — this is an instruction-only activity: the goal is the whole description, so make up each question yourself, one at a time, adapting to how she does",
	}
	if manifest != nil {
		if t := strings.TrimSpace(manifest.Title); t != "" {
			vars["ACTIVITY_TITLE"] = "\"" + t + "\""
		}
		if g := strings.TrimSpace(manifest.Goal); g != "" {
			vars["ACTIVITY_GOAL"] = g
		}
		if p := strings.TrimSpace(manifest.Persona); p != "" {
			vars["ACTIVITY_PERSONA"] = p
		}
		if len(manifest.Items) > 0 {
			vars["ACTIVITY_ITEMS"] = strings.Join(manifest.Items, ", ") + " (in this order; jump to the one she asks for)"
		}
	}
	if s.Child != nil && strings.TrimSpace(s.Child.Grade) != "" {
		grade := strings.TrimSpace(s.Child.Grade)
		vars["GRADE_SUFFIX"] = " (Grade " + grade + ")"
		vars["GRADE_FOR_FORMATTING"] = "in Grade " + grade
	}
	return vars
}

func cleanSites(sites []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, u := range sites {
		u = strings.TrimSpace(u)
		if u == "" || seen[u] {
			continue
		}
		seen[u] = true
		out = append(out, u)
	}
	return out
}

// RegisterAgentProfileRuntime wires both profiles' prompt variables to the
// family's saved state in the user's product workspace, registers the
// family tools, and prepares the parent workspace's presentation table.
func RegisterAgentProfileRuntime(registry *agentprofiles.Registry, workspaceAPIURL string) error {
	factories := map[string]agentprofiles.ToolFactory{
		"sparkquill.set-child-profile":        setChildProfileFactory(workspaceAPIURL),
		"sparkquill.set-parent-label":         setParentLabelFactory(workspaceAPIURL),
		"sparkquill.pin-page":                 pinPageFactory(workspaceAPIURL),
		"sparkquill.unpin-page":               unpinPageFactory(workspaceAPIURL),
		"sparkquill.create-learning-activity": createLearningActivityFactory(workspaceAPIURL),
		"sparkquill.open-file":                openFileFactory(workspaceAPIURL, false),
		"sparkquill.open-activity-file":       openFileFactory(workspaceAPIURL, true),
		"sparkquill.open-activity":            openActivityFactory(workspaceAPIURL),
		"sparkquill.celebrate":                celebrateFactory(),
		"sparkquill.show-scene":               showSceneFactory(),
	}
	for id, factory := range factories {
		if err := registry.RegisterToolFactory(id, factory); err != nil {
			return err
		}
	}
	if err := registry.RegisterInitializer(ParentProfileID, func(ctx context.Context, rt agentprofiles.RuntimeContext) error {
		// The session id is what authorizes workspace execution (DB init is
		// one), the same way every tool call carries it.
		client := workspace.NewClient(workspaceAPIURL, workspace.WithUserID(rt.UserID),
			workspace.WithExtraEnv(map[string]string{"MCP_SESSION_ID": rt.SessionID}))
		_, err := client.InitializeWorkflowDB(ctx, workspace.InitializeWorkflowDBParams{
			DBPath:     path.Join(rt.WorkspacePath, "db/db.sqlite"),
			Migrations: []string{presentationsMigration, presentationsKindIndexMigration},
		})
		return err
	}); err != nil {
		return err
	}
	loader := familyLoader{workspaceAPIURL: workspaceAPIURL}
	if err := registry.RegisterPromptVariables(ParentProfileID, func(ctx context.Context, rt agentprofiles.RuntimeContext) (map[string]string, error) {
		familyRoot := runtimeRoot(rt.UserID, rt.WorkspacePath)
		state, err := loader.load(ctx, rt.UserID, familyRoot)
		if err != nil {
			return nil, err
		}
		vars := ParentPromptVariables(state)
		vars["INBOX_NOTE"] = InboxNote(loader.listInbox(ctx, rt.UserID, familyRoot))
		// Keys written into activity folders before keys/ existed are the
		// child's to read; move them once per process, before this turn.
		if _, done := keySweeps.LoadOrStore(familyRoot, true); !done && strings.TrimSpace(workspaceAPIURL) != "" {
			newFamilyWorkspace(workspaceAPIURL, agentprofiles.ToolRuntimeContext{UserID: rt.UserID, SessionID: rt.SessionID}, familyRoot).sweepAnswerKeys(ctx)
		}
		return vars, nil
	}); err != nil {
		return err
	}
	// On WhatsApp the parent's own chat reaches Quill; "@child" talks as the
	// tutor in the activity the child currently has, "@parent" comes back.
	if err := registry.RegisterChannelRouter(ParentProfileID, func(ctx context.Context, userID, token string) (agentprofiles.ChannelProfileRoute, bool, error) {
		parent, err := registry.Resolve(ParentProfileID, 0, userID)
		if err != nil {
			return agentprofiles.ChannelProfileRoute{}, false, err
		}
		root := strings.Trim(strings.TrimSpace(parent.Runtime.Workspace.Root), "/")
		familyRoot := runtimeRoot(userID, root)
		state, err := loader.load(ctx, userID, familyRoot)
		if err != nil {
			return agentprofiles.ChannelProfileRoute{}, false, err
		}
		var pointer struct {
			Dir string `json:"dir"`
		}
		if raw := loader.read(ctx, userID, path.Join(familyRoot, CurrentActivityFile)); strings.TrimSpace(raw) != "" {
			_ = json.Unmarshal([]byte(raw), &pointer)
		}
		return ChannelRouteFor(token, state, root, pointer.Dir)
	}); err != nil {
		return err
	}
	return registry.RegisterPromptVariables(ChildProfileID, func(ctx context.Context, rt agentprofiles.RuntimeContext) (map[string]string, error) {
		// The activity folder is the child conversation's workspace; the
		// family root is its projects root's parent.
		activityRoot := runtimeRoot(rt.UserID, rt.WorkspacePath)
		familyRoot := familyRootFromActivity(activityRoot)
		state, err := loader.load(ctx, rt.UserID, familyRoot)
		if err != nil {
			return nil, err
		}
		// The activity's own facts (goal, pages, persona) go straight into the
		// prompt: they steer the whole conversation, so they must not depend
		// on the model remembering to read the manifest first.
		var manifest *ActivityManifest
		if raw := loader.read(ctx, rt.UserID, path.Join(activityRoot, "activity.json")); strings.TrimSpace(raw) != "" {
			var m ActivityManifest
			if json.Unmarshal([]byte(raw), &m) == nil {
				manifest = &m
			}
		}
		return ChildPromptVariables(state, activityRoot, manifest), nil
	})
}

// ChannelRouteFor maps SparkQuill's own channel @tokens: "@parent" is Quill,
// "@child" is the tutor in the child's current activity (its keyed
// conversation, attachments into its attempts/ folder). root is the family
// root as product.yaml declares it; currentActivityDir is the handoff
// pointer, family-relative. Any other token is not SparkQuill's.
func ChannelRouteFor(token string, state FamilyState, root, currentActivityDir string) (agentprofiles.ChannelProfileRoute, bool, error) {
	switch strings.ToLower(strings.TrimSpace(token)) {
	case "parent", "quill":
		return agentprofiles.ChannelProfileRoute{ProfileID: ParentProfileID, Label: "Quill"}, true, nil
	case "child":
		name := "the child"
		if state.Child != nil && strings.TrimSpace(state.Child.Name) != "" {
			name = strings.TrimSpace(state.Child.Name)
		}
		root = strings.Trim(strings.TrimSpace(root), "/")
		dir := strings.Trim(strings.TrimSpace(strings.ReplaceAll(currentActivityDir, "\\", "/")), "/")
		if root != "" && strings.HasPrefix(dir, root+"/") {
			dir = strings.TrimPrefix(dir, root+"/")
		}
		if dir == "" {
			return agentprofiles.ChannelProfileRoute{}, true, fmt.Errorf("No activity has been given to %s yet — give one from SparkQuill first, then @child talks in it.", name)
		}
		slug := path.Base(dir)
		if !activitySlugPattern.MatchString(slug) {
			return agentprofiles.ChannelProfileRoute{}, true, fmt.Errorf("%s's current activity could not be found — give one from SparkQuill again.", name)
		}
		return agentprofiles.ChannelProfileRoute{
			ProfileID:       ChildProfileID,
			ConversationKey: slug,
			UploadFolder:    path.Join(root, dir, "attempts"),
			Label:           name + "'s tutor",
		}, true, nil
	}
	return agentprofiles.ChannelProfileRoute{}, false, nil
}

// InboxNote turns the unfiled uploads into one turn-context line. The prompt
// used to make the model run `ls inbox/` before every reply; the platform
// renders the prompt per turn, so it can just say what is there.
func InboxNote(files []string) string {
	if len(files) == 0 {
		return ""
	}
	names := make([]string, 0, len(files))
	for _, f := range files {
		if n := strings.TrimSpace(path.Base(f)); n != "" && n != "." && n != "/" {
			names = append(names, n)
		}
	}
	if len(names) == 0 {
		return ""
	}
	return fmt.Sprintf("INBOX — %d unfiled upload(s): %s. If an upload matters to this request, use the process-file skill; otherwise leave it for later.\n", len(names), strings.Join(names, ", "))
}

// familyRootFromActivity maps ".../Chats/SparkQuill/activities/<activity>"
// to ".../Chats/SparkQuill".
func familyRootFromActivity(activityWorkspace string) string {
	clean := strings.Trim(strings.TrimSpace(activityWorkspace), "/")
	if i := strings.LastIndex(clean, "/activities/"); i >= 0 {
		return clean[:i]
	}
	return path.Dir(clean)
}

type familyLoader struct {
	workspaceAPIURL string
}

func (l familyLoader) client(userID string) *workspace.Client {
	return workspace.NewClient(l.workspaceAPIURL, workspace.WithUserID(userID))
}

// read returns a workspace file's content, or "" when it does not exist.
func (l familyLoader) read(ctx context.Context, userID, filePath string) string {
	if strings.TrimSpace(l.workspaceAPIURL) == "" {
		return ""
	}
	result, err := l.client(userID).ReadWorkspaceFile(ctx, workspace.ReadWorkspaceFileParams{Filepath: filePath})
	if err != nil {
		return ""
	}
	return result.Content
}

// listInbox returns the files waiting in <familyRoot>/inbox, or nothing when
// the folder is missing or the workspace cannot be reached (the note is a
// convenience, never a reason to fail a turn).
func (l familyLoader) listInbox(ctx context.Context, userID, familyRoot string) []string {
	if strings.TrimSpace(l.workspaceAPIURL) == "" {
		return nil
	}
	depth := 1
	result, err := l.client(userID).ListWorkspaceFiles(ctx, workspace.ListWorkspaceFilesParams{Folder: path.Join(strings.Trim(familyRoot, "/"), InboxFolder), MaxDepth: &depth})
	if err != nil {
		return nil
	}
	var files []string
	for _, e := range parseFolderListing(result.Raw) {
		if e.Type == "folder" || strings.TrimSpace(e.FilePath) == "" || strings.HasSuffix(e.FilePath, ".meta.json") {
			continue
		}
		files = append(files, e.FilePath)
	}
	return files
}

type folderEntry struct {
	FilePath string        `json:"filepath"`
	Type     string        `json:"type"`
	Children []folderEntry `json:"children,omitempty"`
}

// parseFolderListing reads the documents API's folder listing in the shapes
// it is known to use: a bare array, {data: [...]}, or a single folder object
// whose children are the listing.
func parseFolderListing(raw json.RawMessage) []folderEntry {
	var list []folderEntry
	if json.Unmarshal(raw, &list) == nil {
		return list
	}
	var wrapped struct {
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(raw, &wrapped) == nil && len(wrapped.Data) > 0 {
		if json.Unmarshal(wrapped.Data, &list) == nil {
			return list
		}
		var one folderEntry
		if json.Unmarshal(wrapped.Data, &one) == nil {
			return one.Children
		}
	}
	var one folderEntry
	if json.Unmarshal(raw, &one) == nil {
		return one.Children
	}
	return nil
}

// load reads family.json from the product root; a missing file is a new
// family (empty state), a corrupt one is an error worth surfacing.
func (l familyLoader) load(ctx context.Context, userID, familyRoot string) (FamilyState, error) {
	var state FamilyState
	raw := l.read(ctx, userID, path.Join(strings.Trim(familyRoot, "/"), FamilyFile))
	if strings.TrimSpace(raw) == "" {
		return state, nil
	}
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		return state, fmt.Errorf("parse %s: %w", FamilyFile, err)
	}
	return state, nil
}
