package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productschedule"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtypes"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

const (
	productConversationRegistryVersion = 1
	productConversationRegistryFile    = "product-conversations.json"
)

var (
	productConversationKeyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,255}$`)
	productConversationLocks      [64]sync.Mutex
)

// ProductConversationRecord is the durable identity binding for one product
// conversation. ConversationID is the logical chat; SessionID is the current
// AgentWorks history/runtime owner. They are intentionally separate so a
// future runtime migration does not change the product conversation identity.
type ProductConversationRecord struct {
	ConnectionID    string `json:"connection_id,omitempty"`
	ConversationID  string `json:"conversation_id"`
	ConversationKey string `json:"conversation_key"`
	ProfileID       string `json:"profile_id"`
	ProfileVersion  int    `json:"profile_version"`
	SessionID       string `json:"session_id"`
	WorkspacePath   string `json:"workspace_path"`
	ResourceID      string `json:"resource_id,omitempty"`
	Title           string `json:"title,omitempty"`
	Description     string `json:"description,omitempty"`
	// Provider is the coding-agent runtime the conversation most recently used.
	// Changing it relaunches the retained CLI while the AgentWorks conversation
	// history remains owned by this stable product session.
	Provider string `json:"provider,omitempty"`
	// ModelID and ReasoningEffort are the runtime options the conversation's
	// already-running tmux-backed CLI process was last launched with. Runtime
	// selections may change mid-chat, but the running process does not
	// notice on its own — the interactive adapter only builds its CLI flags
	// (--model, -c model_reasoning_effort) once, at first acquire, and every
	// later turn against the same owner session id silently reuses that
	// process. bindRuntime compares against these to know when the caller
	// must close the tmux session so the next turn relaunches with the new
	// flags.
	ModelID              string   `json:"model_id,omitempty"`
	ReasoningEffort      string   `json:"reasoning_effort,omitempty"`
	EnabledServers       []string `json:"enabled_servers,omitempty"`
	SelectedSkills       []string `json:"selected_skills,omitempty"`
	WorkflowContextPaths []string `json:"workflow_context_paths,omitempty"`
	// ProjectLLMConfig is loaded from the project's runtime manifest for this request. It is not
	// duplicated into the conversation registry.
	ProjectLLMConfig            *workflowtypes.PresetLLMConfig `json:"-"`
	ProjectSelectedServers      []string                       `json:"-"`
	ProjectSelectedSkills       []string                       `json:"-"`
	ProjectWorkflowContextPaths []string                       `json:"-"`
	CreatedAt                   string                         `json:"created_at"`
	UpdatedAt                   string                         `json:"updated_at"`
}

type productConversationRegistryDocument struct {
	Version int                                  `json:"version"`
	Entries map[string]ProductConversationRecord `json:"entries"`
	// Previous keeps, per entry, the conversations a rotation or a switch
	// moved out of the live slot, newest first, so a product can list,
	// reopen or forget its earlier chats. Identities only; the transcripts
	// stay in chat_history.
	Previous map[string][]ProductConversationRecord `json:"previous,omitempty"`
}

// productConversationPreviousLimit bounds how many earlier conversations one
// slot remembers; the oldest fall off (their transcripts remain on disk).
const productConversationPreviousLimit = 200

// rememberPreviousConversation moves a record to the front of a slot's
// previous list, dropping any older copy of the same session.
func rememberPreviousConversation(document *productConversationRegistryDocument, entryKey string, record ProductConversationRecord) {
	if strings.TrimSpace(record.SessionID) == "" {
		return
	}
	if document.Previous == nil {
		document.Previous = map[string][]ProductConversationRecord{}
	}
	kept := make([]ProductConversationRecord, 0, len(document.Previous[entryKey])+1)
	kept = append(kept, record)
	for _, previous := range document.Previous[entryKey] {
		if previous.SessionID != record.SessionID {
			kept = append(kept, previous)
		}
	}
	if len(kept) > productConversationPreviousLimit {
		kept = kept[:productConversationPreviousLimit]
	}
	document.Previous[entryKey] = kept
}

func (store productConversationRegistryStore) loadDocument(ctx context.Context, path string) (productConversationRegistryDocument, error) {
	document := productConversationRegistryDocument{Version: productConversationRegistryVersion, Entries: map[string]ProductConversationRecord{}}
	raw, exists, err := store.read(ctx, path)
	if err != nil {
		return document, fmt.Errorf("read product conversation registry: %w", err)
	}
	if exists && strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &document); err != nil {
			return document, fmt.Errorf("decode product conversation registry: %w", err)
		}
		if document.Entries == nil {
			document.Entries = map[string]ProductConversationRecord{}
		}
	}
	return document, nil
}

// history reports a slot's live conversation (ok=false when none has been
// opened yet) and the ones moved out of it, newest first.
func (store productConversationRegistryStore) history(ctx context.Context, userID string, profile agentprofiles.Profile, binding productConversationBinding) (current ProductConversationRecord, ok bool, previous []ProductConversationRecord, err error) {
	path := productConversationRegistryPath(userID)
	mutex := productConversationRegistryMutex(path)
	mutex.Lock()
	defer mutex.Unlock()
	document, err := store.loadDocument(ctx, path)
	if err != nil {
		return ProductConversationRecord{}, false, nil, err
	}
	entryKey := productConversationRegistryEntryKey(profile.ID, binding.ConversationKey)
	current, ok = document.Entries[entryKey]
	previous = append([]ProductConversationRecord(nil), document.Previous[entryKey]...)
	return current, ok, previous, nil
}

// removeSlot forgets the live and historical conversation identities for one
// product resource. Project deletion owns this operation; deleting an ordinary
// historical chat continues to use forget so it cannot remove the live slot.
func (store productConversationRegistryStore) removeSlot(ctx context.Context, userID string, profile agentprofiles.Profile, binding productConversationBinding) ([]ProductConversationRecord, error) {
	path := productConversationRegistryPath(userID)
	mutex := productConversationRegistryMutex(path)
	mutex.Lock()
	defer mutex.Unlock()
	document, err := store.loadDocument(ctx, path)
	if err != nil {
		return nil, err
	}
	entryKey := productConversationRegistryEntryKey(profile.ID, binding.ConversationKey)
	records := append([]ProductConversationRecord(nil), document.Previous[entryKey]...)
	if current, ok := document.Entries[entryKey]; ok {
		records = append([]ProductConversationRecord{current}, records...)
	}
	delete(document.Entries, entryKey)
	delete(document.Previous, entryKey)
	document.Version = productConversationRegistryVersion
	if err := store.writeDocument(ctx, path, document); err != nil {
		return nil, err
	}
	return records, nil
}

// liveSessionIDs is every session a profile's slots currently point at — its
// main chat and any isolated ones (a check-in, a second phone).
func (store productConversationRegistryStore) liveSessionIDs(ctx context.Context, userID, profileID string) (map[string]bool, error) {
	path := productConversationRegistryPath(userID)
	mutex := productConversationRegistryMutex(path)
	mutex.Lock()
	defer mutex.Unlock()
	document, err := store.loadDocument(ctx, path)
	if err != nil {
		return nil, err
	}
	live := map[string]bool{}
	prefix := strings.TrimSpace(profileID) + "/"
	for entryKey, record := range document.Entries {
		if strings.HasPrefix(entryKey, prefix) && strings.TrimSpace(record.SessionID) != "" {
			live[record.SessionID] = true
		}
	}
	return live, nil
}

// switchTo makes an earlier conversation the slot's live one again; the one
// it replaces joins the previous list. A session not in that list is
// accepted only when the caller verified it is this slot's (a chat rotated
// before the registry kept history), and gets a fresh conversation id.
func (store productConversationRegistryStore) switchTo(ctx context.Context, userID string, profile agentprofiles.Profile, binding productConversationBinding, sessionID string, verified bool) (ProductConversationRecord, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ProductConversationRecord{}, fmt.Errorf("session_id is required")
	}
	path := productConversationRegistryPath(userID)
	mutex := productConversationRegistryMutex(path)
	mutex.Lock()
	defer mutex.Unlock()
	document, err := store.loadDocument(ctx, path)
	if err != nil {
		return ProductConversationRecord{}, err
	}
	entryKey := productConversationRegistryEntryKey(profile.ID, binding.ConversationKey)
	current, ok := document.Entries[entryKey]
	if !ok {
		// Work can reopen a historical project chat into a new tab-specific key.
		// The caller may only reach this branch after chat_history verified that
		// the selected session belongs to this binding's workspace. Bind that
		// trusted session directly instead of manufacturing an empty conversation
		// that would hide the transcript and launch a new provider-native session.
		if !verified {
			return ProductConversationRecord{}, fmt.Errorf("product conversation %q has not been opened yet", entryKey)
		}
		now := store.now().UTC().Format(time.RFC3339Nano)
		restored := ProductConversationRecord{
			ConversationID:              "conversation-" + store.newID(),
			ConversationKey:             binding.ConversationKey,
			ProfileID:                   profile.ID,
			ProfileVersion:              profile.Version,
			SessionID:                   sessionID,
			WorkspacePath:               binding.WorkspacePath,
			ResourceID:                  binding.ResourceID,
			Title:                       binding.Title,
			Description:                 binding.Description,
			ProjectLLMConfig:            binding.ProjectLLMConfig,
			ProjectSelectedServers:      append([]string(nil), binding.ProjectSelectedServers...),
			ProjectSelectedSkills:       append([]string(nil), binding.ProjectSelectedSkills...),
			ProjectWorkflowContextPaths: append([]string(nil), binding.ProjectWorkflowContextPaths...),
			CreatedAt:                   now,
			UpdatedAt:                   now,
		}
		if strings.TrimSpace(binding.ManifestPath) != "" {
			if err := store.writeManifestSessionID(ctx, binding.ManifestPath, restored.SessionID); err != nil {
				return ProductConversationRecord{}, err
			}
		}
		document.Version = productConversationRegistryVersion
		document.Entries[entryKey] = restored
		if err := store.writeDocument(ctx, path, document); err != nil {
			return ProductConversationRecord{}, err
		}
		return restored, nil
	}
	if current.SessionID == sessionID {
		return current, nil
	}
	now := store.now().UTC().Format(time.RFC3339Nano)
	var target *ProductConversationRecord
	remaining := make([]ProductConversationRecord, 0, len(document.Previous[entryKey]))
	for _, previous := range document.Previous[entryKey] {
		if previous.SessionID == sessionID && target == nil {
			found := previous
			target = &found
			continue
		}
		remaining = append(remaining, previous)
	}
	if target == nil {
		if !verified {
			return ProductConversationRecord{}, fmt.Errorf("session %q is not one of this conversation's earlier chats", sessionID)
		}
		restored := current
		restored.ConversationID = "conversation-" + store.newID()
		restored.SessionID = sessionID
		restored.CreatedAt = now
		target = &restored
	}
	if document.Previous == nil {
		document.Previous = map[string][]ProductConversationRecord{}
	}
	document.Previous[entryKey] = remaining
	rememberPreviousConversation(&document, entryKey, current)
	target.UpdatedAt = now
	if strings.TrimSpace(binding.ManifestPath) != "" {
		if err := store.writeManifestSessionID(ctx, binding.ManifestPath, target.SessionID); err != nil {
			return ProductConversationRecord{}, err
		}
	}
	document.Version = productConversationRegistryVersion
	document.Entries[entryKey] = *target
	if err := store.writeDocument(ctx, path, document); err != nil {
		return ProductConversationRecord{}, err
	}
	return *target, nil
}

// forget drops an earlier conversation from a slot's previous list. The live
// conversation is refused: start a new chat first. A session the registry
// never listed (rotated before it kept history) is simply not there.
func (store productConversationRegistryStore) forget(ctx context.Context, userID string, profile agentprofiles.Profile, binding productConversationBinding, sessionID string) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return fmt.Errorf("session_id is required")
	}
	path := productConversationRegistryPath(userID)
	mutex := productConversationRegistryMutex(path)
	mutex.Lock()
	defer mutex.Unlock()
	document, err := store.loadDocument(ctx, path)
	if err != nil {
		return err
	}
	entryKey := productConversationRegistryEntryKey(profile.ID, binding.ConversationKey)
	if current, ok := document.Entries[entryKey]; ok && current.SessionID == sessionID {
		return fmt.Errorf("this is the live conversation — start a new chat before deleting it")
	}
	remaining := make([]ProductConversationRecord, 0, len(document.Previous[entryKey]))
	for _, previous := range document.Previous[entryKey] {
		if previous.SessionID != sessionID {
			remaining = append(remaining, previous)
		}
	}
	if len(remaining) == len(document.Previous[entryKey]) {
		return nil
	}
	if document.Previous == nil {
		document.Previous = map[string][]ProductConversationRecord{}
	}
	document.Previous[entryKey] = remaining
	document.Version = productConversationRegistryVersion
	return store.writeDocument(ctx, path, document)
}

type productConversationBinding struct {
	ConversationKey             string
	WorkspacePath               string
	ManifestPath                string
	ResourceID                  string
	Title                       string
	Description                 string
	AuthoritativeSessionID      string
	ProjectLLMConfig            *workflowtypes.PresetLLMConfig
	ProjectSelectedServers      []string
	ProjectSelectedSkills       []string
	ProjectWorkflowContextPaths []string
	ProjectNativeAgentTools     bool
}

type productConversationRegistryStore struct {
	read  func(context.Context, string) (string, bool, error)
	write func(context.Context, string, string) error
	now   func() time.Time
	newID func() string
}

func defaultProductConversationRegistryStore() productConversationRegistryStore {
	return productConversationRegistryStore{
		read:  readFileFromWorkspace,
		write: writeRawFileToWorkspace,
		now:   time.Now,
		newID: uuid.NewString,
	}
}

func productConversationRegistryPath(userID string) string {
	return filepath.ToSlash(filepath.Join(chatHistoryRoot(userID), productConversationRegistryFile))
}

func productConversationRegistryEntryKey(profileID, conversationKey string) string {
	return strings.TrimSpace(profileID) + "/" + strings.TrimSpace(conversationKey)
}

func productConversationRegistryMutex(path string) *sync.Mutex {
	var hash uint64 = 1469598103934665603
	for i := 0; i < len(path); i++ {
		hash ^= uint64(path[i])
		hash *= 1099511628211
	}
	return &productConversationLocks[hash%uint64(len(productConversationLocks))]
}

func (store productConversationRegistryStore) resolveOrCreate(
	ctx context.Context,
	userID string,
	profile agentprofiles.Profile,
	binding productConversationBinding,
	preferredSessionID string,
) (ProductConversationRecord, error) {
	path := productConversationRegistryPath(userID)
	mutex := productConversationRegistryMutex(path)
	mutex.Lock()
	defer mutex.Unlock()

	document := productConversationRegistryDocument{
		Version: productConversationRegistryVersion,
		Entries: map[string]ProductConversationRecord{},
	}
	if raw, exists, err := store.read(ctx, path); err != nil {
		return ProductConversationRecord{}, fmt.Errorf("read product conversation registry: %w", err)
	} else if exists && strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &document); err != nil {
			return ProductConversationRecord{}, fmt.Errorf("decode product conversation registry: %w", err)
		}
		if document.Entries == nil {
			document.Entries = map[string]ProductConversationRecord{}
		}
	}

	entryKey := productConversationRegistryEntryKey(profile.ID, binding.ConversationKey)
	now := store.now().UTC().Format(time.RFC3339Nano)
	if existing, ok := document.Entries[entryKey]; ok {
		if strings.TrimSpace(existing.ConversationID) == "" || strings.TrimSpace(existing.SessionID) == "" {
			return ProductConversationRecord{}, fmt.Errorf("product conversation %q has an incomplete durable identity", entryKey)
		}
		changed := existing.ProfileVersion != profile.Version ||
			existing.WorkspacePath != binding.WorkspacePath ||
			existing.ResourceID != binding.ResourceID ||
			existing.Title != binding.Title ||
			existing.Description != binding.Description
		existing.ProfileVersion = profile.Version
		existing.WorkspacePath = binding.WorkspacePath
		existing.ResourceID = binding.ResourceID
		existing.Title = binding.Title
		existing.Description = binding.Description
		// A keyed project manifest owns its session identity. This also makes a
		// completed conversation rotation resilient to a process crash between
		// updating the manifest and this registry document.
		if authoritative := strings.TrimSpace(binding.AuthoritativeSessionID); authoritative != "" && existing.SessionID != authoritative {
			existing.SessionID = authoritative
			changed = true
		}
		if changed {
			existing.UpdatedAt = now
			document.Entries[entryKey] = existing
			if err := store.writeDocument(ctx, path, document); err != nil {
				return ProductConversationRecord{}, err
			}
		}
		existing.ProjectLLMConfig = binding.ProjectLLMConfig
		existing.ProjectSelectedServers = append([]string(nil), binding.ProjectSelectedServers...)
		existing.ProjectSelectedSkills = append([]string(nil), binding.ProjectSelectedSkills...)
		existing.ProjectWorkflowContextPaths = append([]string(nil), binding.ProjectWorkflowContextPaths...)
		return existing, nil
	}

	sessionID := strings.TrimSpace(binding.AuthoritativeSessionID)
	if sessionID == "" {
		sessionID = strings.TrimSpace(preferredSessionID)
	}
	if sessionID == "" {
		sessionID = "product-" + store.newID()
	}
	record := ProductConversationRecord{
		ConversationID:              "conversation-" + store.newID(),
		ConversationKey:             binding.ConversationKey,
		ProfileID:                   profile.ID,
		ProfileVersion:              profile.Version,
		SessionID:                   sessionID,
		WorkspacePath:               binding.WorkspacePath,
		ResourceID:                  binding.ResourceID,
		Title:                       binding.Title,
		Description:                 binding.Description,
		CreatedAt:                   now,
		UpdatedAt:                   now,
		ProjectLLMConfig:            binding.ProjectLLMConfig,
		ProjectSelectedServers:      append([]string(nil), binding.ProjectSelectedServers...),
		ProjectSelectedSkills:       append([]string(nil), binding.ProjectSelectedSkills...),
		ProjectWorkflowContextPaths: append([]string(nil), binding.ProjectWorkflowContextPaths...),
	}
	document.Version = productConversationRegistryVersion
	document.Entries[entryKey] = record
	if err := store.writeDocument(ctx, path, document); err != nil {
		return ProductConversationRecord{}, err
	}
	return record, nil
}

// rotate starts a fresh durable conversation for a product chat. It retains
// the product/project and its files; it deliberately does not erase the old
// chat history, which remains an audit record but is no longer the active
// conversation for this product resource.
func (store productConversationRegistryStore) rotate(
	ctx context.Context,
	userID string,
	profile agentprofiles.Profile,
	binding productConversationBinding,
) (ProductConversationRecord, error) {
	path := productConversationRegistryPath(userID)
	mutex := productConversationRegistryMutex(path)
	mutex.Lock()
	defer mutex.Unlock()

	document := productConversationRegistryDocument{
		Version: productConversationRegistryVersion,
		Entries: map[string]ProductConversationRecord{},
	}
	if raw, exists, err := store.read(ctx, path); err != nil {
		return ProductConversationRecord{}, fmt.Errorf("read product conversation registry: %w", err)
	} else if exists && strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &document); err != nil {
			return ProductConversationRecord{}, fmt.Errorf("decode product conversation registry: %w", err)
		}
		if document.Entries == nil {
			document.Entries = map[string]ProductConversationRecord{}
		}
	}

	now := store.now().UTC().Format(time.RFC3339Nano)
	entryKey := productConversationRegistryEntryKey(profile.ID, binding.ConversationKey)
	createdAt := now
	if existing, ok := document.Entries[entryKey]; ok && strings.TrimSpace(existing.CreatedAt) != "" {
		createdAt = existing.CreatedAt
	}
	record := ProductConversationRecord{
		ConversationID:              "conversation-" + store.newID(),
		ConversationKey:             binding.ConversationKey,
		ProfileID:                   profile.ID,
		ProfileVersion:              profile.Version,
		SessionID:                   "product-" + store.newID(),
		WorkspacePath:               binding.WorkspacePath,
		ResourceID:                  binding.ResourceID,
		Title:                       binding.Title,
		Description:                 binding.Description,
		ProjectLLMConfig:            binding.ProjectLLMConfig,
		ProjectSelectedServers:      append([]string(nil), binding.ProjectSelectedServers...),
		ProjectSelectedSkills:       append([]string(nil), binding.ProjectSelectedSkills...),
		ProjectWorkflowContextPaths: append([]string(nil), binding.ProjectWorkflowContextPaths...),
		CreatedAt:                   createdAt,
		UpdatedAt:                   now,
	}

	// A project manifest is an authoritative runtime binding. Update it before
	// exposing the new record, so an ordinary resolve after this call cannot
	// restore the previous project session.
	if strings.TrimSpace(binding.ManifestPath) != "" {
		if err := store.writeManifestSessionID(ctx, binding.ManifestPath, record.SessionID); err != nil {
			return ProductConversationRecord{}, err
		}
	}
	// The conversation this one replaces stays listed, so the product can
	// reopen it (its transcript is still in chat_history).
	if existing, ok := document.Entries[entryKey]; ok {
		rememberPreviousConversation(&document, entryKey, existing)
	}
	document.Version = productConversationRegistryVersion
	document.Entries[entryKey] = record
	if err := store.writeDocument(ctx, path, document); err != nil {
		return ProductConversationRecord{}, err
	}
	return record, nil
}

// bindRuntime records the runtime selected for a conversation. A provider,
// model or reasoning-effort change is accepted and recorded. restartNeeded
// reports that the tmux-backed CLI process was launched with older settings;
// the caller closes it before the next turn so the shared runner relaunches it.
func (store productConversationRegistryStore) bindRuntime(
	ctx context.Context,
	userID string,
	profile agentprofiles.Profile,
	conversationKey string,
	provider, modelID, reasoningEffort string,
) (boundProvider string, restartNeeded bool, err error) {
	return store.bindRuntimeConfiguration(ctx, userID, profile, conversationKey, provider, modelID, reasoningEffort, nil, nil, nil)
}

func (store productConversationRegistryStore) bindRuntimeConfiguration(
	ctx context.Context,
	userID string,
	profile agentprofiles.Profile,
	conversationKey string,
	provider, modelID, reasoningEffort string,
	enabledServers, selectedSkills, workflowContextPaths []string,
	connectionIDs ...string,
) (boundProvider string, restartNeeded bool, err error) {
	provider = strings.TrimSpace(provider)
	modelID = strings.TrimSpace(modelID)
	reasoningEffort = strings.TrimSpace(reasoningEffort)
	enabledServers = canonicalRuntimeSelection(enabledServers)
	selectedSkills = canonicalRuntimeSelection(selectedSkills)
	workflowContextPaths = canonicalRuntimeSelection(workflowContextPaths)
	path := productConversationRegistryPath(userID)
	mutex := productConversationRegistryMutex(path)
	mutex.Lock()
	defer mutex.Unlock()

	document := productConversationRegistryDocument{Version: productConversationRegistryVersion, Entries: map[string]ProductConversationRecord{}}
	raw, exists, readErr := store.read(ctx, path)
	if readErr != nil {
		return "", false, fmt.Errorf("read product conversation registry: %w", readErr)
	}
	if exists && strings.TrimSpace(raw) != "" {
		if decodeErr := json.Unmarshal([]byte(raw), &document); decodeErr != nil {
			return "", false, fmt.Errorf("decode product conversation registry: %w", decodeErr)
		}
		if document.Entries == nil {
			document.Entries = map[string]ProductConversationRecord{}
		}
	}
	entryKey := productConversationRegistryEntryKey(profile.ID, conversationKey)
	record, ok := document.Entries[entryKey]
	if !ok {
		return "", false, fmt.Errorf("product conversation %q is not registered", entryKey)
	}
	if bound := strings.TrimSpace(record.Provider); bound != "" {
		restartNeeded = !strings.EqualFold(bound, provider) ||
			(modelID != "" && !strings.EqualFold(strings.TrimSpace(record.ModelID), modelID)) ||
			(reasoningEffort != "" && !strings.EqualFold(strings.TrimSpace(record.ReasoningEffort), reasoningEffort)) ||
			!sameRuntimeSelection(record.EnabledServers, enabledServers) ||
			!sameRuntimeSelection(record.SelectedSkills, selectedSkills)
	}
	if len(connectionIDs) > 0 {
		if record.ConnectionID != connectionIDs[0] {
			restartNeeded = true
		}
		record.ConnectionID = connectionIDs[0]
	}
	record.Provider = provider
	if modelID != "" {
		record.ModelID = modelID
	}
	if reasoningEffort != "" {
		record.ReasoningEffort = reasoningEffort
	}
	record.EnabledServers = enabledServers
	record.SelectedSkills = selectedSkills
	record.WorkflowContextPaths = workflowContextPaths
	record.UpdatedAt = store.now().UTC().Format(time.RFC3339Nano)
	document.Version = productConversationRegistryVersion
	document.Entries[entryKey] = record
	if writeErr := store.writeDocument(ctx, path, document); writeErr != nil {
		return "", false, writeErr
	}
	return provider, restartNeeded, nil
}

func canonicalRuntimeSelection(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool { return strings.ToLower(result[i]) < strings.ToLower(result[j]) })
	return result
}

func sameRuntimeSelection(left, right []string) bool {
	left = canonicalRuntimeSelection(left)
	right = canonicalRuntimeSelection(right)
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if !strings.EqualFold(left[index], right[index]) {
			return false
		}
	}
	return true
}

func firstNonEmptyStrings(primary, fallback []string) []string {
	if primary != nil {
		return primary
	}
	return fallback
}

func (store productConversationRegistryStore) writeManifestSessionID(ctx context.Context, manifestPath, sessionID string) error {
	raw, exists, err := store.read(ctx, manifestPath)
	if err != nil {
		return fmt.Errorf("read product manifest: %w", err)
	}
	if !exists {
		return fmt.Errorf("product manifest was not found")
	}
	var manifest map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &manifest); err != nil {
		return fmt.Errorf("decode product manifest: %w", err)
	}
	encodedSessionID, err := json.Marshal(sessionID)
	if err != nil {
		return fmt.Errorf("encode product session id: %w", err)
	}
	manifest["session_id"] = encodedSessionID
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode product manifest: %w", err)
	}
	if err := store.write(ctx, manifestPath, string(encoded)+"\n"); err != nil {
		return fmt.Errorf("write product manifest: %w", err)
	}
	return nil
}

func (store productConversationRegistryStore) writeDocument(ctx context.Context, path string, document productConversationRegistryDocument) error {
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return fmt.Errorf("encode product conversation registry: %w", err)
	}
	if err := store.write(ctx, path, string(data)+"\n"); err != nil {
		return fmt.Errorf("write product conversation registry: %w", err)
	}
	return nil
}

// displayTitle is the name people know the project by: the Crew's identity
// name ("SDE") when it has one, else its folder title ("gptlive1").
func (m productProjectManifest) displayTitle() string {
	return firstNonEmptyTrimmed(m.Identity.Name, m.Title)
}

type productProjectManifest struct {
	KnowledgebaseContractHistory json.RawMessage `json:"knowledgebase_contract_history,omitempty"`
	SharedKnowledgebase          json.RawMessage `json:"shared_knowledgebase,omitempty"`
	KnowledgebaseMode            string          `json:"knowledgebase_mode,omitempty"`
	KnowledgebaseMigration       json.RawMessage `json:"knowledgebase_migration,omitempty"`
	SchemaVersion                int             `json:"schema_version"`
	Product                      string          `json:"product,omitempty"`
	// OwnerID is the project's owner (PLAT-442): carried so manifest rewrites keep it.
	OwnerID string `json:"owner_id,omitempty"`
	// LegacyPaths are the places a Crew lived before the Crew move (PLAT-442 step 4), the migration's record of
	// the old spellings that must keep resolving; carried so manifest rewrites keep them.
	LegacyPaths []string `json:"legacy_paths,omitempty"`
	ID          string   `json:"id"`
	Title       string   `json:"title,omitempty"`
	Label       string   `json:"label,omitempty"`
	Description string   `json:"description,omitempty"`
	SessionID   string   `json:"session_id,omitempty"`
	CreatedAt   string   `json:"created_at,omitempty"`
	UpdatedAt   string   `json:"updated_at,omitempty"`
	Identity    struct {
		Name string `json:"name,omitempty"`
		Icon string `json:"icon,omitempty"`
		// Role is carried so manifest rewrites preserve the Crew's role.
		Role string `json:"role,omitempty"`
	} `json:"identity,omitempty"`
	Schedules            []productschedule.Schedule `json:"schedules,omitempty"`
	Triggers             []productWebhookTrigger    `json:"triggers,omitempty"`
	WorkflowContextPaths []string                   `json:"workflow_context_paths,omitempty"`
	Capabilities         struct {
		LLMConfig       *workflowtypes.PresetLLMConfig `json:"llm_config,omitempty"`
		SelectedServers []string                       `json:"selected_servers,omitempty"`
		SelectedSkills  []string                       `json:"selected_skills,omitempty"`
		// Pointer preserves the contract's three relevant states when schedule
		// updates rewrite the runtime manifest: missing (legacy, migrate once), empty
		// (explicitly attach none), and populated.
		SelectedSecrets           *[]string `json:"selected_secrets,omitempty"`
		SelectedGlobalSecretNames *[]string `json:"selected_global_secret_names,omitempty"`
		WorkflowContextPaths      []string  `json:"workflow_context_paths,omitempty"`
		// The UI writes these on creation; the struct must carry them so
		// server-side manifest rewrites (trigger saves, selection updates)
		// preserve rather than drop them.
		SelectedTools        []string `json:"selected_tools,omitempty"`
		BrowserMode          string   `json:"browser_mode,omitempty"`
		UseCodeExecutionMode bool     `json:"use_code_execution_mode,omitempty"`
		// NativeAgentTools ("Native agent tools" in the crew UI) runs the crew's
		// coding agents in agent_tools mode full: their own tools in a sandbox
		// limited to the crew's folders. On by default: nil means on, only an
		// explicit false turns it off.
		NativeAgentTools *bool `json:"native_agent_tools,omitempty"`
	} `json:"capabilities,omitempty"`
}

type productProjectStore struct {
	listPaths func(context.Context, string) ([]string, bool, error)
	read      func(context.Context, string) (string, bool, error)
}

func defaultProductProjectStore() productProjectStore {
	return productProjectStore{
		listPaths: func(ctx context.Context, root string) ([]string, bool, error) {
			listing, exists, err := listWorkspaceFolder(ctx, root, 3)
			if err != nil || !exists {
				return nil, exists, err
			}
			paths := []string{}
			collectWorkspaceFilePaths(listing, &paths)
			return paths, true, nil
		},
		read: readFileFromWorkspace,
	}
}

func resolveProductProjectBinding(ctx context.Context, userID string, profile agentprofiles.Profile, projectID string) (productConversationBinding, error) {
	return resolveProductProjectBindingWithStore(ctx, userID, profile, projectID, defaultProductProjectStore())
}

// errProjectNotFound marks "no such project in this root", as opposed to a project that is there and broken
// (duplicate id, incomplete manifest, unreadable): only the former may fall through to another location.
var errProjectNotFound = errors.New("project not found")

func resolveProductProjectBindingWithStore(
	ctx context.Context,
	userID string,
	profile agentprofiles.Profile,
	projectID string,
	store productProjectStore,
) (productConversationBinding, error) {
	// A project product may own several AgentWorks-style chat tabs. The stable
	// project id remains the authorization/resource boundary; a suffix gives
	// each chat its own native CLI session and durable conversation entry.
	conversationKey := strings.TrimSpace(projectID)
	resourceProjectID := conversationKey
	if base, _, found := strings.Cut(conversationKey, ":"); found {
		resourceProjectID = strings.TrimSpace(base)
		if resourceProjectID == "" {
			return productConversationBinding{}, fmt.Errorf("project conversation key has no project id")
		}
	}
	projectsRoot, err := cleanAgentProfileWorkspace(profile.Runtime.Workspace.ProjectsRoot, userID)
	if err != nil {
		return productConversationBinding{}, fmt.Errorf("invalid product projects root: %w", err)
	}
	runtimeRoot := agentProfileRuntimeWorkspace(userID, projectsRoot)
	binding, err := resolveProductProjectBindingInRoot(ctx, profile, runtimeRoot, conversationKey, resourceProjectID, store, nil)
	if err == nil || !errors.Is(err, errProjectNotFound) || !strings.EqualFold(strings.TrimSpace(profile.ID), crewProfileID) {
		return binding, err
	}
	// A Crew that has moved to the shared root (PLAT-442 step 4) is found there by its manifest owner: the
	// caller's own, whichever way the move happened. Reading it never depends on the creation flag.
	owner := sanitizeUserIDForPath(userID)
	shared, sharedErr := resolveProductProjectBindingInRoot(ctx, profile, workspaceref.SharedCrewRoot, conversationKey, resourceProjectID, store, func(folder string) bool {
		return sharedCrewOwner(folder) == owner
	})
	if sharedErr == nil {
		return shared, nil
	}
	if !errors.Is(sharedErr, errProjectNotFound) {
		return productConversationBinding{}, sharedErr
	}
	return productConversationBinding{}, err
}

// resolveProductProjectBindingInRoot finds one project in runtimeRoot by its manifest. ownerOK, when set, must accept
// the project's folder name: a shared root holds every owner's projects, and who owns one is the server's registry
// (PLAT-449), never the manifest.
func resolveProductProjectBindingInRoot(
	ctx context.Context,
	profile agentprofiles.Profile,
	runtimeRoot, conversationKey, resourceProjectID string,
	store productProjectStore,
	ownerOK func(folder string) bool,
) (productConversationBinding, error) {
	paths, exists, err := store.listPaths(ctx, runtimeRoot)
	if err != nil {
		return productConversationBinding{}, fmt.Errorf("list product projects: %w", err)
	}
	if !exists {
		return productConversationBinding{}, fmt.Errorf("product projects root does not exist: %w", errProjectNotFound)
	}
	sort.Strings(paths)

	var matched *productConversationBinding
	rootPrefix := strings.TrimSuffix(filepath.ToSlash(runtimeRoot), "/") + "/"
	seenCandidatePaths := make(map[string]struct{}, len(paths))
	for _, candidate := range paths {
		candidate = filepath.ToSlash(strings.TrimSpace(candidate))
		if !strings.HasPrefix(candidate, rootPrefix) || !strings.HasSuffix(candidate, "/product.json") {
			continue
		}
		if ownerOK != nil {
			// The shared root: exactly <root>/<folder>/product.json, and not a hidden bookkeeping folder.
			folder := strings.TrimSuffix(strings.TrimPrefix(candidate, rootPrefix), "/product.json")
			if folder == "" || strings.Contains(folder, "/") || strings.HasPrefix(folder, ".") {
				continue
			}
		}
		// The workspace listing can return a folder both nested under its root
		// and as a top-level item, which repeats the same file path. That is one
		// manifest, not a duplicate project id. Different paths with the same id
		// remain a hard error below.
		if _, seen := seenCandidatePaths[candidate]; seen {
			continue
		}
		seenCandidatePaths[candidate] = struct{}{}
		raw, found, readErr := store.read(ctx, candidate)
		if readErr != nil {
			return productConversationBinding{}, fmt.Errorf("read project manifest %s: %w", candidate, readErr)
		}
		if !found {
			continue
		}
		var manifest productProjectManifest
		if json.Unmarshal([]byte(raw), &manifest) != nil ||
			strings.TrimSpace(manifest.Product) != profile.ID ||
			strings.TrimSpace(manifest.ID) != resourceProjectID {
			continue
		}
		if ownerOK != nil && !ownerOK(strings.TrimSuffix(strings.TrimPrefix(candidate, rootPrefix), "/product.json")) {
			continue
		}
		// While the Crew move command copies this Crew the server binds nothing to it (turns, schedules, webhooks, bots).
		if strings.EqualFold(strings.TrimSpace(profile.ID), crewProfileID) && crewMoveInProgress(filepath.Base(filepath.Dir(filepath.FromSlash(candidate)))) {
			return productConversationBinding{}, errCrewBeingMoved
		}
		if strings.TrimSpace(manifest.Title) == "" || strings.TrimSpace(manifest.SessionID) == "" {
			return productConversationBinding{}, fmt.Errorf("project %q has an incomplete product manifest", resourceProjectID)
		}
		if isProjectProfileID(profile.ID) {
			runtimePath := filepath.ToSlash(filepath.Join(filepath.Dir(candidate), "workflow.json"))
			if runtimeRaw, runtimeFound, runtimeErr := store.read(ctx, runtimePath); runtimeErr != nil {
				return productConversationBinding{}, fmt.Errorf("read project runtime manifest %s: %w", runtimePath, runtimeErr)
			} else if runtimeFound {
				var runtimeManifest productProjectManifest
				if err := json.Unmarshal([]byte(runtimeRaw), &runtimeManifest); err != nil {
					return productConversationBinding{}, fmt.Errorf("decode project runtime manifest %s: %w", runtimePath, err)
				}
				manifest.Capabilities = runtimeManifest.Capabilities
				manifest.Schedules = runtimeManifest.Schedules
				manifest.Triggers = runtimeManifest.Triggers
				manifest.SharedKnowledgebase = runtimeManifest.SharedKnowledgebase
				manifest.KnowledgebaseMode = runtimeManifest.KnowledgebaseMode
				manifest.KnowledgebaseMigration = runtimeManifest.KnowledgebaseMigration
				manifest.KnowledgebaseContractHistory = runtimeManifest.KnowledgebaseContractHistory
				manifest.WorkflowContextPaths = runtimeManifest.WorkflowContextPaths
			}
		}
		if matched != nil {
			return productConversationBinding{}, fmt.Errorf("project id %q is duplicated", resourceProjectID)
		}
		workspacePath := filepath.ToSlash(filepath.Dir(candidate))
		matched = &productConversationBinding{
			ConversationKey:             conversationKey,
			WorkspacePath:               workspacePath,
			ManifestPath:                candidate,
			ResourceID:                  resourceProjectID,
			Title:                       strings.TrimSpace(manifest.Title),
			Description:                 strings.TrimSpace(manifest.Description),
			AuthoritativeSessionID:      strings.TrimSpace(manifest.SessionID),
			ProjectLLMConfig:            manifest.Capabilities.LLMConfig,
			ProjectSelectedServers:      append([]string(nil), manifest.Capabilities.SelectedServers...),
			ProjectSelectedSkills:       append([]string(nil), manifest.Capabilities.SelectedSkills...),
			ProjectWorkflowContextPaths: append([]string(nil), firstNonEmptyStrings(manifest.WorkflowContextPaths, manifest.Capabilities.WorkflowContextPaths)...),
			// No user switch for a Crew or a Code (user, 2026-09-29): native
			// agent tools are always on, even where an older project stored
			// "off".
			ProjectNativeAgentTools: true,
		}
		// Only the legacy/default project chat is tied to product.json's one
		// session_id. Additional tabs are independent and live solely in the
		// shared product conversation registry.
		if conversationKey != resourceProjectID {
			matched.ManifestPath = ""
			matched.AuthoritativeSessionID = ""
		}
	}
	if matched == nil {
		return productConversationBinding{}, fmt.Errorf("project %q was not found: %w", resourceProjectID, errProjectNotFound)
	}
	return *matched, nil
}

// resolveIsolatedScheduleBinding is resolveProductConversationBinding's
// singleton case with a fixed "pulse" key instead of "main" — a second,
// independent registry entry (its own session, workspace binding otherwise
// identical) for a schedule that declared Isolated. Not reachable from the
// client-facing conversation_key parameter: that boundary's job is to reject
// exactly this kind of caller-chosen key on a singleton profile, so this is
// a distinct, server-internal path for the scheduler alone, not a relaxation
// of that check.
func resolveIsolatedScheduleBinding(ctx context.Context, userID string, profile agentprofiles.Profile) (productConversationBinding, error) {
	return resolveIsolatedProductBinding(ctx, userID, profile, "pulse", profile.Name+" (check-in)")
}

// resolveIsolatedProjectAutomationBinding gives one project schedule or
// trigger a conversation of its own. It inherits the Crew project's workspace
// and capability policy, but it is not authoritative for product.json's main
// session id. Every schedule, webhook and caller binding keeps one continuing
// chat across runs, keyed by its ID. None steers or writes into the Crew chat.
func resolveIsolatedProjectAutomationBinding(ctx context.Context, userID string, profile agentprofiles.Profile, projectID, kind, automationID, title string) (productConversationBinding, error) {
	base, err := resolveProductConversationBinding(ctx, userID, profile, projectID)
	if err != nil {
		return productConversationBinding{}, err
	}
	return isolateProjectAutomationBinding(base, projectID, kind, automationID, title)
}

func isolateProjectAutomationBinding(base productConversationBinding, projectID, kind, automationID, title string) (productConversationBinding, error) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind != "schedule" && kind != "trigger" {
		return productConversationBinding{}, fmt.Errorf("invalid isolated automation kind %q", kind)
	}
	key := strings.TrimSpace(projectID) + ":" + kind + ":" + strings.TrimSpace(automationID)
	if !productConversationKeyPattern.MatchString(key) {
		return productConversationBinding{}, fmt.Errorf("invalid isolated automation conversation key %q", key)
	}
	base.ConversationKey = key
	base.ManifestPath = ""
	base.AuthoritativeSessionID = ""
	base.Title = firstNonEmptyTrimmed(title, base.Title+" · "+kind)
	return base, nil
}

// resolveIsolatedProductBinding gives a singleton profile a further
// conversation of its own under a server-chosen key — a schedule's, or a
// second phone's — on the same workspace, apart from the profile's main
// conversation. Server-internal: the client-facing binding keeps rejecting
// keys for singleton profiles.
func resolveIsolatedProductBinding(ctx context.Context, userID string, profile agentprofiles.Profile, key, title string) (productConversationBinding, error) {
	if mode := strings.ToLower(strings.TrimSpace(profile.Runtime.Conversation.Mode)); mode != agentprofiles.ConversationModeSingleton {
		return productConversationBinding{}, fmt.Errorf("isolated conversations are only supported for singleton-conversation profiles, got %q", mode)
	}
	key = strings.TrimSpace(key)
	if key == "" || key == "main" || !productConversationKeyPattern.MatchString(key) {
		return productConversationBinding{}, fmt.Errorf("invalid isolated conversation key %q", key)
	}
	workspacePath, err := cleanAgentProfileWorkspace(profile.Runtime.Workspace.Root, userID)
	if err != nil {
		return productConversationBinding{}, fmt.Errorf("invalid product workspace root: %w", err)
	}
	return productConversationBinding{
		ConversationKey: key,
		WorkspacePath:   workspacePath,
		Title:           firstNonEmptyTrimmed(title, profile.Name),
	}, nil
}

func resolveProductConversationBinding(ctx context.Context, userID string, profile agentprofiles.Profile, requestedKey string) (productConversationBinding, error) {
	mode := strings.ToLower(strings.TrimSpace(profile.Runtime.Conversation.Mode))
	switch mode {
	case agentprofiles.ConversationModeSingleton:
		if key := strings.TrimSpace(requestedKey); key != "" && key != "main" {
			return productConversationBinding{}, fmt.Errorf("singleton product does not accept conversation_key %q", key)
		}
		workspacePath, err := cleanAgentProfileWorkspace(profile.Runtime.Workspace.Root, userID)
		if err != nil {
			return productConversationBinding{}, fmt.Errorf("invalid product workspace root: %w", err)
		}
		return productConversationBinding{
			ConversationKey: "main",
			WorkspacePath:   workspacePath,
			Title:           profile.Name,
		}, nil
	case agentprofiles.ConversationModeKeyed:
		key := strings.TrimSpace(requestedKey)
		if !productConversationKeyPattern.MatchString(key) {
			return productConversationBinding{}, fmt.Errorf("conversation_key is required and must be a stable product resource id")
		}
		if strings.ToLower(strings.TrimSpace(profile.Runtime.Conversation.KeyType)) != agentprofiles.ConversationKeyTypeProject {
			return productConversationBinding{}, fmt.Errorf("unsupported product conversation key type")
		}
		return resolveProductProjectBinding(ctx, userID, profile, key)
	default:
		return productConversationBinding{}, fmt.Errorf("agent profile does not declare a product conversation policy")
	}
}
