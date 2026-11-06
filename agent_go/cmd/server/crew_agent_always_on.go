package server

// Always on, step 2 (owner, 5 Nov: every In team agent is always on): while the owner's Mac is off, an In team agent
// with a standby copy (crew_agent_standby.go) answers on the server from that copy: teammates' messages and held
// messages. It is for other people (owner, 6 Nov): the owner's own chat with it waits for the Mac. The server runs it the
// way a published agent runs (serverAgentCall), from the same conversation: the stored native session is imported with its own native session id and the chat's history
// is seeded from the copy. The first turn makes the server the agent's writer (recorded in the standby record); from
// then on the server answers for it and the owner's Mac refuses to run it until it is handed back (step 3).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// crewStandbyMacRefusalLine is what the owner's Mac says when it is asked to run an agent the server is answering for.
const crewStandbyMacRefusalLine = "This agent is answering from the server while your Mac was off; it comes back to this Mac in a moment."

// crewStandbyServerNote is added to a turn the server runs from a standby copy (resolveAgentProfileForQuery). On the
// Mac the agent has every file and app of the Crew; here it has only what the server holds (owner, 6 Nov), so the
// note names whose agent it is, which files are here, and what this Crew uses that only the Mac has (the list the
// owner's Mac sent with its last copy, crewStandbyMacOnly).
// crewStandbyMacOnlyShown keeps the Mac-only list in every server turn's note short.
const crewStandbyMacOnlyShown = 50

func crewStandbyServerNote(project, person, tab string) string {
	identity := crewStandbyRunAs(project, person)
	ownerName := firstNonEmptyTrimmed(crewPersonShortName(person), "your owner")
	agent := "Main"
	agents := readMacSharedAgents(project)
	if identity.Member {
		// A member's agent is as their Mac listed it, not the owner's agent of the same tab.
		agents = nil
		if a, ok := crewMemberStandbyAgent(project, person, tab); ok {
			agents = []macSharedAgent{a}
		}
	}
	for _, a := range agents {
		if a.Tab == tab && strings.TrimSpace(a.Name) != "" {
			agent = strings.TrimSpace(a.Name)
		}
	}
	files := "none of the Crew's files, only this conversation"
	if t, err := readCrewTeam(project); err == nil && t.Folder.Whole {
		files = "all of the Crew's files"
	} else if err == nil && len(t.Folder.Folders) > 0 {
		files = "only the Team's shared folders: " + strings.Join(t.Folder.Folders, ", ")
	}
	// What is really here: the owner's copy of the team folder, attached as WORK_FOLDER_<NAME> (crew_team_server_copy.go).
	if t, err := readCrewTeam(project); err == nil {
		if top := crewCopyTop(t, person); top != "" {
			files = fmt.Sprintf("your copy of the Team's shared folder, attached as the folder WORK_FOLDER_%s (it syncs with the team, so what you change reaches the Mac when it is back); it holds %s", strings.ToUpper(crewTeamCopyAlias(t)), top)
		} else if identity.Member {
			files = "none of the Crew's files, only this conversation (your copy of the Team's shared folder is not ready yet)"
		}
	}
	var b strings.Builder
	// Whose account really runs this turn (crewStandbyRunAs): said plainly, never the Crew owner's for a member's agent.
	account := fmt.Sprintf("with %s's AI account and the apps and secrets in their Vault on the server", ownerName)
	switch identity.Account() {
	case "member_slot":
		account = fmt.Sprintf("with %s's own login on this server and the apps and secrets in their Vault on the server (never the Crew owner's)", ownerName)
	case "shared_account":
		account = fmt.Sprintf("with the server's shared AI account, because %s has no login of their own on this server, and the apps and secrets in their Vault on the server (never the Crew owner's)", ownerName)
	}
	fmt.Fprintf(&b, "## Answering from the server\n\nYou are %s's agent %q. %s's Mac is off, so you are answering from the server's copy of this conversation, %s.\n\n", ownerName, agent, ownerName, account)
	fmt.Fprintf(&b, "Files here: %s. Everything else in this Crew is only on the Mac: do not assume those files exist here.\n\n", files)
	if items := crewStandbyMacOnly(project, person); len(items) > 0 {
		names := make([]string, 0, min(len(items), crewStandbyMacOnlyShown)+1)
		for _, item := range items[:min(len(items), crewStandbyMacOnlyShown)] {
			names = append(names, item.Name+" ("+item.Kind+")")
		}
		if extra := len(items) - crewStandbyMacOnlyShown; extra > 0 {
			names = append(names, fmt.Sprintf("and %d more", extra))
		}
		fmt.Fprintf(&b, "Only on the Mac, not available now: %s.\n\n", strings.Join(names, ", "))
	}
	if rows, err := readCrewStandby(project, person); err == nil {
		if row := rows[tab]; row.MacProgram != "" && row.ServerProgram != row.MacProgram {
			fmt.Fprintf(&b, "On the Mac you run on %s; this server doesn't have it, so you continue this conversation on %s from its saved history: you have the conversation, but not the details of earlier tool calls.\n\n", providerDisplayLabel(row.MacProgram), crewStandbyServerProgramName(row.ServerProgram))
		}
	}
	b.WriteString("If a request needs a file or app that is only on the Mac, say which one and that it works when the Mac is back; do the rest here.")
	return b.String()
}

