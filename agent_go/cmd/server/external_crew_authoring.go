package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productschedule"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/skills"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

// External Crew authoring (crews:write): create a Crew from a local CLI,
// edit everything about it, and move it between accounts and servers as a
// portable spec. Every write reuses the path the app already uses for that
// part of a Crew: the server-side creation layout (writeCrewCreationManifests),
// the capability selection writer, the
// functions.json writer with project_functions_define's checks, and the project
// schedule service.
//
// Access: Crews have one owner (crew_access.go). Only the owner edits a Crew,
// exactly as in the app; every other user with the Crew product runs it.
// Read-only accounts and disabled accounts never write.

var externalCrewWriteTools = map[string]bool{"create_crew": true, "update_crew": true, "import_crew": true, "write_crew_file": true}

const (
	crewSpecKind          = "agentworks.crew"
	crewSpecSchemaVersion = 1
	crewSpecMaxFiles      = 200
	crewSpecMaxFilesBytes = 4 << 20
	crewSpecMaxFunctions  = 50
	crewSpecMaxSchedules  = 20
	crewPurposeLimit      = 1000
	crewSharingModel      = "A Crew has one main owner (its creator) and can have co-owners who build it together; everything runs as the main owner. New Crews start Only me. Use Share (Only me / Chosen people / Everyone) to let others open it, ask it and call its functions; only its owners edit it."
)

// crewSpec is the portable Crew definition: create_crew's input, export_crew's
// output and import_crew's input: identity, selected skills and project files.
type crewSpec struct {
	RootPath         string                         `json:"root_path,omitempty"`
	SchemaVersion    int                            `json:"schema_version,omitempty"`
	Kind             string                         `json:"kind,omitempty"`
	Name             string                         `json:"name"`
	Icon             string                         `json:"icon,omitempty"`
	Purpose          string                         `json:"purpose"`
	ChatInstructions string                         `json:"chat_instructions,omitempty"`
	SelectedSkills   []string                       `json:"selected_skills,omitempty"`
	Files            map[string]string              `json:"files,omitempty"`
	Functions        []crewFunctionSpec             `json:"functions,omitempty"`
	Schedules        []crewScheduleSpec             `json:"schedules,omitempty"`
	Mode             string                         `json:"mode,omitempty"`
	LLMConfig        *workflowtypes.PresetLLMConfig `json:"llm_config,omitempty"`
	// Role is read only to refuse it: the platform alone marks its own Crews.
	Role string `json:"role,omitempty"`
}

// crewScheduleSpec is a project schedule as authors send it; omitted fields
// keep their current value on update.
type crewScheduleSpec struct {
	ID             string   `json:"id,omitempty"`
	Name           string   `json:"name,omitempty"`
	Description    string   `json:"description,omitempty"`
	Message        string   `json:"message,omitempty"`
	Messages       []string `json:"messages,omitempty"`
	CronExpression string   `json:"cron_expression,omitempty"`
	Timezone       string   `json:"timezone,omitempty"`
	CadenceHours   *int     `json:"cadence_hours,omitempty"`
	RunAt          string   `json:"run_at,omitempty"` // one-time: RFC3339 instant; replaces cron/cadence
	PreferredHour  *int     `json:"preferred_hour,omitempty"`
	Enabled        *bool    `json:"enabled,omitempty"`
	RunDestination string   `json:"run_destination,omitempty"`
}

func (spec crewScheduleSpec) applyTo(schedule *productschedule.Schedule) error {
	if value := strings.TrimSpace(spec.Name); value != "" {
		schedule.Name = value
	}
	if value := strings.TrimSpace(spec.Description); value != "" {
		schedule.Description = value
	}
	if len(spec.Messages) > 0 {
		schedule.Messages = nil
		for _, message := range spec.Messages {
			schedule.Messages = append(schedule.Messages, strings.TrimSpace(message))
		}
	} else if value := strings.TrimSpace(spec.Message); value != "" {
		schedule.Messages = []string{value}
	}
	// A schedule has exactly one timing form; naming a new one replaces the old.
	if value := strings.TrimSpace(spec.CronExpression); value != "" {
		schedule.CronExpression, schedule.CadenceHours, schedule.RunAt = value, 0, ""
	}
	if value := strings.TrimSpace(spec.Timezone); value != "" {
		schedule.Timezone = value
	}
	if spec.CadenceHours != nil {
		schedule.CadenceHours = *spec.CadenceHours
		if *spec.CadenceHours > 0 {
			schedule.CronExpression, schedule.RunAt = "", ""
		}
	}
	if value := strings.TrimSpace(spec.RunAt); value != "" {
		schedule.RunAt, schedule.CronExpression, schedule.CadenceHours = value, "", 0
	}
	if spec.PreferredHour != nil {
		hour := *spec.PreferredHour
		schedule.PreferredHour = &hour
	}
	if spec.Enabled != nil {
		schedule.Enabled = *spec.Enabled
	}
	if strings.TrimSpace(spec.RunDestination) != "" {
		isolated, err := isolatedForRunDestination(spec.RunDestination)
		if err != nil {
			return err
		}
		schedule.Isolated = isolated
	}
	return nil
}

// newSchedule builds a new, validated schedule from spec (enabled unless the
// spec says otherwise).
func (spec crewScheduleSpec) newSchedule() (productschedule.Schedule, error) {
	schedule := productschedule.Schedule{ID: uuid.NewString(), Enabled: true}
	if err := spec.applyTo(&schedule); err != nil {
		return schedule, err
	}
	return schedule, productschedule.Validate(schedule)
}

func crewScheduleSpecFrom(schedule productschedule.Schedule, withID bool) crewScheduleSpec {
	enabled := schedule.Enabled
	out := crewScheduleSpec{
		Name: schedule.Name, Description: schedule.Description, Messages: append([]string(nil), schedule.Messages...),
		CronExpression: schedule.CronExpression, RunAt: schedule.RunAt, Timezone: schedule.Timezone, PreferredHour: schedule.PreferredHour,
		Enabled: &enabled, RunDestination: runDestination(schedule.Isolated),
	}
	if schedule.CadenceHours > 0 {
		hours := schedule.CadenceHours
		out.CadenceHours = &hours
	}
	if withID {
		out.ID = schedule.ID
	}
	return out
}

