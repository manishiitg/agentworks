package server

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"path"
	"path/filepath"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/skills"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/claudecode"
)

type nativeSkillChoice struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Source      string `json:"source"`
	Provider    string `json:"provider"`
	NativePath  string `json:"-"`
}

type nativeSkillCatalog struct {
	Skills    []nativeSkillChoice `json:"skills"`
	Provider  string              `json:"provider"`
	Prepared  bool                `json:"prepared"`
	Supported bool                `json:"supported"`
	Message   string              `json:"message"`
}

// Program/transport support is a server fact. Import discovery is broader and
// must never be used as this chat's invocation authority.
func nativeSkillInputSupported(provider, transport string) bool {
	return provider == "claude-code" && transport == "sdk" || provider == "codex-cli" && transport == "app-server"
}

func currentNativeSkillChoices(snapshot runtimeSkillsResponse, requestedProvider string) nativeSkillCatalog {
	out := nativeSkillCatalog{Skills: []nativeSkillChoice{}, Provider: snapshot.Provider, Prepared: snapshot.Prepared, Supported: nativeSkillInputSupported(snapshot.Provider, snapshot.Transport), Message: snapshot.Message}
	if !snapshot.Prepared {
		return out
	}
	if requestedProvider != "" && requestedProvider != snapshot.Provider {
		out.Supported = false
		out.Message = "The selected program has not prepared skills in this chat yet. Send a message without a skill, then refresh."
		return out
	}
	if !out.Supported {
		out.Message = "Explicit skill invocation is not supported by this program or transport here. You can still import project skills."
		return out
	}
	// A local Claude does not load the host's private attached-instruction
	// projections as slash skills. Do not equate filesystem presence with native
	// availability, enable extra roots or fall back to generic prompting.
	projectOnly := snapshot.Provider == "claude-code" && snapshot.WorkingDirectory != "" && snapshot.Directory != snapshot.WorkingDirectory
	if projectOnly {
		inventory, err := skills.ReadRuntimeInventory(snapshot.WorkingDirectory)
		if err != nil {
			out.Supported = false
			out.Message = "This chat's native project skills are unavailable. Refresh or remove the selection."
			return out
		}
		snapshot.RuntimeInventory = inventory
		snapshot.Directory = snapshot.WorkingDirectory
	}
	prefix := ".agents/skills"
	if snapshot.Provider == "claude-code" {
		prefix = ".claude/skills"
	}
	counts := map[string]int{}
	for _, skill := range snapshot.Skills {
		name := firstNonEmptyTrimmed(skill.Frontmatter.Name, skill.FolderName)
		if path.Dir(path.Dir(skill.FilePath)) == prefix {
			counts[name]++
		}
	}
	for _, skill := range snapshot.Skills {
		name := firstNonEmptyTrimmed(skill.Frontmatter.Name, skill.FolderName)
		if path.Dir(path.Dir(skill.FilePath)) != prefix || counts[name] != 1 || skill.ValidationError != "" || skill.Frontmatter.UserInvocable != nil && !*skill.Frontmatter.UserInvocable {
			continue
		}
		native := llmtypes.NativeSkill{Name: name, Path: filepath.Join(snapshot.Directory, filepath.FromSlash(skill.FilePath))}
		if llmtypes.ValidateNativeSkill(native) != nil {
			continue
		}
		// Claude's existing launch policy disables these built-ins. Do not
		// promise an invocation that the selected native launch turns off.
		if snapshot.Provider == "claude-code" && !claudecode.NativeSkillInvocable(name) {
			continue
		}
		hash := sha256.Sum256([]byte(snapshot.SessionID + "\x00" + snapshot.Provider + "\x00" + snapshot.Directory + "\x00" + skill.FilePath))
		out.Skills = append(out.Skills, nativeSkillChoice{ID: hex.EncodeToString(hash[:16]), Name: name, Description: skill.Frontmatter.Description, Source: "Prepared for this chat", Provider: snapshot.Provider, NativePath: native.Path})
	}
	out.Message = "Choose one skill for this message. Selection requests native invocation; it is not proof the skill ran. Only this chat's prepared native skill files are listed."
	if projectOnly {
		out.Message = "Claude can explicitly invoke native project skills here. Private attached instructions are not native slash skills; they remain available through read_skill."
	}
	return out
}

func (api *StreamingAPI) nativeSkillCatalogHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	snapshot, status, err := api.loadRuntimeSkillInventory(r)
	if err != nil {
		http.Error(w, err.Error(), status)
		return
	}
	writeUsersJSON(w, 200, currentNativeSkillChoices(snapshot, r.URL.Query().Get("provider")))
}

// Called after the existing project/tab/account admission, again when a queued
// row is claimed. Request JSON supplies only an opaque selector, never a path.
func (api *StreamingAPI) prepareRequestedSkill(r *http.Request, req *QueryRequest, session, provider string) (*http.Request, error) {
	if req.RequestedSkillID == "" {
		req.requestedSkillName = ""
		return r, nil
	}
	if req.IsAutoNotification {
		return r, fmt.Errorf("Choose a skill in an explicit chat message, not an automatic follow-up.")
	}
	request := r.Clone(r.Context())
	copiedURL := *r.URL
	request.URL = &copiedURL
	query := request.URL.Query()
	query.Set("workspace_path", req.SelectedFolder)
	query.Set("session_id", session)
	query.Del("file")
	request.URL.RawQuery = query.Encode()
	if _, err := api.ownNativeSkillScope(request, true); err != nil {
		return r, err
	}
	snapshot, _, err := api.loadRuntimeSkillInventory(request)
	if err != nil {
		return r, err
	}
	catalog := currentNativeSkillChoices(snapshot, provider)
	if !catalog.Prepared || !catalog.Supported {
		return r, fmt.Errorf("%s", firstNonEmptyTrimmed(catalog.Message, "Send a message without a skill to prepare this chat's native skills."))
	}
	for _, skill := range catalog.Skills {
		if skill.ID != req.RequestedSkillID {
			continue
		}
		log.Printf("[NATIVE_SKILL] session=%s provider=%s action=requested", session, provider)
		req.requestedSkillName = skill.Name
		if strings.TrimSpace(req.Query) == "" {
			req.Query = "[Skill requested: " + skill.Name + "]"
		}
		return r.WithContext(llmtypes.WithNativeSkill(r.Context(), session, llmtypes.NativeSkill{Name: skill.Name, Path: skill.NativePath})), nil
	}
	return r, fmt.Errorf("The selected skill is no longer available to this chat and program. Refresh skills or remove the selection.")
}