var errCrewNoStandbyCopy = errors.New("this agent has no server copy yet")

// Test seams: the import needs a provider runtime and the turn a full agent run; a test stands in for both.
var (
	crewStandbyImportForTest func(owner, project, tab, sender string) error
	crewStandbyRunForTest    func(ctx context.Context, project string, call crewChatCall) crewChatResult
)

func crewAlwaysOnLog(project, tab, event, reason string, started time.Time) {
	log.Printf("[ALWAYS_ON] project=%s tab=%s event=%s reason=%s ms=%d", project, tab, event, reason, time.Since(started).Milliseconds())
}

// crewStandbyServerWriter: the server answers for this agent now (it ran a turn from the standby copy and has not
// handed the agent back to the Mac yet).
func crewStandbyServerWriter(project, person, tab string) bool {
	if IsLocalMode() || project == "" || person == "" || tab == "" {
		return false
	}
	rows, err := readCrewStandby(project, person)
	return err == nil && rows[tab].Writer == "server"
}

// crewStandbyServesKey: the chat with this key is a teammate's thread on an agent the server answers for from its
// standby copy. Only those run here: writer=server stops the Mac running the agent until it is handed back, but the
// owner's own key still belongs to the Mac, so it is never served (PLAT-856-509).
func crewStandbyServesKey(key string) bool {
	_, ok := crewStandbyServedTab(key)
	return ok
}

// crewStandbyServedTab is the agent of a teammate's thread the server answers for from its standby copy.
func crewStandbyServedTab(key string) (string, bool) {
	return crewStandbyServedTabOf(ownerOfCrewProjectID(crewResourceProjectID(strings.TrimSpace(key))), key)
}

// crewStandbyServedTabOf is the same for the agents of person (the Crew's owner, or a Team member working on their Mac):
// a thread lives in the registry of the person whose agent it is, so the person that runs the turn names the rows.
func crewStandbyServedTabOf(person, key string) (string, bool) {
	key = strings.TrimSpace(key)
	project := crewResourceProjectID(key)
	if IsLocalMode() || project == "" || person == "" || !isSharedThreadKey(key) {
		return "", false
	}
	rows, err := readCrewStandby(project, person)
	if err != nil || len(rows) == 0 {
		return "", false
	}
	tab := crewStandbyTabForKeyOf(project, person, key)
	return tab, rows[tab].Writer == "server"
}

// crewStandbyTabForKey is the Mac agent a chat key of the Crew's online copy belongs to: a side tab or a teammate's
// thread on it, or the owner's Main (by the project, its physical key, or a thread on it).
func crewStandbyTabForKey(project, key string) string {
	return crewStandbyTabForKeyOf(project, ownerOfCrewProjectID(project), key)
}

// crewStandbyTabForKeyOf is the agent a chat key belongs to among the agents of person: their Main is the key their own
// registry gives it (crewOwnerDefaultChatKey); the root-key Main is the Crew owner's alone.
func crewStandbyTabForKeyOf(project, person, key string) string {
	key = strings.TrimSpace(key)
	if project == "" || key == "" {
		return ""
	}
	// The saved Main pointer distinguishes the owner's Main from their other registered agents; a member's Main is the
	// key their own registry gives it.
	if key == crewOwnerDefaultChatKey(project, person) || person == ownerOfCrewProjectID(project) && key == crewOwnerMainChatKey(project) {
		return macMainTab
	}
	return crewAgentPublishTab(key)
}

