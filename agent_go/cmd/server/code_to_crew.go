package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"sort"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/codeproduct"
)

// A Code chat can build Crews (owner 2026-10-16): the same authoring tools MCP has (create_crew, update_crew,
// export_crew, write_crew_file, import_crew) with the same website rules about who may create or change a Crew, and one
// helper that makes a Crew from the Code project itself. The Code project stays as it is.

var codeCrewAuthoringTools = []string{"create_crew", "update_crew", "export_crew", "write_crew_file", "import_crew"}

// codeCrewToolText drops the MCP-only sentence about scopes from a tool description.
func codeCrewToolText(description string) string {
	if i := strings.Index(description, " Requires "); i >= 0 {
		description = description[:i]
	}
	return strings.TrimSpace(description)
}

func (api *StreamingAPI) registerCodeCrewTools(registrar definitionToolRegistrar, gate *productToolGate, userID, sessionID, workspacePath string) error {
	trusted := func(ctx context.Context) context.Context { return internalBotRequestContext(ctx, userID) }
	catalog, err := externalTools()
	if err != nil {
		return err
	}
	for _, name := range codeCrewAuthoringTools {
		for _, tool := range catalog {
			if tool.Name != name {
				continue
			}
			toolName := name
			if gate != nil {
				gate.Declare(toolName)
			}
			if err := registrar.RegisterCustomTool(toolName, codeCrewToolText(tool.Description), tool.InputSchema, func(ctx context.Context, args map[string]interface{}) (string, error) {
				return api.codeCrewAuthoringCall(trusted(ctx), toolName, args)
			}, "code_crew_tools"); err != nil {
				return err
			}
		}
	}
	if gate != nil {
		gate.Declare("make_crew_from_this_project")
	}
	return registrar.RegisterCustomTool("make_crew_from_this_project", "Make a new Crew from this Code project, so other people can use the work: it copies the project's files, PROJECT_INSTRUCTIONS.md, skills, MCP connections and secret selections (names only) and memory into a new Crew you own. Chat history is never copied and this Code project stays as it is. name is optional (default: the project's name). Then add functions with update_crew and share the Crew from its page.", map[string]interface{}{
		"type": "object", "properties": map[string]interface{}{"name": map[string]interface{}{"type": "string", "maxLength": 60}},
	}, func(ctx context.Context, args map[string]interface{}) (string, error) {
		out, err := api.makeCrewFromCodeProject(trusted(ctx), userID, sessionID, workspacePath, stringToolArg(args, "name"))
		if err != nil {
			return "", err
		}
		encoded, err := json.MarshalIndent(out, "", "  ")
		return string(encoded), err
	}, "code_crew_tools")
}

// codeCrewAuthoringCall runs one of the MCP Crew authoring handlers as the person in this chat. With no token on the
// caller, the handlers apply the website's account rules (create permission, Crew product, ownership of what is edited).
func (api *StreamingAPI) codeCrewAuthoringCall(ctx context.Context, name string, args map[string]interface{}) (string, error) {
	request := httptest.NewRequest(http.MethodPost, "/", nil).WithContext(ctx)
	recorder := &externalMCPRecorder{header: http.Header{}}
	api.externalCrewAuthoringCall(recorder, request, name, args)
	status := recorder.status
	if status == 0 {
		status = http.StatusOK
	}
	if status >= 300 {
		var failure struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(recorder.body.Bytes(), &failure)
		return "", fmt.Errorf("%s", firstNonEmptyTrimmed(failure.Error.Message, http.StatusText(status)))
	}
	return recorder.body.String(), nil
}

