package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// The team copy (PLAT-856-509, case B3; owner 4 Nov): while a Crew's Mac is off, the agents its owner shares keep
// answering the people they are shared with from a copy on the server. The copy has the agent's instructions, skills
// and memory, the owner's chat with it so far and the team folder, never the rest of the Mac, and it runs as the owner
// on the server. The owner chooses per Crew: answer from the team copy (the default) or wait for the Mac, when messages
// are held and delivered once it is back. When the Mac returns, each agent gets a note of what was asked and answered.

const (
	teamCopyAnswer = "team_copy"
	teamCopyWait   = "wait"

	teamCopyRecordMaxBytes = 8 << 20
	teamCopyRecordMaxFiles = 2000
	teamCopyChatMaxBytes   = 60 << 10
	teamCopyReportMaxBytes = 24 << 10
	teamMacSeenWithin      = 2 * time.Minute
)

// The agent's own record, from the Crew folder: everything else in it stays on the Mac.
var (
	teamCopyRecordFiles = []string{"PROJECT_INSTRUCTIONS.md", "MEMORY.md", "soul.md", "AGENTS.md", "CLAUDE.md"}
	teamCopyRecordDirs  = []string{"skills", "learnings", "memory"}
)

func teamCopyRecordPath(rel string) bool {
	rel = path.Clean(filepath.ToSlash(rel))
	if rel == "." || rel == ".." || strings.HasPrefix(rel, "../") || path.IsAbs(rel) {
		return false
	}
	if slices.Contains(teamCopyRecordFiles, rel) {
		return true
	}
	first, rest, nested := strings.Cut(rel, "/")
	return nested && rest != "" && slices.Contains(teamCopyRecordDirs, first)
}

// crewTeamCopyRecord is what the Mac sends for its team copy: the Crew's record files and, per shared agent, the
// owner's chat with it so far as text and the program it runs on.
type crewTeamCopyRecord struct {
	Digest   string                     `json:"digest"`
	Files    map[string][]byte          `json:"files"`
	Chats    map[string]string          `json:"chats"`
	Programs map[string]teamCopyProgram `json:"programs,omitempty"`
}

type teamCopyProgram struct {
	Provider string `json:"provider"`
	ModelID  string `json:"model_id,omitempty"`
}

// teamCopyAgent is one shared agent's own part of the record. It is kept in the server's private state, never in the
// Crew's folder: a turn for one agent gets only its own chat (reviewer, 4 Nov: agent A's people must never read the
// owner's chat with agent B).
type teamCopyAgent struct {
	teamCopyProgram
	Chat string `json:"chat,omitempty"`
}

func teamCopyAgentDir(project string) (string, error) {
	p, err := teamCopyStatePath(project)
	return strings.TrimSuffix(p, ".json"), err
}

func readTeamCopyAgent(project, tab string) (teamCopyAgent, bool) {
	var agent teamCopyAgent
	dir, err := teamCopyAgentDir(project)
	if err != nil || (tab != macMainTab && !crewMacTabID(tab)) {
		return agent, false
	}
	raw, err := os.ReadFile(filepath.Join(dir, tab+".json"))
	return agent, err == nil && json.Unmarshal(raw, &agent) == nil
}

type crewTeamCopyAway struct {
	Tab      string `json:"tab"`
	SenderID string `json:"sender_id"`
	Caller   string `json:"caller"`
	At       string `json:"at"`
}

// crewTeamCopyWaiting is a message held for the Mac ("Wait for my Mac"). An agent message also names its inbox, where
// the answer goes once the Mac has it.
type crewTeamCopyWaiting struct {
	ID           string          `json:"id"`
	Tab          string          `json:"tab"`
	Caller       string          `json:"caller"`
	Message      string          `json:"message"`
	SubmissionID string          `json:"submission_id,omitempty"`
	Args         json.RawMessage `json:"args"`
	Inbox        string          `json:"inbox,omitempty"`
	From         int             `json:"from,omitempty"`
	At           string          `json:"at"`
}

type crewTeamCopyState struct {
	WhenMacOff   string                `json:"when_mac_off,omitempty"`
	RecordAt     string                `json:"record_at,omitempty"`
	RecordDigest string                `json:"record_digest,omitempty"`
	Answered     map[string]int        `json:"answered,omitempty"` // UTC day -> messages the team copy took
	CopySince    string                `json:"copy_since,omitempty"`
	Away         []crewTeamCopyAway    `json:"away,omitempty"`
	Waiting      []crewTeamCopyWaiting `json:"waiting,omitempty"`
}