// crewStandbyCanAnswer is the server's routing decision without any work: the server already answers for the agent,
// or the owner's Mac is off and the agent is In team with a current copy of its owner's chat.
func crewStandbyCanAnswer(project, owner, tab string) bool {
	if IsLocalMode() || owner == "" || !crewMacTabID(tab) {
		return false
	}
	rows, err := readCrewStandby(project, owner)
	if err != nil {
		return false
	}
	row, ok := rows[tab]
	if !ok {
		return false
	}
	if row.Writer == "server" {
		return true
	}
	pair, peer := crewStandbyPairPeer(project, owner, row)
	if pair == "" || pair != row.PairID || crewMacSocketFor(owner, project, pair) != nil {
		return false
	}
	if _, ok := row.Sessions[macSharedAgentChatKey(peer, tab)]; !ok {
		return false
	}
	ok, _ = crewStandbyEligible(project, owner, tab)
	return ok
}

// crewStandbyRoute decides whether a teammate's call to this agent runs on the server copy, and prepares it: the owner's
// chat with the agent (what the thread starts from) and the asking teammate's own thread on it are imported from the copy. writes is set for a call that runs a
// turn; it makes the server the agent's writer. Any failure leaves the call on its usual path (the Mac, or held).
func (api *StreamingAPI) crewStandbyRoute(ctx context.Context, owner, project, tab, sender string, writes bool) bool {
	// Placements are the Crew owner's tabs: only they can make the owner's agent "published".
	if owner == ownerOfCrewProjectID(project) && crewPublishedTarget(project, tab) || !crewStandbyCanAnswer(project, owner, tab) {
		return false
	}
	started := time.Now()
	if err := api.importCrewStandby(ctx, owner, project, tab, sender); err != nil {
		if !crewStandbyServerWriter(project, owner, tab) {
			crewAlwaysOnLog(project, tab, "not_on_server", "import_failed", started)
			crewStandbyImportFailed.Store(project+"\x00"+owner+"\x00"+tab, time.Now())
			log.Printf("[ALWAYS_ON] project=%s tab=%s import_error=%q", project, tab, err)
			return false
		}
		log.Printf("[ALWAYS_ON] project=%s tab=%s event=import_retry_later import_error=%q", project, tab, err)
	}
	crewStandbyImportFailed.Delete(project + "\x00" + owner + "\x00" + tab)
	// The decision, the in-flight mark and the writer are settled under the lock the hand back flips the writer under:
	// a call routed here is never lost to a flip that happens before its turn is running or queued.
	lock := crewStandbyLock(project, owner)
	lock.Lock()
	defer lock.Unlock()
	if !crewStandbyCanAnswer(project, owner, tab) {
		crewAlwaysOnLog(project, tab, "not_on_server", "handed_back", started)
		return false
	}
	if writes {
		addCrewStandbyFlight(project, owner, tab)
		markCrewStandbyWriterLocked(project, owner, tab)
	}
	return true
}

// Calls routed to the server copy whose turn is not yet visible as running or queued (crewStandbyRoute until the turn
// is admitted). handback_done answers Busy while any is in flight. An entry that is never released (a caller that
// stopped before its turn) expires.
const crewStandbyFlightExpiry = 2 * time.Minute

var crewStandbyFlights = struct {
	sync.Mutex
	at map[string][]time.Time
}{at: map[string][]time.Time{}}

func addCrewStandbyFlight(project, person, tab string) {
	crewStandbyFlights.Lock()
	defer crewStandbyFlights.Unlock()
	key := project + "\x00" + person + "\x00" + tab
	crewStandbyFlights.at[key] = append(crewStandbyFlights.at[key], time.Now())
}

// releaseCrewStandbyFlight: one routed call's turn is running or queued now.
func releaseCrewStandbyFlight(project, person, tab string) {
	crewStandbyFlights.Lock()
	defer crewStandbyFlights.Unlock()
	key := project + "\x00" + person + "\x00" + tab
	if flights := crewStandbyFlights.at[key]; len(flights) > 1 {
		crewStandbyFlights.at[key] = flights[1:]
	} else {
		delete(crewStandbyFlights.at, key)
	}
}

func crewStandbyInFlight(project, person, tab string) bool {
	crewStandbyFlights.Lock()
	defer crewStandbyFlights.Unlock()
	key := project + "\x00" + person + "\x00" + tab
	kept := crewStandbyFlights.at[key][:0]
	for _, at := range crewStandbyFlights.at[key] {
		if time.Since(at) < crewStandbyFlightExpiry {
			kept = append(kept, at)
		}
	}
	if len(kept) == 0 {
		delete(crewStandbyFlights.at, key)
		return false
	}
	crewStandbyFlights.at[key] = kept
	return true
}

