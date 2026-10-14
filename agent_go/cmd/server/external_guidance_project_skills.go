package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path"
	"sort"
	"strings"
)

// Only explicit full skill fetches need decoration; ordinary file/Brain reads
// stay unbuffered. Product handlers still authorize the read before delivery.
func externalProjectSkillFetch(name string, args map[string]any) bool {
	if name == "get_skill" {
		return true
	}
	if name == "brain_skills" {
		return externalArg(args, "action") == "get"
	}
	switch name {
	case "read_file", "read_crew_file", "read_workflow_knowledge":
		return path.Base(externalArg(args, "path")) == "SKILL.md"
	case "brain_read":
		return externalArg(args, "action") == "read" && path.Base(externalArg(args, "path")) == "SKILL.md"
	}
	return false
}

// Receipt identity is source + project + location, never the declared skill
// name. Only opaque identity/version digests enter the guidance store; private
// paths and skill contents remain in the already-authorized fetch response.
func externalDeliveredProjectSkill(name string, args, value map[string]any) (string, string, map[string]any) {
	if !externalProjectSkillFetch(name, args) {
		return "", "", nil
	}
	body := value
	if result, ok := value["result"].(map[string]any); ok {
		body = result
	}
	if body["truncated"] == true || body["has_more"] == true || body["error"] != nil {
		return "", "", nil
	}
	source, project, location := "workflow", externalArg(value, "workflow_id"), externalArg(args, "path")
	if project == "" {
		project = externalArg(args, "workflow_id")
	}
	if name == "read_crew_file" {
		source, project = "crew", externalArg(value, "crew_id")
	}
	files := []map[string]any{}
	coverage := "SKILL.md"
	if name == "brain_skills" {
		coverage = "package"
		source, project, location = "brain", "", externalArg(body, "folder_path")
		full := false
		items, _ := body["files"].([]any)
		for _, item := range items {
			file, _ := item.(map[string]any)
			filePath := externalArg(file, "path")
			if filePath == "SKILL.md" && externalArg(file, "content") != "" {
				full = true
			}
			files = append(files, map[string]any{"path": filePath, "content": file["content"], "content_base64": file["content_base64"]})
		}
		if !full {
			return "", "", nil
		}
	} else {
		content, _ := body["content"].(string)
		if content == "" || body["encoding"] == "base64" {
			return "", "", nil
		}
		if name == "brain_read" {
			source, project = "brain", ""
		}
		if name == "get_skill" {
			source, project, location = "server", "agentworks", "agentworks"
		} else {
			location = path.Dir(location)
		}
		files = append(files, map[string]any{"path": "SKILL.md", "content": content})
	}
	if location == "" || source != "brain" && project == "" {
		return "", "", nil
	}
	sort.Slice(files, func(i, j int) bool { return externalArg(files[i], "path") < externalArg(files[j], "path") })
	raw, _ := json.Marshal(files)
	versionHash := sha256.Sum256(raw)
	identity, _ := json.Marshal([]string{source, project, strings.Trim(location, "/"), coverage})
	identityHash := sha256.Sum256(identity)
	topic := "project-skill-" + hex.EncodeToString(identityHash[:])[:24]
	version := hex.EncodeToString(versionHash[:])[:12]
	return topic, version, map[string]any{"topic": topic, "version": version, "source": source, "project_id": project, "path": location, "coverage": coverage, "tool": name}
}