// crewIdentityChecks applies the identity limits the Crew identity tool
// (set_work_identity) and Builder creation enforce.
func crewIdentityChecks(name, icon, purpose string) error {
	if name == "" || utf8.RuneCountInString(name) > 60 {
		return fmt.Errorf("crew name must be 1-60 characters")
	}
	if utf8.RuneCountInString(icon) > 8 {
		return fmt.Errorf("crew icon must be at most 8 characters")
	}
	if utf8.RuneCountInString(purpose) > crewPurposeLimit {
		return fmt.Errorf("crew purpose must be at most %d characters", crewPurposeLimit)
	}
	return nil
}

// crewSpecFilePath confines one spec file to the Crew's ordinary project
// files: never its manifests, functions.json (edited through functions),
// private chats (builder/), databases (db/) or git metadata.
func crewSpecFilePath(root, rel string) (string, error) {
	clean := strings.TrimSpace(rel)
	if clean == "" || path.IsAbs(clean) || path.Clean(clean) != clean || strings.HasPrefix(clean, "../") || clean == crewFunctionsFileName || clean == ".git" || strings.HasPrefix(clean, ".git/") {
		return "", fmt.Errorf("file path %q is not an editable Crew file", rel)
	}
	full, ok := confineSharedProjectPath(root, clean)
	if !ok {
		return "", fmt.Errorf("file path %q is not an editable Crew file", rel)
	}
	return full, nil
}

func checkCrewSpecFiles(files map[string]string) error {
	if len(files) > crewSpecMaxFiles {
		return fmt.Errorf("at most %d files per call", crewSpecMaxFiles)
	}
	total := 0
	for rel, content := range files {
		if _, err := crewSpecFilePath("crew", rel); err != nil {
			return err
		}
		if len(content) > sharedProjectFileContentCap || isSharedProjectBinaryContent(content) {
			return fmt.Errorf("file %q must be text up to %d bytes", rel, sharedProjectFileContentCap)
		}
		total += len(content)
	}
	if total > crewSpecMaxFilesBytes {
		return fmt.Errorf("files may total at most %d bytes", crewSpecMaxFilesBytes)
	}
	return nil
}

func writeCrewSpecFiles(ctx context.Context, root string, files map[string]string) error {
	names := make([]string, 0, len(files))
	for rel := range files {
		names = append(names, rel)
	}
	sort.Strings(names)
	for _, rel := range names {
		full, err := crewSpecFilePath(root, rel)
		if err != nil {
			return err
		}
		if err := createWorkspaceFolder(ctx, path.Dir(full)); err != nil {
			return fmt.Errorf("create folder for %s: %w", rel, err)
		}
		if err := writeRawFileToWorkspace(ctx, full, files[rel]); err != nil {
			return fmt.Errorf("write %s: %w", rel, err)
		}
	}
	return nil
}

func crewLocalSkillManifest(name string) string { return "skills/" + name + "/SKILL.md" }

// checkCrewSkillsAvailable accepts built-in skills and project-local skills (skills/<name>/SKILL.md) either already in
// the Crew or arriving in this same call. Unknown names fail before anything
// is written.
func checkCrewSkillsAvailable(ctx context.Context, root string, names []string, incoming map[string]string) error {
	builtinScope := root
	if builtinScope == "" {
		builtinScope = "Crew"
	}
	for _, name := range names {
		if skills.IsBuiltinSkillIn(builtinScope, name) {
			continue
		}
		if _, ok := incoming[crewLocalSkillManifest(name)]; ok {
			continue
		}
		if root != "" {
			if _, found, err := readFileFromWorkspace(ctx, root+"/"+crewLocalSkillManifest(name)); err == nil && found {
				continue
			}
		}
		if root != "" {
			if _, err := skills.GetSkillIn(getWorkspaceAPIURL(), root, name); err == nil {
				continue
			}
		}
		return fmt.Errorf("skill %q is not installed in this Crew; ship it under files as %s or install it in this Crew", name, crewLocalSkillManifest(name))
	}
	return nil
}

// normalizeCrewSpec trims and checks a whole spec without touching storage
// (skill availability aside), so a bad spec never leaves a half-made Crew.
func normalizeCrewSpec(spec crewSpec) (crewSpec, error) {
	spec.Name, spec.Icon = strings.TrimSpace(spec.Name), strings.TrimSpace(spec.Icon)
	spec.Purpose = strings.TrimSpace(spec.Purpose)
	var instructionsErr error
	spec.ChatInstructions, instructionsErr = workflowtypes.NormalizeChatInstructions(spec.ChatInstructions)
	if instructionsErr != nil {
		return spec, instructionsErr
	}
	if strings.TrimSpace(spec.Role) != "" {
		return spec, fmt.Errorf("role is set by the platform; remove it from the Crew settings")
	}
	spec.Mode = strings.TrimSpace(spec.Mode)
	switch spec.Mode {
	case "", "dev", "cowork":
	default:
		return spec, fmt.Errorf("choose Dev or Cowork for this Crew")
	}
	if config := spec.LLMConfig; config != nil {
		if config.SchemaVersion != 2 || config.Mode != workflowtypes.LLMConfigModeExplicit && config.Mode != workflowtypes.LLMConfigModeProviderProfile {
			return spec, fmt.Errorf("choose an existing model configuration")
		}
		if config.Mode == workflowtypes.LLMConfigModeExplicit && (config.BuilderLLM == nil || strings.TrimSpace(config.BuilderLLM.Provider) == "" || strings.TrimSpace(config.BuilderLLM.ModelID) == "") || config.Mode == workflowtypes.LLMConfigModeProviderProfile && strings.TrimSpace(config.Provider) == "" {
			return spec, fmt.Errorf("choose a provider and model for this Crew")
		}
	}
	if spec.Kind != "" && spec.Kind != crewSpecKind {
		return spec, fmt.Errorf("spec kind must be %q", crewSpecKind)
	}
	if spec.SchemaVersion > crewSpecSchemaVersion {
		return spec, fmt.Errorf("spec schema_version %d is newer than this server supports (%d)", spec.SchemaVersion, crewSpecSchemaVersion)
	}
	if err := crewIdentityChecks(spec.Name, spec.Icon, spec.Purpose); err != nil {
		return spec, err
	}
	names, err := validateCrewCreationNames("skill", spec.SelectedSkills)
	if err != nil {
		return spec, err
	}
	spec.SelectedSkills = names
	if err := checkCrewSpecFiles(spec.Files); err != nil {
		return spec, err
	}
	if len(spec.Functions) > crewSpecMaxFunctions || len(spec.Schedules) > crewSpecMaxSchedules {
		return spec, fmt.Errorf("at most %d functions and %d schedules", crewSpecMaxFunctions, crewSpecMaxSchedules)
	}
	seen := map[string]bool{}
	for i := range spec.Functions {
		spec.Functions[i] = spec.Functions[i].normalized()
		if err := spec.Functions[i].validate(); err != nil {
			return spec, fmt.Errorf("function %q: %w", spec.Functions[i].Name, err)
		}
		if seen[spec.Functions[i].Name] {
			return spec, fmt.Errorf("function %q is declared twice", spec.Functions[i].Name)
		}
		seen[spec.Functions[i].Name] = true
	}
	for i, schedule := range spec.Schedules {
		if _, err := schedule.newSchedule(); err != nil {
			return spec, fmt.Errorf("schedule %d: %w", i+1, err)
		}
	}
	return spec, nil
}

