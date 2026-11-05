package server

// Always on, step 2 (owner, 5 Nov: every In team agent is always on): while the owner's Mac is off, an In team agent
// with a standby copy (crew_agent_standby.go) answers on the server from that copy: teammates' messages, held messages
// and the owner's own website chat with it. The server runs it the way a published agent runs (serverAgentCall), from
// the same conversation: the stored native session is imported with its own native session id and the chat's history
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
	"time"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// crewStandbyMacRefusalLine is what the owner's Mac says when it is asked to run an agent the server is answering for.
const crewStandbyMacRefusalLine = "This agent is answering from the server while your Mac was off; it comes back to this Mac in a moment."

// crewStandbyServerNote is added to a turn the server runs from a standby copy (resolveAgentProfileForQuery).
const crewStandbyServerNote = "## Answering from the server\n\nYour owner's Mac is off, so you are answering from the server's copy of this conversation. You use your owner's AI account, apps and secrets on the server and the Team's shared files. Apps, files and sign-ins that exist only on the Mac are not available here: if this request needs one, say so plainly and suggest asking again when the Mac is back."

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
func crewStandbyServerWriter(project, tab string) bool {
	if IsLocalMode() || project == "" || tab == "" {
		return false
	}
	rows, err := readCrewStandby(project)
	return err == nil && rows[tab].Writer == "server"
}

// crewStandbyServesKey: the chat with this key belongs to an agent the server answers for from its standby copy.
func crewStandbyServesKey(key string) bool {
	project := crewResourceProjectID(key)
	if IsLocalMode() || project == "" {
		return false
	}
	rows, err := readCrewStandby(project)
	if err != nil || len(rows) == 0 {
		return false
	}
	return rows[crewStandbyTabForKey(project, key)].Writer == "server"
}

// crewStandbyTabForKey is the Mac agent a chat key of the Crew's online copy belongs to: a side tab or a teammate's
// thread on it, or the owner's Main (by the project, its physical key, or a thread on it).
func crewStandbyTabForKey(project, key string) string {
	key = strings.TrimSpace(key)
	if project == "" || key == "" {
		return ""
	}
	if key == crewOwnerDefaultChatKey(project, ownerOfCrewProjectID(project)) {
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
	rows, err := readCrewStandby(project)
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
	b, err := crewroot.ReadSync(project)
	if err != nil || b == nil || b.PairID == "" || b.PairID != row.PairID || crewMacSocketFor(owner, project, b.PairID) != nil {
		return false
	}
	if _, ok := row.Sessions[macSharedAgentChatKey(b.PeerProjectID, tab)]; !ok {
		return false
	}
	ok, _ = crewStandbyEligible(project, owner, tab)
	return ok
}

// crewStandbyRoute decides whether this agent's call runs on the server copy, and prepares it: the owner's chat with
// the agent (and the asking teammate's own thread on it) is imported from the copy. writes is set for a call that runs a
// turn; it makes the server the agent's writer. Any failure leaves the call on its usual path (the Mac, or held).
func (api *StreamingAPI) crewStandbyRoute(ctx context.Context, owner, project, tab, sender string, writes bool) bool {
	if crewPublishedTarget(project, tab) || !crewStandbyCanAnswer(project, owner, tab) {
		return false
	}
	started := time.Now()
	if err := api.importCrewStandby(ctx, owner, project, tab, sender); err != nil {
		if !crewStandbyServerWriter(project, tab) {
			crewAlwaysOnLog(project, tab, "not_on_server", "import_failed", started)
			log.Printf("[ALWAYS_ON] project=%s tab=%s import_error=%q", project, tab, err)
			return false
		}
		log.Printf("[ALWAYS_ON] project=%s tab=%s event=import_retry_later import_error=%q", project, tab, err)
	}
	if writes {
		markCrewStandbyWriter(project, tab)
	}
	return true
}

// markCrewStandbyWriter records that the server answers for this agent from its standby copy at the copy's revision.
func markCrewStandbyWriter(project, tab string) {
	started := time.Now()
	lock := crewStandbyLock(project)
	lock.Lock()
	defer lock.Unlock()
	rows, err := readCrewStandby(project)
	if err != nil {
		return
	}
	row, ok := rows[tab]
	if !ok || row.Writer == "server" {
		return
	}
	row.Writer, row.WriterRevision, row.WriterSince = "server", row.Revision, time.Now().UTC().Format(time.RFC3339)
	rows[tab] = row
	if err = writeCrewStandby(project, rows); err != nil {
		log.Printf("[ALWAYS_ON] project=%s tab=%s event=writer_not_saved", project, tab)
		return
	}
	crewAlwaysOnLog(project, tab, "writer_server", fmt.Sprintf("revision_%d", row.Revision), started)
}

// importCrewStandby brings the owner's chat with the agent, and the sender's own thread on it when a teammate asks,
// from the standby copy into this server's conversations. A copy already imported at its revision is left as it is.
func (api *StreamingAPI) importCrewStandby(ctx context.Context, owner, project, tab, sender string) error {
	if crewStandbyImportForTest != nil {
		return crewStandbyImportForTest(owner, project, tab, sender)
	}
	claims := &UserClaims{UserID: owner, Username: owner}
	ctx = context.WithValue(ctx, UserContextKey, claims)
	roots, err := api.crewSyncRoots(ctx, claims, project)
	if err != nil {
		return err
	}
	binding, err := crewroot.ReadSync(project)
	if err != nil || binding == nil || binding.Side != "server" || binding.PeerProjectID == "" {
		return fmt.Errorf("this Crew is not paired with its Mac")
	}
	dir, err := crewStandbyDir(project)
	if err != nil {
		return err
	}
	lock := crewStandbyLock(project)
	lock.Lock()
	defer lock.Unlock()
	rows, err := readCrewStandby(project)
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
		if err = api.importCrewStandbySession(ctx, claims, roots, binding, tab, key, filepath.Join(dir, tab, session.Dir), session); err != nil {
			if i == 0 {
				return err
			}
			log.Printf("[ALWAYS_ON] project=%s tab=%s event=thread_not_imported import_error=%q", project, tab, err)
			continue
		}
		session.Imported = session.Revision
		row.Sessions[key] = session
		changed = true
		log.Printf("[ALWAYS_ON] project=%s tab=%s event=imported copy=%s revision=%d bytes=%d ms=%d", project, tab, session.Dir, session.Revision, session.Bytes, time.Since(started).Milliseconds())
	}
	if !changed {
		return nil
	}
	rows[tab] = row
	return writeCrewStandby(project, rows)
}

