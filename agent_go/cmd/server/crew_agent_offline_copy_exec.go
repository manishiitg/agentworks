package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/multi-llm-provider-go/pkg/sessiontransfer"
)

// An offline copy (PLAT-746-1024 slice 2) is one ordinary tab in the source Team's online home, owned by the home's
// virtual Team user and run with the home's AI accounts. It answers as the source agent under the source's CURRENT
// rules. Its copy reference stays in the source's private offline-copies folder (never the Team record, which members
// read), so the home tab key, session and revision are server-only. The tab's visible history stays empty: the
// source's native transcript lives only in the virtual user's private runtime, and each asker's thread starts from it.

type crewTeamOfflineCopyRef struct {
	Home     string             `json:"home"`
	ChatKey  string             `json:"chat_key"`
	Session  string             `json:"session"`
	Revision uint64             `json:"revision"`
	Binding  crewOfflineBinding `json:"binding"`
}

const crewOfflineCopyTabPrefix = "copy-"

func crewOfflineCopyChatKey(home, agent string) string {
	return home + codeChatSideMarker + crewOfflineCopyTabPrefix + strings.TrimPrefix(agent, crewTeamAgentIDPrefix)
}

// crewOfflineCopySourceAgent is the source agent ID a home copy key names, or "".
func crewOfflineCopySourceAgent(home, key string) string {
	tab, ok := strings.CutPrefix(key, home+codeChatSideMarker+crewOfflineCopyTabPrefix)
	if !ok || tab == "" {
		return ""
	}
	return crewTeamAgentIDPrefix + tab
}

// crewOfflineCopyHomeKey: the key is an offline copy base or one of its askers' threads in this home.
func crewOfflineCopyHomeKey(home, key string) bool {
	return crewOfflineCopySourceAgent(home, key) != "" || strings.HasPrefix(key, home+codeChatSideMarker+sharedThreadChatPrefix+crewOfflineCopyTabPrefix)
}

// crewOfflineCopyAdmits is the source-row audience for every copy path (resolver, binder, endpoint and thread reads).
// It is the canonical nonrecursive source gate; it never projects reach.
func crewOfflineCopyAdmits(t crewTeam, viewer string, a crewTeamRegisteredAgent) bool {
	return viewer != "" && crewTeamAccountActive(viewer) && crewTeamReachRow(t, viewer, a)
}

func crewOfflineRowBinding(a crewTeamRegisteredAgent) (crewOfflineBinding, bool) {
	if a.Kind != "terminal" || a.Terminal == nil || !a.InTeam {
		return crewOfflineBinding{}, false
	}
	b := crewOfflineBinding{Provider: a.Terminal.Provider, Session: a.ChatKey, Attachment: a.Terminal.AttachmentID}
	return b, b.valid()
}

func readCrewOfflineCopyRef(dir string) (crewTeamOfflineCopyRef, bool, error) {
	var ref crewTeamOfflineCopyRef
	raw, err := os.ReadFile(filepath.Join(dir, "copy.json"))
	if errors.Is(err, os.ErrNotExist) {
		return ref, false, nil
	}
	if err != nil {
		return ref, false, err
	}
	if err = json.Unmarshal(raw, &ref); err != nil {
		return ref, false, err
	}
	return ref, true, nil
}

// crewOfflineCopyBound is the copy's identity check (not freshness): the reference still names this home, this source
// attachment and this key. Replies and threads keep working while a newer revision is being built.
func crewOfflineCopyBound(t crewTeam, a crewTeamRegisteredAgent) (crewTeamOfflineCopyRef, bool, error) {
	b, ok := crewOfflineRowBinding(a)
	if !ok || t.OnlineHome == "" {
		return crewTeamOfflineCopyRef{}, false, nil
	}
	dir, err := crewOfflineDirectory(t.ProjectID, a.ID)
	if err != nil {
		return crewTeamOfflineCopyRef{}, false, err
	}
	ref, found, err := readCrewOfflineCopyRef(dir)
	if err != nil || !found {
		return ref, false, err
	}
	return ref, ref.Home == t.OnlineHome && ref.Binding == b && ref.ChatKey == crewOfflineCopyChatKey(t.OnlineHome, a.ID) && ref.Session != "", nil
}

// crewOfflineCopyReady: bound, and built from the complete latest synced history (no partial bootstrap).
func crewOfflineCopyReady(t crewTeam, a crewTeamRegisteredAgent) (crewTeamOfflineCopyRef, bool, error) {
	ref, bound, err := crewOfflineCopyBound(t, a)
	if err != nil || !bound {
		return ref, false, err
	}
	dir, err := crewOfflineDirectory(t.ProjectID, a.ID)
	if err != nil {
		return ref, false, err
	}
	h, err := readCrewOfflineHistory(dir, ref.Binding)
	if err != nil {
		return ref, false, err
	}
	return ref, h.Revision > 0 && h.Offset == h.SourceSize && ref.Revision == h.Revision, nil
}