func (s crewTeamCopyState) mode() string {
	if s.WhenMacOff == teamCopyWait {
		return teamCopyWait
	}
	return teamCopyAnswer
}

var (
	teamCopyMu          sync.Mutex
	teamCopyHandingBack sync.Map
)

func teamCopyStatePath(project string) (string, error) {
	if project == "" || strings.ContainsAny(project, `/\`) || project == "." || project == ".." {
		return "", fmt.Errorf("invalid Crew identity")
	}
	state, err := crewroot.StateRoot()
	return filepath.Join(state, "crew-sync", project+"-team-copy.json"), err
}

func readTeamCopyState(project string) crewTeamCopyState {
	var s crewTeamCopyState
	if p, err := teamCopyStatePath(project); err == nil {
		if raw, err := os.ReadFile(p); err == nil {
			_ = json.Unmarshal(raw, &s)
		}
	}
	return s
}

func updateTeamCopyState(project string, change func(*crewTeamCopyState)) (crewTeamCopyState, error) {
	teamCopyMu.Lock()
	defer teamCopyMu.Unlock()
	s := readTeamCopyState(project)
	change(&s)
	p, err := teamCopyStatePath(project)
	if err != nil {
		return s, err
	}
	if err = os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return s, err
	}
	raw, _ := json.MarshalIndent(s, "", "  ")
	if err = os.WriteFile(p+".tmp", raw, 0600); err != nil {
		return s, err
	}
	return s, os.Rename(p+".tmp", p)
}

// teamCopyRoute is where a shared agent's message goes while its owner's Mac is off: the team copy, once the server has
// the agent's record, the owner chose it and has their own login for its program here (owner, 4 Nov: never the
// server's account); else it waits for the Mac. The reason names why it waits.
func teamCopyRoute(project, tab string) (string, string) {
	if crewPublishedTarget(project, tab) {
		return teamCopyWait, "published"
	}
	s := readTeamCopyState(project)
	if s.mode() == teamCopyWait {
		return teamCopyWait, "chose_wait"
	}
	agent, ok := readTeamCopyAgent(project, tab)
	if s.RecordAt == "" || !ok {
		return teamCopyWait, "no_record"
	}
	if agent.Provider == "" || ownDefaultProviderAccountID(context.Background(), ownerOfCrewProjectID(project), agent.Provider) == "" {
		return teamCopyWait, "no_own_login"
	}
	return teamCopyAnswer, ""
}

// teamCopyAgentKey carries the agent's own part of the record into its team copy turn (desktopMacSharedAgent).
type teamCopyAgentKey struct{}

// crewTeamCopyKey marks a shared agent's turn that runs on the server as the team copy (desktopMacSharedAgent).
type crewTeamCopyKey struct{}

func teamCopyTurn(ctx context.Context) bool {
	on, _ := ctx.Value(crewTeamCopyKey{}).(bool)
	return on
}

// teamCopyCall runs a shared agent's ask or read on the server, in the same per-person thread the Mac would use,
// as the Crew's owner.
func (api *StreamingAPI) teamCopyCall(ctx context.Context, owner, project string, call crewChatCall) crewChatResult {
	fail := func(status int, message string) crewChatResult {
		body, _ := json.Marshal(map[string]string{"error": message})
		return crewChatResult{ID: call.ID, Status: status, Body: body}
	}
	claims := userClaimsForDirectoryID(owner)
	if claims == nil {
		return fail(http.StatusServiceUnavailable, "Its owner's Mac is off. Try again later.")
	}
	profile, err := api.agentProfiles.Resolve(crewProfileID, 0, owner)
	if err != nil {
		return fail(http.StatusServiceUnavailable, "Its owner's Mac is off. Try again later.")
	}
	agent, _ := readTeamCopyAgent(project, call.Tab)
	ctx = context.WithValue(context.WithValue(context.WithValue(context.WithoutCancel(ctx), UserContextKey, claims), crewTeamCopyKey{}, true), teamCopyAgentKey{}, agent)
	result := api.desktopMacSharedAgent(ctx, claims, profile, project, call)
	log.Printf("[TEAM_COPY] project=%s tab=%s operation=%s status=%d", project, call.Tab, call.Operation, result.Status)
	if call.Operation == "agent_ask" && result.Status == http.StatusOK {
		var args struct {
			SenderID string `json:"sender_id"`
		}
		_ = json.Unmarshal(call.Args, &args)
		now := time.Now().UTC()
		_, _ = updateTeamCopyState(project, func(s *crewTeamCopyState) {
			if s.CopySince == "" {
				s.CopySince = now.Format(time.RFC3339Nano)
				log.Printf("[TEAM_COPY] project=%s switch=mac->team_copy reason=mac_off", project)
			}
			if s.Answered == nil {
				s.Answered = map[string]int{}
			}
			day := now.Format("2006-01-02")
			s.Answered[day]++
			for old := range s.Answered {
				if old < now.AddDate(0, 0, -7).Format("2006-01-02") {
					delete(s.Answered, old)
				}
			}
			if !slices.ContainsFunc(s.Away, func(a crewTeamCopyAway) bool { return a.Tab == call.Tab && a.SenderID == args.SenderID }) {
				s.Away = append(s.Away, crewTeamCopyAway{Tab: call.Tab, SenderID: args.SenderID, Caller: call.Caller, At: now.Format(time.RFC3339Nano)})
			}
		})
	}
	return result
}

// holdForMac keeps a message for the Mac ("Wait for my Mac", no team copy yet, or no own login for it).
func holdForMac(project, reason string, call crewChatCall, inbox string, from int) error {
	_, err := updateTeamCopyState(project, func(s *crewTeamCopyState) {
		s.Waiting = append(s.Waiting, crewTeamCopyWaiting{ID: uuid.NewString(), Tab: call.Tab, Caller: call.Caller, Message: call.Message, SubmissionID: call.SubmissionID,
			Args: call.Args, Inbox: inbox, From: from, At: time.Now().UTC().Format(time.RFC3339Nano)})
	})
	log.Printf("[TEAM_COPY] project=%s tab=%s action=held_for_mac reason=%s inbox=%q err=%v", project, call.Tab, reason, inbox, err)
	return err
}

// teamCopyMacBack runs when a Mac offers its Crews again: each shared agent gets a note of what its team copy was
// asked and answered while the Mac was off, and the messages held for the Mac are delivered.
func (api *StreamingAPI) teamCopyMacBack(owner, project, pair string) {
	if IsLocalMode() {
		return
	}
	s := readTeamCopyState(project)
	if len(s.Away) == 0 && len(s.Waiting) == 0 && s.CopySince == "" {
		return
	}
	// One hand-back per Crew at a time: the Mac's connect and its next offer both start one.
	if _, busy := teamCopyHandingBack.LoadOrStore(project, true); busy {
		return
	}
	defer teamCopyHandingBack.Delete(project)
	time.Sleep(3 * time.Second) // the Mac's connection is registered once its offer is admitted
	// Without the Mac, only what now belongs to a published agent moves on: its held messages go to it on the server.
	macOn := crewMacSocketFor(owner, project, pair) != nil
	s = readTeamCopyState(project)
	reported := map[string]bool{}
	tabs := map[string][]crewTeamCopyAway{}
	for _, away := range s.Away {
		tabs[away.Tab] = append(tabs[away.Tab], away)
	}
	for tab, asks := range tabs {
		if crewPublishedTarget(project, tab) {
			// Published since: the agent lives on the server now, so the Mac's tab gets no note.
			reported[tab] = true
			log.Printf("[TEAM_COPY] project=%s tab=%s action=away_report skipped reason=published people=%d", project, tab, len(asks))
			continue
		}
		if !macOn {
			continue
		}
		text := api.teamCopyReport(owner, project, tab, asks)
		if text == "" {
			reported[tab] = true
			continue
		}
		result := callCrewMacFor(context.Background(), owner, project, crewChatCall{Operation: "away_report", Tab: tab, Message: text}, 60*time.Second)
		log.Printf("[TEAM_COPY] project=%s tab=%s action=away_report people=%d mac_status=%d", project, tab, len(asks), result.Status)
		if result.Status == http.StatusOK {
			reported[tab] = true
		}
	}
	delivered := map[string]bool{}
	for _, held := range s.Waiting {
		// An agent published while the message waited is answered on the server from then on.
		published := crewPublishedTarget(project, held.Tab)
		if !published && !macOn {
			continue
		}
		send := func(call crewChatCall, timeout time.Duration) crewChatResult {
			if published {
				return api.publishedAgentCall(context.Background(), owner, project, call)
			}
			return callCrewMacFor(context.Background(), owner, project, call, timeout)
		}
		result := send(crewChatCall{Operation: "agent_ask", Tab: held.Tab, Message: held.Message, Caller: held.Caller, Args: held.Args, SubmissionID: held.SubmissionID}, 60*time.Second)
		log.Printf("[TEAM_COPY] project=%s tab=%s action=deliver_held inbox=%q status=%d published=%t", project, held.Tab, held.Inbox, result.Status, published)
		if result.Status != http.StatusOK {
			continue
		}
		delivered[held.ID] = true
		if held.Inbox != "" {
			var args struct {
				SenderID string `json:"sender_id"`
			}
			_ = json.Unmarshal(held.Args, &args)
			held := held
			read := func(after, wait int) (map[string]any, int) {
				raw, _ := json.Marshal(map[string]any{"sender_id": args.SenderID, "after": after, "wait_seconds": wait, "submission": held.SubmissionID})
				result := send(crewChatCall{Operation: "agent_read", Tab: held.Tab, Caller: held.Caller, Args: raw}, time.Duration(wait+40)*time.Second)
				var out map[string]any
				_ = json.Unmarshal(result.Body, &out)
				return out, result.Status
			}
			go api.awaitMacAgentAnswer(held.Inbox, held.SubmissionID, held.From, read)
		}
	}
	after, _ := updateTeamCopyState(project, func(next *crewTeamCopyState) {
		next.Away = slices.DeleteFunc(next.Away, func(a crewTeamCopyAway) bool { return reported[a.Tab] })
		next.Waiting = slices.DeleteFunc(next.Waiting, func(w crewTeamCopyWaiting) bool { return delivered[w.ID] })
		if len(next.Away) == 0 {
			next.CopySince = ""
		}
	})
	if s.CopySince != "" && after.CopySince == "" && macOn {
		log.Printf("[TEAM_COPY] project=%s switch=team_copy->mac reason=mac_back since=%s", project, s.CopySince)
	}
}

// teamCopyReport is the note one agent gets when its Mac is back: each person's messages to the team copy and its
// answers, newest last, within a size limit.
func (api *StreamingAPI) teamCopyReport(owner, project, tab string, asks []crewTeamCopyAway) string {
	claims := userClaimsForDirectoryID(owner)
	profile, err := api.agentProfiles.Resolve(crewProfileID, 0, owner)
	if claims == nil || err != nil {
		return ""
	}
	ctx := context.WithValue(context.Background(), UserContextKey, claims)
	r, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://localhost/api/agent-profiles/work/conversation", nil)
	r = mux.SetURLVars(r, map[string]string{"id": crewProfileID})
	var b strings.Builder
	for _, ask := range asks {
		conversation, err := api.resolveAgentProfileConversation(r, profile, sharedThreadKey(project, tab, ask.SenderID), "")
		if err != nil {
			continue
		}
		items, _, err := codeChatTranscript(ctx, owner, conversation.SessionID, conversation.WorkspacePath, 0, 0)
		if err != nil {
			continue
		}
		fmt.Fprintf(&b, "\n### %s\n", firstNonEmptyTrimmed(ask.Caller, "Someone"))
		for _, item := range items {
			if item.At < ask.At || strings.TrimSpace(item.Text) == "" {
				continue
			}
			who := "They asked"
			if item.Role == "assistant" {
				who = "The team copy answered"
			}
			text := item.Text
			if len([]rune(text)) > 2000 {
				text = string([]rune(text)[:2000]) + "…"
			}
			fmt.Fprintf(&b, "%s: %s\n", who, text)
		}
	}
	if b.Len() == 0 {
		return ""
	}
	body := b.String()
	if len(body) > teamCopyReportMaxBytes {
		body = "…" + body[len(body)-teamCopyReportMaxBytes:]
	}
	return "[While your owner's Mac was off, your team copy on the server answered the people you are shared with. Read what was asked and answered so you know it; update your memory if something there matters. Reply with one line.]\n" + body
}

// handleTeamCopy is the owner's "When your Mac is off" setting and the Mac's record for the team copy, from the Mac
// that holds the pair.
func (api *StreamingAPI) handleTeamCopy(w http.ResponseWriter, r *http.Request, project string, input crewSyncRequest) {
	claims := GetUserFromContext(r.Context())
	roots, err := api.crewSyncRoots(r.Context(), claims, project)
	if err != nil {
		externalError(w, 403, "sync_unavailable", err.Error())
		return
	}
	b, err := crewroot.ReadSync(roots.ID)
	if err != nil || b == nil || b.Mode != "relay" || b.Side != "server" || b.Editing != "local" || input.PairID == "" || b.PairID != input.PairID {
		externalError(w, 409, "wrong_pair", "Change this from the Mac that holds this Crew.")
		return
	}
	if input.Action == "team_copy_record" {
		if input.TeamCopy == nil {
			externalError(w, 400, "invalid_request", "Send the team copy's record.")
			return
		}
		if err = saveTeamCopyRecord(roots.Resources, roots.ID, *input.TeamCopy); err != nil {
			log.Printf("[TEAM_COPY] project=%s action=record refused reason=%q", roots.ID, err.Error())
			externalError(w, 400, "invalid_record", err.Error())
			return
		}
		_, _ = updateTeamCopyState(roots.ID, func(s *crewTeamCopyState) {
			s.RecordAt, s.RecordDigest = time.Now().UTC().Format(time.RFC3339Nano), input.TeamCopy.Digest
		})
		log.Printf("[TEAM_COPY] project=%s action=record files=%d chats=%d digest=%s", roots.ID, len(input.TeamCopy.Files), len(input.TeamCopy.Chats), input.TeamCopy.Digest)
	} else if input.WhenMacOff != "" {
		if input.WhenMacOff != teamCopyAnswer && input.WhenMacOff != teamCopyWait {
			externalError(w, 400, "invalid_choice", "Choose Answer from the team copy or Wait for my Mac.")
			return
		}
		_, _ = updateTeamCopyState(roots.ID, func(s *crewTeamCopyState) { s.WhenMacOff = input.WhenMacOff })
		log.Printf("[TEAM_COPY] project=%s owner=%s action=set when_mac_off=%s", roots.ID, logUsernameForUserID(claims.UserID), input.WhenMacOff)
	}
	status := teamCopyStatus(claims.UserID, roots.ID, b)
	externalJSON(w, crewSyncReply{ProjectID: roots.ID, Binding: b, TeamCopy: &status})
}

// saveTeamCopyRecord replaces the record's files in the Crew's online folder (only record paths are written or removed)
// and each shared agent's own part in private state.
func saveTeamCopyRecord(base, project string, record crewTeamCopyRecord) error {
	if base == "" {
		return fmt.Errorf("this Crew has no online folder")
	}
	if len(record.Files) > teamCopyRecordMaxFiles {
		return fmt.Errorf("the agents' record has more than %d files", teamCopyRecordMaxFiles)
	}
	total := 0
	for rel, data := range record.Files {
		if !teamCopyRecordPath(rel) {
			return fmt.Errorf("%q is not part of an agent's record", rel)
		}
		total += len(data)
	}
	for tab, chat := range record.Chats {
		if tab != macMainTab && !crewMacTabID(tab) {
			return fmt.Errorf("unknown agent in the record")
		}
		total += len(chat)
	}
	for tab := range record.Programs {
		if tab != macMainTab && !crewMacTabID(tab) {
			return fmt.Errorf("unknown agent in the record")
		}
	}
	if total > teamCopyRecordMaxBytes {
		return fmt.Errorf("the agents' record is over %d MB", teamCopyRecordMaxBytes>>20)
	}
	for rel, data := range record.Files {
		if err := writeTeamCopyFile(base, rel, data); err != nil {
			return err
		}
	}
	// A record file removed on the Mac is removed here too; nothing outside the record is touched.
	var stale []string
	for _, dir := range teamCopyRecordDirs {
		root := filepath.Join(base, dir)
		_ = filepath.WalkDir(root, func(p string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return nil
			}
			rel, _ := filepath.Rel(base, p)
			if _, kept := record.Files[filepath.ToSlash(rel)]; !kept && entry.Type().IsRegular() {
				stale = append(stale, p)
			}
			return nil
		})
	}
	for _, name := range teamCopyRecordFiles {
		if _, kept := record.Files[name]; !kept {
			if info, err := os.Lstat(filepath.Join(base, name)); err == nil && info.Mode().IsRegular() {
				stale = append(stale, filepath.Join(base, name))
			}
		}
	}
	for _, p := range stale {
		_ = os.Remove(p)
	}
	dir, err := teamCopyAgentDir(project)
	if err != nil {
		return err
	}
	_ = os.RemoveAll(dir)
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	tabs := map[string]bool{}
	for tab := range record.Chats {
		tabs[tab] = true
	}
	for tab := range record.Programs {
		tabs[tab] = true
	}
	for tab := range tabs {
		raw, _ := json.Marshal(teamCopyAgent{teamCopyProgram: record.Programs[tab], Chat: record.Chats[tab]})
		if err = os.WriteFile(filepath.Join(dir, tab+".json"), raw, 0600); err != nil {
			return err
		}
	}
	return nil
}

// writeTeamCopyFile writes one file under base, never through a link.
func writeTeamCopyFile(base, rel string, data []byte) error {
	target := filepath.Join(base, filepath.FromSlash(path.Clean(rel)))
	if !strings.HasPrefix(target, filepath.Clean(base)+string(filepath.Separator)) {
		return fmt.Errorf("invalid record path")
	}
	for dir := filepath.Dir(target); dir != filepath.Clean(base); dir = filepath.Dir(dir) {
		if info, err := os.Lstat(dir); err == nil && info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("invalid record path")
		}
	}
	if info, err := os.Lstat(target); err == nil && !info.Mode().IsRegular() {
		return fmt.Errorf("invalid record path")
	}
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return err
	}
	if err := os.WriteFile(target+".team-copy", data, 0644); err != nil {
		return err
	}
	return os.Rename(target+".team-copy", target)
}

type crewTeamCopyStatus struct {
	WhenMacOff    string `json:"when_mac_off"`
	Ready         bool   `json:"ready"`
	RecordAt      string `json:"record_at,omitempty"`
	AnsweredToday int    `json:"answered_today"`
	Waiting       int    `json:"waiting"`
	MacOn         bool   `json:"mac_on"`
	// NeedsLogin names the programs the owner must sign in to on this server once so their team copy can answer.
	NeedsLogin []string          `json:"needs_login,omitempty"`
	People     []teamMacPresence `json:"people,omitempty"`
}

type teamMacPresence struct {
	UserID string `json:"user_id"`
	Name   string `json:"name"`
	MacOn  bool   `json:"mac_on"`
}

func teamCopyStatus(owner, project string, b *crewroot.SyncBinding) crewTeamCopyStatus {
	s := readTeamCopyState(project)
	out := crewTeamCopyStatus{WhenMacOff: s.mode(), Ready: s.RecordAt != "", RecordAt: s.RecordAt, AnsweredToday: s.Answered[time.Now().UTC().Format("2006-01-02")],
		Waiting: len(s.Waiting), MacOn: crewPlace(owner, project, b) == "mac"}
	for _, agent := range readMacSharedAgents(project) {
		if _, reason := teamCopyRoute(project, agent.Tab); reason == "no_own_login" {
			part, _ := readTeamCopyAgent(project, agent.Tab)
			if name := providerAccountProductName(part.Provider); part.Provider != "" && !slices.Contains(out.NeedsLogin, name) {
				out.NeedsLogin = append(out.NeedsLogin, name)
			}
		}
	}
	for _, person := range crewTeamPeople(project) {
		if person != owner {
			out.People = append(out.People, teamMacPresence{UserID: person, Name: shareOwnerLabel(person), MacOn: time.Since(teamMacSeen(project, person)) <= teamMacSeenWithin})
		}
	}
	return out
}

// teamMacsSeen is when each team member's Mac last synced the team folder, in memory only.
var teamMacsSeen = struct {
	sync.Mutex
	at map[string]time.Time
}{at: map[string]time.Time{}}

func noteTeamMacSeen(project, user string) {
	teamMacsSeen.Lock()
	teamMacsSeen.at[project+"/"+user] = time.Now()
	teamMacsSeen.Unlock()
}

func teamMacSeen(project, user string) time.Time {
	teamMacsSeen.Lock()
	defer teamMacsSeen.Unlock()
	return teamMacsSeen.at[project+"/"+user]
}

// --- On the Mac ---

var teamCopyPushed = struct {
	sync.Mutex
	digest map[string]string
}{digest: map[string]string{}}

// startTeamCopyRecords keeps each shared Crew's team copy current on its server: every two minutes, when the record
// changed, the Mac sends it.
func (api *StreamingAPI) startTeamCopyRecords() context.CancelFunc {
	ctx, cancel := context.WithCancel(context.Background())
	if !IsLocalMode() {
		go api.sweepHeldForPublished(ctx)
		return cancel
	}
	go func() {
		tick := time.NewTicker(2 * time.Minute)
		defer tick.Stop()
		for {
			state, err := crewroot.StateRoot()
			if err == nil {
				files, _ := filepath.Glob(filepath.Join(state, "crew-sync", "*-agents.json"))
				for _, file := range files {
					if ctx.Err() != nil {
						return
					}
					api.pushTeamCopyRecord(ctx, strings.TrimSuffix(filepath.Base(file), "-agents.json"))
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
		}
	}()
	return cancel
}

// pushTeamCopyRecord sends this Crew's record for its team copy when it changed since the last send.
func (api *StreamingAPI) pushTeamCopyRecord(ctx context.Context, project string) {
	shared := readMacSharedAgents(project)
	if len(shared) == 0 {
		return
	}
	claims, binding, connection, err := api.desktopCrewChatBinding(ctx, project)
	if err != nil || binding.Mode != "relay" {
		return
	}
	roots, err := api.crewSyncRoots(ctx, claims, project)
	if err != nil {
		return
	}
	record := crewTeamCopyRecord{Files: map[string][]byte{}, Chats: map[string]string{}, Programs: map[string]teamCopyProgram{}}
	total := 0
	for _, root := range []string{roots.Resources, roots.Files} {
		_ = filepath.WalkDir(root, func(p string, entry fs.DirEntry, err error) error {
			if err != nil || p == root {
				return nil
			}
			rel, _ := filepath.Rel(root, p)
			rel = filepath.ToSlash(rel)
			if entry.IsDir() {
				first, _, _ := strings.Cut(rel, "/")
				if !slices.Contains(teamCopyRecordDirs, first) {
					return filepath.SkipDir
				}
				return nil
			}
			if !entry.Type().IsRegular() || !teamCopyRecordPath(rel) || len(record.Files) >= teamCopyRecordMaxFiles {
				return nil
			}
			if _, have := record.Files[rel]; have {
				return nil
			}
			data, err := os.ReadFile(p)
			if err != nil || total+len(data) > teamCopyRecordMaxBytes/2 {
				return nil
			}
			total += len(data)
			record.Files[rel] = data
			return nil
		})
	}
	profile, err := api.agentProfiles.Resolve(crewProfileID, 0, claims.UserID)
	if err != nil {
		return
	}
	r, _ := http.NewRequestWithContext(context.WithValue(ctx, UserContextKey, claims), http.MethodPost, "http://localhost/api/agent-profiles/work/conversation", nil)
	r = mux.SetURLVars(r, map[string]string{"id": crewProfileID})
	for _, agent := range shared {
		conversation, err := api.resolveAgentProfileConversation(r, profile, macSharedAgentChatKey(project, agent.Tab), "")
		if err != nil {
			continue
		}
		if conversation.Provider != "" {
			record.Programs[agent.Tab] = teamCopyProgram{Provider: conversation.Provider, ModelID: conversation.ModelID}
		}
		items, _, err := codeChatTranscript(r.Context(), claims.UserID, conversation.SessionID, conversation.WorkspacePath, 0, 0)
		if err != nil || len(items) == 0 {
			continue
		}
		record.Chats[agent.Tab] = teamCopyChatText(firstNonEmptyTrimmed(agent.Name, "this agent"), items)
	}
	record.Digest = teamCopyDigest(record)
	teamCopyPushed.Lock()
	same := teamCopyPushed.digest[project] == record.Digest
	teamCopyPushed.Unlock()
	if same {
		return
	}
	if _, err = desktopCrewSyncCall(ctx, claims.UserID, &connection, binding.PeerProjectID, crewSyncRequest{Action: "team_copy_record", PairID: binding.PairID, TeamCopy: &record}); err != nil {
		log.Printf("[TEAM_COPY] local project=%s action=record_push failed: %v", project, err)
		return
	}
	teamCopyPushed.Lock()
	teamCopyPushed.digest[project] = record.Digest
	teamCopyPushed.Unlock()
	log.Printf("[TEAM_COPY] local project=%s action=record_push files=%d chats=%d digest=%s", project, len(record.Files), len(record.Chats), record.Digest)
}

// teamCopyChatText is the owner's chat with one agent as plain text, the newest part when it is long.
func teamCopyChatText(agent string, items []codeTurnItem) string {
	var b strings.Builder
	for _, item := range items {
		if strings.TrimSpace(item.Text) == "" {
			continue
		}
		who := "Owner"
		if item.Role == "assistant" {
			who = agent
		}
		fmt.Fprintf(&b, "**%s** (%s):\n%s\n\n", who, item.At, item.Text)
	}
	text := b.String()
	if len(text) > teamCopyChatMaxBytes {
		text = "…(earlier messages left out)\n\n" + text[len(text)-teamCopyChatMaxBytes:]
	}
	return text
}

func teamCopyDigest(record crewTeamCopyRecord) string {
	h := sha256.New()
	names := make([]string, 0, len(record.Files))
	for name := range record.Files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		sum := sha256.Sum256(record.Files[name])
		fmt.Fprintf(h, "f\x00%s\x00%x\n", name, sum)
	}
	tabs := make([]string, 0, len(record.Chats))
	for tab := range record.Chats {
		tabs = append(tabs, tab)
	}
	sort.Strings(tabs)
	for _, tab := range tabs {
		sum := sha256.Sum256([]byte(record.Chats[tab]))
		fmt.Fprintf(h, "c\x00%s\x00%x\n", tab, sum)
	}
	programs := make([]string, 0, len(record.Programs))
	for tab, program := range record.Programs {
		programs = append(programs, tab+"\x00"+program.Provider+"\x00"+program.ModelID)
	}
	sort.Strings(programs)
	fmt.Fprintf(h, "p\x00%s\n", strings.Join(programs, "\x01"))
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// desktopTeamCopyReport, on the Mac, puts the team copy's note in the agent's own chat as its next turn.
func (api *StreamingAPI) desktopTeamCopyReport(ctx context.Context, project string, call crewChatCall) crewChatResult {
	reply := func(status int, body any) crewChatResult {
		data, _ := json.Marshal(body)
		return crewChatResult{ID: call.ID, Status: status, Body: data}
	}
	if !slices.ContainsFunc(readAllMacSharedAgents(project), func(a macSharedAgent) bool { return a.Tab == call.Tab }) || strings.TrimSpace(call.Message) == "" {
		return reply(403, map[string]string{"error": "This agent isn't shared."})
	}
	attribution := map[string]interface{}{"source": "agent_message", "sender_label": "Team copy", "display_content": "While your Mac was off, the team copy answered people's messages. Here is what was asked and answered."}
	body, _ := json.Marshal(AgentProfileChatRequest{Message: call.Message, ConversationKey: macSharedAgentChatKey(project, call.Tab), DeliveryMode: "queue"})
	req, _ := http.NewRequestWithContext(context.WithValue(ctx, deliveredAgentMessageKey{}, attribution), http.MethodPost, "http://localhost/api/agent-profiles/work/query", strings.NewReader(string(body)))
	req = mux.SetURLVars(req, map[string]string{"id": crewProfileID})
	req.Header.Set("Content-Type", "application/json")
	capture := &internalResponseCapture{}
	api.handleAgentProfileChatQuery(capture, req)
	status := capture.status
	if status == 0 {
		status = 200
	}
	log.Printf("[TEAM_COPY] local project=%s tab=%s operation=away_report status=%d", project, call.Tab, status)
	if status >= 300 {
		return reply(status, map[string]string{"error": "The note could not be added."})
	}
	return reply(200, map[string]any{"status": "accepted"})
}

// teamCopyMacBackAll hands each Crew this Mac offers back from its team copy.
func (api *StreamingAPI) teamCopyMacBackAll(sock *crewMacSocket) {
	sock.mu.Lock()
	twins := map[string]string{}
	for project, pair := range sock.twins {
		twins[project] = pair
	}
	sock.mu.Unlock()
	for project, pair := range twins {
		go api.teamCopyMacBack(sock.owner, project, pair)
	}
}

// desktopTeamCopy reads or sets "When your Mac is off" on the online copy, for the Team tab and the Crew's chat. Choosing
// the team copy sends the agents' record at once, so it is ready before the Mac goes off.
func (api *StreamingAPI) desktopTeamCopy(ctx context.Context, claims *UserClaims, project string, binding *crewroot.SyncBinding, connection desktopConnection, whenMacOff string) (crewSyncReply, int, string, error) {
	if binding == nil || binding.Mode != "relay" || binding.Editing != "local" {
		return crewSyncReply{}, 409, "reach_off", fmt.Errorf("Turn on Use from phone and web first.")
	}
	if whenMacOff == teamCopyAnswer {
		api.pushTeamCopyRecord(ctx, project)
	}
	reply, err := desktopCrewSyncCall(ctx, claims.UserID, &connection, binding.PeerProjectID, crewSyncRequest{Action: "team_copy", PairID: binding.PairID, WhenMacOff: whenMacOff})
	if err != nil {
		return crewSyncReply{}, 409, crewSyncErrorCode(err), err
	}
	return reply, 200, "", nil
}

// sweepHeldForPublished, on the server, delivers messages held for a Mac whose agent was published since, without
// waiting for that Mac to come back: every two minutes.
func (api *StreamingAPI) sweepHeldForPublished(ctx context.Context) {
	tick := time.NewTicker(2 * time.Minute)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		state, err := crewroot.StateRoot()
		if err != nil {
			continue
		}
		files, _ := filepath.Glob(filepath.Join(state, "crew-sync", "*-team-copy.json"))
		for _, file := range files {
			project := strings.TrimSuffix(filepath.Base(file), "-team-copy.json")
			held := readTeamCopyState(project).Waiting
			if !slices.ContainsFunc(held, func(w crewTeamCopyWaiting) bool { return crewPublishedTarget(project, w.Tab) }) {
				continue
			}
			if b, err := crewroot.ReadSync(project); err == nil && b != nil {
				api.teamCopyMacBack(ownerOfCrewProjectID(project), project, b.PairID)
			}
		}
	}
}
