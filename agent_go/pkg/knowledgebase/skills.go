package knowledgebase

import (
	"context"
	"errors"
	"path"
	"sort"
	"strings"
)

// Company skills (PLAT-576). A Brain skill is a folder holding a SKILL.md plus any supporting files (references/,
// scripts/, assets), the same package every coding CLI reads. Skills are built on the ordinary entry operations, so
// folder roles, versions, request idempotency and Git backup apply unchanged: a Reader can list and get a skill, an
// Editor can publish one, and a skill that carries scripts needs an Owner, because its scripts run on colleagues'
// machines and in agents' sandboxes.

const (
	skillFile         = "SKILL.md"
	maxSkillFiles     = 200
	maxSkillsListed   = 500
	skillListingDepth = 1024
)

var scriptExtensions = map[string]bool{".py": true, ".sh": true, ".bash": true, ".zsh": true, ".js": true, ".mjs": true, ".cjs": true, ".ts": true, ".rb": true, ".pl": true, ".php": true, ".ps1": true, ".bat": true, ".cmd": true, ".lua": true, ".go": true}

// skillHasScripts reports whether a package carries anything an agent would run.
func skillHasScripts(paths []string) bool {
	for _, p := range paths {
		if strings.HasPrefix(p, "scripts/") || scriptExtensions[strings.ToLower(path.Ext(p))] {
			return true
		}
	}
	return false
}

// skillFrontmatter reads name and description from SKILL.md's YAML front matter (simple key: value lines).
func skillFrontmatter(content string) (name, description string) {
	if !strings.HasPrefix(content, "---\n") {
		return "", ""
	}
	end := strings.Index(content[4:], "\n---")
	if end < 0 {
		return "", ""
	}
	for _, line := range strings.Split(content[4:4+end], "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		switch strings.TrimSpace(key) {
		case "name":
			name = value
		case "description":
			description = value
		}
	}
	return name, description
}

// subRequestID derives a stable request ID for one step of a skill operation, so a retried publish replays every step.
func subRequestID(base, step string) string {
	if len(base) > 100 {
		base = base[:100]
	}
	return base + "-" + digest([]byte(step))[:12]
}

func (s *Service) skillsTool(ctx context.Context, p Principal, args map[string]any) (any, error) {
	// Every step is its own request: the outer call's request record must not cover the steps.
	p.requestTool, p.requestArguments = "", nil
	switch stringArg(args, "action") {
	case "list":
		return s.listSkills(ctx, p, args)
	case "get":
		return s.getSkill(ctx, p, args)
	case "publish":
		return s.publishSkill(ctx, p, args)
	}
	return nil, badArg("Unknown skills action.")
}

// skillEntries lists every entry under a folder the caller can read, following pagination.
func (s *Service) skillEntries(ctx context.Context, p Principal, folder, glob string, limit int) ([]map[string]any, error) {
	var out []map[string]any
	cursor := ""
	for {
		a := map[string]any{"folder_path": folder, "depth": skillListingDepth, "limit": 100}
		if glob != "" {
			a["glob"] = glob
		}
		if cursor != "" {
			a["cursor"] = cursor
		}
		page, err := s.Call(ctx, p, "list_knowledgebase", a)
		if err != nil {
			return nil, err
		}
		m := asMap(page)
		for _, item := range asSlice(m["entries"]) {
			out = append(out, asMap(item))
			if len(out) >= limit {
				return out, nil
			}
		}
		cursor = stringArg(m, "next_cursor")
		if cursor == "" {
			return out, nil
		}
	}
}

func asSlice(v any) []any {
	if xs, ok := v.([]any); ok {
		return xs
	}
	return nil
}