// markCrewStandbyWriterLocked records that the server answers for this agent from its standby copy at the copy's
// revision. The caller holds the project's standby lock.
func markCrewStandbyWriterLocked(project, person, tab string) {
	started := time.Now()
	rows, err := readCrewStandby(project, person)
	if err != nil {
		return
	}
	row, ok := rows[tab]
	if !ok || row.Writer == "server" {
		return
	}
	row.Writer, row.WriterRevision, row.WriterSince = "server", row.Revision, time.Now().UTC().Format(time.RFC3339)
	rows[tab] = row
	if err = writeCrewStandby(project, person, rows); err != nil {
		log.Printf("[ALWAYS_ON] project=%s tab=%s event=writer_not_saved", project, tab)
		return
	}
	crewAlwaysOnLog(project, tab, "writer_server", fmt.Sprintf("revision_%d", row.Revision), started)
}

// importCrewStandby brings the agent's person's chat with the agent (the owner's, or the member's whose agent it is), and
// the sender's own thread on it when a teammate asks, from the standby copy into this server's conversations of that
// person. A copy already imported at its revision is left as it is.
func (api *StreamingAPI) importCrewStandby(ctx context.Context, owner, project, tab, sender string) error {
	if crewStandbyImportForTest != nil {
		return crewStandbyImportForTest(owner, project, tab, sender)
	}
	claims := &UserClaims{UserID: owner, Username: owner}
	ctx = context.WithValue(ctx, UserContextKey, claims)
	roots, binding, err := api.crewStandbyImportRoots(ctx, claims, project, tab)
	if err != nil {
		return err
	}
	dir, err := crewStandbyPersonDir(project, owner)
	if err != nil {
		return err
	}
	lock := crewStandbyLock(project, owner)
	lock.Lock()
	defer lock.Unlock()
	rows, err := readCrewStandby(project, owner)
	if err != nil {
		return err
	}
	row, ok := rows[tab]
	if !ok || row.PairID != binding.PairID {
		return errCrewNoStandbyCopy
	}
	keys := []string{macSharedAgentChatKey(binding.PeerProjectID, tab)}
	if sender != "" && sender != owner {
		keys = append(keys, sharedThreadKey(binding.PeerProjectID, tab, sender))
	}
	changed := false
	for i, key := range keys {
		session, ok := row.Sessions[key]
		if !ok {
			if i == 0 {
				return errCrewNoStandbyCopy
			}
			continue // a teammate with no thread yet starts from the owner's chat (crew_shared_fork.go)
		}
		if session.Imported == session.Revision {
			continue
		}
		started := time.Now()
		program, err := api.importCrewStandbySession(ctx, claims, roots, binding, tab, key, filepath.Join(dir, tab, session.Dir), session)
		if err != nil {
			if i == 0 {
				return err
			}
			log.Printf("[ALWAYS_ON] project=%s tab=%s event=thread_not_imported import_error=%q", project, tab, err)
			continue
		}
		session.Imported = session.Revision
		row.Sessions[key] = session
		if i == 0 {
			row.MacProgram, row.ServerProgram = session.Provider, program
		}
		changed = true
		mode := "native"
		if program != session.Provider {
			mode = "history"
		}
		log.Printf("[ALWAYS_ON] project=%s tab=%s event=imported copy=%s revision=%d bytes=%d mode=%s mac_program=%s server_program=%s ms=%d", project, tab, session.Dir, session.Revision, session.Bytes, mode, session.Provider, program, time.Since(started).Milliseconds())
	}
	if !changed {
		return nil
	}
	rows[tab] = row
	return writeCrewStandby(project, owner, rows)
}

