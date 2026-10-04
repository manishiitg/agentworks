package sparkquillproduct

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path"
	"strings"

	"github.com/google/uuid"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	orchestratorevents "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/presentations"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

// The family's tools as product tool factories. Every tool talks to the
// user's workspace through the workspace API (never the local disk), reads
// and writes the same files the standalone app does (family.json, activity
// manifests, memory/ mirrors), and reaches the surface either through a
// presentation row (durable: a file or an activity shown on the right) or a
// ProductInteractionEvent (transient: suggestions, a celebration, a scene).
const toolCategory = "family_tools"

const (
	presentationsMigration          = `CREATE TABLE IF NOT EXISTS ui_presentations (id TEXT PRIMARY KEY, kind TEXT NOT NULL, schema_version INTEGER NOT NULL, session_id TEXT, title TEXT NOT NULL, payload_json TEXT NOT NULL, resources_json TEXT NOT NULL, actions_json TEXT NOT NULL, status TEXT NOT NULL, revision INTEGER NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL)`
	presentationsKindIndexMigration = `CREATE INDEX IF NOT EXISTS idx_ui_presentations_kind ON ui_presentations(kind)`
)

// familyWorkspace is one turn's view of the family root through the
// workspace API.
type familyWorkspace struct {
	client *workspace.Client
	root   string // the family root (parent: the conversation workspace; child: derived from the activity)
}

func newFamilyWorkspace(workspaceAPIURL string, runtime agentprofiles.ToolRuntimeContext, root string) familyWorkspace {
	return familyWorkspace{
		client: workspace.NewClient(workspaceAPIURL,
			workspace.WithUserID(runtime.UserID),
			workspace.WithExtraEnv(map[string]string{"MCP_SESSION_ID": runtime.SessionID})),
		root: runtimeRoot(runtime.UserID, root),
	}
}

// runtimeRoot maps a profile workspace path ("Chats/SparkQuill") to the
// per-user path the folder guard and the file API are keyed on
// ("_users/<id>/Chats/SparkQuill"); an already-expanded path is kept.
func runtimeRoot(userID, workspacePath string) string {
	ref, ok := workspaceref.Parse(workspacePath)
	if !ok {
		// A path escaping the workspace maps to a folder that does not exist,
		// never to the user's whole tree.
		return workspaceref.PhysicalPath(userID, "_invalid_workspace_path")
	}
	return ref.PhysicalKeepOwner(strings.TrimSpace(userID))
}

func (w familyWorkspace) path(parts ...string) string {
	return path.Join(append([]string{w.root}, parts...)...)
}

func (w familyWorkspace) read(ctx context.Context, rel string) (string, bool) {
	result, err := w.client.ReadWorkspaceFile(ctx, workspace.ReadWorkspaceFileParams{Filepath: w.path(rel)})
	if err != nil {
		return "", false
	}
	return result.Content, true
}

// exists reports whether rel names a file, checked via a directory listing
// so it works for any file type (binary images included) rather than read,
// which decodes content as text and fails on anything that isn't.
func (w familyWorkspace) exists(ctx context.Context, rel string) bool {
	base := path.Base(rel)
	for _, e := range w.list(ctx, path.Dir(rel)) {
		if e.Type != "folder" && path.Base(e.FilePath) == base {
			return true
		}
	}
	return false
}

func (w familyWorkspace) write(ctx context.Context, rel, content string) error {
	_, err := w.client.UpdateWorkspaceFile(ctx, workspace.UpdateWorkspaceFileParams{Filepath: w.path(rel), Content: content})
	return err
}

// list returns the direct entries of a folder, or nothing when it is missing.
func (w familyWorkspace) list(ctx context.Context, rel string) []folderEntry {
	depth := 1
	result, err := w.client.ListWorkspaceFiles(ctx, workspace.ListWorkspaceFilesParams{Folder: w.path(rel), MaxDepth: &depth})
	if err != nil {
		return nil
	}
	return parseFolderListing(result.Raw)
}

