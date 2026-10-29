package server

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productpolicy"
	"io"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf8"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/guidance"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/agentworksproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/relayproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/accesstokens"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/externalguidance"
	step_based_workflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productguide"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/skills"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

// Versions include visible source content, scope metadata and effective mapping notes.
func externalGuidanceVersionFor(claims *UserClaims) string {
	h := sha256.New()
	for _, topic := range externalGuidanceTopics {
		if externalTopicVisible(claims, topic) {
			h.Write([]byte(topic.Name + "\n" + externalTopicVersionFor(claims, topic) + "\n"))
		}
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

func externalTopicVersionFor(claims *UserClaims, topic externalGuidanceTopic) string {
	h := sha256.New()
	h.Write([]byte(topic.Name + "\n" + topic.Description + "\n" + externalTopicNoteFor(claims, topic) + "\n" + strings.Join(topic.Scopes, ",") + "\n" + strings.Join(topic.Tools, ",") + "\n"))
	if topic.SourceVersion != "" {
		h.Write([]byte(topic.SourceVersion + "\n"))
	}
	if _, body, err := externalGuidanceContentForProducts(topic.Name, builderProductSelection(claims)); err == nil {
		h.Write([]byte(body))
	}
	if topic.Source == "mcp-usage" {
		h.Write([]byte(productguide.Versions(builderProductSelection(claims))))
	}
	if topic.Source == "panel" {
		for _, file := range skills.BuiltinDefinition(topic.Name).SupportingFiles {
			h.Write([]byte(file.RelPath + "\n" + builderProductSelection(claims).Text(string(file.Content)) + "\n"))
		}
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
}

type externalGuidanceTopic struct {
	Name         string
	Description  string
	ExternalNote string
	// Source says where the text comes from: the Builder reference (default),
	// a file under external_topics/ ("embedded") or a Flow Builder skill ("relay").
	Source string
	// Scopes the token must allow for the topic to be listed and served.
	Scopes        []string
	Tools         []string
	Content       string
	SourceVersion string
}

//go:embed external_topics/*.md
var externalTopicFiles embed.FS

// externalTopicVisible reports whether this caller may see a topic. App
// sessions see all; a token needs every scope the topic names.
func externalTopicVisible(claims *UserClaims, topic externalGuidanceTopic) bool {
	if topic.Source == "relay" && !productEnabled("relays") {
		return false
	}
	if claims == nil || claims.AccessToken == nil {
		return true
	}
	for _, scope := range topic.Scopes {
		if !claims.AccessToken.Allows(scope) {
			return false
		}
	}
	return true
}

var (
	externalUnavailableOnce  sync.Once
	externalUnavailableTools []string
)

// externalUnavailableToolNames lists operations the Builder and Run surfaces
// know that this connection's catalog does not expose, so guidance that names
// one can say it is unavailable without a hand-written list going stale.
func externalUnavailableToolNames() []string {
	externalUnavailableOnce.Do(func() {
		catalog, err := externalTools()
		if err != nil {
			return
		}
		exposed := map[string]bool{}
		for _, tool := range catalog {
			exposed[tool.Name] = true
		}
		seen := map[string]bool{}
		// Operations guidance can name that live outside the product tool lists.
		known := append(agentworksproduct.ChatTools("builder"), agentworksproduct.RunTools()...)
		known = append(known, "get_goal_metrics", "run-plan-drift")
		for _, name := range known {
			if !exposed[name] && !seen[name] {
				seen[name] = true
				externalUnavailableTools = append(externalUnavailableTools, name)
			}
		}
		sort.Strings(externalUnavailableTools)
	})
	return externalUnavailableTools
}

// externalToolVerbs are the prefixes Builder tools use; a snake_case word in
// guidance that starts with one is treated as an operation name.
var externalToolVerbs = []string{"list_", "get_", "create_", "update_", "delete_", "add_", "set_", "read_", "run_", "call_", "mutate_", "apply_", "query_", "preview_", "validate_", "publish_", "restore_", "install_", "uninstall_", "import_", "export_", "test_", "diff_", "execute_", "manage_", "trigger_", "answer_", "reply_", "search_", "migrate_", "change_", "record_", "submit_", "send_", "dismiss_", "mark_", "perform_", "configure_", "debug_", "query_", "setup_", "consolidate_", "check_", "stamp_", "write_", "patch_", "move_", "rename_", "enable_", "disable_", "stop_", "start_", "open_", "close_"}

// externalNotOperationSuffixes end parameter and field names (call_id,
// run_dir, write_access), which are never operations.
var externalNotOperationSuffixes = []string{"_id", "_ids", "_dir", "_count", "_access", "_payload", "_path", "_paths", "_name", "_key", "_url", "_version", "_limit", "_at", "_by", "_hours", "_seconds", "_text", "_body", "_data", "_root", "_token", "_kind", "_index"}

var externalToolWord = regexp.MustCompile(`[a-z][a-z0-9]*(?:_[a-z0-9]+)+`)

// externalNamedOperations returns the operation names a text mentions: the
// known Builder and Run tools, and any snake_case word that starts with a tool
// verb (Builder tools registered in code are not all in the product lists).
func externalNamedOperationsFor(claims *UserClaims, body string) []string {
	// call_tool, get_api_spec and search_tools are this connection's own entry points.
	entry := map[string]bool{"call_tool": true, "get_api_spec": true, "search_tools": true}
	index := externalOperationIndexFor()
	allowed := func(tool externalTool) bool { return claims == nil || externalTokenAllows(claims, tool) }
	var names []string
	for _, name := range externalOperationWords(body) {
		if entry[name] {
			continue
		}
		exposed := false
		for _, tool := range index.byName[name] {
			if allowed(tool) {
				exposed = true
				break
			}
		}
		// An explicitly qualified action is available through its admitted
		// parent, even when it is not a standalone tool.
		for _, tool := range index.byAction[name] {
			if exposed {
				break
			}
			exposed = externalTextMentions(body, tool.Name+" action="+name) && allowed(tool)
		}
		if !exposed {
			names = append(names, name)
		}
	}
	return names
}

// externalOperationIndex groups the fixed external catalog by tool name and by action, so a guide checks only the
// operations it mentions (checking every tool and action per guide made each help call take seconds).
type externalOperationIndex struct {
	byName, byAction map[string][]externalTool
}

var (
	externalOperationIndexMu    sync.Mutex
	externalOperationIndexBuilt bool
	externalOperationIndexMap   externalOperationIndex
	// Guide text -> sorted operation-like names it mentions. Guides are the fixed embedded topics; the cap only
	// guards against a caller passing ever-new text.
	externalOperationWordCache sync.Map
	externalOperationWordCount atomic.Int64
)

// The external catalog is built once per process (externalCatalogOnce), so the index is built once from a
// successful catalog; a failed read is not cached and is tried again on the next call.
func externalOperationIndexFor() externalOperationIndex {
	externalOperationIndexMu.Lock()
	defer externalOperationIndexMu.Unlock()
	if externalOperationIndexBuilt {
		return externalOperationIndexMap
	}
	index := externalOperationIndex{byName: map[string][]externalTool{}, byAction: map[string][]externalTool{}}
	catalog, err := externalTools()
	if err != nil {
		return index
	}
	for _, tool := range catalog {
		index.byName[tool.Name] = append(index.byName[tool.Name], tool)
		properties, _ := tool.InputSchema["properties"].(map[string]any)
		action, _ := properties["action"].(map[string]any)
		names, _ := strictStringSliceToolArg(action["enum"])
		for _, name := range names {
			index.byAction[name] = append(index.byAction[name], tool)
		}
	}
	externalOperationIndexMap, externalOperationIndexBuilt = index, true
	return index
}

// externalOperationWords lists the operation names a guide's text mentions: known operations outside this
// catalog, and snake_case words that start with a tool verb. It depends only on the text.
func externalOperationWords(body string) []string {
	if cached, ok := externalOperationWordCache.Load(body); ok {
		return cached.([]string)
	}
	found := map[string]bool{}
	for _, name := range externalUnavailableToolNames() {
		if externalTextMentions(body, name) {
			found[name] = true
		}
	}
	for _, loc := range externalToolWord.FindAllStringIndex(body, -1) {
		word := body[loc[0]:loc[1]]
		if loc[0] > 0 && (body[loc[0]-1] == '.' || body[loc[0]-1] == '/' || body[loc[0]-1] == '$') {
			continue // a file name, path or variable, not an operation
		}
		if loc[1] < len(body) && (body[loc[1]] == '.' || body[loc[1]] == '/') {
			continue
		}
		notOperation := false
		for _, suffix := range externalNotOperationSuffixes {
			if strings.HasSuffix(word, suffix) {
				notOperation = true
				break
			}
		}
		if notOperation {
			continue
		}
		for _, verb := range externalToolVerbs {
			if strings.HasPrefix(word, verb) {
				found[word] = true
				break
			}
		}
	}
	names := make([]string, 0, len(found))
	for name := range found {
		names = append(names, name)
	}
	sort.Strings(names)
	if externalOperationWordCount.Add(1) <= 256 {
		externalOperationWordCache.Store(body, names)
	}
	return names
}

// externalTopicNote is the topic's hand-written mapping note plus an automatic
// list of every operation its text names that this connection does not have.
func externalTopicNote(topic externalGuidanceTopic) string {
	return externalTopicNoteFor(nil, topic)
}

func externalTopicNoteFor(claims *UserClaims, topic externalGuidanceTopic) string {
	note := "Reference material does not grant permission or establish live runtime context. Use only tools/actions present in this connection. Use project-relative paths with workflow_id or crew_id; server bridge variables and URLs are not external client credentials. " + topic.ExternalNote
	_, body, err := externalGuidanceContent(topic.Name)
	if err != nil {
		return note
	}
	var named []string
	for _, name := range externalNamedOperationsFor(claims, body) {
		if !externalTextMentions(topic.ExternalNote, name) {
			named = append(named, name)
		}
	}
	if len(named) == 0 {
		return note
	}
	auto := "Not in this connection's catalog (do not call): " + strings.Join(named, ", ") + ". Describe the needed change in your reply, or delegate it with sessions action=builder_chat when that is available."
	if note == "" {
		return auto
	}
	return note + "\n" + auto
}

// externalTextMentions reports whether text names the operation as a whole word.
func externalTextMentions(text, name string) bool {
	if name == "" {
		return false
	}
	isWord := func(r byte) bool {
		return r == '_' || r == '-' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= 0x80
	}
	for i := 0; i+len(name) <= len(text); i++ {
		if text[i:i+len(name)] != name {
			continue
		}
		if (i == 0 || !isWord(text[i-1])) && (i+len(name) == len(text) || !isWord(text[i+len(name)])) {
			return true
		}
	}
	return false
}

// externalGuidanceTopics discovers explicitly marked canonical references.
// Unmarked files remain private; mapping notes explain internal operations
// that are unavailable through the current external connection.
var externalGuidanceTopics = loadExternalGuidanceTopics()

func loadExternalGuidanceTopics() []externalGuidanceTopic {
	builder, err := guidance.ExternalTopics()
	if err != nil {
		panic(err)
	}
	relay, err := relayproduct.ExternalTopics()
	if err != nil {
		panic(err)
	}
	embedded, err := externalguidance.Load(externalTopicFiles, "external_topics/*.md")
	if err != nil {
		panic(err)
	}
	seen := map[string]bool{}
	var topics []externalGuidanceTopic
	for _, source := range []struct {
		name   string
		topics []externalguidance.Topic
	}{{"", builder}, {"relay", relay}, {"embedded", embedded}} {
		for _, item := range source.topics {
			if !regexp.MustCompile(`^[a-z][a-z0-9-]*$`).MatchString(item.Name) || seen[item.Name] {
				panic("invalid or duplicate external guidance topic: " + item.Name)
			}
			for _, scope := range item.Scopes {
				if !slices.Contains(accesstokens.Scopes, scope) {
					panic("invalid external guidance scope: " + scope)
				}
			}
			if source.name == "" {
				if _, err := guidance.MaterializeReferenceKindsAsSkills("workshop", []string{item.Name}); err != nil {
					panic(err)
				}
			}
			seen[item.Name] = true
			topics = append(topics, externalGuidanceTopic{Name: item.Name, Description: item.Description, ExternalNote: item.ExternalNote, Scopes: item.Scopes, Tools: item.Tools, Source: source.name, Content: item.Content})
		}
	}
	// Use the same procedures for external schema guidance and native chat.
	// Existing action resolution, visibility and delivery receipts still apply.
	for _, name := range skills.WorkspacePanelNames() {
		patterns := skills.WorkspacePanelExternalTools(name)
		if len(patterns) == 0 {
			continue
		}
		definition := skills.BuiltinDefinition(name)
		if seen[name] {
			panic("duplicate panel guidance: " + name)
		}
		seen[name] = true
		topics = append(topics, externalGuidanceTopic{Name: name, Description: definition.Description, Source: "panel", Tools: patterns, Content: definition.Content, SourceVersion: definition.Metadata["version"]})
	}
	topics = append(topics,
		externalGuidanceTopic{Name: "product-guide", Description: "Choose AgentWorks products: purpose, abilities, access requirements and examples.", Source: "product-guide", SourceVersion: productguide.GuideVersion},
		externalGuidanceTopic{Name: productguide.SkillName, Description: productguide.SkillDescription, Source: "mcp-usage", SourceVersion: productguide.SkillVersion},
		externalGuidanceTopic{Name: productguide.CLISetupTopic, Description: productguide.CLISetupDescription, Source: "cli-setup", SourceVersion: productguide.CLISetupVersion},
	)
	sort.Slice(topics, func(i, j int) bool { return topics[i].Name < topics[j].Name })
	return topics
}

// externalToolMutates reports whether a catalog tool performs mutations.
func externalToolMutates(name string, catalog []externalTool) bool {
	for i := range catalog {
		if catalog[i].Name == name {
			return catalog[i].mutates
		}
	}
	return false
}

func externalGuidanceTopicByName(name string) *externalGuidanceTopic {
	for i := range externalGuidanceTopics {
		if externalGuidanceTopics[i].Name == name {
			return &externalGuidanceTopics[i]
		}
	}
	return nil
}

// externalGuidanceContent renders one allowlisted topic through the canonical
// builder-reference renderer. It reuses the server's own materialization, so
// external guidance can never drift from the builder's copy.
func externalGuidanceContent(topic string) (description, body string, err error) {
	return externalGuidanceContentForProducts(topic, productpolicy.Selection{})
}

func externalGuidanceContentForProducts(topic string, selection productpolicy.Selection) (description, body string, err error) {
	meta := externalGuidanceTopicByName(topic)
	if meta == nil || meta.Source == "relay" && !productEnabled("relays") {
		return "", "", fmt.Errorf("topic %q is not exposed", topic)
	}
	topic = meta.Name
	if meta.Source == "product-guide" {
		return meta.Description, productguide.Guide(selection), nil
	}
	if meta.Source == "mcp-usage" {
		return meta.Description, productguide.SkillBody(selection), nil
	}
	if meta.Source == "cli-setup" {
		return meta.Description, productguide.CLISetup(selection), nil
	}
	if meta.Source != "" {
		return selection.Text(meta.Description), selection.Text(meta.Content), nil
	}

	skills, err := guidance.MaterializeReferenceKindsAsSkills("workshop", []string{topic})
	if err != nil {
		return "", "", err
	}
	if len(skills) != 1 {
		return "", "", fmt.Errorf("topic %q not available in workshop mode", topic)
	}
	body = skills[0].Content
	if index := strings.Index(body, "\n\n"); strings.HasPrefix(body, "PATH DISCIPLINE\n") && index >= 0 {
		body = body[index+2:]
	}
	return selection.Text(meta.Description), selection.Text(body), nil
}

// externalAgentContext serves get_agent_context. It is a global tool: no
// workflow is required, but when workflow_id names a visible workflow the
// caller's access level on it is included.
func (api *StreamingAPI) externalAgentContext(w http.ResponseWriter, r *http.Request, args map[string]any, visible []DiscoveredWorkflow) {
	claims := GetUserFromContext(r.Context())
	catalog, err := externalTools()
	if err != nil {
		externalError(w, 500, "schema_error", err.Error())
		return
	}
	available := make([]string, 0, len(catalog))
	for _, tool := range catalog {
		if !tool.hidden && externalTokenAllows(claims, tool) {
			available = append(available, tool.Name)
		}
	}
	sort.Strings(available)
	out := map[string]any{
		"guidance_version": externalGuidanceVersionFor(GetUserFromContext(r.Context())),
		"available_tools":  available,
		"preparation":      externalPreparation(claims),
	}
	if token := claims.AccessToken; token != nil {
		caps := map[string]any{"connection_id": token.ID, "scopes": token.Scopes, "expires_at": token.ExpiresAt.UTC().Format("2006-01-02T15:04:05Z")}
		if token.AllWorkflows {
			caps["workflows"] = "all"
		} else {
			caps["workflows"] = token.WorkflowIDs
		}
		out["token_capabilities"] = caps
	} else {
		out["token_capabilities"] = "app_session"
	}
	if id := externalArg(args, "workflow_id"); id != "" {
		for i := range visible {
			if visible[i].Manifest != nil && visible[i].Manifest.ID == id {
				out["workflow_id"] = id
				role := workflowAccessForManifest(claims, visible[i].Manifest)
				out["role"] = string(role)
				// available_tools reflects token scopes only; effective_tools
				// additionally reflects the caller's role on this workflow, so
				// readers are not shown mutations the dispatch guard would deny.
				// Run tools stay: readers may run, which is what Run mode is for.
				if role != WorkflowAccessOwner && role != WorkflowAccessWrite {
					effective := make([]string, 0, len(available))
					for _, name := range available {
						if !externalToolMutates(name, catalog) {
							effective = append(effective, name)
						}
					}
					out["effective_tools"] = effective
				} else {
					out["effective_tools"] = available
				}
				break
			}
		}
		if _, ok := out["role"]; !ok {
			externalError(w, 404, "workflow_not_found", "Workflow does not exist or is not accessible.")
			return
		}
	}
	externalJSON(w, out)
}

// externalPreparation returns the checklist for this connection. The agent
// reads workflows, files, plans, runs, guidance, and knowledge, and answers
// from what it finds; Builder authoring requires a separate explicit grant. A token with runs:execute may
// additionally operate the run-mode tools in pinned Run-mode sessions.
func externalPreparation(claims *UserClaims) []string {
	steps := []string{
		"Choose a product with help action=topic topic=product-guide. For an installable MCP usage skill, request help action=skills topics=[agentworks-mcp]. These are versioned server guidance, independent of Brain access.",
		"Use only tools and actions present in this connection’s catalog. Each call rechecks existing consent, current account/project roles and connection bounds. Workflow Run-session tools start model turns; direct Crew and project handlers execute without a model turn.",
		"Call workflow action=list to discover workflow IDs; IDs are never filesystem paths.",
		"Load only the guidance topics relevant to the task; topic list via help action=topics.",
		"Use files action=link for preview/download URLs; links identify a file and never grant permission.",
		"Use files download (not files action=read) for a local copy; downloads refuse to overwrite existing files.",
	}
	if claims != nil && claims.AccessToken != nil && claims.AccessToken.Allows("dashboards:read") {
		steps[1] = "Use only tools present in this connection’s catalog. Dashboards: dashboard action=list discovers accessible projects and published documents; action=get reads the selected source. Links require recipients to have current project access."
		if dashboardAuthoringAllowed(claims, nil) {
			steps = append(steps, "Create or update a dashboard draft, validate its revision, preview it when runs:execute is granted, then dashboard action=publish using expected_revision. action=restore republishes a previous published revision. Use {{dashboard_assets}} and {{dashboard_scripts}} for revision-scoped assets and scripts; styling follows the user’s preferences.")
		}
	}
	canRun := claims == nil || claims.AccessToken == nil || claims.AccessToken.Allows("runs:execute")
	if canRun {
		steps = append(steps, "To run: call a run-mode tool such as runs action=step (its reply carries session_id), then poll runs action=status for completion. Read run evidence with runs action=list, runs action=get, and runs action=logs.")
		steps = append(steps, "To chat: send questions with the chat tool and read replies with runs action=status; pass session_id to continue a conversation, and answer pending questions with runs action=reply_input.")
	}
	if claims != nil && externalTokenAllows(claims, externalTool{Name: "builder_chat"}) {
		steps = append(steps, "For direct workflow edits, plan action=list returns native operation schemas. Read workflow action=plan for expected_revision, then call plan with that revision, a unique request_id and the exact arguments. Existing Builder consent and current owner/editor/team roles apply; native validation and plan changelog remain. Builder action=chat can delegate an editing request to the configured model when requested; poll action=status and answer action=reply_input by operation_id.")
	}
	if claims != nil && externalTokenAllows(claims, externalTool{Name: "workflow_google_account"}) {
		steps = append(steps, "For personal Google or Slack accounts, use integrations with workflow_id or crew_id. Start with action=google_list_saved or slack_list_saved. The corresponding attach/detach action changes only this item; connect/reconnect returns a browser sign-in link; disconnect removes the saved connection everywhere. Existing Builder access, account ownership and current item access apply.")
	}
	if claims != nil && externalTokenAllows(claims, externalTool{Name: "crew_execution"}) {
		steps = append(steps, "Crew owners use terminal, browser and integrations for the direct operations offered by get_api_spec. terminal action=history reads existing owner execution records. sessions runtime_tools/runtime_call use only your own chat. Current authoring, selected account, sandbox and browser checks apply.")
	}
	if claims != nil && claims.AccessToken != nil {
		if productEnabled("relays") {
			steps = append(steps, "For Flows (kind=relay), use files for guarded source/memory/skill edits when current authoring consent allows it, or sessions action=builder_chat to delegate changes. relay action=test tests a draft, action=publish creates an immutable version, and action=run/get_run executes or reads production runs. Everyone else with access chats with a Flow in Run mode in the app: it reads the published source, shows every run and runs a published version, but changes nothing. Creation requires relays:write consent covering all accessible workflows.")
		}
		steps = append(steps, "This token is restricted: unavailable tools are omitted from the tools list.")
	}
	return steps
}

// externalGuidanceTopicList serves list_guidance_topics.
func (api *StreamingAPI) externalGuidanceTopicList(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	topics := make([]map[string]string, 0, len(externalGuidanceTopics))
	for _, topic := range externalGuidanceTopics {
		if !externalTopicVisible(claims, topic) {
			continue
		}
		item := map[string]string{"name": topic.Name, "description": topic.Description, "version": externalTopicVersionFor(claims, topic)}
		if topic.SourceVersion != "" {
			item["source_version"] = topic.SourceVersion
		}
		topics = append(topics, item)
	}
	externalJSON(w, map[string]any{"guidance_version": externalGuidanceVersionFor(GetUserFromContext(r.Context())), "topics": topics, "refresh": "Each topic has its own version. Fetch a topic again only when its version differs from the one you saved; pass known_version to help action=topic to skip an unchanged body."})
}

// externalGuidanceTopicBody serves get_guidance_topic.
func (api *StreamingAPI) externalGuidanceTopicBody(w http.ResponseWriter, r *http.Request, args map[string]any) {
	name := externalArg(args, "topic")
	allowed := externalGuidanceTopicByName(name)
	if allowed == nil || !externalTopicVisible(GetUserFromContext(r.Context()), *allowed) {
		externalError(w, 404, "unknown_topic", "Topic is not part of the external guidance profile. Use help action=topics.")
		return
	}
	claims := GetUserFromContext(r.Context())
	version := externalTopicVersionFor(claims, *allowed)
	// A topic receipt does not say that a particular reference was loaded.
	// Explicit file reads always return the requested canonical bundle member.
	if path := externalArg(args, "path"); path != "" {
		files, err := externalGuidanceSkillFiles(claims, *allowed)
		if err != nil {
			externalError(w, 500, "guidance_unavailable", "Guidance could not be rendered.")
			return
		}
		for _, raw := range files {
			file := raw.(map[string]any)
			if file["path"] == path {
				out := map[string]any{"guidance_version": externalGuidanceVersionFor(claims), "topic": name, "version": version, "path": path, "content": file["content"]}
				if allowed.SourceVersion != "" {
					out["source_version"] = allowed.SourceVersion
				}
				externalJSON(w, out)
				return
			}
		}
		externalError(w, 404, "unknown_reference", "That file is not bundled with this guide. Read help action=topic without path, then choose a listed reference.")
		return
	}
	if known := externalArg(args, "known_version"); known != "" && known == version {
		externalJSON(w, map[string]any{"guidance_version": externalGuidanceVersionFor(GetUserFromContext(r.Context())), "topic": name, "version": version, "unchanged": true})
		return
	}
	description, body, err := externalGuidanceContentForProducts(name, builderProductSelection(GetUserFromContext(r.Context())))
	if err != nil {
		externalError(w, 500, "guidance_unavailable", err.Error())
		return
	}
	out := map[string]any{"guidance_version": externalGuidanceVersionFor(GetUserFromContext(r.Context())), "topic": name, "version": version, "description": description, "content": body}
	if allowed.SourceVersion != "" {
		out["source_version"] = allowed.SourceVersion
	}
	if note := externalTopicNoteFor(GetUserFromContext(r.Context()), *allowed); note != "" {
		out["external_note"] = note
	}
	externalJSON(w, out)
}

// externalKnowledgePrefixes are the workflow-root-relative path prefixes
// servable through read_workflow_knowledge. Everything else — plan files,
// run state, ownership, private paths — stays behind read_file or typed
// tools. The workspace service enforces its own confinement on top.
var externalKnowledgePrefixes = []string{"learnings/", "knowledgebase/"}

// externalListKnowledge serves the workflow knowledge base and local skills.
// Older contracts also expose their legacy guide archive in the inventory.
// inventory, workspace skill folders, and the skill wiring (workflow-selected
// skills plus per-step enabled_skills) for one workflow.
func (api *StreamingAPI) externalListKnowledge(w http.ResponseWriter, r *http.Request, workflow DiscoveredWorkflow, args map[string]any) {
	out := map[string]any{"workflow_id": workflow.Manifest.ID}
	limit := externalInt(args, "limit", 200)
	offset := externalInt(args, "offset", 0)
	hasMore := false
	truncated := false
	directories := []string{"knowledgebase", "skills"}
	if !step_based_workflow.UsesWorkflowSkills(workflow.Manifest.Version) {
		directories = append(directories, "learnings")
	}
	for _, dir := range directories {
		result, err := externalFileRequest(r.Context(), wf.Request{Root: workflow.WorkspacePath, Operation: "list", Path: dir, Depth: 4, Limit: limit, Offset: offset})
		if err != nil {
			var upstream *externalUpstreamError
			if errors.As(err, &upstream) && upstream.status == 404 {
				out[dir] = []any{}
				continue
			}
			externalFailure(w, err)
			return
		}
		out[dir] = result.Entries
		hasMore = hasMore || result.NextOffset > 0
		truncated = truncated || result.Truncated
	}
	out["has_more"] = hasMore
	out["truncated"] = truncated
	if hasMore {
		out["next_offset"] = offset + limit
	}
	out["selected_skills"] = workflow.Manifest.Capabilities.SelectedSkills
	stepSkills := externalStepSkills(r.Context(), workflow)
	out["step_skills"] = stepSkills
	allowed := externalAllowedSkillFolders(workflow, stepSkills)
	folders, err := externalWorkspaceSkillFolders(r)
	if err != nil {
		// An unavailable skill catalog must not look like an empty workspace.
		out["warnings"] = []string{"skill catalog unavailable: " + err.Error()}
		folders = []string{}
	}
	visible := make([]string, 0, len(folders))
	for _, folder := range folders {
		if allowed[folder] {
			visible = append(visible, folder)
		}
	}
	out["workspace_skills"] = visible
	externalJSON(w, out)
}

// externalAllowedSkillFolders returns the workspace skill folders this
// workflow may expose: its selected skills plus every per-step enabled skill.
// Global skills are shared across workflows, so workflow visibility alone must
// not grant the whole catalog — a token scoped to one workflow would otherwise
// read unrelated skills through that workflow's ticket.
func externalAllowedSkillFolders(workflow DiscoveredWorkflow, stepSkills map[string][]string) map[string]bool {
	allowed := map[string]bool{}
	if workflow.Manifest != nil {
		for _, name := range workflow.Manifest.Capabilities.SelectedSkills {
			if externalSkillFolderOK(name) {
				allowed[name] = true
			}
		}
	}
	for _, names := range stepSkills {
		for _, name := range names {
			if externalSkillFolderOK(name) {
				allowed[name] = true
			}
		}
	}
	return allowed
}

// externalStepSkills extracts per-step enabled_skills from the workflow's
// step config without mutating anything. It unmarshals into the production
// StepConfigFile shape ({"steps": [{id, agent_configs: {enabled_skills}}]})
// so discovery can never drift from what the runner reads; validation is
// skipped because a malformed file must hide skills, not fail the call.
func externalStepSkills(ctx context.Context, workflow DiscoveredWorkflow) map[string][]string {
	out := map[string][]string{}
	result, err := externalFileRequest(ctx, wf.Request{Root: workflow.WorkspacePath, Operation: "read", Path: "planning/step_config.json"})
	if err != nil || !result.Exists {
		return out
	}
	var configFile step_based_workflow.StepConfigFile
	if err := json.Unmarshal([]byte(result.Content), &configFile); err != nil {
		return out
	}
	for _, step := range configFile.Steps {
		if step.ID == "" || step.AgentConfigs == nil {
			continue
		}
		for _, name := range step.AgentConfigs.EnabledSkills {
			if name != "" {
				out[step.ID] = append(out[step.ID], name)
			}
		}
	}
	return out
}

// externalWorkspaceSkillFolders lists workspace-root skill folders through the
// service-token-only shared-assets endpoint. The agent server authorizes the
// root; folder contents are served per folder by read_workflow_knowledge.
func externalWorkspaceSkillFolders(r *http.Request) ([]string, error) {
	response, err := sharedAssetRequest(r, "skills", ".", "list")
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return nil, fmt.Errorf("catalog status %d", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	var body struct {
		Data []struct {
			// Field name must match workspace sharedAssetEntry's filepath tag;
			// covered by the non-empty skill discovery test.
			Path string `json:"filepath"`
			Type string `json:"type"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	folders := []string{}
	for _, item := range body.Data {
		if item.Type == "folder" && externalSkillFolderOK(item.Path) {
			folders = append(folders, item.Path)
		}
	}
	sort.Strings(folders)
	return folders, nil
}

// externalSkillFolderOK strictly validates a workspace skill folder name so a
// crafted knowledge path can never address outside skills/.
func externalSkillFolderOK(name string) bool {
	if name == "" || name == "." || strings.ContainsAny(name, "/\\") {
		return false
	}
	for _, c := range name {
		if !(c == '-' || c == '_' || c == '.' || ('0' <= c && c <= '9') || ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z')) {
			return false
		}
	}
	return true
}

// externalReadKnowledge serves read_workflow_knowledge. Paths under
// learnings/ or knowledgebase/ are read from the workflow root; paths under
// skills/ are read from the workspace-root skill catalog through the
// service-token-only shared-assets endpoint. Nothing else is addressable.
func (api *StreamingAPI) externalReadKnowledge(w http.ResponseWriter, r *http.Request, workflow DiscoveredWorkflow, args map[string]any) {
	raw := externalArg(args, "path")
	clean, err := wf.CleanRelative(raw)
	if err != nil || clean == "." || clean != raw {
		externalError(w, 400, "invalid_arguments", "Path must be a clean workflow-relative path.")
		return
	}
	allowed := false
	for _, prefix := range append(externalKnowledgePrefixes, "skills/") {
		if strings.HasPrefix(clean, prefix) && len(clean) > len(prefix) {
			allowed = true
			break
		}
	}
	if !allowed {
		externalError(w, 403, "protected_path", "Choose a knowledgebase/ or skills/ file from files action=knowledge_list. Older guide files remain readable as workflow history.")
		return
	}
	if strings.HasPrefix(clean, "knowledgebase/") && workflow.Manifest.KnowledgebaseMode == "shared" {
		externalError(w, 403, "protected_path", "This workflow uses shared Brain MCP tools; its local knowledge archive is unavailable to agents.")
		return
	}
	if strings.HasPrefix(clean, "skills/") {
		allowed := externalAllowedSkillFolders(workflow, externalStepSkills(r.Context(), workflow))
		api.externalReadSkillFile(w, r, workflow, strings.TrimPrefix(clean, "skills/"), allowed)
		return
	}
	result, err := externalFileRequest(r.Context(), wf.Request{Root: workflow.WorkspacePath, Operation: "read", Path: clean})
	if err != nil {
		externalFailure(w, err)
		return
	}
	rawFile, _ := json.Marshal(result)
	var linked map[string]any
	_ = json.Unmarshal(rawFile, &linked)
	linked["workflow_id"] = workflow.Manifest.ID
	externalJSON(w, linked)
}

// externalReadSkillFile serves one file from a workspace-root skill folder.
// The folder name is strictly validated and the read is confined to skills/
// by the workspace service, so a crafted path cannot escape the catalog.
func (api *StreamingAPI) externalReadSkillFile(w http.ResponseWriter, r *http.Request, workflow DiscoveredWorkflow, rest string, allowed map[string]bool) {
	slash := strings.Index(rest, "/")
	if slash < 0 {
		externalError(w, 400, "invalid_arguments", "Skill path must be skills/<folder>/<file>.")
		return
	}
	folder, file := rest[:slash], rest[slash+1:]
	if !externalSkillFolderOK(folder) || file == "" || strings.HasSuffix(file, "/") {
		externalError(w, 400, "invalid_arguments", "Skill path must be skills/<folder>/<file>.")
		return
	}
	// A workflow's own files follow its Share, including local skills. The
	// selection restriction below applies only to the shared workspace catalog.
	local, localErr := externalFileRequest(r.Context(), wf.Request{Root: workflow.WorkspacePath, Operation: "read", Path: "skills/" + rest})
	if localErr == nil && local.Exists {
		externalJSON(w, map[string]any{"workflow_id": workflow.Manifest.ID, "path": "skills/" + rest, "content": local.Content, "exists": true})
		return
	}
	if localErr != nil {
		var upstream *externalUpstreamError
		if !errors.As(localErr, &upstream) || upstream.status != 404 {
			externalFailure(w, localErr)
			return
		}
	}
	if !allowed[folder] {
		externalError(w, 403, "forbidden", "This workflow does not use that skill. Only its selected and step-enabled skills are readable.")
		return
	}
	response, err := sharedAssetRequest(r, "skills", folder+"/"+file, "read")
	if err != nil {
		externalError(w, 502, "workspace_unavailable", err.Error())
		return
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case 404:
		externalError(w, 404, "not_found", "Skill file does not exist.")
		return
	case 403:
		externalError(w, 403, "protected_path", "Skill file is not accessible.")
		return
	case 200:
		break
	default:
		externalError(w, 502, "workspace_error", "Skill catalog request failed.")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, wf.MaxFileBytes+1))
	if err != nil {
		externalError(w, 502, "workspace_error", err.Error())
		return
	}
	if len(raw) > wf.MaxFileBytes {
		externalError(w, 413, "too_large", "Skill file exceeds the 2 MiB read limit.")
		return
	}
	out := map[string]any{"workflow_id": workflow.Manifest.ID, "path": "skills/" + folder + "/" + file, "exists": true}
	if utf8.Valid(raw) {
		out["encoding"] = "utf-8"
		out["content"] = string(raw)
	} else {
		out["encoding"] = "base64"
		out["content"] = base64.StdEncoding.EncodeToString(raw)
	}
	out["revision"] = wf.Revision(raw)
	externalJSON(w, out)
}