func (s *Service) listSkills(ctx context.Context, p Principal, args map[string]any) (any, error) {
	entries, err := s.skillEntries(ctx, p, stringArg(args, "folder_path"), skillFile, maxSkillsListed)
	if err != nil {
		return nil, err
	}
	query := strings.ToLower(strings.TrimSpace(stringArg(args, "query")))
	skills := []any{}
	for _, e := range entries {
		if stringArg(e, "filename") != skillFile {
			continue
		}
		read, err := s.Call(ctx, p, "read_knowledgebase", map[string]any{"entry_id": stringArg(e, "entry_id")})
		if err != nil {
			continue
		}
		folder := stringArg(e, "folder_path")
		name, description := skillFrontmatter(stringArg(asMap(read), "content"))
		if name == "" {
			name = path.Base(folder)
		}
		if query != "" && !strings.Contains(strings.ToLower(name+" "+description+" "+folder), query) {
			continue
		}
		skills = append(skills, map[string]any{"name": name, "description": description, "folder_path": folder, "version": stringArg(e, "version"), "updated_at": stringArg(e, "updated_at"), "updated_by": stringArg(e, "updated_by")})
	}
	return map[string]any{"skills": skills, "count": len(skills)}, nil
}

func (s *Service) getSkill(ctx context.Context, p Principal, args map[string]any) (any, error) {
	folder := strings.Trim(stringArg(args, "folder_path"), "/")
	if folder == "" {
		return nil, badArg("folder_path names the skill's folder.")
	}
	entries, err := s.skillEntries(ctx, p, folder, "", maxSkillFiles+1)
	if err != nil {
		return nil, err
	}
	files := []any{}
	var name, description, version string
	for _, e := range entries {
		read, err := s.Call(ctx, p, "read_knowledgebase", map[string]any{"entry_id": stringArg(e, "entry_id")})
		if err != nil {
			return nil, err
		}
		r := asMap(read)
		rel := strings.TrimPrefix(stringArg(e, "path"), folder+"/")
		file := map[string]any{"path": rel}
		if encoded, ok := r["content_base64"].(string); ok {
			file["content_base64"] = encoded
		} else {
			file["content"] = stringArg(r, "content")
		}
		if rel == skillFile {
			name, description = skillFrontmatter(stringArg(r, "content"))
			version = stringArg(e, "version")
		}
		files = append(files, file)
	}
	if version == "" {
		return nil, kbErr("NOT_FOUND", "No skill (SKILL.md) in that folder.")
	}
	if name == "" {
		name = path.Base(folder)
	}
	return map[string]any{"name": name, "description": description, "folder_path": folder, "version": version, "files": files,
		"install": "Write each file under your own skills folder as <skills>/" + path.Base(folder) + "/<path> (content as text, content_base64 decoded)."}, nil
}

type skillFileInput struct {
	path, content, encoded string
	binary                 bool
}