// externalCrewAuthorGate is the account-level half of every Crew write:
// a signed-in, enabled account that is not read-only and may use the Crew
// product. Ownership of an existing Crew is checked separately.
func externalCrewAuthorGate(claims *UserClaims) (int, string) {
	if claims == nil || strings.TrimSpace(claims.UserID) == "" {
		return http.StatusUnauthorized, "Sign in to AgentWorks."
	}
	if userAccessForClaims(claims).Disabled {
		return http.StatusForbidden, "This account is disabled."
	}
	if workflowAccessForClaims(claims) == WorkflowAccessRead {
		return http.StatusForbidden, "Read-only accounts cannot create or edit Crews."
	}
	if !productEnabled("work") || !userAllowedProduct(claims, "work") {
		return http.StatusForbidden, "The Crew product is not available to this account."
	}
	return 0, ""
}

// crewCreateGate is externalCrewAuthorGate plus the account's create permission: "can create" is the one switch for
// making new workflows, Relays and Crews. An editor or viewer edits and uses what exists but makes no new Crew.
func crewCreateGate(claims *UserClaims) (int, string) {
	if status, message := externalCrewAuthorGate(claims); status != 0 {
		return status, message
	}
	if !userAccessForClaims(claims).CanCreateIn("work") {
		return http.StatusForbidden, "This account cannot create Crews. An administrator can allow it under the account's create permissions."
	}
	return 0, ""
}

func (api *StreamingAPI) externalCrewAuthoringCall(w http.ResponseWriter, r *http.Request, name string, args map[string]any) {
	ctx := r.Context()
	claims := GetUserFromContext(ctx)
	if externalCrewWriteTools[name] {
		if status, message := externalCrewAuthorGate(claims); status != 0 {
			externalError(w, status, "forbidden", message)
			return
		}
	}
	switch name {
	case "write_crew_file":
		api.externalWriteCrewFile(w, r, claims, args)
	case "create_crew", "import_crew":
		if status, message := crewCreateGate(claims); status != 0 {
			externalError(w, status, "forbidden", message)
			return
		}
		// A new Crew is outside any Crew list a bounded token names; only
		// a connection reaching all of the user's Crews may mint one.
		if claims.AccessToken != nil && !claims.AccessToken.AllCrews {
			externalError(w, http.StatusForbidden, "insufficient_scope", "Creating a Crew needs a connection that covers all your Crews.")
			return
		}
		var spec crewSpec
		raw := any(args)
		if name == "import_crew" {
			raw = args["spec"]
		}
		if err := remarshalExternalArgs(raw, &spec); err != nil {
			externalError(w, http.StatusBadRequest, "invalid_arguments", err.Error())
			return
		}
		enableSchedules := name == "create_crew"
		if name == "import_crew" {
			enableSchedules, _ = args["enable_schedules"].(bool)
		}
		crewID, setupErr := api.createCrewFromSpec(ctx, claims, spec, enableSchedules)
		if setupErr != nil && crewID == "" {
			externalError(w, http.StatusBadRequest, "crew_not_created", externalCrewCreateError(crewID, setupErr))
			return
		}
		crew, manifest, summary, ok := api.externalCrewResolve(ctx, claims, crewID)
		if !ok {
			out := map[string]any{"crew_id": crewID, "created": true}
			if setupErr != nil {
				out["warnings"] = []string{projectCreationWarning(setupErr)}
			}
			externalJSON(w, out)
			return
		}
		out := externalCrewDescription(ctx, crew, manifest, summary)
		out["created"] = true
		if setupErr != nil {
			out["warnings"] = []string{projectCreationWarning(setupErr)}
		}
		externalJSON(w, out)
	case "update_crew", "export_crew":
		crew, manifest, summary, ok := api.externalCrewResolve(ctx, claims, externalArg(args, "crew_id"))
		if !ok {
			externalError(w, http.StatusNotFound, "not_found", "Crew not found or not allowed for this connection.")
			return
		}
		// Export carries the Crew's skill files and function instructions,
		// which Run-mode users never see; only the owner may take it away.
		if !crew.OwnedByCaller {
			externalError(w, http.StatusForbidden, "forbidden", fmt.Sprintf("Only the Crew's owner (%v) can %s it.", summary["owner"], strings.TrimSuffix(name, "_crew")))
			return
		}
		if name == "export_crew" {
			spec, err := exportCrewSpec(ctx, crew, manifest)
			if err != nil {
				externalError(w, http.StatusBadGateway, "workspace_unavailable", err.Error())
				return
			}
			externalJSON(w, map[string]any{"spec": spec})
			return
		}
		var update crewUpdate
		if err := remarshalExternalArgs(args, &update); err != nil {
			externalError(w, http.StatusBadRequest, "invalid_arguments", err.Error())
			return
		}
		if err := api.updateCrewFromSpec(ctx, claims, crew, manifest, update); err != nil {
			externalError(w, http.StatusBadRequest, "update_failed", err.Error())
			return
		}
		crew, manifest, summary, ok = api.externalCrewResolve(ctx, claims, manifest.ID)
		if !ok {
			externalJSON(w, map[string]any{"crew_id": manifest.ID, "updated": true})
			return
		}
		out := externalCrewDescription(ctx, crew, manifest, summary)
		out["updated"] = true
		if full, _ := args["return_spec"].(bool); !full {
			out = externalCrewUpdateReply(out, update)
		}
		externalJSON(w, out)
	default:
		externalError(w, http.StatusNotFound, "unknown_tool", "Tool is not exposed by this API.")
	}
}