// crewTeamOfflineCopyTarget resolves an offline source row to its ready copy for this viewer. It validates the source
// row directly and never calls crewTeamReachableIn. Not admitted or a read failure is an error (refusal, not fallback).
func crewTeamOfflineCopyTarget(ctx context.Context, t crewTeam, row crewTeamReachableAgent, viewer string) (crewTeamReachableAgent, bool, error) {
	a, ok := t.Agents[row.ID]
	if !ok || row.Kind != "agent" {
		return row, false, nil
	}
	if !crewOfflineCopyAdmits(t, viewer, a) {
		return row, false, admissionRefused("This agent is not shared with you now.")
	}
	ref, ready, err := crewOfflineCopyReady(t, a)
	if err != nil {
		log.Printf("[AGENT_OFFLINE_COPY] project=%s agent=%s event=refused reason=copy_read_failed", t.ProjectID, a.ID)
		return row, false, admissionRefused("This agent's offline copy is unavailable now.")
	}
	if !ready {
		return row, false, nil
	}
	copy := row
	copy.OfflineCopyID, copy.OfflineCopyRevision = ref.ChatKey, ref.Revision
	return copy, true, nil
}

// crewTeamBindOfflineCopyAsk turns a NEW offline ask into the copy's home target. The public source identity
// (Team/agent/address) is kept on the endpoint; the runtime home, virtual owner and tab come only from the server.
func crewTeamBindOfflineCopyAsk(ctx context.Context, viewer string, original triggerTarget, routed crewTeamReachableAgent) (triggerTarget, error) {
	t, err := readCrewTeam(routed.Team)
	if err != nil {
		return original, err
	}
	copy, ready, err := crewTeamOfflineCopyTarget(ctx, t, routed, viewer)
	if err != nil {
		return original, err
	}
	if !ready || copy.OfflineCopyID != routed.OfflineCopyID {
		return original, admissionRefused("This agent's offline copy changed. Ask again.")
	}
	owner := ownerOfCrewProjectID(t.OnlineHome)
	if owner != "team-"+t.ProjectID {
		return original, admissionRefused("This Team's online home changed.")
	}
	a := t.Agents[routed.ID]
	key := copy.OfflineCopyID
	log.Printf("[AGENT_OFFLINE_COPY] project=%s agent=%s revision=%d caller=%s event=ask_bound", t.ProjectID, a.ID, copy.OfflineCopyRevision, viewer)
	return triggerTarget{TeamAgentID: a.ID, OfflineCopyTeam: t.ProjectID, Kind: triggerCallerCrew, CrewID: t.OnlineHome, CrewProfile: crewProfileID, CrewOwner: owner,
		Path: crewProjectRootByID(t.OnlineHome), Label: a.Name, Chat: &codeChat{OwnerID: owner, ID: key, Key: key, Name: a.Name}}, nil
}

// crewOfflineCopyEndpointAdmits rechecks a copy endpoint at delivery, reply and thread time: the source row admits the
// viewer now, and the endpoint still names the bound copy in the current home as its virtual owner.
func crewOfflineCopyEndpointAdmits(t crewTeam, viewer string, e agentMessageEndpoint) error {
	a, ok := t.Agents[e.TeamAgentID]
	if !ok || !crewOfflineCopyAdmits(t, viewer, a) {
		return admissionRefused("This agent is not shared with you now.")
	}
	ref, bound, err := crewOfflineCopyBound(t, a)
	if err != nil || !bound {
		return admissionRefused("This agent's offline copy changed.")
	}
	key := e.ChatKey
	if e.PublishedAgent != "" {
		key = e.PublishedAgent
	}
	if e.ID != ref.Home || e.UserID != ownerOfCrewProjectID(ref.Home) || key != ref.ChatKey {
		return admissionRefused("This agent's offline copy changed.")
	}
	return nil
}

// crewOfflineCopySharedChat admits an asker's thread base in a home: the copy tab, under the SOURCE row's current
// audience. Hidden or Live, each asker still gets their own thread (sharedThreadKey).
func crewOfflineCopySharedChat(viewer, home, key string) (*codeChat, bool) {
	t, err := readCrewTeam(home)
	if err != nil || t.RosterSource == "" {
		return nil, false
	}
	agent := crewOfflineCopySourceAgent(home, key)
	if agent == "" {
		return nil, false
	}
	source, err := readCrewTeam(t.RosterSource)
	if err != nil || source.OnlineHome != home {
		return nil, false
	}
	a, ok := source.Agents[agent]
	if !ok || !crewOfflineCopyAdmits(source, viewer, a) {
		return nil, false
	}
	ref, bound, err := crewOfflineCopyBound(source, a)
	if err != nil || !bound || ref.ChatKey != key {
		return nil, false
	}
	owner := ownerOfCrewProjectID(home)
	return &codeChat{OwnerID: owner, ID: key, Key: key, Name: a.Name, Purpose: a.Purpose, SessionID: ref.Session}, true
}