// crewStandbyImportRoots is where a person's standby copy is imported from and into: the Crew owner's, by the Crew's own
// pair; a member's, by the pair the Team recorded for them and the Mac project their row names, with their own staging
// folder and copy of the Team files (crewMemberTransfer), never the owner's.
func (api *StreamingAPI) crewStandbyImportRoots(ctx context.Context, claims *UserClaims, project, tab string) (crewroot.Roots, *crewroot.SyncBinding, error) {
	if claims.UserID == ownerOfCrewProjectID(project) {
		roots, err := api.crewSyncRoots(ctx, claims, project)
		if err != nil {
			return roots, nil, err
		}
		binding, err := crewroot.ReadSync(project)
		if err != nil || binding == nil || binding.Side != "server" || binding.PeerProjectID == "" {
			return roots, nil, fmt.Errorf("this Crew is not paired with its Mac")
		}
		return roots, binding, nil
	}
	rows, err := readCrewStandby(project, claims.UserID)
	if err != nil {
		return crewroot.Roots{}, nil, err
	}
	pair, peer := crewStandbyPairPeer(project, claims.UserID, rows[tab])
	roots, binding, member, err := api.crewMemberTransfer(ctx, claims, project, pair)
	if err != nil {
		return roots, nil, err
	}
	if !member || binding == nil || peer == "" {
		return roots, nil, fmt.Errorf("this Crew is not paired with their Mac")
	}
	binding.PeerProjectID = peer
	return roots, binding, nil
}

// importCrewStandbySession imports one stored conversation (native session + history) into the server's chat with the
// same address: the owner's tab, the Crew's Main, or a teammate's thread on the agent. Publish's import does the work:
// the native session keeps its id, the history becomes the chat's dialogue. When this server lacks the agent's AI
// program, the chat continues from that history on a program it has (owner, 6 Nov). It returns the program the
// server's chat runs.
func (api *StreamingAPI) importCrewStandbySession(ctx context.Context, claims *UserClaims, roots crewroot.Roots, binding *crewroot.SyncBinding, tab, macKey, copyDir string, session crewAgentStandbySession) (string, error) {
	raw, err := os.ReadFile(filepath.Join(copyDir, "conversation.json"))
	if err != nil {
		return "", err
	}
	var conversation crewHandoffConversation
	if err = json.Unmarshal(raw, &conversation); err != nil {
		return "", err
	}
	native, err := readCrewStandbyBundle(copyDir)
	if err != nil {
		return "", err
	}
	if err = native.Validate(); err != nil {
		return "", err
	}
	conversation.Native = native
	// Publish's import reads the native files from this session's stage; a later copy (or a teammate thread of the
	// same session) may have replaced them there, so this copy is staged again first.
	if err = stageCrewNative(roots, native); err != nil {
		return "", err
	}
	main, err := api.crewHandoffMain(ctx, claims, roots.ID)
	if err != nil {
		return "", err
	}
	key := main.ConversationKey
	if macKey != binding.PeerProjectID {
		if !strings.HasPrefix(macKey, binding.PeerProjectID+codeChatSideMarker) {
			return "", fmt.Errorf("a standby conversation has an invalid address")
		}
		key = roots.ID + strings.TrimPrefix(macKey, binding.PeerProjectID)
	}
	store := defaultProductConversationRegistryStore()
	path := productConversationRegistryPath(claims.UserID)
	lock := productConversationRegistryMutex(path)
	lock.Lock()
	doc, err := store.loadDocument(ctx, path)
	if err != nil {
		lock.Unlock()
		return "", err
	}
	entry := productConversationRegistryEntryKey(crewProfileID, key)
	current, exists := doc.Entries[entry]
	if !exists {
		current = main
		current.ConversationKey = key
		current.IsMainChat = key == main.ConversationKey
		current.SessionID = "product-" + uuid.NewSHA1(uuid.NameSpaceOID, []byte(binding.PairID+"/standby/"+key)).String()
		current.ConversationID = conversation.ConversationID
		doc.Entries[entry] = current
		// The owner's tab keeps its name in the left menu on the website.
		if key != main.ConversationKey && !hiddenFromTabStrip(key) {
			if doc.Agents == nil {
				doc.Agents = map[string]ProductAgentIdentity{}
			}
			identity := doc.Agents[entry]
			agents := readMacSharedAgents(roots.ID)
			if claims.UserID != ownerOfCrewProjectID(roots.ID) {
				agents = nil // a member's tab is named as their Mac listed it
				if a, ok := crewMemberStandbyAgent(roots.ID, claims.UserID, tab); ok {
					agents = []macSharedAgent{a}
				}
			}
			for _, agent := range agents {
				if agent.Tab == tab && strings.TrimSpace(identity.Name) == "" {
					identity.Name, identity.Purpose = agent.Name, agent.Purpose
				}
			}
			doc.Agents[entry] = identity
		}
		if err = store.writeDocument(ctx, path, doc); err != nil {
			lock.Unlock()
			return "", err
		}
	}
	lock.Unlock()
	current.handoffAgentToolsMode = main.handoffAgentToolsMode
	transfer := fmt.Sprintf("standby/%s/%s/%d", tab, session.Dir, session.Revision)
	if err = api.importCrewHandoffConversation(ctx, claims, roots, binding.PairID, transfer, &conversation, current, crewImportNativeElseHistory); err != nil {
		return "", err
	}
	program := current.Provider
	if doc, e := store.loadDocument(ctx, path); e == nil {
		if after, ok := doc.Entries[entry]; ok && !after.FreshNative {
			program = after.Provider
		} else if ok {
			program = firstNonEmptyTrimmed(after.Provider, program)
		}
	}
	return program, nil
}

