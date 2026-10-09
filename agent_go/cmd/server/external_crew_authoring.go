package server

import (
	"context"
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
)

// External Crew authoring (crews:write): create a Crew from a local CLI,
// edit everything about it, and move it between accounts and servers as a
// portable spec. Every write reuses the path the app already uses for that
// part of a Crew: the server-side creation layout (writeCrewCreationManifests),
// the playbook template installer, the capability selection writer, the
// functions.json writer with define_function's checks, and the project
// schedule service.
//
// Access: Crews have one owner (crew_access.go). Only the owner edits a Crew,
// exactly as in the app; every other user with the Crew product runs it.
// Read-only accounts and disabled accounts never write.

var externalCrewWriteTools = map[string]bool{"create_crew": true, "update_crew": true, "import_crew": true}

const (
	crewSpecKind          = "agentworks.crew"
	crewSpecSchemaVersion = 1
	crewSpecMaxFiles      = 200
	crewSpecMaxFilesBytes = 4 << 20
	crewSpecMaxFunctions  = 50
	crewSpecMaxSchedules  = 20
	crewPurposeLimit      = 1000
	crewSharingModel      = "Crews have a single owner. Every user with the Crew product can open a Crew in Run mode and call its functions; only the owner edits it. There is no per-Crew share list."
)

// crewSpec is the portable Crew definition: create_crew's input, export_crew's
// output and import_crew's input. Its core fields (name, role, purpose,
// selected_skills, files) are the Crew Agent Playbook catalog entry fields
// (crewAgentTemplate), so a catalog agent entry imports as-is.
type crewSpec struct {
	SchemaVersion  int                `json:"schema_version,omitempty"`
	Kind           string             `json:"kind,omitempty"`
	Name           string             `json:"name"`
	Icon           string             `json:"icon,omitempty"`
	Role           string             `json:"role"`
	Purpose        string             `json:"purpose"`
	SelectedSkills []string           `json:"selected_skills,omitempty"`
	Files          map[string]string  `json:"files,omitempty"`
	Functions      []crewFunctionSpec `json:"functions,omitempty"`
	Schedules      []crewScheduleSpec `json:"schedules,omitempty"`
	Templates      []crewTemplateRef  `json:"templates,omitempty"`
}