// crewOfflineCopyRule is the source agent's current rule for a copy key in a home, so each turn follows the owner's
// latest When others ask / Answer only / Make changes.
func crewOfflineCopyRule(home crewTeam, key string) (crewTeamAgentRule, bool) {
	agent := crewOfflineCopySourceAgent(home.ProjectID, key)
	if agent == "" || home.RosterSource == "" {
		return crewTeamAgentRule{}, false
	}
	source, err := readCrewTeam(home.RosterSource)
	if err != nil || source.OnlineHome != home.ProjectID {
		return crewTeamAgentRule{}, false
	}
	a, ok := source.Agents[agent]
	if !ok {
		return crewTeamAgentRule{}, false
	}
	return a.Rule, true
}

// One build per source agent at a time; a later commit rebuilds after the current one finishes.
var crewOfflineCopyBuilds sync.Map

// refreshCrewOfflineCopy builds or updates the copy after a committed sync. It runs in the background, never on the
// upload request, and never provisions an online home: a Team without one keeps using its Team agent.
func (api *StreamingAPI) refreshCrewOfflineCopy(sourceProject, agent string) {
	mu, _ := crewOfflineCopyBuilds.LoadOrStore(sourceProject+"\x00"+agent, &sync.Mutex{})
	mu.(*sync.Mutex).Lock()
	defer mu.(*sync.Mutex).Unlock()
	ctx := context.Background()
	fail := func(reason string) {
		log.Printf("[AGENT_OFFLINE_COPY] project=%s agent=%s event=refused reason=%s", sourceProject, agent, reason)
	}
	t, err := readCrewTeam(sourceProject)
	if err != nil {
		fail("team_unavailable")
		return
	}
	a, ok := t.Agents[agent]
	b, bound := crewOfflineRowBinding(a)
	if !ok || !bound {
		fail("source_unbound")
		return
	}
	if t.OnlineHome == "" {
		log.Printf("[AGENT_OFFLINE_COPY] project=%s agent=%s event=skipped reason=no_online_home", sourceProject, agent)
		return
	}
	home := t.OnlineHome
	owner := ownerOfCrewProjectID(home)
	if owner != "team-"+sourceProject {
		fail("home_owner_changed")
		return
	}
	dir, err := crewOfflineDirectory(sourceProject, agent)
	if err != nil {
		fail("storage_unavailable")
		return
	}
	h, err := readCrewOfflineHistory(dir, b)
	if err != nil || h.Revision == 0 || h.Offset != h.SourceSize {
		return // not ready: partial bootstrap or nothing synced yet
	}
	if ref, found, _ := readCrewOfflineCopyRef(dir); found && ref.Revision == h.Revision && ref.Binding == b && ref.Home == home {
		return
	}
	ref, err := api.buildCrewOfflineCopy(ctx, t, a, b, h, dir)
	if err != nil {
		fail("build_failed")
		return
	}
	// Publish the reference only if the source is still bound exactly as built.
	latest, err := readCrewTeam(sourceProject)
	if err != nil {
		fail("team_unavailable")
		return
	}
	if now, ok := crewOfflineRowBinding(latest.Agents[agent]); !ok || now != b || latest.OnlineHome != home {
		fail("source_changed")
		return
	}
	if err = fsutil.WriteJSONAtomic(filepath.Join(dir, "copy.json"), ref, 0600); err != nil {
		fail("ref_write_failed")
		return
	}
	log.Printf("[AGENT_OFFLINE_COPY] project=%s agent=%s home=%s revision=%d event=copy_ready", sourceProject, agent, home, ref.Revision)
	publishCrewPanelChanged(sourceProject)
}

func crewOfflineNativeFormat(provider string) (string, string, bool) {
	switch provider {
	case "claude":
		return "claude-code", "claude-jsonl", true
	case "codex":
		return "codex-cli", "codex-rollout", true
	}
	return "", "", false
}