func externalCrewCreateError(crewID string, err error) string {
	if crewID == "" {
		return err.Error()
	}
	return fmt.Sprintf("Crew %s was created but not finished: %v. Fix it with workspace action=update.", crewID, err)
}

func remarshalExternalArgs(raw any, out any) error {
	encoded, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	return json.Unmarshal(encoded, out)
}

// createCrewFromSpec creates a Crew in the caller's own tree with the UI
// creation layout, then applies files, skills, functions and
// schedules. The spec is fully checked first; a later storage failure
// returns the new Crew's ID so the author can finish it with update_crew.
func (api *StreamingAPI) createCrewFromSpec(ctx context.Context, claims *UserClaims, spec crewSpec, enableSchedules bool) (string, error) {
	return api.createWorkspaceFromSpec(ctx, claims, "work", spec, enableSchedules)
}

func (api *StreamingAPI) createWorkspaceFromSpec(ctx context.Context, claims *UserClaims, profileID string, spec crewSpec, enableSchedules bool, reservedID ...string) (string, error) {
	creation := workspaceCreation{}
	if len(reservedID) > 0 {
		creation.ReservedID = reservedID[0]
	}
	return api.createWorkspace(ctx, claims, profileID, spec, enableSchedules, creation)
}

// workspaceCreation carries what only server code may choose for a new Crew: its identity and its platform role.
type workspaceCreation struct {
	ReservedID string
	Role       string
}

func (api *StreamingAPI) createWorkspace(ctx context.Context, claims *UserClaims, profileID string, spec crewSpec, enableSchedules bool, creation workspaceCreation) (string, error) {
	// A platform-made Crew (its role is set by server code only) is the server's act, not the person's, so the
	// person's create permission does not apply: everyone gets their AgentWorks.
	if creation.Role == "" {
		if status, message := projectCreationGate(claims, profileID); status != 0 {
			return "", fmt.Errorf("%s", message)
		}
	} else if claims == nil || strings.TrimSpace(claims.UserID) == "" {
		return "", fmt.Errorf("Sign in to create a Crew.")
	}
	spec, err := normalizeCrewSpec(spec)
	if err != nil {
		return "", err
	}
	if spec.RootPath != "" {
		spec.RootPath, err = validateNewLocalCrewRoot(spec.RootPath)
		if err != nil {
			return "", err
		}
		if creation.Role != "" {
			return "", fmt.Errorf("platform Crews use managed folders")
		}
	}
	if spec.Mode != "" && spec.Mode != "dev" && spec.Mode != "cowork" {
		return "", fmt.Errorf("Choose Dev or Cowork for this Crew")
	}
	if err := checkCrewSkillsAvailable(ctx, "", spec.SelectedSkills, spec.Files); err != nil {
		return "", err
	}
	if len(spec.Schedules) > 0 && api.projectActions == nil {
		return "", fmt.Errorf("schedules are unavailable on this server")
	}
	if api.agentProfiles == nil {
		return "", fmt.Errorf("the Crew product is unavailable")
	}
	userID := claims.UserID
	profile, err := api.agentProfiles.Resolve(profileID, 0, userID)
	if err != nil {
		return "", err
	}
	projectsRoot, err := projectProfileProjectsRoot(profile, userID)
	if err != nil {
		return "", fmt.Errorf("invalid Crew projects root: %w", err)
	}
	runtimeRoot := crewCreationRoot(profileID, agentProfileRuntimeWorkspace(userID, projectsRoot))
	crewID := uuid.NewString()
	if creation.ReservedID != "" {
		if _, err := uuid.Parse(creation.ReservedID); err != nil {
			return "", fmt.Errorf("invalid reserved project identity")
		}
		crewID = creation.ReservedID
	}
	workspacePath := crewCreationPath(runtimeRoot, spec.Name, crewID[:8])
	if projectExistsAtPath(ctx, workspacePath) {
		return "", fmt.Errorf("crew folder collision; retry")
	}
	if _, err := writeCrewCreationManifests(ctx, userID, profile, profileID, "", spec.Name, spec.Purpose, spec.Icon, crewID, workspacePath, nil, crewCreationOptions{Mode: spec.Mode, LLMConfig: spec.LLMConfig, Role: creation.Role, LocalRoot: spec.RootPath}); err != nil {
		return "", err
	}
	if spec.ChatInstructions != "" {
		if err := updateCrewChatInstructions(ctx, workspacePath, spec.ChatInstructions); err != nil {
			return crewID, err
		}
	}
	if folder, shared := workspaceref.MustParse(workspacePath).SharedProject(); shared {
		defer ensureSharedCrewFolderAccess(folder, userID)
	}
	if err := writeCrewSpecFiles(ctx, workspacePath, spec.Files); err != nil {
		return crewID, err
	}
	if err := applyCrewCreationSelections(ctx, profileID, workspacePath, spec.SelectedSkills, nil, nil, nil); err != nil {
		return crewID, err
	}
	if len(spec.Functions) > 0 {
		target := triggerTarget{Kind: triggerCallerCrew, Path: workspacePath, Label: spec.Name, CrewID: crewID, CrewProfile: profileID, CrewOwner: sanitizeUserIDForPath(userID)}
		functions := []crewFunction{}
		now := time.Now().UTC()
		for _, fn := range spec.Functions {
			functions, _ = upsertCrewFunction(functions, fn, externalCrewAuthorStamp(claims), now)
		}
		if err := writeCrewFunctions(ctx, target, functions); err != nil {
			return crewID, err
		}
	}
	for _, scheduleSpec := range spec.Schedules {
		schedule, _ := scheduleSpec.newSchedule()
		if !enableSchedules {
			schedule.Enabled = false
		}
		if _, err := api.projectActions.CreateProjectSchedule(ctx, userID, profileID, crewID, schedule); err != nil {
			return crewID, fmt.Errorf("schedule %q: %w", schedule.Name, err)
		}
	}
	return crewID, nil
}