func (w familyWorkspace) move(ctx context.Context, fromRel, toRel string) error {
	_, err := w.client.MoveWorkspaceFile(ctx, workspace.MoveWorkspaceFileParams{SourceFilepath: w.path(fromRel), DestinationFilepath: w.path(toRel)})
	return err
}

// relocateAnswerKeys moves every *-KEY.md found directly in an activity folder
// to the parent-only keys folder and returns the new family-relative paths.
// The activity folder is the child's sandbox, so nothing parent-only may stay
// in it, whatever the prompt asked for.
func (w familyWorkspace) relocateAnswerKeys(ctx context.Context, rel, slug string) []string {
	var moved []string
	taken := map[string]bool{}
	for _, e := range w.list(ctx, rel) {
		name := path.Base(strings.TrimSpace(e.FilePath))
		if e.Type == "folder" || !isAnswerKey(name) {
			continue
		}
		dest := answerKeyDestination(slug, name, taken)
		if err := w.move(ctx, path.Join(rel, name), dest); err != nil {
			continue
		}
		moved = append(moved, dest)
	}
	return moved
}

// sweepAnswerKeys relocates keys left inside any activity folder (written
// before keys had their own folder). Cheap, and safe to repeat.
func (w familyWorkspace) sweepAnswerKeys(ctx context.Context) int {
	n := 0
	for _, e := range w.list(ctx, ActivitiesFolder) {
		if e.Type != "folder" {
			continue
		}
		slug := path.Base(strings.TrimSpace(e.FilePath))
		if slug == "" || slug == "." {
			continue
		}
		n += len(w.relocateAnswerKeys(ctx, path.Join(ActivitiesFolder, slug), slug))
	}
	return n
}

func (w familyWorkspace) loadFamily(ctx context.Context) (FamilyState, error) {
	var state FamilyState
	raw, ok := w.read(ctx, FamilyFile)
	if !ok || strings.TrimSpace(raw) == "" {
		return state, nil
	}
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		return state, fmt.Errorf("parse %s: %w", FamilyFile, err)
	}
	return state, nil
}

// saveFamily writes family.json and the memory/ mirrors the skills read.
func (w familyWorkspace) saveFamily(ctx context.Context, state FamilyState) error {
	content, err := encodeJSON(state)
	if err != nil {
		return err
	}
	if err := w.write(ctx, FamilyFile, content); err != nil {
		return fmt.Errorf("save %s: %w", FamilyFile, err)
	}
	if state.Child != nil {
		profile, _ := encodeJSON(state.Child)
		_ = w.write(ctx, "memory/child-profile.json", profile)
	}
	return nil
}

// interactionKind is the event kind the binding declared for this tool
// (product.yaml `interaction.kind`), or the product's default.
func interactionKind(runtime agentprofiles.ToolRuntimeContext, fallback string) string {
	if runtime.Interaction != nil && strings.TrimSpace(runtime.Interaction.Kind) != "" {
		return strings.TrimSpace(runtime.Interaction.Kind)
	}
	return fallback
}

func emitInteraction(runtime agentprofiles.ToolRuntimeContext, kind string, payload map[string]interface{}) {
	if runtime.Emit == nil {
		return
	}
	runtime.Emit(&orchestratorevents.ProductInteractionEvent{Product: ProductName, Kind: kind, Payload: payload})
}

func stringArg(args map[string]interface{}, key string) string {
	if v, ok := args[key]; ok && v != nil {
		return strings.TrimSpace(fmt.Sprint(v))
	}
	return ""
}

// jsonResult is what a tool hands back to the model; angle brackets stay
// readable so a report like dropped: ["<input>"] says what it means.
func jsonResult(v interface{}) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return "", err
	}
	return strings.TrimRight(buf.String(), "\n"), nil
}

// ---- family state --------------------------------------------------------

