package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/projectactions"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/coding-agent-loop/workspace/crewsync"
)

// A move carries only this owner's Crew records. The destination keeps its
// ownership, access, app-account IDs and filesystem routing. NDJSON lets a long
// collection of chats use the same resumable disk transport as project files.
type crewMoveHeader struct {
	Callers     []crewMoveCaller                      `json:"callers,omitempty"`
	Watchers    map[string]crewMoveWatcherState       `json:"watchers,omitempty"`
	Webhooks    []projectWebhookDefinition            `json:"webhooks"`
	SourceID    string                                `json:"source_id"`
	SourceOwner string                                `json:"source_owner"`
	MainKey     string                                `json:"main_key"`
	Settings    map[string]map[string]json.RawMessage `json:"settings"`
	Functions   []projectFunctionDefinition           `json:"functions"`
	Schedules   []projectactions.Schedule             `json:"schedules"`
}
type crewMoveChat struct {
	Key          string                   `json:"key"`
	Previous     bool                     `json:"previous,omitempty"`
	Identity     ProductAgentIdentity     `json:"identity"`
	Conversation *crewHandoffConversation `json:"conversation"`
}

func crewMoveRecordPath(roots crewroot.Roots, name string) (string, error) {
	p, found, err := crewroot.Resolve(fsutil.WorkspaceDocsRoot(), roots.Virtual+"/"+name)
	if err != nil {
		return "", err
	}
	if !found || !crewroot.Within(roots.Private, p) {
		return "", fmt.Errorf("Crew settings storage is unavailable")
	}
	return p, nil
}