func externalCrewAuthorStamp(claims *UserClaims) string {
	return externalCrewCaller(claims).Label
}

// crewUpdate is update_crew's input: identity fields plus optional sections.
type crewUpdate struct {
	CrewID  string  `json:"crew_id"`
	Name    *string `json:"name"`
	Icon    *string `json:"icon"`
	Purpose *string `json:"purpose"`
	Skills  *struct {
		Set    *[]string `json:"set"`
		Add    []string  `json:"add"`
		Remove []string  `json:"remove"`
	} `json:"skills"`
	Functions *struct {
		Upsert []crewFunctionSpec `json:"upsert"`
		Delete []string           `json:"delete"`
	} `json:"functions"`
	Schedules *struct {
		Add    []crewScheduleSpec `json:"add"`
		Update []crewScheduleSpec `json:"update"`
		Remove []string           `json:"remove"`
	} `json:"schedules"`
	Files       map[string]string `json:"files"`
	RemoveFiles []string          `json:"remove_files"`
}

// updateCrewFromSpec checks every section before writing any of them, then
// applies identity, files, skills, functions and schedules in that order.
func (api *StreamingAPI) updateCrewFromSpec(ctx context.Context, claims *UserClaims, crew crewProjectBinding, manifest productProjectManifest, update crewUpdate) error {
	root := crew.Binding.WorkspacePath
	if reason := publishedWriteRefusal(ctx, root); reason != "" {
		return fmt.Errorf("%s", reason)
	}
	trimmed := func(value *string) *string {
		if value == nil {
			return nil
		}
		out := strings.TrimSpace(*value)
		return &out
	}
	name, icon, purpose := trimmed(update.Name), trimmed(update.Icon), trimmed(update.Purpose)
	identityChanged := name != nil || icon != nil || purpose != nil
	if identityChanged {
		pick := func(value *string, current string) string {
			if value != nil {
				return *value
			}
			return strings.TrimSpace(current)
		}
		finalName := pick(name, firstNonEmptyTrimmed(manifest.Identity.Name, manifest.Title))
		if err := crewIdentityChecks(finalName, pick(icon, manifest.Identity.Icon), pick(purpose, manifest.Description)); err != nil {
			return err
		}
	}
	if err := checkCrewSpecFiles(update.Files); err != nil {
		return err
	}
	for _, rel := range update.RemoveFiles {
		if _, err := crewSpecFilePath(root, rel); err != nil {
			return err
		}
	}
	var skillSet, skillAdd, skillRemove []string
	if update.Skills != nil {
		var err error
		if update.Skills.Set != nil {
			if skillSet, err = validateCrewCreationNames("skill", *update.Skills.Set); err != nil {
				return err
			}
		}
		if skillAdd, err = validateCrewCreationNames("skill", update.Skills.Add); err != nil {
			return err
		}
		if skillRemove, err = validateCrewCreationNames("skill", update.Skills.Remove); err != nil {
			return err
		}
		if err := checkCrewSkillsAvailable(ctx, root, append(append([]string{}, skillSet...), skillAdd...), update.Files); err != nil {
			return err
		}
	}
	target := triggerTarget{Kind: triggerCallerCrew, Path: root, Label: manifest.displayTitle(), CrewID: manifest.ID, CrewProfile: manifest.Product, CrewOwner: crew.OwnerID}
	var functions []crewFunction
	if update.Functions != nil {
		var err error
		if functions, err = readCrewFunctions(ctx, target); err != nil {
			return err
		}
		if len(update.Functions.Upsert) > crewSpecMaxFunctions {
			return fmt.Errorf("at most %d functions per call", crewSpecMaxFunctions)
		}
		for i := range update.Functions.Upsert {
			update.Functions.Upsert[i] = update.Functions.Upsert[i].normalized()
			if err := update.Functions.Upsert[i].validate(); err != nil {
				return fmt.Errorf("function %q: %w", update.Functions.Upsert[i].Name, err)
			}
		}
		for _, remove := range update.Functions.Delete {
			if _, found := findCrewFunction(functions, strings.TrimSpace(remove)); !found {
				return fmt.Errorf("crew has no function %q", remove)
			}
		}
	}
	var scheduleAdds []productschedule.Schedule
	// A retry after a dropped connection must be safe: an add whose name already exists is already there, and a
	// remove of an id that is gone is already removed (PLAT-837).
	scheduleExists := map[string]bool{}
	scheduleNames := map[string]bool{}
	if update.Schedules != nil {
		if api.projectActions == nil {
			return fmt.Errorf("schedules are unavailable on this server")
		}
		current := map[string]productschedule.Schedule{}
		for _, schedule := range manifest.Schedules {
			current[schedule.ID] = schedule
			scheduleExists[schedule.ID] = true
			scheduleNames[strings.ToLower(strings.TrimSpace(schedule.Name))] = true
		}
		for i, spec := range update.Schedules.Add {
			schedule, err := spec.newSchedule()
			if err != nil {
				return fmt.Errorf("new schedule %d: %w", i+1, err)
			}
			if name := strings.ToLower(strings.TrimSpace(schedule.Name)); name != "" && scheduleNames[name] {
				continue
			}
			scheduleNames[strings.ToLower(strings.TrimSpace(schedule.Name))] = true
			scheduleAdds = append(scheduleAdds, schedule)
		}
		for _, spec := range update.Schedules.Update {
			existing, ok := current[strings.TrimSpace(spec.ID)]
			if !ok {
				return fmt.Errorf("crew has no schedule %q", spec.ID)
			}
			if err := spec.applyTo(&existing); err != nil {
				return err
			}
			if err := productschedule.Validate(existing); err != nil {
				return err
			}
		}
	}

	if identityChanged {
		if err := updateCrewIdentity(ctx, root, name, icon, purpose); err != nil {
			return err
		}
	}
	if err := writeCrewSpecFiles(ctx, root, update.Files); err != nil {
		return err
	}
	for _, rel := range update.RemoveFiles {
		full, _ := crewSpecFilePath(root, rel)
		if err := deleteWorkspaceFile(ctx, full); err != nil {
			return fmt.Errorf("remove %s: %w", rel, err)
		}
	}
	if update.Skills != nil {
		if err := updateProductSelectedSkills(ctx, manifest.Product, root, func(current []string) []string {
			if update.Skills.Set != nil {
				current = skillSet
			}
			drop := map[string]bool{}
			for _, name := range skillRemove {
				drop[name] = true
			}
			next := []string{}
			for _, name := range append(append([]string{}, current...), skillAdd...) {
				if !drop[name] {
					next = append(next, name)
				}
			}
			return next
		}); err != nil {
			return fmt.Errorf("update skills: %w", err)
		}
	}
	if update.Functions != nil {
		now := time.Now().UTC()
		for _, fn := range update.Functions.Upsert {
			functions, _ = upsertCrewFunction(functions, fn, externalCrewAuthorStamp(claims), now)
		}
		drop := map[string]bool{}
		for _, name := range update.Functions.Delete {
			drop[strings.TrimSpace(name)] = true
		}
		kept := functions[:0]
		for _, fn := range functions {
			if !drop[fn.Name] {
				kept = append(kept, fn)
			}
		}
		if err := writeCrewFunctions(ctx, target, kept); err != nil {
			return err
		}
	}
	if update.Schedules != nil {
		userID := claims.UserID
		for _, schedule := range scheduleAdds {
			if _, err := api.projectActions.CreateProjectSchedule(ctx, userID, manifest.Product, manifest.ID, schedule); err != nil {
				return fmt.Errorf("schedule %q: %w", schedule.Name, err)
			}
		}
		for _, spec := range update.Schedules.Update {
			var applyErr error
			if _, err := api.projectActions.UpdateProjectSchedule(ctx, userID, projectScheduleJobID(manifest.Product, manifest.ID, strings.TrimSpace(spec.ID)), func(schedule *productschedule.Schedule) {
				applyErr = spec.applyTo(schedule)
			}); err != nil {
				return fmt.Errorf("schedule %q: %w", spec.ID, err)
			}
			if applyErr != nil {
				return applyErr
			}
		}
		for _, id := range update.Schedules.Remove {
			if !scheduleExists[strings.TrimSpace(id)] {
				continue
			}
			if err := api.projectActions.DeleteProjectSchedule(ctx, userID, projectScheduleJobID(manifest.Product, manifest.ID, strings.TrimSpace(id))); err != nil {
				return fmt.Errorf("schedule %q: %w", id, err)
			}
		}
	}
	return nil
}