// importCrewStandbySession imports one stored conversation (native session + history) into the server's chat with the
// same address: the owner's tab, the Crew's Main, or a teammate's thread on the agent. Publish's import does the work:
// the native session keeps its id, the history becomes the chat's dialogue.
func (api *StreamingAPI) importCrewStandbySession(ctx context.Context, claims *UserClaims, roots crewroot.Roots, binding *crewroot.SyncBinding, tab, macKey, copyDir string, session crewAgentStandbySession) error {
	raw, err := os.ReadFile(filepath.Join(copyDir, "conversation.json"))
	if err != nil {
		return err
	}
	var conversation crewHandoffConversation
	if err = json.Unmarshal(raw, &conversation); err != nil {
		return err
	}
	native, err := readCrewStandbyBundle(copyDir)
	if err != nil {
		return err
	}
	if err = native.Validate(); err != nil {
		return err
	}
	conversation.Native = native
	// Publish's import reads the native files from this session's stage; a later copy (or a teammate thread of the
	// same session) may have replaced them there, so this copy is staged again first.
	if err = stageCrewNative(roots, native); err != nil {
		return err
	}
	main, err := api.crewHandoffMain(ctx, claims, roots.ID)
	if err != nil {
		return err
	}
	key := main.ConversationKey
	if macKey != binding.PeerProjectID {
		if !strings.HasPrefix(macKey, binding.PeerProjectID+codeChatSideMarker) {
			return fmt.Errorf("a standby conversation has an invalid address")
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
		return err
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
			for _, agent := range readMacSharedAgents(roots.ID) {
				if agent.Tab == tab && strings.TrimSpace(identity.Name) == "" {
					identity.Name, identity.Purpose = agent.Name, agent.Purpose
				}
			}
			doc.Agents[entry] = identity
		}
		if err = store.writeDocument(ctx, path, doc); err != nil {
			lock.Unlock()
			return err
		}
	}
	lock.Unlock()
	current.handoffAgentToolsMode = main.handoffAgentToolsMode
	transfer := fmt.Sprintf("standby/%s/%s/%d", tab, session.Dir, session.Revision)
	return api.importCrewHandoffConversation(ctx, claims, roots, binding.PairID, transfer, &conversation, current, true)
}

// crewAlwaysOnPlaceLabel is where an agent of the owner's Mac answers now, for the Team tab (approved mockup): the
// server decides the words; serving is set when the server answers for it.
func crewAlwaysOnPlaceLabel(project, owner, tab string, macOn, yours bool) (label string, serving bool) {
	switch {
	case crewStandbyServerWriter(project, tab):
		if macOn {
			return "Answering from the server", true
		}
		return "Mac off · answering from the server", true
	case macOn:
		return "Mac", false
	case crewStandbyCanAnswer(project, owner, tab):
		return "Mac off · answering from the server", true
	case yours:
		return "Mac off · waits for your Mac", false
	}
	return "Mac off · waits for their Mac", false
}

// crewAlwaysOnAgentsLine is the Agents card's line for the owner of a Mac Crew with the Team on.
const crewAlwaysOnAgentsLine = "In-team agents are always on: when your Mac is off, they answer from the server. New agents join too, unless their person chose Only me."