func setChildProfileFactory(workspaceAPIURL string) agentprofiles.ToolFactory {
	return func(runtime agentprofiles.ToolRuntimeContext, _ json.RawMessage) (agentprofiles.ToolSpec, error) {
		ws := newFamilyWorkspace(workspaceAPIURL, runtime, runtime.WorkspacePath)
		return agentprofiles.ToolSpec{
			Name: "set_child_profile", Category: toolCategory,
			Description: "Save or update the child's profile — name, grade, and school board — once the parent tells you. Call this whenever you learn any of these so future sessions and material are tailored to the right level.",
			Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{
				"name":  map[string]interface{}{"type": "string", "description": "the child's name"},
				"grade": map[string]interface{}{"type": "string", "description": "the child's grade/class, e.g. 10"},
				"board": map[string]interface{}{"type": "string", "description": "the school board, e.g. CBSE, ICSE, State Board"},
			}},
			Execute: func(ctx context.Context, args map[string]interface{}) (string, error) {
				name, grade, board := stringArg(args, "name"), stringArg(args, "grade"), stringArg(args, "board")
				if name == "" && grade == "" && board == "" {
					return "", fmt.Errorf("provide at least one of name, grade, board")
				}
				state, err := ws.loadFamily(ctx)
				if err != nil {
					return "", err
				}
				if state.Child == nil {
					state.Child = &Child{Language: "en"}
				}
				if name != "" {
					state.Child.Name = name
				}
				if grade != "" {
					state.Child.Grade = grade
				}
				if board != "" {
					state.Child.Board = board
				}
				if err := ws.saveFamily(ctx, state); err != nil {
					return "", err
				}
				emitInteraction(runtime, "family_updated", map[string]interface{}{"child": state.Child})
				return jsonResult(map[string]interface{}{"status": "ok", "name": state.Child.Name, "grade": state.Child.Grade, "board": state.Child.Board})
			},
		}, nil
	}
}

func setParentLabelFactory(workspaceAPIURL string) agentprofiles.ToolFactory {
	return func(runtime agentprofiles.ToolRuntimeContext, _ json.RawMessage) (agentprofiles.ToolSpec, error) {
		ws := newFamilyWorkspace(workspaceAPIURL, runtime, runtime.WorkspacePath)
		return agentprofiles.ToolSpec{
			Name: "set_parent_label", Category: toolCategory,
			Description: "Save how the parent wants to be referred to when you talk ABOUT them to the child — e.g. \"mom\", \"dad\", \"grandma\", or their first name. Call this once you learn it.",
			Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{
				"label": map[string]interface{}{"type": "string", "description": "e.g. mom, dad, grandma, or a first name"},
			}, "required": []string{"label"}},
			Execute: func(ctx context.Context, args map[string]interface{}) (string, error) {
				label := stringArg(args, "label")
				if label == "" {
					return "", fmt.Errorf("label is required")
				}
				state, err := ws.loadFamily(ctx)
				if err != nil {
					return "", err
				}
				state.ParentLabel = label
				if err := ws.saveFamily(ctx, state); err != nil {
					return "", err
				}
				emitInteraction(runtime, "family_updated", map[string]interface{}{"parent_label": label})
				return jsonResult(map[string]interface{}{"status": "ok", "label": label})
			},
		}, nil
	}
}

// ---- activities ----------------------------------------------------------