// crewStandbyImportFailed remembers, per agent, when the server last could not start from its copy (for example no
// usable AI account on the server), so the Team tab says the agent waits for the Mac rather than promising an answer.
var crewStandbyImportFailed sync.Map // project\x00person\x00tab -> time.Time

func crewStandbyRecentlyFailed(project, person, tab string) bool {
	at, ok := crewStandbyImportFailed.Load(project + "\x00" + person + "\x00" + tab)
	return ok && time.Since(at.(time.Time)) < 30*time.Minute
}

// crewAlwaysOnPlaceLabel is where an agent of the owner's Mac answers now and where its server copy stands, for the
// Team tab (owner, 6 Nov, approved mockups): the server decides the words and the tone the screen colours them by:
// "ok" (the server holds a copy; the screen shows it quietly, not green), "wait" (no copy yet, or the Mac is taking the agent back), "server" (the server
// answers for it) or "" (plain). A finished turn whose copy has not gone up yet is not known here, so there is no
// "syncing" state: the label says when the last copy arrived.
func crewAlwaysOnPlaceLabel(project, owner, tab string, macOn, yours, inTeam bool) (label, tone string) {
	switch {
	case crewStandbyServerWriter(project, owner, tab):
		if macOn {
			return "Taking back from the server…", "wait"
		}
		return "Mac off · answering from the server" + crewStandbyOnProgram(project, owner, tab), "server"
	case macOn && !inTeam:
		return "Only you · on your Mac", ""
	case macOn:
		return crewStandbyCopyLabel(project, owner, tab)
	case crewStandbyCanAnswer(project, owner, tab) && !crewStandbyRecentlyFailed(project, owner, tab):
		return "Mac off · answering from the server", "server"
	case yours:
		return "Mac off · waits for your Mac", ""
	}
	return "Mac off · waits for their Mac", ""
}

// crewStandbyOnProgram names the program the server answers on when it is not the agent's own (" (on Muse)").
func crewStandbyOnProgram(project, person, tab string) string {
	rows, err := readCrewStandby(project, person)
	if row := rows[tab]; err == nil && row.MacProgram != "" && row.ServerProgram != row.MacProgram {
		return " (on " + crewStandbyServerProgramName(row.ServerProgram) + ")"
	}
	return ""
}

// crewStandbyServerProgramName names the program the server continued on; an imported chat that has not run a turn
// yet has none recorded, and then it runs on this server's default.
func crewStandbyServerProgramName(program string) string {
	if strings.TrimSpace(program) == "" {
		return "this server's AI program"
	}
	return providerDisplayLabel(program)
}

// crewStandbyCopyLabel is a shared agent's chip while its Mac is on: when its last copy reached the server.
func crewStandbyCopyLabel(project, person, tab string) (string, string) {
	rows, err := readCrewStandby(project, person)
	row, ok := rows[tab]
	if err != nil || !ok || len(row.Sessions) == 0 {
		return "Not on the server yet · it copies after its next reply", "wait"
	}
	if at, err := time.Parse(time.RFC3339, row.CopiedAt); err == nil {
		return "Always on · copy on the server " + crewAccessAgo(at), "ok"
	}
	return "Always on", "ok"
}

// crewAlwaysOnAgentsLine is the Agents card's line for the owner of a Mac Crew with the Team on.
const crewAlwaysOnAgentsLine = "Shared agents are always on: when your Mac is off, they answer teammates from the server. " + crewTeamAgentsLine

// crewTeamAgentsLine is the Agents card's line (owner, 6 Nov: each agent's own switch is the only control).
const crewTeamAgentsLine = "New agents are shared; turn one off to keep it to yourself. Keep at least one shared."