// makeCrewFromCodeProject copies this Code project into a new Crew owned by the same person.
func (api *StreamingAPI) makeCrewFromCodeProject(ctx context.Context, userID, sessionID, workspacePath, name string) (map[string]interface{}, error) {
	claims := GetUserFromContext(ctx)
	if claims == nil {
		claims = &UserClaims{UserID: userID}
	}
	if status, message := crewCreateGate(claims); status != 0 {
		return nil, fmt.Errorf("%s", message)
	}
	project, err := api.codeChatsFor(ctx, userID, sessionID, workspacePath)
	if err != nil {
		return nil, err
	}
	if project.OwnerID != userID {
		return nil, fmt.Errorf("only the owner of this Code makes a Crew from it")
	}
	binding, projectID, ok := api.externalCodeRunOwnProject(ctx, userID, project.ProjectID)
	if !ok {
		return nil, fmt.Errorf("this Code project is unavailable")
	}
	root := strings.TrimSuffix(strings.TrimSpace(binding.WorkspacePath), "/")
	store := defaultProductProjectStore()
	var manifest productProjectManifest
	if raw, found, e := store.read(ctx, root+"/product.json"); e == nil && found {
		_ = json.Unmarshal([]byte(raw), &manifest)
	}
	title := firstNonEmptyTrimmed(strings.TrimSpace(name), manifest.Title, manifest.Label, projectID)
	if r := []rune(title); len(r) > 60 {
		title = string(r[:60])
	}
	paths, _, err := store.listPaths(ctx, root)
	if err != nil {
		return nil, fmt.Errorf("could not read the project's files: %w", err)
	}
	sort.Strings(paths)
	files, skipped, total := map[string]string{}, []string{}, 0
	for _, full := range paths {
		rel := strings.TrimPrefix(strings.TrimPrefix(path.Clean(strings.ReplaceAll(full, "\\", "/")), root), "/")
		if rel == "" || rel == "product.json" || rel == crewFunctionsFileName || codeCopyHidden(rel) {
			continue
		}
		if _, e := crewSpecFilePath("crew", rel); e != nil {
			continue
		}
		content, found, e := store.read(ctx, full)
		if e != nil || !found {
			continue
		}
		if len(content) > sharedProjectFileContentCap || isSharedProjectBinaryContent(content) {
			skipped = append(skipped, rel)
			continue
		}
		if len(files) >= crewSpecMaxFiles || total+len(content) > crewSpecMaxFilesBytes {
			skipped = append(skipped, rel)
			continue
		}
		files[rel] = content
		total += len(content)
	}
	selected, _ := productSelectedSkills(ctx, codeproduct.ProfileID, binding.WorkspacePath)
	skills := []string{}
	for _, skill := range selected {
		if _, local := files[crewLocalSkillManifest(skill)]; local {
			skills = append(skills, skill)
		} else if _, e := validateCrewCreationSkills([]string{skill}); e == nil {
			skills = append(skills, skill)
		}
	}
	purpose := firstNonEmptyTrimmed(manifest.Description, "Made from the Code project "+title+". Its PROJECT_INSTRUCTIONS.md and files came with it.")
	if r := []rune(purpose); len(r) > crewPurposeLimit {
		purpose = string(r[:crewPurposeLimit])
	}
	spec := crewSpec{Name: title, Role: "Works on " + title, Purpose: purpose, SelectedSkills: skills, Files: files}
	crewID, err := api.createCrewFromSpec(ctx, claims, spec, false)
	if err != nil {
		return nil, fmt.Errorf("%s", externalCrewCreateError(crewID, err))
	}
	out := map[string]interface{}{"crew_id": crewID, "name": title, "files_copied": len(files), "link": crewAppLink(crewID)}
	if len(skipped) > 0 {
		out["files_not_copied"] = skipped
		out["note"] = "Some files were too large, not text or over the Crew's limits; add them to the Crew by hand if needed."
	}
	if crew, _, _, found := api.externalCrewResolve(ctx, claims, crewID); found {
		servers, _, _ := productSelectedServers(ctx, codeproduct.ProfileID, binding.WorkspacePath)
		secrets, _, _ := productSelectedSecrets(ctx, codeproduct.ProfileID, binding.WorkspacePath)
		var global []string
		if g, e := productSelectedGlobalSecrets(ctx, codeproduct.ProfileID, binding.WorkspacePath); e == nil && g != nil {
			global = *g
		}
		if e := applyCrewCreationSelections(ctx, "work", crew.Binding.WorkspacePath, nil, servers, secrets, global); e != nil {
			out["selections_error"] = "The Crew was made, but its connection and secret choices were not copied: " + e.Error()
		} else {
			out["connections_copied"], out["secret_names_copied"] = len(servers), len(secrets)+len(global)
		}
	}
	return out, nil
}

// codeCopyHidden keeps private and generated folders out of a copy: hidden names, chats, dependencies.
func codeCopyHidden(rel string) bool {
	for _, part := range strings.Split(rel, "/") {
		if strings.HasPrefix(part, ".") || part == "node_modules" || part == "__pycache__" {
			return true
		}
	}
	lower := strings.ToLower(rel)
	return strings.HasPrefix(lower, "chats/") || strings.HasPrefix(lower, "builder/") || strings.HasPrefix(lower, "db/")
}

// crewAppLink is the address of a Crew in the app.
func crewAppLink(crewID string) string {
	return strings.TrimRight(strings.TrimSpace(os.Getenv("PUBLIC_URL")), "/") + "/crew/" + crewID
}