func createLearningActivityFactory(workspaceAPIURL string) agentprofiles.ToolFactory {
	return func(runtime agentprofiles.ToolRuntimeContext, _ json.RawMessage) (agentprofiles.ToolSpec, error) {
		ws := newFamilyWorkspace(workspaceAPIURL, runtime, runtime.WorkspacePath)
		return agentprofiles.ToolSpec{
			Name: "create_learning_activity", Category: toolCategory,
			Description: "Finalize an activity you've already built. First create the folder " + ActivitiesFolder + "/<yyyy-mm-dd>-<slug>/ and write its page into it as <name>" + FragmentSuffix + " written however you like (your own styles, pictures, complete games and inline JavaScript; <button data-choose=…> sends a choice to the tutor). This tool finishes every " + FragmentSuffix + " item into <name>.html (wires the buttons, game progress bridge and print hook, numbers any <div class=q> questions), writes activity.json and product.json, and reports anything it removed (form wrappers, details, links, remote resources). Then call open_activity(dir) so the parent sees it. A plain .html item is accepted as-is only when you intentionally supply all needed bridge code yourself",
			Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{
				"dir":     map[string]interface{}{"type": "string", "description": "the activity folder you created: " + ActivitiesFolder + "/<yyyy-mm-dd>-<slug>"},
				"title":   map[string]interface{}{"type": "string", "description": "short human title, e.g. \"Fractions — Quick Check\""},
				"subject": map[string]interface{}{"type": "string", "description": "the school subject, e.g. Math"},
				"topic":   map[string]interface{}{"type": "string", "description": "the topic within the subject, e.g. Fractions"},
				"items":   map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "bare filenames inside the folder, in order. Never an answer key: keys live in " + KeysFolder + "/ and any *-KEY.md still in the folder is moved there. Empty = instruction-only activity; then goal is required."},
				"goal":    map[string]interface{}{"type": "string", "description": "WHAT the activity is for and what finishing looks like, plus anything the parent genuinely cares about"},
				"persona": map[string]interface{}{"type": "string", "description": "the tutor's tone/personality for this activity, e.g. \"playful coach\""},
			}, "required": []string{"dir", "title"}},
			Execute: func(ctx context.Context, args map[string]interface{}) (string, error) {
				rel, slug, err := activityFolder(ws.root, stringArg(args, "dir"))
				if err != nil {
					return "", err
				}
				title := stringArg(args, "title")
				if title == "" {
					return "", fmt.Errorf("title is required")
				}
				var items []string
				var sections []SectionInfo
				var reports []RenderReport
				marks := 0
				if raw, ok := args["items"].([]interface{}); ok {
					for _, it := range raw {
						name := path.Base(strings.TrimSpace(fmt.Sprint(it)))
						if name == "" || name == "." || it == nil || isAnswerKey(name) {
							continue
						}
						content, found := ws.read(ctx, path.Join(rel, name))
						if !found {
							return "", fmt.Errorf("item %q not found in the activity folder — write it first", name)
						}
						if strings.HasSuffix(strings.ToLower(name), FragmentSuffix) {
							page, report, err := RenderActivityPage(content, PageMeta{Title: title})
							if err != nil {
								return "", fmt.Errorf("render %s: %w", name, err)
							}
							name = renderedName(name)
							if err := ws.write(ctx, path.Join(rel, name), page); err != nil {
								return "", fmt.Errorf("write %s: %w", name, err)
							}
							sections = append(sections, report.Sections...)
							marks += report.Marks
							reports = append(reports, report)
						}
						items = append(items, name)
					}
				}
				goal := stringArg(args, "goal")
				if len(items) == 0 && goal == "" {
					return "", fmt.Errorf("either items (files in the folder) or goal (for an instruction-only activity) is required")
				}
				manifest := ActivityManifest{Title: title, Subject: stringArg(args, "subject"), Topic: stringArg(args, "topic"), Items: items, Goal: goal, Persona: stringArg(args, "persona"), CreatedAt: nowStamp(), Sections: sections, Marks: marks}
				content, err := encodeJSON(manifest)
				if err != nil {
					return "", err
				}
				if err := ws.write(ctx, path.Join(rel, "activity.json"), content); err != nil {
					return "", fmt.Errorf("write activity.json: %w", err)
				}
				// The activity is also the child's project: product.json is what
				// binds one child conversation to this folder. Keep an existing
				// session id so re-finalizing does not reset her conversation.
				project := activityProject{SchemaVersion: 1, Product: ChildProfileID, ID: slug, Title: title, Description: goal, SessionID: "product-" + uuid.NewString()}
				if existing, ok := ws.read(ctx, path.Join(rel, "product.json")); ok {
					var prev activityProject
					if json.Unmarshal([]byte(existing), &prev) == nil && strings.TrimSpace(prev.SessionID) != "" {
						project.SessionID = prev.SessionID
					}
				}
				projectContent, _ := encodeJSON(project)
				if err := ws.write(ctx, path.Join(rel, "product.json"), projectContent); err != nil {
					return "", fmt.Errorf("write product.json: %w", err)
				}
				emitInteraction(runtime, "activity_created", map[string]interface{}{"dir": rel, "title": title, "items": len(items)})
				result := map[string]interface{}{"status": "ok", "dir": rel, "title": title, "items": len(items)}
				if keys := ws.relocateAnswerKeys(ctx, rel, slug); len(keys) > 0 {
					result["answer_keys_moved_to"] = keys
				}
				if len(reports) > 0 {
					var dropped, warnings []string
					for _, r := range reports {
						dropped = append(dropped, r.Dropped...)
						warnings = append(warnings, r.Warnings...)
					}
					result["pages"] = items
					result["sections"] = sections
					result["questions"] = func() int {
						n := 0
						for _, r := range reports {
							n += r.Questions
						}
						return n
					}()
					result["marks"] = marks
					if len(dropped) > 0 {
						result["dropped"] = dropped
					}
					if len(warnings) > 0 {
						result["warnings"] = warnings
					}
				}
				return jsonResult(result)
			},
		}, nil
	}
}