// crewTemplateRef names an installed first-party Crew Agent Playbook.
type crewTemplateRef struct {
	ID      string `json:"id"`
	Version int    `json:"version,omitempty"`
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
func crewIdentityChecks(name, icon, role, purpose string) error {
	if name == "" || utf8.RuneCountInString(name) > 60 {
		return fmt.Errorf("crew name must be 1-60 characters")
	}
	if utf8.RuneCountInString(icon) > 8 {
		return fmt.Errorf("crew icon must be at most 8 characters")
	}
	if role == "" || utf8.RuneCountInString(role) > 120 {
		return fmt.Errorf("crew role is required (at most 120 characters)")
	}
	if purpose == "" || utf8.RuneCountInString(purpose) > crewPurposeLimit {
		return fmt.Errorf("crew purpose is required (at most %d characters)", crewPurposeLimit)
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
	for _, name := range names {
		if skills.IsBuiltinSkill(name) {
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
func normalizeCrewSpec(spec crewSpec) (crewSpec, []*crewAgentTemplate, error) {
	spec.Name, spec.Icon = strings.TrimSpace(spec.Name), strings.TrimSpace(spec.Icon)
	spec.Role, spec.Purpose = strings.TrimSpace(spec.Role), strings.TrimSpace(spec.Purpose)
	if spec.Kind != "" && spec.Kind != crewSpecKind {
		return spec, nil, fmt.Errorf("spec kind must be %q", crewSpecKind)
	}
	if spec.SchemaVersion > crewSpecSchemaVersion {
		return spec, nil, fmt.Errorf("spec schema_version %d is newer than this server supports (%d)", spec.SchemaVersion, crewSpecSchemaVersion)
	}
	if err := crewIdentityChecks(spec.Name, spec.Icon, spec.Role, spec.Purpose); err != nil {
		return spec, nil, err
	}
	names, err := validateCrewCreationNames("skill", spec.SelectedSkills)
	if err != nil {
		return spec, nil, err
	}
	spec.SelectedSkills = names
	if err := checkCrewSpecFiles(spec.Files); err != nil {
		return spec, nil, err
	}
	if len(spec.Functions) > crewSpecMaxFunctions || len(spec.Schedules) > crewSpecMaxSchedules {
		return spec, nil, fmt.Errorf("at most %d functions and %d schedules", crewSpecMaxFunctions, crewSpecMaxSchedules)
	}
	seen := map[string]bool{}
	for i := range spec.Functions {
		spec.Functions[i] = spec.Functions[i].normalized()
		if err := spec.Functions[i].validate(); err != nil {
			return spec, nil, fmt.Errorf("function %q: %w", spec.Functions[i].Name, err)
		}
		if seen[spec.Functions[i].Name] {
			return spec, nil, fmt.Errorf("function %q is declared twice", spec.Functions[i].Name)
		}
		seen[spec.Functions[i].Name] = true
	}
	for i, schedule := range spec.Schedules {
		if _, err := schedule.newSchedule(); err != nil {
			return spec, nil, fmt.Errorf("schedule %d: %w", i+1, err)
		}
	}
	var templates []*crewAgentTemplate
	for _, ref := range spec.Templates {
		template, err := loadCrewAgentTemplate(strings.TrimSpace(ref.ID))
		if err != nil {
			return spec, nil, err
		}
		if template != nil {
			templates = append(templates, template)
		}
	}
	return spec, templates, nil
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
	if !userAllowedProduct(claims, "work") {
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
	if !userAccessForClaims(claims).CanCreate {
		return http.StatusForbidden, "This account cannot create Crews or workflows. An administrator can give it the create permission."
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
		crewID, err := api.createCrewFromSpec(ctx, claims, spec, enableSchedules)
		if err != nil {
			status := http.StatusBadRequest
			if crewID != "" {
				status = http.StatusBadGateway
			}
			externalError(w, status, "crew_not_created", externalCrewCreateError(crewID, err))
			return
		}
		crew, manifest, summary, ok := api.externalCrewResolve(ctx, claims, crewID)
		if !ok {
			externalJSON(w, map[string]any{"crew_id": crewID, "created": true})
			return
		}
		out := externalCrewDescription(ctx, crew, manifest, summary)
		out["created"] = true
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
		externalJSON(w, out)
	default:
		externalError(w, http.StatusNotFound, "unknown_tool", "Tool is not exposed by this API.")
	}
}

func externalCrewCreateError(crewID string, err error) string {
	if crewID == "" {
		return err.Error()
	}
	return fmt.Sprintf("Crew %s was created but not finished: %v. Fix it with update_crew.", crewID, err)
}

func remarshalExternalArgs(raw any, out any) error {
	encoded, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	return json.Unmarshal(encoded, out)
}

// createCrewFromSpec creates a Crew in the caller's own tree with the UI
// creation layout, then applies templates, files, skills, functions and
// schedules. The spec is fully checked first; a later storage failure
// returns the new Crew's ID so the author can finish it with update_crew.
func (api *StreamingAPI) createCrewFromSpec(ctx context.Context, claims *UserClaims, spec crewSpec, enableSchedules bool) (string, error) {
	spec, templates, err := normalizeCrewSpec(spec)
	if err != nil {
		return "", err
	}
	incoming := map[string]string{}
	for _, template := range templates {
		for rel, content := range template.Files {
			incoming[rel] = content
		}
	}
	for rel, content := range spec.Files {
		incoming[rel] = content
	}
	if err := checkCrewSkillsAvailable(ctx, "", spec.SelectedSkills, incoming); err != nil {
		return "", err
	}
	if len(spec.Schedules) > 0 && api.productSchedules == nil {
		return "", fmt.Errorf("schedules are unavailable on this server")
	}
	if api.agentProfiles == nil {
		return "", fmt.Errorf("the Crew product is unavailable")
	}
	userID := claims.UserID
	profile, err := api.agentProfiles.Resolve("work", 0, userID)
	if err != nil {
		return "", err
	}
	projectsRoot, err := cleanAgentProfileWorkspace(profile.Runtime.Workspace.ProjectsRoot, userID)
	if err != nil {
		return "", fmt.Errorf("invalid Crew projects root: %w", err)
	}
	runtimeRoot := crewCreationRoot("work", agentProfileRuntimeWorkspace(userID, projectsRoot))
	crewID := uuid.NewString()
	workspacePath := runtimeRoot + "/" + slugifyCrewTitle(spec.Name) + "-" + crewID[:8]
	if projectExistsAtPath(ctx, workspacePath) {
		return "", fmt.Errorf("crew folder collision; retry")
	}
	if _, err := writeCrewCreationManifests(ctx, userID, profile, "work", "", spec.Name, spec.Purpose, spec.Role, spec.Icon, crewID, workspacePath, nil); err != nil {
		return "", err
	}
	for _, template := range templates {
		if err := applyCrewAgentTemplate(ctx, workspacePath, template); err != nil {
			return crewID, err
		}
	}
	if err := writeCrewSpecFiles(ctx, workspacePath, spec.Files); err != nil {
		return crewID, err
	}
	if err := applyCrewCreationSelections(ctx, "work", workspacePath, spec.SelectedSkills, nil, nil, nil); err != nil {
		return crewID, err
	}
	if len(spec.Functions) > 0 {
		target := triggerTarget{Kind: triggerCallerCrew, Path: workspacePath, Label: spec.Name, CrewID: crewID, CrewProfile: "work", CrewOwner: sanitizeUserIDForPath(userID)}
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
		if _, err := api.productSchedules.CreateProjectSchedule(ctx, userID, "work", crewID, schedule); err != nil {
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
	Role    *string `json:"role"`
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
	trimmed := func(value *string) *string {
		if value == nil {
			return nil
		}
		out := strings.TrimSpace(*value)
		return &out
	}
	name, icon, role, purpose := trimmed(update.Name), trimmed(update.Icon), trimmed(update.Role), trimmed(update.Purpose)
	identityChanged := name != nil || icon != nil || role != nil || purpose != nil
	if identityChanged {
		pick := func(value *string, current string) string {
			if value != nil {
				return *value
			}
			return strings.TrimSpace(current)
		}
		finalName := pick(name, firstNonEmptyTrimmed(manifest.Identity.Name, manifest.Title))
		if err := crewIdentityChecks(finalName, pick(icon, manifest.Identity.Icon), pick(role, manifest.Identity.Role), pick(purpose, manifest.Description)); err != nil {
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
	target := triggerTarget{Kind: triggerCallerCrew, Path: root, Label: manifest.displayTitle(), CrewID: manifest.ID, CrewProfile: "work", CrewOwner: crew.OwnerID}
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
	if update.Schedules != nil {
		if api.productSchedules == nil {
			return fmt.Errorf("schedules are unavailable on this server")
		}
		current := map[string]productschedule.Schedule{}
		for _, schedule := range manifest.Schedules {
			current[schedule.ID] = schedule
		}
		for i, spec := range update.Schedules.Add {
			schedule, err := spec.newSchedule()
			if err != nil {
				return fmt.Errorf("new schedule %d: %w", i+1, err)
			}
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
		for _, id := range update.Schedules.Remove {
			if _, ok := current[strings.TrimSpace(id)]; !ok {
				return fmt.Errorf("crew has no schedule %q", id)
			}
		}
	}

	if identityChanged {
		if err := updateCrewIdentity(ctx, root, name, icon, role, purpose); err != nil {
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
		if err := updateProductSelectedSkills(ctx, "work", root, func(current []string) []string {
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
			if _, err := api.productSchedules.CreateProjectSchedule(ctx, userID, "work", manifest.ID, schedule); err != nil {
				return fmt.Errorf("schedule %q: %w", schedule.Name, err)
			}
		}
		for _, spec := range update.Schedules.Update {
			var applyErr error
			if _, err := api.productSchedules.UpdateProjectSchedule(ctx, userID, projectScheduleJobID("work", manifest.ID, strings.TrimSpace(spec.ID)), func(schedule *productschedule.Schedule) {
				applyErr = spec.applyTo(schedule)
			}); err != nil {
				return fmt.Errorf("schedule %q: %w", spec.ID, err)
			}
			if applyErr != nil {
				return applyErr
			}
		}
		for _, id := range update.Schedules.Remove {
			if err := api.productSchedules.DeleteProjectSchedule(ctx, userID, projectScheduleJobID("work", manifest.ID, strings.TrimSpace(id))); err != nil {
				return fmt.Errorf("schedule %q: %w", id, err)
			}
		}
	}
	return nil
}

// updateCrewIdentity edits product.json the way set_work_identity and the
// Identity panel do: omitted fields are preserved, purpose is the project
// description, and unrelated manifest fields pass through untouched.
func updateCrewIdentity(ctx context.Context, root string, name, icon, role, purpose *string) error {
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
	for key, value := range map[string]*string{"name": name, "icon": icon, "role": role} {
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

// crewInstalledTemplates reads product.json's installed-template receipts.
func crewInstalledTemplates(ctx context.Context, root string) []crewTemplateRef {
	raw, found, err := readFileFromWorkspace(ctx, root+"/product.json")
	if err != nil || !found {
		return nil
	}
	var document struct {
		Templates []crewTemplateRef `json:"templates"`
	}
	if json.Unmarshal([]byte(raw), &document) != nil {
		return nil
	}
	return document.Templates
}

// exportCrewSpec renders a Crew as a portable spec: identity, skills with
// the project-local skill files they need, declared functions, schedules
// (without IDs) and installed template references. Private areas (chats,
// databases, MEMORY.md, secrets, model connections) are never exported.
func exportCrewSpec(ctx context.Context, crew crewProjectBinding, manifest productProjectManifest) (crewSpec, error) {
	root := crew.Binding.WorkspacePath
	spec := crewSpec{
		SchemaVersion: crewSpecSchemaVersion, Kind: crewSpecKind,
		Name: manifest.displayTitle(), Icon: strings.TrimSpace(manifest.Identity.Icon),
		Role: strings.TrimSpace(manifest.Identity.Role), Purpose: strings.TrimSpace(manifest.Description),
		SelectedSkills: append([]string(nil), manifest.Capabilities.SelectedSkills...),
		Templates:      crewInstalledTemplates(ctx, root),
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
	target := triggerTarget{Kind: triggerCallerCrew, Path: root, Label: manifest.displayTitle(), CrewID: manifest.ID, CrewProfile: "work", CrewOwner: crew.OwnerID}
	functions, err := readCrewFunctions(ctx, target)
	if err != nil {
		return spec, err
	}
	for _, fn := range functions {
		spec.Functions = append(spec.Functions, crewFunctionSpec{Name: fn.Name, Description: fn.Description, Instructions: fn.Instructions, InputSchema: fn.InputSchema, ResultSchema: fn.ResultSchema})
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
	}
	out := map[string]any{
		"crew_id": manifest.ID, "name": label, "identity": summary["identity"],
		"role": strings.TrimSpace(manifest.Identity.Role), "purpose": description, "description": description,
		"owner":     summary["owner"],
		"skills":    append([]string{}, manifest.Capabilities.SelectedSkills...),
		"schedules": schedules,
		"templates": crewInstalledTemplates(ctx, crew.Binding.WorkspacePath),
		"functions": externalCrewFunctionSummaries(ctx, crew, manifest, label),
		"access": map[string]any{
			"owner": summary["owner"], "your_access": yourAccess, "can_edit": crew.OwnedByCaller,
			"sharing": crewSharingModel,
		},
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
	}}
	scheduleSpec := map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{
		"id":              str("Schedule ID (update only; from get_crew)."),
		"name":            str("Schedule name."),
		"description":     str("Optional note."),
		"message":         str("The instruction sent to the Crew on each run."),
		"messages":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Several instructions, sent one turn at a time."},
		"cron_expression": str("Five-field cron, e.g. 0 9 * * 1-5."),
		"timezone":        str("IANA timezone, e.g. Asia/Kolkata."),
		"cadence_hours":   map[string]any{"type": "integer", "minimum": 0, "description": "Run this many hours after the last run (instead of cron)."},
		"preferred_hour":  map[string]any{"type": "integer", "minimum": 0, "maximum": 23},
		"enabled":         map[string]any{"type": "boolean"},
		"run_destination": map[string]any{"type": "string", "enum": []any{runDestinationCrewChat, runDestinationIsolated}, "description": "crew_chat (default) or isolated."},
	}}
	props := map[string]any{
		"schema_version":  map[string]any{"type": "integer"},
		"kind":            map[string]any{"type": "string", "enum": []any{crewSpecKind}},
		"name":            map[string]any{"type": "string", "minLength": 1, "maxLength": 60, "description": "Display name."},
		"icon":            map[string]any{"type": "string", "maxLength": 8, "description": "One emoji or short glyph."},
		"role":            map[string]any{"type": "string", "minLength": 1, "maxLength": 120, "description": "Short role, e.g. Support triage lead."},
		"purpose":         map[string]any{"type": "string", "minLength": 1, "maxLength": crewPurposeLimit, "description": "What the Crew is for and how it works; its standing instructions."},
		"selected_skills": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Skill folder names: built-in, installed on the server, or shipped under files as skills/<name>/SKILL.md."},
		"files":           map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}, "description": "Crew-relative text files (path -> content), e.g. skills/<name>/SKILL.md. Manifests, functions.json, builder/ and db/ are refused."},
		"functions":       map[string]any{"type": "array", "items": fnSpec},
		"schedules":       map[string]any{"type": "array", "items": scheduleSpec},
		"templates": map[string]any{"type": "array", "description": "First-party Crew Agent Playbook templates to install.", "items": map[string]any{"type": "object", "required": []any{"id"}, "properties": map[string]any{
			"id": map[string]any{"type": "string"}, "version": map[string]any{"type": "integer"},
		}}},
	}
	return props, fnSpec, scheduleSpec
}