// updateCrewIdentity edits product.json the way set_work_identity and the
// Identity panel do: omitted fields are preserved, purpose is the project
// description, and unrelated manifest fields pass through untouched.
func updateCrewIdentity(ctx context.Context, root string, name, icon, purpose *string) error {
	manifestPath := root + "/product.json"
	lock := productConversationRegistryMutex(manifestPath)
	lock.Lock()
	defer lock.Unlock()
	raw, found, err := readFileFromWorkspace(ctx, manifestPath)
	if err != nil || !found {
		return firstError(err, fmt.Errorf("crew manifest not found"))
	}
	var document map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &document); err != nil {
		return fmt.Errorf("decode crew manifest: %w", err)
	}
	identity, _ := document["identity"].(map[string]interface{})
	if identity == nil {
		identity = map[string]interface{}{}
	}
	for key, value := range map[string]*string{"name": name, "icon": icon} {
		if value == nil {
			continue
		}
		if *value == "" {
			delete(identity, key)
		} else {
			identity[key] = *value
		}
	}
	document["identity"] = identity
	if purpose != nil {
		document["description"] = *purpose
	}
	document["updated_at"] = time.Now().UTC().Format(time.RFC3339)
	encoded, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return err
	}
	return writeFileToWorkspace(ctx, manifestPath, string(encoded)+"\n")
}

// exportCrewSpec renders a Crew as a portable spec: identity, skills with
// the project-local skill files they need, declared functions, schedules
// (without IDs). Private areas (chats,
// databases, MEMORY.md, secrets, model connections) are never exported.
func exportCrewSpec(ctx context.Context, crew crewProjectBinding, manifest productProjectManifest) (crewSpec, error) {
	root := crew.Binding.WorkspacePath
	spec := crewSpec{
		SchemaVersion: crewSpecSchemaVersion, Kind: crewSpecKind,
		Name: manifest.displayTitle(), Icon: strings.TrimSpace(manifest.Identity.Icon),
		Purpose:          strings.TrimSpace(manifest.Description),
		ChatInstructions: manifest.ChatInstructions,
		SelectedSkills:   append([]string(nil), manifest.Capabilities.SelectedSkills...),
	}
	files := map[string]string{}
	for _, name := range spec.SelectedSkills {
		folder := root + "/skills/" + name
		listing, exists, err := listWorkspaceFolder(ctx, folder, sharedProjectFileTreeDepth)
		if err != nil || !exists {
			continue
		}
		entries, _ := flattenSharedProjectFiles(root, listing)
		for _, entry := range entries {
			if entry.Type != "file" || !strings.HasPrefix(entry.Path, "skills/"+name+"/") {
				continue
			}
			content, found, err := readFileFromWorkspace(ctx, root+"/"+entry.Path)
			if err != nil {
				return spec, fmt.Errorf("read %s: %w", entry.Path, err)
			}
			if found && len(content) <= sharedProjectFileContentCap && !isSharedProjectBinaryContent(content) {
				files[entry.Path] = content
			}
		}
	}
	if len(files) > 0 {
		spec.Files = files
	}
	target := triggerTarget{Kind: triggerCallerCrew, Path: root, Label: manifest.displayTitle(), CrewID: manifest.ID, CrewProfile: manifest.Product, CrewOwner: crew.OwnerID}
	functions, err := readCrewFunctions(ctx, target)
	if err != nil {
		return spec, err
	}
	for _, fn := range functions {
		spec.Functions = append(spec.Functions, crewFunctionSpec{Name: fn.Name, Description: fn.Description, Instructions: fn.Instructions, InputSchema: fn.InputSchema, ResultSchema: fn.ResultSchema, Execution: fn.Execution, Agent: fn.Agent})
	}
	for _, schedule := range manifest.Schedules {
		spec.Schedules = append(spec.Schedules, crewScheduleSpecFrom(schedule, false))
	}
	return spec, nil
}