// ---- pinned pages ---------------------------------------------------------

// PinsStateFile is where the parent's pinned pages live: the app's own
// per-key state file (the same one its Pin button writes), so the tool and
// the UI never disagree.
const PinsStateFile = "state/pins.json"

// PinnedPage is one tab at the top of the parent's screen.
type PinnedPage struct {
	Path  string `json:"path"`
	Title string `json:"title"`
}

type pinsState struct {
	Key  string `json:"key"`
	Data struct {
		Pins []PinnedPage `json:"pins"`
	} `json:"data"`
}

func (w familyWorkspace) loadPins(ctx context.Context) []PinnedPage {
	raw, ok := w.read(ctx, PinsStateFile)
	if !ok {
		return nil
	}
	var st pinsState
	if json.Unmarshal([]byte(raw), &st) != nil {
		return nil
	}
	return st.Data.Pins
}

func (w familyWorkspace) savePins(ctx context.Context, pins []PinnedPage) error {
	var st pinsState
	st.Key = "pins"
	st.Data.Pins = pins
	if st.Data.Pins == nil {
		st.Data.Pins = []PinnedPage{}
	}
	content, err := encodeJSON(st)
	if err != nil {
		return err
	}
	return w.write(ctx, PinsStateFile, content)
}

// familyRelative normalises a path the model passed (bare, family-relative,
// or prefixed with the runtime root) to family-relative form.
func familyRelative(root, raw string) string {
	clean := strings.Trim(strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/")), "/")
	if r := strings.Trim(root, "/"); r != "" && strings.HasPrefix(clean, r+"/") {
		clean = strings.TrimPrefix(clean, r+"/")
	}
	return clean
}

func pinPageFactory(workspaceAPIURL string) agentprofiles.ToolFactory {
	return func(runtime agentprofiles.ToolRuntimeContext, _ json.RawMessage) (agentprofiles.ToolSpec, error) {
		ws := newFamilyWorkspace(workspaceAPIURL, runtime, runtime.WorkspacePath)
		return agentprofiles.ToolSpec{
			Name: "pin_page", Category: toolCategory,
			Description: "Pin any HTML page you made for the parent (an exam tracker, a date sheet, a revision plan, anything) as a tab at the top of their screen. Write the page first (e.g. pages/<slug>.html, your own design, no form controls), then call this with its path and a short tab title. Pinning again with the same path just renames the tab. The parent can also pin or unpin from the page itself.",
			Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{
				"path":  map[string]interface{}{"type": "string", "description": "the page's path relative to the workspace, e.g. pages/exam-tracker.html"},
				"title": map[string]interface{}{"type": "string", "description": "short tab title, e.g. Exam tracker"},
			}, "required": []string{"path", "title"}},
			Execute: func(ctx context.Context, args map[string]interface{}) (string, error) {
				rel := familyRelative(ws.root, stringArg(args, "path"))
				title := strings.TrimSpace(stringArg(args, "title"))
				if rel == "" || title == "" {
					return "", fmt.Errorf("path and title are required")
				}
				if !strings.HasSuffix(strings.ToLower(rel), ".html") && !strings.HasSuffix(strings.ToLower(rel), ".htm") {
					return "", fmt.Errorf("only an HTML page can be pinned as a tab: %s", rel)
				}
				if _, found := ws.read(ctx, rel); !found {
					return "", fmt.Errorf("%s does not exist — write the page first", rel)
				}
				pins := ws.loadPins(ctx)
				replaced := false
				for i := range pins {
					if pins[i].Path == rel {
						pins[i].Title = title
						replaced = true
					}
				}
				if !replaced {
					pins = append(pins, PinnedPage{Path: rel, Title: title})
				}
				if err := ws.savePins(ctx, pins); err != nil {
					return "", err
				}
				emitInteraction(runtime, "pins_updated", map[string]interface{}{"pins": pins})
				return jsonResult(map[string]interface{}{"status": "ok", "pinned": rel, "title": title, "pins": len(pins)})
			},
		}, nil
	}
}