// buildCrewOfflineCopy imports the complete synced transcript into the copy tab as the virtual Team user. A fresh
// native identity is forked every revision; the source's own session store is never written.
func (api *StreamingAPI) buildCrewOfflineCopy(ctx context.Context, t crewTeam, a crewTeamRegisteredAgent, b crewOfflineBinding, h crewOfflineHistory, dir string) (crewTeamOfflineCopyRef, error) {
	program, format, ok := crewOfflineNativeFormat(b.Provider)
	if !ok {
		return crewTeamOfflineCopyRef{}, fmt.Errorf("unsupported program")
	}
	home := t.OnlineHome
	owner := ownerOfCrewProjectID(home)
	claims := &UserClaims{UserID: owner}
	ctx = internalBotRequestContext(context.WithValue(ctx, UserContextKey, (*UserClaims)(nil)), owner)
	profile, err := api.agentProfiles.Resolve(crewProfileID, 0, owner)
	if err != nil {
		return crewTeamOfflineCopyRef{}, err
	}
	key := crewOfflineCopyChatKey(home, a.ID)
	store := defaultProductConversationRegistryStore()
	binding, err := resolveProductProjectBinding(ctx, owner, profile, key)
	if err != nil {
		return crewTeamOfflineCopyRef{}, err
	}
	record, err := store.resolveOrCreate(ctx, owner, profile, binding, "")
	if err != nil {
		return crewTeamOfflineCopyRef{}, err
	}
	name, purpose := a.Name, a.Purpose
	if _, err = store.setAgentIdentity(ctx, owner, crewProfileID, record.ConversationKey, &name, &purpose, owner); err != nil {
		return crewTeamOfflineCopyRef{}, err
	}
	roots, found, err := crewStorageRoots(record.WorkspacePath)
	if err != nil || !found {
		return crewTeamOfflineCopyRef{}, fmt.Errorf("home storage unavailable")
	}
	stage, err := os.MkdirTemp(roots.Private, "offline-copy-")
	if err != nil {
		return crewTeamOfflineCopyRef{}, err
	}
	defer os.RemoveAll(stage)
	source := filepath.Join(stage, "source")
	if err = os.Mkdir(source, 0700); err != nil {
		return crewTeamOfflineCopyRef{}, err
	}
	journal, err := os.OpenFile(filepath.Join(source, "session.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return crewTeamOfflineCopyRef{}, err
	}
	sum := sha256.New()
	out := io.MultiWriter(journal, sum)
	var size int64
	for _, batch := range h.Batches {
		if err = assembleChunked(filepath.Join(dir, "chunks"), batch, out); err != nil {
			break
		}
		size += batch.Size
	}
	if cerr := journal.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return crewTeamOfflineCopyRef{}, err
	}
	bundle := &sessiontransfer.Bundle{Provider: program, Format: format, Transport: "tmux", SessionID: b.Session, Workspace: string(filepath.Separator),
		Date: time.Now().UTC().Format("2006/01/02"), Files: map[string]sessiontransfer.File{"session.jsonl": {Size: size, SHA256: hex.EncodeToString(sum.Sum(nil))}}, Root: source}
	if err = bundle.Validate(); err != nil {
		return crewTeamOfflineCopyRef{}, err
	}
	cwd, err := crewCLIWorkingDir(roots.Virtual, owner, record.SessionID, program, false)
	if err != nil {
		return crewTeamOfflineCopyRef{}, err
	}
	forked, err := sessiontransfer.ForkJSONL(bundle, filepath.Join(stage, "native"), cwd)
	if err != nil {
		return crewTeamOfflineCopyRef{}, err
	}
	runtime, err := api.importCrewNativeInto(ctx, claims, roots, record, record.SessionID, forked, cwd)
	if err != nil {
		return crewTeamOfflineCopyRef{}, err
	}
	// The visible chat stays empty: no source text in Team files, history search or the home's Builder view.
	now := time.Now().UTC()
	path, ok := workProjectChatHistoryConversationPath(owner, record.WorkspacePath, record.SessionID, now)
	if !ok || path == "" {
		return crewTeamOfflineCopyRef{}, fmt.Errorf("home conversation storage unavailable")
	}
	data, err := json.Marshal(map[string]any{"session_id": record.SessionID, "user_id": owner, "workspace_path": record.WorkspacePath, "agent_mode": "multi-agent",
		"title": a.Name, "conversation_history": []any{}, "runtime": runtime, "created_at": record.CreatedAt, "updated_at": now.Format(time.RFC3339)})
	if err != nil {
		return crewTeamOfflineCopyRef{}, err
	}
	if err = writeRawFileToWorkspace(ctx, path, string(data)); err != nil {
		return crewTeamOfflineCopyRef{}, err
	}
	if err = writeChatHistoryResumeSnapshot(ctx, path, data); err != nil {
		return crewTeamOfflineCopyRef{}, err
	}
	if err = updatePersistedChatHistoryIndex(owner, record.SessionID, "multi-agent", nil, runtime, path, int64(len(data)), now, nil); err != nil {
		return crewTeamOfflineCopyRef{}, err
	}
	return crewTeamOfflineCopyRef{Home: home, ChatKey: key, Session: record.SessionID, Revision: h.Revision, Binding: b}, nil
}