// externalCrewDescription is get_crew's answer: the full editable spec plus
// ownership and how the caller may use the Crew.
func externalCrewDescription(ctx context.Context, crew crewProjectBinding, manifest productProjectManifest, summary map[string]interface{}) map[string]any {
	label := fmt.Sprint(summary["name"])
	description := strings.TrimSpace(manifest.Description)
	schedules := []crewScheduleSpec{}
	for _, schedule := range manifest.Schedules {
		schedules = append(schedules, crewScheduleSpecFrom(schedule, true))
	}
	yourAccess := "run"
	if crew.OwnedByCaller {
		yourAccess = "owner"
		if crew.CoOwner {
			yourAccess = "co_owner"
		}
	}
	out := map[string]any{
		"crew_id": manifest.ID, "name": label, "identity": summary["identity"],
		"purpose": description, "description": description, "chat_instructions": manifest.ChatInstructions,
		"owner":     summary["owner"],
		"skills":    append([]string{}, manifest.Capabilities.SelectedSkills...),
		"schedules": schedules,
		"functions": externalCrewFunctionSummaries(ctx, crew, manifest, label),
		"access": map[string]any{
			"owner": summary["owner"], "your_access": yourAccess, "can_edit": crew.OwnedByCaller,
			"sharing": crewSharingModel,
		},
	}
	if files := crewFilesPath(crew.Binding.WorkspacePath); files != crew.Binding.WorkspacePath {
		out["files_path"] = files
		out["user_files_prefix"] = "_files/"
	}
	if llm := manifest.Capabilities.LLMConfig; llm != nil {
		out["model"] = map[string]any{"mode": llm.Mode, "provider": llm.Provider}
	}
	return out
}

// externalCrewSpecSchemas returns the catalog schema for a crew spec's
// properties plus the function and schedule item schemas update_crew reuses.
func externalCrewSpecSchemas() (map[string]any, map[string]any, map[string]any) {
	str := func(description string) map[string]any {
		return map[string]any{"type": "string", "description": description}
	}
	jsonSchema := map[string]any{"type": "object", "description": "JSON Schema subset: type object|array|string|number|integer|boolean, properties, required, items, enum."}
	fnSpec := map[string]any{"type": "object", "additionalProperties": false, "required": []any{"name", "description", "instructions"}, "properties": map[string]any{
		"name":          str("snake_case function name, e.g. triage_ticket."),
		"description":   str("What the function does, for callers."),
		"instructions":  str("What the Crew does when this function is called."),
		"input_schema":  jsonSchema,
		"result_schema": jsonSchema,
		"execution":     map[string]any{"type": "string", "enum": []any{"tab", "fresh"}, "description": crewFunctionExecutionHelp},
		"agent":         map[string]any{"type": "string", "description": crewFunctionAgentHelp},
	}}
	scheduleSpec := map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{
		"id":              str("Schedule ID (update only; from workspace action=get)."),
		"name":            str("Schedule name."),
		"description":     str("Optional note."),
		"message":         str("The instruction sent to the Crew on each run."),
		"messages":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Several instructions, sent one turn at a time."},
		"cron_expression": str("Five-field cron, e.g. 0 9 * * 1-5."),
		"timezone":        str("IANA timezone, e.g. Asia/Kolkata."),
		"cadence_hours":   map[string]any{"type": "integer", "minimum": 0, "description": "Run this many hours after the last run (instead of cron)."},
		"preferred_hour":  map[string]any{"type": "integer", "minimum": 0, "maximum": 23},
		"enabled":         map[string]any{"type": "boolean"},
		"run_destination": map[string]any{"type": "string", "enum": []any{runDestinationChat, runDestinationIsolated}, "description": "chat (default) or isolated."},
	}}
	props := map[string]any{
		"root_path":         str("Local app only, at creation: full path of an existing folder to use directly. Crew resources are kept in its .agentworks folder; existing files and instructions are preserved. Omit for a managed folder."),
		"schema_version":    map[string]any{"type": "integer"},
		"kind":              map[string]any{"type": "string", "enum": []any{crewSpecKind}},
		"name":              map[string]any{"type": "string", "minLength": 1, "maxLength": 60, "description": "Display name."},
		"icon":              map[string]any{"type": "string", "maxLength": 8, "description": "One emoji or short glyph."},
		"chat_instructions": map[string]any{"type": "string", "maxLength": workflowtypes.ChatInstructionsLimit, "description": "Optional shared app chat guidance. No secrets; never grants authority."},
		"purpose":           map[string]any{"type": "string", "minLength": 1, "maxLength": crewPurposeLimit, "description": "What the Crew is for and how it works; its standing instructions."},
		"selected_skills":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Skill folder names: built-in, installed on the server, or shipped under files as skills/<name>/SKILL.md."},
		"files":             map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}, "description": "Crew-relative text files (path -> content), e.g. skills/<name>/SKILL.md. Manifests, functions.json, builder/ and db/ are refused."},
		"functions":         map[string]any{"type": "array", "items": fnSpec},
		"schedules":         map[string]any{"type": "array", "items": scheduleSpec},
	}
	return props, fnSpec, scheduleSpec
}

// externalCrewUpdateReply is update_crew's answer unless the caller asks for the whole spec (return_spec): what was
// applied and the Crew's current names, not every function's instructions and schemas, which cost thousands of tokens
// for a one-file change. get_crew returns the full description.
func externalCrewUpdateReply(full map[string]any, update crewUpdate) map[string]any {
	changed := []string{}
	for name, set := range map[string]bool{
		"name": update.Name != nil, "icon": update.Icon != nil, "purpose": update.Purpose != nil,
		"skills": update.Skills != nil, "functions": update.Functions != nil, "schedules": update.Schedules != nil,
		"files": len(update.Files) > 0, "remove_files": len(update.RemoveFiles) > 0,
	} {
		if set {
			changed = append(changed, name)
		}
	}
	sort.Strings(changed)
	functions := []string{}
	if list, ok := full["functions"].([]map[string]interface{}); ok {
		for _, fn := range list {
			functions = append(functions, fmt.Sprint(fn["name"]))
		}
	}
	schedules := []map[string]any{}
	if list, ok := full["schedules"].([]crewScheduleSpec); ok {
		for _, schedule := range list {
			entry := map[string]any{"id": schedule.ID, "name": schedule.Name}
			if schedule.Enabled != nil {
				entry["enabled"] = *schedule.Enabled
			}
			schedules = append(schedules, entry)
		}
	}
	files := make([]string, 0, len(update.Files))
	for path := range update.Files {
		files = append(files, path)
	}
	sort.Strings(files)
	return map[string]any{
		"crew_id": full["crew_id"], "name": full["name"], "updated": true,
		"changed": changed, "skills": full["skills"], "functions": functions, "schedules": schedules,
		"files_written": files, "files_removed": update.RemoveFiles,
		"note": "Compact reply. workspace action=get returns the full spec; pass return_spec=true to get it here.",
	}
}