func unpinPageFactory(workspaceAPIURL string) agentprofiles.ToolFactory {
	return func(runtime agentprofiles.ToolRuntimeContext, _ json.RawMessage) (agentprofiles.ToolSpec, error) {
		ws := newFamilyWorkspace(workspaceAPIURL, runtime, runtime.WorkspacePath)
		return agentprofiles.ToolSpec{
			Name: "unpin_page", Category: toolCategory,
			Description: "Remove a pinned page's tab from the top of the parent's screen. The file itself stays where it is.",
			Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{
				"path": map[string]interface{}{"type": "string", "description": "the pinned page's path, e.g. pages/exam-tracker.html"},
			}, "required": []string{"path"}},
			Execute: func(ctx context.Context, args map[string]interface{}) (string, error) {
				rel := familyRelative(ws.root, stringArg(args, "path"))
				pins := ws.loadPins(ctx)
				kept := pins[:0]
				removed := false
				for _, p := range pins {
					if p.Path == rel {
						removed = true
						continue
					}
					kept = append(kept, p)
				}
				if !removed {
					return "", fmt.Errorf("%s is not pinned", rel)
				}
				if err := ws.savePins(ctx, kept); err != nil {
					return "", err
				}
				emitInteraction(runtime, "pins_updated", map[string]interface{}{"pins": kept})
				return jsonResult(map[string]interface{}{"status": "ok", "unpinned": rel, "pins": len(kept)})
			},
		}, nil
	}
}

// ---- showing things -------------------------------------------------------

func presentationActivity(binding *agentprofiles.PresentationBinding) *orchestratorevents.PresentationActivity {
	if binding == nil || binding.Activity == nil {
		return nil
	}
	return &orchestratorevents.PresentationActivity{Label: binding.Activity.Label, Destination: binding.Activity.Destination, Detail: binding.Activity.Detail}
}

