package codexcli

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/internal/nativeinput"
)

func codexNativeQuestion(ctx context.Context, method string, raw json.RawMessage, thread, turn string, files *codexFileChanges) (any, error) {
	switch method {
	case "item/commandExecution/requestApproval":
		return codexCommandApproval(ctx, raw, thread, turn)
	case "item/fileChange/requestApproval":
		return codexFileChangeApproval(ctx, raw, thread, turn, files)
	case "mcpServer/elicitation/request":
		return codexElicitation(ctx, raw, thread)
	}
	if method != "item/tool/requestUserInput" {
		return nil, fmt.Errorf("unsupported interactive request %s; use the chat clarification tool", method)
	}
	var req struct {
		ThreadID  string `json:"threadId"`
		TurnID    string `json:"turnId"`
		Questions []struct {
			ID       string               `json:"id"`
			Question string               `json:"question"`
			IsOther  bool                 `json:"isOther"`
			IsSecret bool                 `json:"isSecret"`
			Options  []nativeinput.Option `json:"options"`
		} `json:"questions"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	if req.ThreadID != thread || req.TurnID != turn {
		return nil, fmt.Errorf("question belongs to a different turn")
	}
	qs := make([]nativeinput.Question, 0, len(req.Questions))
	for _, q := range req.Questions {
		if q.IsSecret {
			return nil, fmt.Errorf("use the account or Vault setup to enter secrets")
		}
		qs = append(qs, nativeinput.Question{ID: q.ID, Question: q.Question, Options: q.Options, AllowOther: q.IsOther || len(q.Options) == 0})
	}
	answers, err := nativeinput.Ask(ctx, qs)
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	for _, a := range answers {
		out[a.ID] = map[string]any{"answers": nativeinput.Values(a)}
	}
	return map[string]any{"answers": out}, nil
}

// PLAT-872: never promote a single command decision into a session/policy grant.
func codexCommandApproval(ctx context.Context, raw json.RawMessage, thread, turn string) (any, error) {
	var req struct {
		ThreadID               string            `json:"threadId"`
		TurnID                 string            `json:"turnId"`
		ItemID                 string            `json:"itemId"`
		Command                string            `json:"command"`
		Cwd                    string            `json:"cwd"`
		Reason                 string            `json:"reason"`
		AvailableDecisions     []json.RawMessage `json:"availableDecisions"`
		NetworkApprovalContext json.RawMessage   `json:"networkApprovalContext"`
		AdditionalPermissions  json.RawMessage   `json:"additionalPermissions"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	if req.ThreadID != thread || req.TurnID != turn || req.ItemID == "" {
		return nil, fmt.Errorf("command approval belongs to a different turn or missing item")
	}
	deny := map[string]any{"decision": "decline"}
	if len(req.AvailableDecisions) > 0 {
		acceptsOnce := false
		for _, decision := range req.AvailableDecisions {
			if string(decision) == `"accept"` {
				acceptsOnce = true
			}
		}
		if !acceptsOnce {
			return deny, nil
		}
	}
	hasNetwork := len(req.NetworkApprovalContext) > 0 && string(req.NetworkApprovalContext) != "null"
	description := "Allow Codex to run this command?\n\n" + req.Command
	if hasNetwork {
		description = "Allow this command's network access?\n\n" + string(req.NetworkApprovalContext) + "\n\n" + req.Command
	}
	if strings.TrimSpace(req.Command) == "" && !hasNetwork {
		return deny, nil
	}
	if req.Cwd != "" {
		description += "\n\nWorking folder: " + req.Cwd
	}
	if req.Reason != "" {
		description += "\n\nReason: " + req.Reason
	}
	if len(req.AdditionalPermissions) > 0 && string(req.AdditionalPermissions) != "null" {
		description += "\n\nRequested access: " + string(req.AdditionalPermissions)
	}
	allowed, err := nativeinput.ApproveCommand(ctx, description)
	if err != nil || !allowed {
		return deny, nil
	}
	return map[string]any{"decision": "accept"}, nil
}

// codexFileChanges remembers the files each pending fileChange item touches,
// so its approval card can name them (the approval request carries only the
// item ID).
type codexFileChanges struct {
	mu    sync.Mutex
	items map[string][]codexFileChange
}

type codexFileChange struct {
	Path string `json:"path"`
	Diff string `json:"diff"`
	Kind struct {
		Type     string `json:"type"`
		MovePath string `json:"move_path"`
	} `json:"kind"`
}

func (f *codexFileChanges) observe(raw json.RawMessage) {
	var item struct {
		ID      string            `json:"id"`
		Type    string            `json:"type"`
		Changes []codexFileChange `json:"changes"`
	}
	if f == nil || json.Unmarshal(raw, &item) != nil || item.Type != "fileChange" || item.ID == "" {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.items == nil {
		f.items = map[string][]codexFileChange{}
	}
	if len(f.items) > 200 {
		f.items = map[string][]codexFileChange{}
	}
	f.items[item.ID] = item.Changes
}

func (f *codexFileChanges) get(id string) []codexFileChange {
	if f == nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.items[id]
}

const codexApprovalDiffBytes = 4000

// #3133: a file change is approved once; "for this session" grants are never
// offered, matching command approvals (PLAT-872).
func codexFileChangeApproval(ctx context.Context, raw json.RawMessage, thread, turn string, files *codexFileChanges) (any, error) {
	var req struct {
		ThreadID  string  `json:"threadId"`
		TurnID    string  `json:"turnId"`
		ItemID    string  `json:"itemId"`
		Reason    *string `json:"reason"`
		GrantRoot *string `json:"grantRoot"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	if req.ThreadID != thread || req.TurnID != turn || req.ItemID == "" {
		return nil, fmt.Errorf("file change approval belongs to a different turn or missing item")
	}
	deny := map[string]any{"decision": "decline"}
	changes := files.get(req.ItemID)
	var b strings.Builder
	b.WriteString("Allow Codex to change these files?\n")
	for _, c := range changes {
		verb := map[string]string{"add": "add", "delete": "delete"}[c.Kind.Type]
		if verb == "" {
			verb = "edit"
		}
		b.WriteString("\n" + verb + " " + c.Path)
		if c.Kind.MovePath != "" {
			b.WriteString(" -> " + c.Kind.MovePath)
		}
	}
	if len(changes) == 0 && (req.GrantRoot == nil || *req.GrantRoot == "") {
		// Nothing to show the person: never approve blind.
		log.Printf("[NATIVE_APPROVAL] provider=codex-cli kind=file_change item=%s action=decline reason=no_changes_seen", req.ItemID)
		return deny, nil
	}
	if req.GrantRoot != nil && *req.GrantRoot != "" {
		b.WriteString("\n\nWrite access asked for: " + *req.GrantRoot)
	}
	if req.Reason != nil && *req.Reason != "" {
		b.WriteString("\n\nReason: " + *req.Reason)
	}
	diff := ""
	for _, c := range changes {
		diff += c.Diff
	}
	if diff != "" {
		if len(diff) > codexApprovalDiffBytes {
			diff = diff[:codexApprovalDiffBytes] + "\n..."
		}
		b.WriteString("\n\n" + diff)
	}
	allowed, err := nativeinput.ApproveCommand(ctx, b.String())
	if err != nil || !allowed {
		log.Printf("[NATIVE_APPROVAL] provider=codex-cli kind=file_change item=%s files=%d action=decline", req.ItemID, len(changes))
		return deny, nil
	}
	log.Printf("[NATIVE_APPROVAL] provider=codex-cli kind=file_change item=%s files=%d action=accept", req.ItemID, len(changes))
	return map[string]any{"decision": "accept"}, nil
}

// #3133: an MCP server's form or link request, shown as the chat's card.
func codexElicitation(ctx context.Context, raw json.RawMessage, thread string) (any, error) {
	var req struct {
		ThreadID        string          `json:"threadId"`
		ServerName      string          `json:"serverName"`
		Mode            string          `json:"mode"`
		Message         string          `json:"message"`
		URL             string          `json:"url"`
		RequestedSchema json.RawMessage `json:"requestedSchema"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	if req.ThreadID != thread {
		return nil, fmt.Errorf("form request belongs to a different thread")
	}
	var result nativeinput.FormResult
	switch req.Mode {
	case "url":
		result = nativeinput.AskURL(ctx, "codex-cli", req.ServerName, req.Message, req.URL)
	case "form", "openai/form", "openaiForm", "":
		result = nativeinput.AskForm(ctx, "codex-cli", req.ServerName, req.Message, req.RequestedSchema)
	default:
		log.Printf("[NATIVE_FORM] provider=codex-cli server=%s mode=%s action=decline reason=unsupported_mode", req.ServerName, req.Mode)
		result = nativeinput.FormResult{Action: "decline"}
	}
	out := map[string]any{"action": result.Action, "content": nil}
	if result.Action == "accept" && result.Content != nil {
		out["content"] = result.Content
	}
	return out, nil
}