// externalWriteCrewFile writes one project file of a Crew its caller owns, text or binary (an image, PDF or spreadsheet),
// through the same guarded, revision-checked writer as a workflow's write_file. The Crew's manifests, functions.json, its
// chats (builder/), databases and hidden folders are refused. Everyone who can run the Crew may write under shared/<their id>/ (bounded in size); the owner writes anywhere editable. Without expected_revision the file's current revision is
// read first, so the write replaces it (update_crew files does the same); a request_id makes a retry safe.
func (api *StreamingAPI) externalWriteCrewFile(w http.ResponseWriter, r *http.Request, claims *UserClaims, args map[string]any) {
	ctx := r.Context()
	crew, _, _, ok := api.externalCrewResolve(ctx, claims, externalArg(args, "crew_id"))
	if !ok {
		externalError(w, http.StatusNotFound, "not_found", "Crew not found or not allowed for this connection.")
		return
	}
	rel := externalArg(args, "path")
	root := agentProfileRuntimeWorkspace(crew.OwnerID, crew.Binding.WorkspacePath)
	clean, err := wf.CleanRelative(rel)
	// Everyone who can run the Crew has one place to put files: shared/<their id>/. The owner writes anywhere editable.
	sharedFolder := "shared/" + sanitizeUserIDForPath(claims.UserID)
	inOwnShared := err == nil && strings.HasPrefix(clean, sharedFolder+"/")
	if !crew.OwnedByCaller && !inOwnShared {
		externalError(w, http.StatusForbidden, "forbidden", "Only the Crew's owner can write its files; you can add files under "+sharedFolder+"/ .")
		return
	}
	if crew.OwnedByCaller && !inOwnShared && claims.AccessToken != nil && !claims.AccessToken.Allows("crews:write") {
		externalError(w, http.StatusForbidden, "insufficient_scope", "Writing outside "+sharedFolder+"/ needs crews:write.")
		return
	}
	if _, pathErr := crewSpecFilePath(root, rel); pathErr != nil || err != nil || wf.ProtectedWrite(clean) {
		externalError(w, http.StatusForbidden, "protected_path", "That path is not an editable Crew file (manifests, functions.json, chats, databases and hidden folders are protected).")
		return
	}
	_, hasText := args["content"]
	_, hasBinary := args["content_base64"]
	if hasText == hasBinary {
		externalError(w, http.StatusBadRequest, "invalid_arguments", "Pass content (UTF-8 text) or content_base64 (a binary file), exactly one.")
		return
	}
	if !crew.OwnedByCaller {
		if message := externalCrewSharedFolderRoom(ctx, root, sharedFolder, clean, args); message != "" {
			externalError(w, http.StatusRequestEntityTooLarge, "quota_exceeded", message)
			return
		}
	}
	expected := externalArg(args, "expected_revision")
	if expected == "" {
		current, err := externalFileRequest(ctx, wf.Request{Root: root, Operation: "read", Path: clean})
		if err != nil {
			externalFailure(w, err)
			return
		}
		expected = wf.MissingRevision
		if current.Exists {
			expected = current.Revision
		}
	}
	requestID := externalArg(args, "request_id")
	if requestID == "" {
		requestID = uuid.NewString()
	}
	req := wf.WriteRequest{Root: root, Path: rel, Content: externalArg(args, "content"), ContentBase64: externalArg(args, "content_base64"), ExpectedRevision: expected, RequestID: requestID,
		Actor: claims.UserID, Identity: wf.EditIdentity{UserID: claims.UserID, Username: claims.Username, Source: "public_mcp"}}
	if claims.AccessToken != nil {
		req.Identity.ConnectionID = claims.AccessToken.ID
		req.Guard = claims.AccessToken.FileGuard
		req.Actor += "/" + claims.AccessToken.ID
	}
	externalPostSharedFileWrite(w, r, req)
}

// Limits for what a person who does not own the Crew may leave in their shared/<id>/ folder, so one account cannot fill
// the disk: a file at most 5 MiB (decoded), the folder at most 50 MiB and 200 files.
const (
	crewSharedFileMaxBytes   = 5 << 20
	crewSharedFolderMaxBytes = 50 << 20
	crewSharedFolderMaxFiles = 200
)

// externalCrewSharedFolderRoom returns why a non-owner's write does not fit, or "" when it does. Replacing an existing
// file counts only the growth.
func externalCrewSharedFolderRoom(ctx context.Context, root, folder, clean string, args map[string]any) string {
	size := len(externalArg(args, "content"))
	if encoded := externalArg(args, "content_base64"); encoded != "" {
		size = base64.StdEncoding.DecodedLen(len(encoded))
	}
	if size > crewSharedFileMaxBytes {
		return fmt.Sprintf("A file in %s/ may be at most %d MiB.", folder, crewSharedFileMaxBytes>>20)
	}
	listing, err := externalFileRequest(ctx, wf.Request{Root: root, Operation: "list", Path: folder, Depth: 8, Limit: 1000})
	if err != nil {
		return ""
	}
	var total int64
	files, replacing := 0, int64(0)
	for _, entry := range listing.Entries {
		if entry.Type != "file" {
			continue
		}
		files++
		total += entry.Size
		if entry.Path == clean || strings.TrimPrefix(entry.Path, root+"/") == clean {
			replacing = entry.Size
		}
	}
	if replacing == 0 && files >= crewSharedFolderMaxFiles {
		return fmt.Sprintf("%s/ already holds %d files, the most allowed.", folder, files)
	}
	if total-replacing+int64(size) > crewSharedFolderMaxBytes {
		return fmt.Sprintf("%s/ would pass %d MiB.", folder, crewSharedFolderMaxBytes>>20)
	}
	return ""
}