// openFileFactory shows one workspace file on the right. conversationOnly
// pins it to the conversation workspace (the child's activity, with a focus
// argument); otherwise any path under the family root is allowed.
func openFileFactory(workspaceAPIURL string, conversationOnly bool) agentprofiles.ToolFactory {
	return func(runtime agentprofiles.ToolRuntimeContext, _ json.RawMessage) (agentprofiles.ToolSpec, error) {
		ws := newFamilyWorkspace(workspaceAPIURL, runtime, runtime.WorkspacePath)
		description := "Show a workspace file to the parent on the right side of the screen — a page, or any other file directly (an image, a PDF, whatever it is). Call this right after you create or update a file the parent should see (study material, a test, the progress page, a generated picture). Pass the path relative to the workspace."
		params := map[string]interface{}{"path": map[string]interface{}{"type": "string", "description": "workspace-relative path to the file to display"}}
		if conversationOnly {
			description = "Show a lesson, worksheet, one of her own saved pages, or any other file directly (an image, a PDF, whatever it is) on the right side of her screen. Pass the path relative to the activity folder. PASS focus WHENEVER you are talking about one specific question or section on a page — that is what actually scrolls the page to it; omit it to keep her current position (for example right after recording an answer), and omit it entirely for a non-page file."
			params["focus"] = map[string]interface{}{"type": "string", "description": "id of the element to scroll to — a question (\"q4\"), a section (\"s2\"), a worked example (\"s2-1\"), or a figure (\"fig1\"). Ignored if no such id exists."}
		}
		return agentprofiles.ToolSpec{
			Name: "open_file", Category: toolCategory, Description: description,
			Parameters: map[string]interface{}{"type": "object", "properties": params, "required": []string{"path"}},
			Execute: func(ctx context.Context, args map[string]interface{}) (string, error) {
				if runtime.Presentation == nil || strings.TrimSpace(runtime.Presentation.Kind) == "" {
					return "", fmt.Errorf("open_file: profile did not declare a presentation kind for this tool")
				}
				rel := strings.Trim(strings.TrimSpace(stringArg(args, "path")), "/")
				if rel == "" || strings.Contains(rel, "..") {
					return "", fmt.Errorf("invalid path")
				}
				rel = strings.TrimPrefix(rel, ws.root+"/")
				if !ws.exists(ctx, rel) {
					return "", fmt.Errorf("no file at %q", rel)
				}
				focus := strings.TrimPrefix(stringArg(args, "focus"), "#")
				if focus != "" && focus[0] >= '0' && focus[0] <= '9' {
					focus = "q" + focus
				}
				fullPath := ws.path(rel)
				event, err := presentations.Upsert(ctx, ws.client, presentations.Presentation{
					Kind: runtime.Presentation.Kind, IdentityKey: fullPath, Title: path.Base(rel),
					WorkspacePath: runtime.WorkspacePath, SessionID: runtime.SessionID,
					Activity:  presentationActivity(runtime.Presentation),
					Payload:   map[string]interface{}{"path": fullPath, "focus": focus},
					Resources: []map[string]string{{"kind": "workspace.file", "path": fullPath, "role": "primary"}},
				})
				if err != nil {
					return "", fmt.Errorf("show file: %w", err)
				}
				if runtime.Emit != nil {
					runtime.Emit(&event)
				}
				return jsonResult(map[string]interface{}{"status": "ok", "opened": rel})
			},
		}, nil
	}
}

func openActivityFactory(workspaceAPIURL string) agentprofiles.ToolFactory {
	return func(runtime agentprofiles.ToolRuntimeContext, _ json.RawMessage) (agentprofiles.ToolSpec, error) {
		ws := newFamilyWorkspace(workspaceAPIURL, runtime, runtime.WorkspacePath)
		return agentprofiles.ToolSpec{
			Name: "open_activity", Category: toolCategory,
			Description: "Show a whole activity (its title, goal, and item list) to the parent on the right side of the screen — a dedicated overview with its own 'Give to <child>' button, not a single file. Call this right after create_learning_activity finishes and whenever the parent asks to see an EXISTING activity as a whole. Pass the activity folder (dir).",
			Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{
				"dir": map[string]interface{}{"type": "string", "description": "the activity folder: " + ActivitiesFolder + "/<slug>"},
			}, "required": []string{"dir"}},
			Execute: func(ctx context.Context, args map[string]interface{}) (string, error) {
				if runtime.Presentation == nil || strings.TrimSpace(runtime.Presentation.Kind) == "" {
					return "", fmt.Errorf("open_activity: profile did not declare a presentation kind for this tool")
				}
				rel, slug, err := activityFolder(ws.root, stringArg(args, "dir"))
				if err != nil {
					return "", err
				}
				raw, ok := ws.read(ctx, path.Join(rel, "activity.json"))
				if !ok {
					return "", fmt.Errorf("no activity found at %q (create it first)", rel)
				}
				var manifest ActivityManifest
				if err := json.Unmarshal([]byte(raw), &manifest); err != nil {
					return "", fmt.Errorf("activity.json at %q is not valid: %w", rel, err)
				}
				fullDir := ws.path(rel)
				event, err := presentations.Upsert(ctx, ws.client, presentations.Presentation{
					Kind: runtime.Presentation.Kind, IdentityKey: fullDir, Title: manifest.Title,
					WorkspacePath: runtime.WorkspacePath, SessionID: runtime.SessionID,
					Activity: presentationActivity(runtime.Presentation),
					Payload: map[string]interface{}{"dir": fullDir, "activity_id": slug, "title": manifest.Title, "subject": manifest.Subject, "topic": manifest.Topic,
						"items": manifest.Items, "goal": manifest.Goal, "persona": manifest.Persona, "child_profile": ChildProfileID},
					Resources: []map[string]string{{"kind": "workspace.folder", "path": fullDir, "role": "primary"}},
				})
				if err != nil {
					return "", fmt.Errorf("show activity: %w", err)
				}
				if runtime.Emit != nil {
					runtime.Emit(&event)
				}
				return jsonResult(map[string]interface{}{"status": "ok", "opened": rel})
			},
		}, nil
	}
}

