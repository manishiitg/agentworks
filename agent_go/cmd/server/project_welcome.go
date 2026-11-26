package server

import (
	"log"
	"net/http"
	"strings"

	"gopkg.in/yaml.v3"
)

// projectWelcomeFile is a project's welcome page (owner, 25 Nov: "can we customize this page for a new chat per
// project"): Markdown shown on an empty project and at the top of each new chat. Its owner or co-owners write it,
// usually by asking the project's chat; a User only sees it (crew_setup_lock.go locks it).
const projectWelcomeFile = "WELCOME.md"

// projectWelcomeMaxStarters keeps the starter buttons to one short row or two.
const projectWelcomeMaxStarters = 6

type projectWelcomeStarter struct {
	Label   string `json:"label" yaml:"label"`
	Note    string `json:"note,omitempty" yaml:"note"`
	Message string `json:"message" yaml:"message"`
}

type projectWelcome struct {
	// Body is the page's Markdown; Root is the project folder its relative image and video paths start from.
	Body     string                  `json:"body,omitempty"`
	Root     string                  `json:"root,omitempty"`
	Starters []projectWelcomeStarter `json:"starters"`
}

// parseProjectWelcome splits WELCOME.md into its Markdown and the starters listed in its front matter:
//
//	---
//	starters:
//	  - label: Make a promo
//	    note: From a short brief
//	    message: Make a 30-second promo video for
//	---
func parseProjectWelcome(text string) projectWelcome {
	out := projectWelcome{Starters: []projectWelcomeStarter{}}
	text = strings.TrimPrefix(strings.ReplaceAll(text, "\r\n", "\n"), "\ufeff")
	body := text
	if strings.HasPrefix(text, "---\n") {
		if end := strings.Index(text[4:], "\n---"); end >= 0 {
			var front struct {
				Starters []projectWelcomeStarter `yaml:"starters"`
			}
			if err := yaml.Unmarshal([]byte(text[4:4+end]), &front); err == nil {
				for _, s := range front.Starters {
					s.Label, s.Note, s.Message = strings.TrimSpace(s.Label), strings.TrimSpace(s.Note), strings.TrimSpace(s.Message)
					if s.Message == "" {
						s.Message = s.Label
					}
					if s.Label != "" && len(out.Starters) < projectWelcomeMaxStarters {
						out.Starters = append(out.Starters, s)
					}
				}
			}
			body = strings.TrimPrefix(text[4+end+4:], "\n")
		}
	}
	out.Body = strings.TrimSpace(body)
	return out
}

// handleProjectWelcome returns a project's welcome page to anyone who can open its chats: GET
// /api/agent-profiles/{id}/welcome?project_id=…. No WELCOME.md means an empty reply, and the screen keeps its own words.
func (api *StreamingAPI) handleProjectWelcome(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	projectID := strings.TrimSpace(r.URL.Query().Get("project_id"))
	userID, slot, ok := api.agentProfileConversationSlot(w, r, projectID)
	if !ok {
		return
	}
	root := agentProfileRuntimeWorkspace(userID, slot.binding.WorkspacePath)
	text, found, err := readFileFromWorkspace(r.Context(), root+"/"+projectWelcomeFile)
	if err != nil {
		log.Printf("[PROJECT_WELCOME] project=%s caller=%s outcome=unreadable err=%v", projectID, userID, err)
	}
	out := projectWelcome{Starters: []projectWelcomeStarter{}}
	if found && err == nil {
		out = parseProjectWelcome(text)
		if out.Body != "" || len(out.Starters) > 0 {
			out.Root = root
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeAgentProfileJSON(w, http.StatusOK, out)
}
