package server

import (
	"bufio"
	"bytes"
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

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/redaction"
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

// crewOfflineCopyHomeKey: the key is an offline copy's one chat in this home.
func crewOfflineCopyHomeKey(home, key string) bool {
	return crewOfflineCopySourceAgent(home, key) != ""
}

func crewOfflineRowBinding(t crewTeam, a crewTeamRegisteredAgent) (crewOfflineBinding, bool) {
	if a.Kind != "terminal" {
		return crewOfflineAppBinding(t, a)
	}
	if a.Terminal == nil {
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
	b, ok := crewOfflineRowBinding(t, a)
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
	return ref, ref.Home == t.OnlineHome && sameCrewOfflineSource(ref.Binding, b) && ref.ChatKey == crewOfflineCopyChatKey(t.OnlineHome, a.ID) && ref.Session != "", nil
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
	if !crewTeamSourceRowAdmits(t, viewer, a) {
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
	if !ok || !crewTeamSourceRowAdmits(t, viewer, a) {
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
// audience. Every asker's message goes into the copy's one chat (#3469).
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
	if !ok || !crewTeamSourceRowAdmits(source, viewer, a) {
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

// crewOfflineCopyFailedAt holds the last failed build per source agent; the next attempt waits crewOfflineCopyRetryAfter.
var crewOfflineCopyFailedAt sync.Map

// crewOfflineCopyTooLarge remembers source agents already logged as too large for an offline copy.
var crewOfflineCopyTooLarge sync.Map

const (
	crewOfflineCopyRetryAfter = 30 * time.Minute
	// A stage folder older than this belongs to no running build (a build holds it for minutes) and is left over from
	// a restart or a failed cleanup.
	crewOfflineCopyStaleStage = 2 * time.Hour
)

// sweepCrewOfflineCopyStages removes stage folders left in a home's private storage by builds that never finished
// (#3035: 13 leftover 1.4 GB copies in one day). Only direct children named offline-copy-* are touched.
func sweepCrewOfflineCopyStages(private string) {
	entries, err := os.ReadDir(private)
	if err != nil {
		return
	}
	removed := 0
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "offline-copy-") {
			continue
		}
		info, err := entry.Info()
		if err != nil || info.Mode()&os.ModeSymlink != 0 || time.Since(info.ModTime()) < crewOfflineCopyStaleStage {
			continue
		}
		if err := os.RemoveAll(filepath.Join(private, entry.Name())); err != nil {
			log.Printf("[AGENT_OFFLINE_COPY] event=stage_sweep_failed stage=%s error=%q", entry.Name(), err.Error())
			continue
		}
		removed++
	}
	if removed > 0 {
		log.Printf("[AGENT_OFFLINE_COPY] event=stage_sweep removed=%d", removed)
	}
}

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
	b, bound := crewOfflineRowBinding(t, a)
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
	prior, found, _ := readCrewOfflineCopyRef(dir)
	if found && prior.Revision == h.Revision && sameCrewOfflineSource(prior.Binding, b) && prior.Home == home {
		return
	}
	// #3035: a failed build is not retried on every sync. A large active chat failed 26 times in a day, each attempt
	// staging a full copy of its journal.
	key := sourceProject + "\x00" + agent
	// #3035: a history over the transfer limit can never become a copy. Building it anyway staged a full copy of a
	// 2.3 GB chat on every sync and failed with "invalid native session file". Say why once and stop.
	if h.SourceSize > sessiontransfer.MaxBytes() {
		if _, logged := crewOfflineCopyTooLarge.LoadOrStore(key, h.SourceSize); !logged {
			log.Printf("[AGENT_OFFLINE_COPY] project=%s agent=%s event=skipped reason=too_large source_bytes=%d limit_bytes=%d", sourceProject, agent, h.SourceSize, sessiontransfer.MaxBytes())
		}
		return
	}
	crewOfflineCopyTooLarge.Delete(key)
	if at, ok := crewOfflineCopyFailedAt.Load(key); ok && time.Since(at.(time.Time)) < crewOfflineCopyRetryAfter {
		return
	}
	ref, err := api.buildCrewOfflineCopy(ctx, t, a, b, h, dir)
	if err != nil {
		crewOfflineCopyFailedAt.Store(key, time.Now())
		log.Printf("[AGENT_OFFLINE_COPY] project=%s agent=%s event=refused reason=build_failed error=%q retry_after=%s", sourceProject, agent, err.Error(), crewOfflineCopyRetryAfter)
		return
	}
	crewOfflineCopyFailedAt.Delete(key)
	// Publish the reference only if the source is still bound exactly as built. The short source Team mutex orders this reread and write, so neither loses the other.
	if reason := publishCrewOfflineCopyRef(sourceProject, agent, home, b, dir, ref); reason != "" {
		fail(reason)
		return
	}
	log.Printf("[AGENT_OFFLINE_COPY] project=%s agent=%s home=%s revision=%d event=copy_ready", sourceProject, agent, home, ref.Revision)
	publishCrewPanelChanged(sourceProject)
}

func publishCrewOfflineCopyRef(sourceProject, agent, home string, b crewOfflineBinding, dir string, ref crewTeamOfflineCopyRef) string {
	teamDir, err := crewTeamDir(sourceProject)
	if err != nil {
		return "team_unavailable"
	}
	mu := crewTeamRecordMutex(teamDir)
	mu.Lock()
	defer mu.Unlock()
	latest, err := readCrewTeam(sourceProject)
	if err != nil {
		return "team_unavailable"
	}
	if now, ok := crewOfflineRowBinding(latest, latest.Agents[agent]); !ok || !sameCrewOfflineSource(now, b) || latest.OnlineHome != home {
		return "source_changed"
	}
	if err = fsutil.WriteJSONAtomic(filepath.Join(dir, "copy.json"), ref, 0600); err != nil {
		return "ref_write_failed"
	}
	return ""
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
	program, format, native := crewOfflineNativeFormat(b.Provider)
	if !native && b.Provider != "agentworks" {
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
	if !native {
		// App source: no native session to fork. Each asker's thread starts fresh with the private prelude.
		if err = writeCrewOfflineAppPrelude(dir, b, h); err != nil {
			return crewTeamOfflineCopyRef{}, err
		}
		return crewTeamOfflineCopyRef{Home: home, ChatKey: key, Session: record.SessionID, Revision: h.Revision, Binding: b}, nil
	}
	roots, found, err := crewStorageRoots(record.WorkspacePath)
	if err != nil || !found {
		return crewTeamOfflineCopyRef{}, fmt.Errorf("home storage unavailable")
	}
	sweepCrewOfflineCopyStages(roots.Private)
	stage, err := os.MkdirTemp(roots.Private, "offline-copy-")
	if err != nil {
		return crewTeamOfflineCopyRef{}, err
	}
	defer func() {
		if err := os.RemoveAll(stage); err != nil {
			log.Printf("[AGENT_OFFLINE_COPY] event=stage_cleanup_failed stage=%s error=%q", filepath.Base(stage), err.Error())
		}
	}()
	pr, pw := io.Pipe()
	go func() {
		var e error
		for _, batch := range h.Batches {
			if e = assembleChunked(filepath.Join(dir, "chunks"), batch, pw); e != nil {
				break
			}
		}
		pw.CloseWithError(e)
	}()
	bundle, err := stageCrewOfflineNativeBundle(pr, b, program, format, stage)
	pr.CloseWithError(io.ErrClosedPipe)
	if err != nil {
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

type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// stageCrewOfflineNativeBundle writes the model's runtime journal (stage/source/session.jsonl) from the private raw
// journal. The raw source stays as synced; every string value of the runtime copy goes through the shared secret
// policy, keeping each row's JSON structure and native handles.
func stageCrewOfflineNativeBundle(raw io.Reader, b crewOfflineBinding, program, format, stage string) (*sessiontransfer.Bundle, error) {
	source := filepath.Join(stage, "source")
	if err := os.Mkdir(source, 0700); err != nil {
		return nil, err
	}
	journal, err := os.OpenFile(filepath.Join(source, "session.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	sum := sha256.New()
	counted := &countingWriter{w: io.MultiWriter(journal, sum)}
	err = redactCrewOfflineJSONL(raw, counted)
	if cerr := journal.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return nil, err
	}
	bundle := &sessiontransfer.Bundle{Provider: program, Format: format, Transport: "tmux", SessionID: b.Session, Workspace: string(filepath.Separator),
		Date: time.Now().UTC().Format("2006/01/02"), Files: map[string]sessiontransfer.File{"session.jsonl": {Size: counted.n, SHA256: hex.EncodeToString(sum.Sum(nil))}}, Root: source}
	if err = bundle.Validate(); err != nil {
		return nil, err
	}
	return bundle, nil
}

// redactCrewOfflineJSONL copies native JSONL rows with every string value passed through the shared secret policy.
func redactCrewOfflineJSONL(r io.Reader, w io.Writer) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64<<10), crewOfflineDeltaLimit)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		decoder := json.NewDecoder(bytes.NewReader(line))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			return fmt.Errorf("invalid native history line")
		}
		if text, ok := value.(string); ok {
			value = redaction.Text(text)
		}
		redactCrewOfflineValue(value)
		var out bytes.Buffer
		encoder := json.NewEncoder(&out)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(value); err != nil { // Encode ends the row with a newline
			return err
		}
		if _, err := w.Write(out.Bytes()); err != nil {
			return err
		}
	}
	return scanner.Err()
}