// ---- transient surface interactions ---------------------------------------

func celebrateFactory() agentprofiles.ToolFactory {
	return func(runtime agentprofiles.ToolRuntimeContext, _ json.RawMessage) (agentprofiles.ToolSpec, error) {
		return agentprofiles.ToolSpec{
			Name: "celebrate", Category: toolCategory,
			Description: "Award 1-3 stars for genuine effort or progress, right now, in the moment — finishing a test, working through something hard, a nice improvement, real persistence. Shown live in the chat; not tracked as a total. Never for just showing up or a single easy answer.",
			Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{
				"stars":  map[string]interface{}{"type": "integer", "description": "how many stars, 1 to 3"},
				"reason": map[string]interface{}{"type": "string", "description": "one short, warm sentence about what earned it"},
			}, "required": []string{"stars", "reason"}},
			Execute: func(_ context.Context, args map[string]interface{}) (string, error) {
				stars := 1
				if f, ok := args["stars"].(float64); ok {
					stars = int(f)
				}
				if stars < 1 {
					stars = 1
				}
				if stars > 3 {
					stars = 3
				}
				reason := stringArg(args, "reason")
				if reason == "" {
					return "", fmt.Errorf("reason is required")
				}
				emitInteraction(runtime, interactionKind(runtime, "celebrate"), map[string]interface{}{"stars": stars, "reason": reason})
				return jsonResult(map[string]interface{}{"status": "ok", "stars_awarded": stars})
			},
		}, nil
	}
}

func showSceneFactory() agentprofiles.ToolFactory {
	return func(runtime agentprofiles.ToolRuntimeContext, _ json.RawMessage) (agentprofiles.ToolSpec, error) {
		return agentprofiles.ToolSpec{
			Name: "show_scene", Category: toolCategory,
			Description: "Show a small, self-contained HTML visual inline in this reply. Use inline CSS/JavaScript and no remote assets or network calls. Images saved in the activity folder can be referenced with a relative <img src=\"filename.png\">. A choice that needs a tutor reply calls SQ.choose(text, button); local interactions can use JavaScript. Keep timer loops finite. Use a durable activity page if the child should return to the content later.",
			Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{
				"html": map[string]interface{}{"type": "string", "description": "the small, self-contained HTML snippet to show inline"},
			}, "required": []string{"html"}},
			Execute: func(_ context.Context, args map[string]interface{}) (string, error) {
				html := stringArg(args, "html")
				if html == "" {
					return "", fmt.Errorf("html is required")
				}
				emitInteraction(runtime, interactionKind(runtime, "scene"), map[string]interface{}{"html": html})
				return `{"status":"ok"}`, nil
			},
		}, nil
	}
}
