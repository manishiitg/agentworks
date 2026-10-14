package server

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productguide"
)

// A small catalog by default; only explicitly requested, changed topic files
// are returned. Files follow the same path/content layout as Brain packages.
func (api *StreamingAPI) externalGuidanceSkills(w http.ResponseWriter, r *http.Request, args map[string]any) {
	claims := GetUserFromContext(r.Context())
	selected := map[string]bool{}
	if raw, ok := args["topics"].([]any); ok {
		for _, item := range raw {
			name, _ := item.(string)
			topic := externalGuidanceTopicByName(name)
			if topic == nil || !externalTopicVisible(claims, *topic) {
				externalError(w, 404, "unknown_topic", "Use a topic listed by help action=skills.")
				return
			}
			selected[topic.Name] = true
		}
	}
	known, _ := args["known_versions"].(map[string]any)
	skills := []map[string]any{}
	for _, topic := range externalGuidanceTopics {
		if !externalTopicVisible(claims, topic) {
			continue
		}
		version := externalTopicVersionFor(claims, topic)
		file := map[string]any{"path": "SKILL.md"}
		item := map[string]any{"name": topic.Name, "description": topic.Description, "version": version, "files": []any{file}}
		if topic.SourceVersion != "" {
			item["source_version"] = topic.SourceVersion
		}
		if selected[topic.Name] {
			if known[topic.Name] == version {
				item["unchanged"] = true
			} else {
				files, err := externalGuidanceSkillFiles(claims, topic)
				if err != nil {
					externalError(w, 500, "guidance_unavailable", "Guidance could not be rendered.")
					return
				}
				item["files"] = files
			}
		}
		skills = append(skills, item)
	}
	externalJSON(w, map[string]any{"guidance_version": externalGuidanceVersionFor(claims), "skills": skills, "refresh": "Save topic versions. Pass topics and known_versions to fetch only selected changed SKILL.md files; do not install every topic for every task."})
}

func externalGuidanceSkillFiles(claims *UserClaims, topic externalGuidanceTopic) ([]any, error) {
	if topic.Name == productguide.SkillName {
		selection := builderProductSelection(claims)
		return []any{
			map[string]any{"path": "SKILL.md", "content": productguide.Skill(selection)},
			map[string]any{"path": "references/products.md", "content": productguide.Guide(selection)},
			map[string]any{"path": "references/versions.json", "content": productguide.Versions(selection)},
		}, nil
	}
	body, err := externalGuidanceSkillContent(claims, topic)
	if err != nil {
		return nil, err
	}
	return []any{map[string]any{"path": "SKILL.md", "content": body}}, nil
}

// Render the same canonical SKILL.md for explicit skill fetch and schema preparation.
func externalGuidanceSkillContent(claims *UserClaims, topic externalGuidanceTopic) (string, error) {
	_, body, err := externalGuidanceContentForProducts(topic.Name, builderProductSelection(claims))
	if err != nil {
		return "", err
	}
	description, _ := json.Marshal(topic.Description)
	return fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n\n", topic.Name, description) + externalTopicNoteFor(claims, topic) + "\n\n" + body, nil
}