func (api *StreamingAPI) captureCrewMoveData(ctx context.Context, claims *UserClaims, roots crewroot.Roots, main ProductConversationRecord, snapshot *crewsync.Snapshot) error {
	if err := captureCrewMoveDatabase(ctx, roots, snapshot); err != nil {
		return err
	}
	h := crewMoveHeader{SourceID: roots.ID, SourceOwner: claims.UserID, MainKey: main.ConversationKey, Settings: map[string]map[string]json.RawMessage{}}
	for _, name := range []string{"product.json", "workflow.json"} {
		p, err := crewMoveRecordPath(roots, name)
		if err != nil {
			return err
		}
		raw, err := os.ReadFile(p)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		var doc map[string]json.RawMessage
		if err = json.Unmarshal(raw, &doc); err != nil {
			return err
		}
		selected := map[string]json.RawMessage{}
		for _, key := range []string{"title", "label", "description", "chat_instructions", "identity", "email_inbox_enabled", "knowledgebase_mode"} {
			if value, ok := doc[key]; ok {
				selected[key] = value
			}
		}
		var caps map[string]json.RawMessage
		if raw := doc["capabilities"]; len(raw) > 0 {
			if err = json.Unmarshal(raw, &caps); err != nil {
				return err
			}
		}
		portable := map[string]json.RawMessage{}
		for _, key := range []string{"selected_skills", "selected_tools", "browser_mode", "use_code_execution_mode", "native_agent_tools"} {
			if value, ok := caps[key]; ok {
				portable[key] = value
			}
		}
		selected["capabilities"], _ = json.Marshal(portable)
		h.Settings[name] = selected
	}
	schedules, err := readProjectSchedules(ctx, roots.Virtual)
	if err != nil {
		return err
	}
	for _, schedule := range schedules {
		if schedule.Project.Kind != "crew" || schedule.Project.ID != roots.ID || schedule.RunAs != claims.UserID {
			return fmt.Errorf("a schedule does not belong to this Crew's owner")
		}
	}
	h.Schedules = schedules
	if err = api.captureCrewMoveCallers(ctx, roots, &h); err != nil {
		return err
	}
	if err = captureCrewMoveWatchers(&h); err != nil {
		return err
	}
	h.Functions, err = readProjectFunctionDefinitions(ctx, roots.Virtual)
	if err != nil {
		return err
	}
	h.Webhooks, err = readProjectWebhookDefinitions(ctx, roots.Virtual)
	if err != nil {
		return err
	}
	for i := range h.Webhooks {
		hook := &h.Webhooks[i]
		if hook.Project.Kind != "crew" || hook.Project.ID != roots.ID || hook.RunAs != claims.UserID {
			return fmt.Errorf("a webhook does not belong to this Crew's owner")
		}
		hook.Auth.EncryptedSecret = ""
	}
	f, err := os.CreateTemp(snapshot.StagingDir, "crew-state-")
	if err != nil {
		return err
	}
	defer f.Close()
	snapshot.StatePath = f.Name()
	encoder := json.NewEncoder(f)
	if err = encoder.Encode(h); err != nil {
		return err
	}
	store := defaultProductConversationRegistryStore()
	doc, err := store.loadDocument(ctx, productConversationRegistryPath(claims.UserID))
	if err != nil {
		return err
	}
	keys := make([]string, 0, len(doc.Entries))
	for key, c := range doc.Entries {
		if c.ProfileID == crewProfileID && canonicalCrewWorkspaceRoot(c.WorkspacePath) == roots.Virtual {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		current := doc.Entries[key]
		records := append([]ProductConversationRecord{}, doc.Previous[key]...)
		// Oldest first so the destination's previous-history list keeps its order.
		for i, j := 0, len(records)-1; i < j; i, j = i+1, j-1 {
			records[i], records[j] = records[j], records[i]
		}
		records = append(records, current)
		for i, record := range records {
			if canonicalCrewWorkspaceRoot(record.WorkspacePath) != roots.Virtual {
				return fmt.Errorf("a chat does not belong to this Crew")
			}
			closeWorkflowPolicyCLI(record.SessionID, record.Provider, "moving Crew online")
			conversation, err := api.captureCrewHandoffConversation(ctx, claims, roots, record)
			if err != nil {
				return err
			}
			identity := doc.Agents[key]
			identity.ConnectionID, identity.UpdatedBy = "", ""
			if err = encoder.Encode(crewMoveChat{current.ConversationKey, i < len(records)-1, identity, conversation}); err != nil {
				return err
			}
			if at, err := f.Seek(0, io.SeekCurrent); err != nil || at > crewSyncPayloadBytes {
				return fmt.Errorf("this Crew's saved chats are too large to move in one transfer")
			}
		}
	}
	return f.Close()
}

func readCrewMoveData(p string, visit func(crewMoveHeader, *crewMoveChat) error) error {
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()
	decoder := json.NewDecoder(io.LimitReader(f, crewSyncPayloadBytes+1))
	var h crewMoveHeader
	if err = decoder.Decode(&h); err != nil {
		return err
	}
	if err = visit(h, nil); err != nil {
		return err
	}
	for {
		var chat crewMoveChat
		if err = decoder.Decode(&chat); err == io.EOF {
			return nil
		} else if err != nil {
			return err
		}
		if err = visit(h, &chat); err != nil {
			return err
		}
	}
}

func crewMoveKey(key string, h crewMoveHeader, destination, main string) (string, error) {
	if key == h.MainKey || key == h.SourceID || key == "" {
		return main, nil
	}
	if !strings.HasPrefix(key, h.SourceID+":") || !productConversationKeyPattern.MatchString(key) {
		return "", fmt.Errorf("a moved chat has an invalid Crew address")
	}
	return destination + strings.TrimPrefix(key, h.SourceID), nil
}

func (api *StreamingAPI) disableCrewMoveSchedules(ctx context.Context, roots crewroot.Roots, statePath string) error {
	var h crewMoveHeader
	f, err := os.Open(statePath)
	if err != nil {
		return err
	}
	defer f.Close()
	if err = json.NewDecoder(f).Decode(&h); err != nil {
		return err
	}
	if h.SourceID != roots.ID || h.SourceOwner != roots.Owner {
		return fmt.Errorf("move schedules do not belong to this Crew")
	}
	if err = api.pauseCrewMoveCallers(ctx, h); err != nil {
		return err
	}
	for i := range h.Schedules {
		h.Schedules[i].Enabled = false
	}
	p, err := crewMoveRecordPath(roots, "schedules.json")
	if err != nil {
		return err
	}
	if err = fsutil.WriteJSONAtomic(p, projectactions.Schedules{Schedules: h.Schedules}, 0600); err != nil {
		return err
	}
	log.Printf("[CREW_SYNC] project=%s phase=schedules-paused count=%d", roots.ID, len(h.Schedules))
	return nil
}

func (api *StreamingAPI) importCrewMoveData(ctx context.Context, claims *UserClaims, roots crewroot.Roots, pair, transfer string, main ProductConversationRecord, statePath string) error {
	binding, err := crewroot.ReadSync(roots.ID)
	if err != nil || binding == nil {
		return fmt.Errorf("this Crew's move is unavailable")
	}
	if err = validateCrewMoveData(statePath, binding.PeerProjectID, roots.ID, main.ConversationKey); err != nil {
		return err
	}
	apply := func(h crewMoveHeader, chat *crewMoveChat) error {
		if h.SourceID != binding.PeerProjectID || h.SourceOwner == "" {
			return fmt.Errorf("move records do not match the paired Crew")
		}
		if chat != nil {
			key, err := crewMoveKey(chat.Key, h, roots.ID, main.ConversationKey)
			if err != nil {
				return err
			}
			if err = validateCrewHandoffConversation(chat.Conversation); err != nil {
				return err
			}
			store := defaultProductConversationRegistryStore()
			path := productConversationRegistryPath(claims.UserID)
			lock := productConversationRegistryMutex(path)
			lock.Lock()
			doc, err := store.loadDocument(ctx, path)
			if err != nil {
				lock.Unlock()
				return err
			}
			entry := productConversationRegistryEntryKey(crewProfileID, key)
			current, exists := doc.Entries[entry]
			if !exists {
				current = main
				current.ConversationKey = key
				current.IsMainChat = key == main.ConversationKey
				current.SessionID = "product-" + uuid.NewSHA1(uuid.NameSpaceOID, []byte(pair+"/"+key)).String()
				current.ConversationID = "conversation-" + uuid.NewSHA1(uuid.NameSpaceOID, []byte(pair+"/initial/"+key)).String()
				doc.Entries[entry] = current
			}
			if doc.Agents == nil {
				doc.Agents = map[string]ProductAgentIdentity{}
			}
			identity := chat.Identity
			// Account identities are destination-owned. Native import resolves an
			// available destination account; archives retain the destination default.
			if identity.Engine != "" {
				if chosen, _ := api.crewNativeDestination(ctx, claims, roots, current, identity, identity.ModelID, identity.Engine); chosen != nil {
					identity = *chosen
				} else {
					identity.Engine, identity.ModelID, identity.ConnectionID = current.Provider, current.ModelID, current.ConnectionID
				}
			} else {
				identity.ConnectionID = ""
			}
			identity.UpdatedBy = claims.UserID
			doc.Agents[entry] = identity
			err = store.writeDocument(ctx, path, doc)
			lock.Unlock()
			if err != nil {
				return err
			}
			current.handoffAgentToolsMode = main.handoffAgentToolsMode
			return api.importCrewHandoffConversation(ctx, claims, roots, pair, transfer+"/"+chat.Conversation.ConversationID, chat.Conversation, current)
		}
		if err = setCrewMovePhase(roots.ID, pair, transfer, "schedules_watchers"); err != nil {
			return err
		}
		if err := api.importCrewMoveCallers(ctx, claims, roots, h, pair, main.ConversationKey); err != nil {
			return err
		}
		ids := map[string]string{}
		for _, s := range h.Schedules {
			ids[s.ID] = uuid.NewSHA1(uuid.NameSpaceOID, []byte(pair+"/schedule/"+s.ID)).String()
		}
		for i := range h.Schedules {
			s := &h.Schedules[i]
			original := *s
			if s.Project.Kind != "crew" || s.Project.ID != h.SourceID || s.RunAs != h.SourceOwner {
				return fmt.Errorf("a schedule does not belong to the moving owner")
			}
			s.ID, s.Project.ID, s.RunAs = ids[s.ID], roots.ID, claims.UserID
			if s.Action.Tab != "" {
				s.Action.Tab, err = crewMoveKey(s.Action.Tab, h, roots.ID, main.ConversationKey)
				if err != nil {
					return err
				}
			}
			if s.When.After != nil {
				for n, id := range s.When.After.Schedules {
					mapped, ok := ids[id]
					if !ok {
						return fmt.Errorf("a schedule depends on another project's schedule")
					}
					s.When.After.Schedules[n] = mapped
				}
			}
			if err = rebindCrewMoveWatcher(s, h, roots.ID, main.ConversationKey, pair); err != nil {
				return err
			}
			if err = s.Validate(); err != nil {
				return err
			}
			if state, ok := h.Watchers[original.ID]; ok {
				if err = importCrewMoveWatcher(state, claims.UserID, original, *s); err != nil {
					return err
				}
			}
		}
		p, err := crewMoveRecordPath(roots, "schedules.json")
		if err != nil {
			return err
		}
		if err = fsutil.WriteJSONAtomic(p, projectactions.Schedules{Schedules: h.Schedules}, 0600); err != nil {
			return err
		}
		if err = setCrewMovePhase(roots.ID, pair, transfer, "dashboards_functions"); err != nil {
			return err
		}
		for name, selected := range h.Settings {
			if name != "product.json" && name != "workflow.json" {
				return fmt.Errorf("invalid moved Crew settings")
			}
			p, err := crewMoveRecordPath(roots, name)
			if err != nil {
				return err
			}
			raw, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			var dst map[string]json.RawMessage
			if err = json.Unmarshal(raw, &dst); err != nil {
				return err
			}
			for _, key := range []string{"title", "label", "description", "chat_instructions", "identity", "email_inbox_enabled", "knowledgebase_mode"} {
				if v, ok := selected[key]; ok {
					dst[key] = v
				}
			}
			var caps, incoming map[string]json.RawMessage
			if len(dst["capabilities"]) > 0 {
				if err = json.Unmarshal(dst["capabilities"], &caps); err != nil {
					return err
				}
			}
			if caps == nil {
				caps = map[string]json.RawMessage{}
			}
			if len(selected["capabilities"]) > 0 {
				if err = json.Unmarshal(selected["capabilities"], &incoming); err != nil {
					return err
				}
			}
			for _, key := range []string{"selected_skills", "selected_tools", "browser_mode", "use_code_execution_mode", "native_agent_tools"} {
				if v, ok := incoming[key]; ok {
					caps[key] = v
				}
			}
			dst["capabilities"], _ = json.Marshal(caps)
			if err = fsutil.WriteJSONAtomic(p, dst, 0600); err != nil {
				return err
			}
		}
		for i := range h.Functions {
			fn := &h.Functions[i]
			fn.ID = uuid.NewSHA1(uuid.NameSpaceOID, []byte(pair+"/function/"+fn.ID)).String()
			fn.CreatedBy, fn.UpdatedAt = externalCrewAuthorStamp(claims), time.Now().UTC()
			// Server caller grants are local identities and must be selected there.
			for n, caller := range fn.AllowedCallers {
				if caller.Type == triggerCallerCrew && caller.ID == h.SourceID {
					fn.AllowedCallers[n].ID = roots.ID
				} else {
					fn.Enabled = false
					fn.AllowedCallers = nil
					break
				}
			}

		}
		p, err = crewMoveRecordPath(roots, "functions.json")
		if err != nil {
			return err
		}
		if err = fsutil.WriteJSONAtomic(p, projectFunctionsDocument{Functions: h.Functions}, 0600); err != nil {
			return err
		}
		// Mac webhooks are definitions only. Online activation gets new secrets;
		// source ciphertext and secret values never enter this transfer artifact.
		var oldHooks projectWebhooksDocument
		hookPath, e := crewMoveRecordPath(roots, "webhooks.json")
		if e != nil {
			return e
		}
		if raw, e := os.ReadFile(hookPath); e == nil {
			if e = json.Unmarshal(raw, &oldHooks); e != nil {
				return e
			}
		} else if !os.IsNotExist(e) {
			return e
		}
		for i := range h.Webhooks {
			hook := &h.Webhooks[i]
			hook.ID = uuid.NewSHA1(uuid.NameSpaceOID, []byte(pair+"/hook/"+hook.ID)).String()
			hook.Project.ID, hook.RunAs, hook.Path = roots.ID, claims.UserID, "/api/hooks/"+hook.ID
			if hook.Action.Tab != "" {
				hook.Action.Tab, err = crewMoveKey(hook.Action.Tab, h, roots.ID, main.ConversationKey)
				if err != nil {
					return err
				}
			}
			for _, old := range oldHooks.Webhooks {
				if old.ID == hook.ID && old.Project == hook.Project && old.RunAs == hook.RunAs && old.Auth.Mode == hook.Auth.Mode {
					hook.Auth.EncryptedSecret = old.Auth.EncryptedSecret
				}
			}
			if hook.Auth.EncryptedSecret == "" {
				secret := make([]byte, 32)
				if _, err = rand.Read(secret); err != nil {
					return err
				}
				hook.Auth.EncryptedSecret, err = encryptSecretValueWithAAD(hex.EncodeToString(secret), productWebhookAAD(roots.ID, hook.ID))
				if err != nil {
					return err
				}
			}
		}
		if err = fsutil.WriteJSONAtomic(hookPath, projectWebhooksDocument{Webhooks: h.Webhooks}, 0600); err != nil {
			return err
		}
		log.Printf("[CREW_SYNC] project=%s phase=records-imported schedules=%d functions=%d webhooks=%d", roots.ID, len(h.Schedules), len(h.Functions), len(h.Webhooks))
		return nil
	}
	if err = setCrewMovePhase(roots.ID, pair, transfer, "chats"); err != nil {
		return err
	}
	var header crewMoveHeader
	if err = readCrewMoveData(statePath, func(h crewMoveHeader, chat *crewMoveChat) error {
		if chat == nil {
			header = h
			return nil
		}
		return apply(h, chat)
	}); err != nil {
		return err
	}
	return apply(header, nil)
}

// Validate every record before changing destination files or settings. An owner
// may supply content, but never another Crew's routing, schedule caller or login.
func validateCrewMoveData(p, source, destination, main string) error {
	return readCrewMoveData(p, func(h crewMoveHeader, chat *crewMoveChat) error {
		if h.SourceID != source || h.SourceOwner == "" {
			return fmt.Errorf("move records do not match the paired Crew")
		}
		if chat != nil {
			if _, err := crewMoveKey(chat.Key, h, destination, main); err != nil {
				return err
			}
			if err := validateCrewHandoffConversation(chat.Conversation); err != nil {
				return err
			}
			if chat.Conversation.Native != nil {
				return chat.Conversation.Native.Validate()
			}
			return nil
		}
		if err := validateCrewMoveCallers(h); err != nil {
			return err
		}
		for name, selected := range h.Settings {
			if name != "product.json" && name != "workflow.json" {
				return fmt.Errorf("invalid moved Crew settings")
			}
			var caps map[string]json.RawMessage
			if v := selected["capabilities"]; len(v) > 0 {
				if err := json.Unmarshal(v, &caps); err != nil {
					return err
				}
			}
		}
		ids := map[string]bool{}
		for _, s := range h.Schedules {
			if ids[s.ID] {
				return fmt.Errorf("duplicate moved schedule")
			}
			ids[s.ID] = true
		}
		for id, state := range h.Watchers {
			if !ids[id] || (state.Wake != nil && (state.Wake.Owner != h.SourceOwner || state.Wake.JobID != projectScheduleJobID(crewProfileID, h.SourceID, id))) {
				return fmt.Errorf("watcher state does not belong to the moving Crew")
			}
		}
		for _, s := range h.Schedules {
			if _, ok := h.Watchers[s.ID]; ok {
				var input scheduleExecutionInput
				if json.Unmarshal(s.Input, &input) != nil || input.Condition == nil {
					return fmt.Errorf("moved watcher state has no watcher")
				}
			}
			if s.Project.Kind != "crew" || s.Project.ID != source || s.RunAs != h.SourceOwner {
				return fmt.Errorf("a schedule does not belong to the moving owner")
			}
			if err := s.Validate(); err != nil {
				return err
			}
			if s.Action.Tab != "" {
				if _, err := crewMoveKey(s.Action.Tab, h, destination, main); err != nil {
					return err
				}
			}
			if err := rebindCrewMoveWatcher(&s, h, destination, main, "validation"); err != nil {
				return err
			}
			if s.When.After != nil {
				for _, id := range s.When.After.Schedules {
					if !ids[id] {
						return fmt.Errorf("a schedule depends on another project's schedule")
					}
				}
			}
		}
		for _, hook := range h.Webhooks {
			if hook.ID == "" || hook.Project.Kind != "crew" || hook.Project.ID != source || hook.RunAs != h.SourceOwner || hook.Auth.EncryptedSecret != "" || (hook.Auth.Mode != "bearer" && hook.Auth.Mode != "github") || !json.Valid(hook.Input) || hook.Action.Type != "message" {
				return fmt.Errorf("invalid moved webhook")
			}
			if hook.Action.Tab != "" {
				if _, err := crewMoveKey(hook.Action.Tab, h, destination, main); err != nil {
					return err
				}
			}
		}
		ids = map[string]bool{}
		for _, fn := range h.Functions {
			if fn.ID == "" || fn.Name == "" || ids[fn.ID] || (fn.Action.Type != "message" && fn.Action.Type != "run") || !json.Valid(fn.Input) {
				return fmt.Errorf("invalid moved function")
			}
			ids[fn.ID] = true
			if fn.Action.Tab != "" && (len([]rune(fn.Action.Tab)) > productAgentNameMaxRunes || crewAgentChatID(fn.Action.Tab) == "") {
				return fmt.Errorf("invalid moved function tab")
			}
		}
		return nil
	})
}

func rebindCrewMoveWatcher(s *projectactions.Schedule, h crewMoveHeader, destination, main, pair string) error {
	var input scheduleExecutionInput
	if err := json.Unmarshal(s.Input, &input); err != nil {
		return err
	}
	input.TeamAuthorizedBy = ""
	input.TeamPolicyRevision = 0
	if cfg := input.Condition; cfg != nil {
		// Target.ChatID is a short tab ID; dispatch prefixes the destination Crew.
		if cfg.Target.TriggerID != "" {
			cfg.Target.TriggerID = uuid.NewSHA1(uuid.NameSpaceOID, []byte(pair+"/hook/"+cfg.Target.TriggerID)).String()
		}
		// Personal account IDs and a GitHub watcher's local runtime are not
		// destination authority. Retain the definition but require its account.
		if cfg.PersonalGmail() {
			cfg.EmailAccountID = ""
			s.Enabled = false
		}
		if pr := cfg.GitHubPR; pr != nil {
			if pr.ChatKey != "" {
				mapped, err := crewMoveKey(pr.ChatKey, h, destination, main)
				if err != nil {
					return err
				}
				pr.ChatKey = mapped
			}
			pr.SessionID, pr.Worktree, pr.Login = "", "", ""
			s.Enabled = false
		}
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return err
	}
	s.Input = raw
	return nil
}

func crewMoveNotice(statePath string) string {
	message := "Your agents, chats and schedules are available online."
	_ = readCrewMoveData(statePath, func(h crewMoveHeader, chat *crewMoveChat) error {
		if chat != nil {
			return nil
		}
		if len(h.Webhooks) > 0 {
			message += " Webhooks have new URLs and credentials; open Webhooks to connect callers."
		}
		for _, s := range h.Schedules {
			var input scheduleExecutionInput
			if json.Unmarshal(s.Input, &input) == nil && input.Condition != nil && (input.Condition.PersonalGmail() || input.Condition.GitHubPR != nil) {
				message += " Account-based watchers are paused until you choose their accounts online."
				break
			}
		}
		return nil
	})
	return message
}