func (s *Service) publishSkill(ctx context.Context, p Principal, args map[string]any) (any, error) {
	folder := strings.Trim(stringArg(args, "folder_path"), "/")
	if err := validatePath(folder, false); err != nil || folder == "" {
		return nil, badArg("folder_path names the skill's folder, for example Company/Skills/release-notes.")
	}
	base := stringArg(args, "request_id")
	raw := asSlice(args["files"])
	if len(raw) == 0 || len(raw) > maxSkillFiles {
		return nil, badArg("files must hold 1 to %d files.", maxSkillFiles)
	}
	inputs := map[string]skillFileInput{}
	paths := []string{}
	for _, item := range raw {
		m := asMap(item)
		rel := strings.Trim(stringArg(m, "path"), "/")
		if err := validatePath(folder+"/"+rel, true); err != nil || rel == "" {
			return nil, badArg("Invalid skill file path %q.", rel)
		}
		if _, dup := inputs[strings.ToLower(rel)]; dup {
			return nil, badArg("Duplicate skill file path %q.", rel)
		}
		in := skillFileInput{path: rel}
		text, hasText := m["content"].(string)
		encoded, hasEncoded := m["content_base64"].(string)
		if hasText == hasEncoded {
			return nil, badArg("Each file needs exactly one of content or content_base64 (%s).", rel)
		}
		in.content, in.encoded, in.binary = text, encoded, hasEncoded
		inputs[strings.ToLower(rel)] = in
		paths = append(paths, rel)
	}
	if _, ok := inputs[strings.ToLower(skillFile)]; !ok || inputs[strings.ToLower(skillFile)].binary {
		return nil, badArg("A skill needs a SKILL.md at its top level.")
	}
	sort.Strings(paths)
	need := roleEditor
	if skillHasScripts(paths) {
		need = roleOwner
	}
	if s.effective(p, folder) < need {
		if need == roleOwner {
			return nil, kbErr("FORBIDDEN", "Publishing a skill with scripts needs Owner on its folder: its scripts run on other people's machines and in agents' sandboxes.")
		}
		return nil, kbErr("FORBIDDEN", "Publishing a skill needs Editor on its folder.")
	}
	// Folders: the skill folder and every subfolder its files use, parents first.
	folders := map[string]bool{folder: true}
	for _, rel := range paths {
		for dir := path.Dir(rel); dir != "."; dir = path.Dir(dir) {
			folders[folder+"/"+dir] = true
		}
	}
	ordered := make([]string, 0, len(folders))
	for f := range folders {
		ordered = append(ordered, f)
	}
	sort.Slice(ordered, func(i, j int) bool {
		return strings.Count(ordered[i], "/") < strings.Count(ordered[j], "/") || strings.Count(ordered[i], "/") == strings.Count(ordered[j], "/") && ordered[i] < ordered[j]
	})
	if err := s.ensureFolders(ctx, p, base, folder, ordered); err != nil {
		return nil, err
	}
	existing := map[string]map[string]any{}
	current, err := s.skillEntries(ctx, p, folder, "", maxSkillFiles*2)
	if err != nil {
		return nil, err
	}
	for _, e := range current {
		existing[strings.ToLower(strings.TrimPrefix(stringArg(e, "path"), folder+"/"))] = e
	}
	created, updated, deleted := []any{}, []any{}, []any{}
	var version string
	for _, rel := range paths {
		in := inputs[strings.ToLower(rel)]
		a := map[string]any{"request_id": subRequestID(base, "file\x00"+rel)}
		if in.binary {
			a["content_base64"] = in.encoded
		} else {
			a["content"] = in.content
		}
		var result any
		if e, ok := existing[strings.ToLower(rel)]; ok {
			a["entry_id"], a["expected_version"] = stringArg(e, "entry_id"), stringArg(e, "version")
			result, err = s.Call(ctx, p, "update_knowledgebase", a)
			if err == nil && asMap(result)["changed"] == true {
				updated = append(updated, rel)
			}
		} else {
			kind := "source"
			if rel == skillFile {
				kind = "skill"
			}
			a["folder_path"], a["filename"], a["type"], a["title"] = folder+dirOf(rel), path.Base(rel), kind, path.Base(rel)
			result, err = s.Call(ctx, p, "create_knowledgebase", a)
			if err == nil {
				created = append(created, rel)
			}
		}
		if err != nil {
			return nil, err
		}
		if rel == skillFile {
			version = stringArg(asMap(result), "version")
		}
	}
	// Publishing replaces the package: files the new version no longer has are removed.
	for rel, e := range existing {
		if _, keep := inputs[rel]; keep {
			continue
		}
		if _, err := s.Call(ctx, p, "delete_knowledgebase", map[string]any{"entry_id": stringArg(e, "entry_id"), "expected_version": stringArg(e, "version"), "request_id": subRequestID(base, "delete\x00"+rel)}); err != nil {
			return nil, err
		}
		deleted = append(deleted, stringArg(e, "path"))
	}
	return map[string]any{"published": true, "folder_path": folder, "version": version, "files": len(paths), "created": created, "updated": updated, "deleted": deleted, "has_scripts": need == roleOwner}, nil
}

func dirOf(rel string) string {
	if dir := path.Dir(rel); dir != "." {
		return "/" + dir
	}
	return ""
}

// ensureFolders creates any missing folder, parents first; an existing folder is fine.
func (s *Service) ensureFolders(ctx context.Context, p Principal, base, skillRoot string, folders []string) error {
	// The skill folder's own parents must exist already: a skill is published into a folder people were granted.
	parent := path.Dir(skillRoot)
	if parent == "." {
		parent = ""
	}
	for _, f := range folders {
		up := path.Dir(f)
		if f == skillRoot {
			up = parent
		}
		if up == "." {
			up = ""
		}
		_, err := s.Call(ctx, p, "create_knowledgebase_folder", map[string]any{"folder_path": up, "name": path.Base(f), "request_id": subRequestID(base, "folder\x00"+f)})
		var kerr *Error
		if err != nil && !(errors.As(err, &kerr) && kerr.Code == "NAME_CONFLICT") {
			return err
		}
	}
	return nil
}
