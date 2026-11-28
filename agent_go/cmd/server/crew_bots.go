package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"slices"
	"sort"
	"strings"
	"time"
)

// A bot is one agent of a project on the server that its maker shares for others to use without joining the project
// (#4545). Each person who uses it gets a private chat with it (their own conversation, Run mode, cost to them), and
// the project's functions its maker picks show as its buttons; Ask is always on. A bot is not a co-owner, User or Team
// share (those are people who work in the project), and not the dashboard (shared by link).
type crewBot struct {
	Icon string `json:"icon,omitempty"`
	// Everyone is anyone at the company; otherwise People and the people of Teams are its audience.
	Everyone  bool     `json:"everyone,omitempty"`
	People    []string `json:"people,omitempty"`
	Teams     []string `json:"teams,omitempty"`
	Functions []string `json:"functions,omitempty"`
	// Maker is the person whose agent this is (the main owner or a co-owner); only they change it.
	Maker string `json:"maker"`
	Since string `json:"since,omitempty"`
}

const crewBotIconMaxRunes = 8

// crewBotRoot is the folder of a project on the server from its registry record.
func crewBotRoot(rec projectOwnerRecord) string {
	if rec.CanonicalRoot != "" {
		return rec.CanonicalRoot
	}
	return "Crew/" + rec.Folder
}

// crewBotAdmits says whether user may use the bot at key: the project's builders always may; otherwise the person must
// be an active account with the project product and in the bot's audience. Read on every call, so a Team leave or an
// audience change takes effect at once.
func crewBotAdmits(rec projectOwnerRecord, key, user string) bool {
	bot, ok := rec.Bots[key]
	user = strings.TrimSpace(user)
	if !ok || user == "" || !crewLivesOnServer(rec.ProjectID) {
		return false
	}
	if user == rec.OwnerID || containsID(rec.CoOwners, user) {
		return true
	}
	if !crewTeamAccountActive(user) {
		return false
	}
	if bot.Everyone || containsID(bot.People, user) {
		return true
	}
	for _, team := range crewTeamsOn() {
		if containsID(bot.Teams, team.ProjectID) && (team.OwnerID == user || containsID(crewTeamJoinedPeople(team), user)) {
			return true
		}
	}
	return false
}

// crewBotAdmitsAny says whether a bot of the project at root admits user. It opens the project to them in Run mode,
// as any shared project does, but the project is not listed in their Projects: they reach it through Bots.
func crewBotAdmitsAny(root, user string) bool {
	rec, ok := crewOwnerRecordAt(root)
	if !ok {
		return false
	}
	for key := range rec.Bots {
		if crewBotAdmits(rec, key, user) {
			return true
		}
	}
	return false
}

// crewBotAgent is the bot's agent in its maker's chats, with its current name and purpose.
func crewBotAgent(ctx context.Context, rec projectOwnerRecord, key string) (crewOwnerChat, bool) {
	bot, ok := rec.Bots[key]
	if !ok {
		return crewOwnerChat{}, false
	}
	store := defaultProductConversationRegistryStore()
	records, err := store.projectChats(ctx, bot.Maker, rec.Product, rec.ProjectID)
	if err != nil {
		return crewOwnerChat{}, false
	}
	for _, record := range records {
		if record.ConversationKey == key {
			identities, _ := store.agentIdentities(ctx, bot.Maker, rec.Product, rec.ProjectID)
			return crewOwnerChat{Record: record, Identity: identities[key], OwnerID: bot.Maker}, true
		}
	}
	return crewOwnerChat{}, false
}

// crewBotSharedAgents are the bots of the project at root that admit caller, as shared agents (for the project's Ask
// chat and ask_crew with chat_id).
func crewBotSharedAgents(ctx context.Context, caller, root string) []sharedCrewAgent {
	rec, ok := crewOwnerRecordAt(root)
	if !ok {
		return nil
	}
	out := []sharedCrewAgent{}
	for _, key := range crewBotKeys(rec) {
		if !crewBotAdmits(rec, key, caller) {
			continue
		}
		tab, found := crewBotAgent(ctx, rec, key)
		if !found {
			continue
		}
		out = append(out, sharedCrewAgent{ChatID: key, Name: firstNonEmptyTrimmed(tab.Identity.Name, tab.Record.Title, "Agent"), Purpose: tab.Identity.Purpose, Examples: tab.Identity.Examples, Selected: true, OwnerName: crewOwnerDisplayName(tab.OwnerID), tab: tab})
	}
	return out
}

func crewBotKeys(rec projectOwnerRecord) []string {
	keys := make([]string, 0, len(rec.Bots))
	for key := range rec.Bots {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

type crewBotFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"input_schema,omitempty"`
}

type crewBotView struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Purpose      string            `json:"purpose,omitempty"`
	Icon         string            `json:"icon,omitempty"`
	ProjectID    string            `json:"project_id"`
	ProjectTitle string            `json:"project_title"`
	Workspace    string            `json:"workspace"`
	MakerName    string            `json:"maker_name"`
	Mine         bool              `json:"mine,omitempty"`
	Functions    []crewBotFunction `json:"functions"`
	// Audience is shown to the maker only.
	Everyone bool     `json:"everyone,omitempty"`
	People   []string `json:"people,omitempty"`
	Teams    []string `json:"teams,omitempty"`
}

func crewBotFunctionsFor(ctx context.Context, rec projectOwnerRecord, root string, bot crewBot) []crewBotFunction {
	out := []crewBotFunction{{Name: "ask", Description: "Ask it anything in your own chat."}}
	if len(bot.Functions) == 0 {
		return out
	}
	target := triggerTarget{Kind: triggerCallerCrew, Path: root, CrewID: rec.ProjectID, CrewProfile: rec.Product, CrewOwner: rec.OwnerID}
	functions, err := readCrewFunctions(ctx, target)
	if err != nil {
		return out
	}
	for _, fn := range offeredCrewFunctions(ctx, target, functions) {
		if fn.Name != "ask" && containsID(bot.Functions, fn.Name) {
			out = append(out, crewBotFunction{Name: fn.Name, Description: fn.Description, InputSchema: fn.InputSchema})
		}
	}
	return out
}

// crewBotsFor lists every bot on the server that admits caller, the caller's own first.
func crewBotsFor(ctx context.Context, caller string) []crewBotView {
	records, err := defaultProjectOwners().All()
	if err != nil {
		return nil
	}
	out := []crewBotView{}
	for _, rec := range records {
		if len(rec.Bots) == 0 || !isProjectProfileID(rec.Product) {
			continue
		}
		root := crewBotRoot(rec)
		_, title := crewOwnerAndTitle(rec.ProjectID)
		for _, key := range crewBotKeys(rec) {
			if !crewBotAdmits(rec, key, caller) {
				continue
			}
			tab, found := crewBotAgent(ctx, rec, key)
			if !found {
				continue
			}
			bot := rec.Bots[key]
			view := crewBotView{ID: key, Name: firstNonEmptyTrimmed(tab.Identity.Name, tab.Record.Title, "Agent"), Purpose: tab.Identity.Purpose, Icon: bot.Icon, ProjectID: rec.ProjectID, ProjectTitle: firstNonEmptyTrimmed(title, rec.Folder), Workspace: root, MakerName: crewOwnerDisplayName(bot.Maker), Mine: bot.Maker == caller, Functions: crewBotFunctionsFor(ctx, rec, root, bot)}
			if view.Mine {
				view.Everyone, view.People, view.Teams = bot.Everyone, bot.People, bot.Teams
			}
			out = append(out, view)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Mine != out[j].Mine {
			return out[i].Mine
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

// crewBotChange is the maker's edit of one agent's bot. Nil fields keep what is saved.
type crewBotChange struct {
	Workspace string    `json:"workspace"`
	Agent     string    `json:"agent"`
	On        *bool     `json:"on,omitempty"`
	Icon      *string   `json:"icon,omitempty"`
	Name      *string   `json:"name,omitempty"`
	Purpose   *string   `json:"purpose,omitempty"`
	Everyone  *bool     `json:"everyone,omitempty"`
	People    *[]string `json:"people,omitempty"`
	Teams     *[]string `json:"teams,omitempty"`
	Functions *[]string `json:"functions,omitempty"`
}

// crewBotEditable is the agent at key in the project at root when caller made it there (the main owner or a co-owner).
func crewBotEditable(ctx context.Context, caller, root, key string) (projectOwnerRecord, crewOwnerChat, error) {
	rec, ok := crewOwnerRecordAt(root)
	if !ok || !crewLivesOnServer(rec.ProjectID) {
		return rec, crewOwnerChat{}, fmt.Errorf("Bots are for projects on the server. A project on a Mac shares its agents through its Team.")
	}
	if caller != rec.OwnerID && !containsID(rec.CoOwners, caller) {
		return rec, crewOwnerChat{}, fmt.Errorf("Only the person who made this agent can make it a bot.")
	}
	short, side := strings.CutPrefix(key, rec.ProjectID+codeChatSideMarker)
	if !side || short == "" || strings.Contains(short, ":") || hiddenFromTabStrip(key) || crewFunctionTabKey(key) {
		return rec, crewOwnerChat{}, fmt.Errorf("Choose one of the project's own agents.")
	}
	store := defaultProductConversationRegistryStore()
	records, err := store.projectChats(ctx, caller, rec.Product, rec.ProjectID)
	if err != nil {
		return rec, crewOwnerChat{}, err
	}
	for _, record := range records {
		if record.ConversationKey == key {
			identities, _ := store.agentIdentities(ctx, caller, rec.Product, rec.ProjectID)
			return rec, crewOwnerChat{Record: record, Identity: identities[key], OwnerID: caller}, nil
		}
	}
	return rec, crewOwnerChat{}, fmt.Errorf("Only the person who made this agent can make it a bot.")
}

func crewBotTeamChoices(user string) []map[string]string {
	out := []map[string]string{}
	for _, team := range crewTeamsOn() {
		if team.OwnerID == user || containsID(crewTeamJoinedPeople(team), user) {
			out = append(out, map[string]string{"team_id": team.ProjectID, "name": team.Name})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return strings.ToLower(out[i]["name"]) < strings.ToLower(out[j]["name"]) })
	return out
}

// applyCrewBotChange validates and saves the maker's change, then returns the bot as its maker sees it.
func applyCrewBotChange(ctx context.Context, caller string, change crewBotChange) (map[string]any, error) {
	root, key := strings.TrimSpace(change.Workspace), strings.TrimSpace(change.Agent)
	rec, tab, err := crewBotEditable(ctx, caller, root, key)
	if err != nil {
		return nil, err
	}
	bot, existed := rec.Bots[key]
	if existed && bot.Maker != caller {
		return nil, fmt.Errorf("Only the person who made this agent can change its bot.")
	}
	if change.On != nil && !*change.On {
		if err := writeCrewBot(root, key, nil); err != nil {
			return nil, err
		}
		log.Printf("[CREW_BOT] actor=%s project=%s agent=%s action=off", caller, rec.ProjectID, key)
		return crewBotState(ctx, caller, root, key)
	}
	if !existed && (change.On == nil || !*change.On) {
		return nil, fmt.Errorf("Turn the bot on first (on=true).")
	}
	if !existed {
		bot = crewBot{Maker: caller, Since: time.Now().UTC().Format(time.RFC3339), Icon: "🤖"}
	}
	if change.Icon != nil {
		icon := strings.TrimSpace(*change.Icon)
		if len([]rune(icon)) > crewBotIconMaxRunes {
			return nil, fmt.Errorf("Use one emoji or a few letters for the icon.")
		}
		bot.Icon = icon
	}
	if change.Everyone != nil {
		bot.Everyone = *change.Everyone
	}
	if change.People != nil {
		people := []string{}
		for _, id := range *change.People {
			id = strings.TrimSpace(id)
			if id == "" || containsID(people, id) {
				continue
			}
			if !crewTeamAccountActive(id) {
				return nil, fmt.Errorf("Choose people from the company directory.")
			}
			people = append(people, id)
		}
		bot.People = people
	}
	if change.Teams != nil {
		choices := crewBotTeamChoices(caller)
		teams := []string{}
		for _, id := range *change.Teams {
			if !slices.ContainsFunc(choices, func(c map[string]string) bool { return c["team_id"] == id }) {
				return nil, fmt.Errorf("Choose a Team you are in.")
			}
			if !containsID(teams, id) {
				teams = append(teams, id)
			}
		}
		bot.Teams = teams
	}
	if change.Functions != nil {
		target := triggerTarget{Kind: triggerCallerCrew, Path: root, CrewID: rec.ProjectID, CrewProfile: rec.Product, CrewOwner: rec.OwnerID}
		functions, _ := readCrewFunctions(ctx, target)
		names := []string{}
		for _, name := range *change.Functions {
			if name == "ask" {
				continue
			}
			if _, found := findCrewFunction(offeredCrewFunctions(ctx, target, functions), name); !found {
				return nil, fmt.Errorf("The project has no function %q.", name)
			}
			if !containsID(names, name) {
				names = append(names, name)
			}
		}
		bot.Functions = names
	}
	if change.Name != nil || change.Purpose != nil {
		if change.Name != nil && strings.TrimSpace(*change.Name) == "" {
			return nil, fmt.Errorf("A bot needs a name.")
		}
		if change.Purpose != nil && len([]rune(strings.Join(strings.Fields(*change.Purpose), " "))) > productAgentPurposeMaxRunes {
			return nil, fmt.Errorf("Keep the purpose under %d characters.", productAgentPurposeMaxRunes)
		}
		if _, err := defaultProductConversationRegistryStore().setAgentIdentity(ctx, caller, tab.Record.ProfileID, key, change.Name, change.Purpose, caller); err != nil {
			return nil, err
		}
	}
	if err := writeCrewBot(root, key, &bot); err != nil {
		return nil, err
	}
	log.Printf("[CREW_BOT] actor=%s project=%s agent=%s action=save everyone=%t people=%d teams=%d functions=%d", caller, rec.ProjectID, key, bot.Everyone, len(bot.People), len(bot.Teams), len(bot.Functions))
	return crewBotState(ctx, caller, root, key)
}

func writeCrewBot(root, key string, bot *crewBot) error {
	identity, ok := projectIdentityOf(root)
	if !ok {
		return fmt.Errorf("Project unavailable")
	}
	return defaultProjectOwners().modify(func(projects map[string]projectOwnerRecord) (bool, error) {
		id := projectOwnerKey(identity.Product, identity.Folder)
		record, exists := projects[id]
		if !exists {
			return false, fmt.Errorf("Project unavailable")
		}
		bots := map[string]crewBot{}
		for k, v := range record.Bots {
			bots[k] = v
		}
		if bot == nil {
			if _, had := bots[key]; !had {
				return false, nil
			}
			delete(bots, key)
		} else {
			bots[key] = *bot
		}
		if len(bots) == 0 {
			bots = nil
		}
		record.Bots = bots
		projects[id] = record
		return true, nil
	})
}

// crewBotState is the maker's card for one agent: whether it is a bot, its settings and what can be chosen.
func crewBotState(ctx context.Context, caller, root, key string) (map[string]any, error) {
	rec, tab, err := crewBotEditable(ctx, caller, root, key)
	if err != nil {
		return nil, err
	}
	target := triggerTarget{Kind: triggerCallerCrew, Path: root, CrewID: rec.ProjectID, CrewProfile: rec.Product, CrewOwner: rec.OwnerID}
	functions, _ := readCrewFunctions(ctx, target)
	available := []crewBotFunction{}
	for _, fn := range offeredCrewFunctions(ctx, target, functions) {
		if fn.Name != "ask" {
			available = append(available, crewBotFunction{Name: fn.Name, Description: fn.Description})
		}
	}
	people := []map[string]string{}
	bot, on := rec.Bots[key]
	for _, id := range bot.People {
		people = append(people, map[string]string{"user_id": id, "name": crewOwnerDisplayName(id)})
	}
	out := map[string]any{
		"on": on, "agent": key, "name": firstNonEmptyTrimmed(tab.Identity.Name, tab.Record.Title, "Agent"), "purpose": tab.Identity.Purpose,
		"icon": bot.Icon, "everyone": bot.Everyone, "people": people, "teams": nonNilStrings(bot.Teams), "functions": nonNilStrings(bot.Functions),
		"available_functions": available, "team_choices": crewBotTeamChoices(caller),
	}
	return out, nil
}

// crewBotCall runs one of the bot's functions as the signed-in person, on the same typed-call path MCP and dashboards
// use (access, caller policy and call ownership are checked there).
func (api *StreamingAPI) crewBotCall(w http.ResponseWriter, r *http.Request, caller string, botID, function string, args map[string]any, submissionID string) {
	projectID, _, _ := strings.Cut(botID, codeChatSideMarker)
	root := crewProjectRootByID(projectID)
	rec, ok := crewOwnerRecordAt(root)
	if root == "" || !ok || !crewBotAdmits(rec, botID, caller) {
		externalError(w, 404, "not_found", "This bot is not shared with you.")
		return
	}
	if !containsID(rec.Bots[botID].Functions, function) {
		externalError(w, 404, "not_found", "This bot has no such button. Ask it in its chat instead.")
		return
	}
	if args == nil {
		args = map[string]any{}
	}
	req := httptest.NewRequest(http.MethodPost, "/api/external/call", nil)
	req = req.WithContext(r.Context())
	rec2 := httptest.NewRecorder()
	api.externalCrewCall(rec2, req, "project_functions_call", map[string]any{"crew_id": projectID, "function": function, "input": args, "submission_id": submissionID, "wait_seconds": float64(20)})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(rec2.Code)
	_, _ = w.Write(rec2.Body.Bytes())
}

// handleCrewBots serves the Bots page and the maker's bot card:
// GET  /api/bots                          bots shared with me and my own
// GET  /api/bots/agent?workspace=&agent=  the maker's card for one agent
// PUT  /api/bots/agent                    save the maker's change
// POST /api/bots/call                     run one of a bot's buttons
func (api *StreamingAPI) handleCrewBots(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if IsLocalMode() && !IsMultiUserMode() {
		externalError(w, 404, "server_only", "Bots are shared from projects on the server.")
		return
	}
	claims := GetUserFromContext(r.Context())
	if claims == nil || claims.UserID == "" || claims.Provider == "bot_route" || !userAllowedProduct(claims, crewProfileID) {
		externalError(w, 401, "authentication_required", "Sign in to use bots.")
		return
	}
	caller := sanitizeUserIDForPath(claims.UserID)
	path := strings.TrimSuffix(r.URL.Path, "/")
	switch {
	case r.Method == http.MethodGet && strings.HasSuffix(path, "/bots"):
		externalJSON(w, map[string]any{"bots": crewBotsFor(r.Context(), caller)})
	case r.Method == http.MethodGet && strings.HasSuffix(path, "/bots/agent"):
		state, err := crewBotState(r.Context(), caller, r.URL.Query().Get("workspace"), r.URL.Query().Get("agent"))
		if err != nil {
			externalError(w, 403, "not_allowed", err.Error())
			return
		}
		externalJSON(w, state)
	case r.Method == http.MethodPut && strings.HasSuffix(path, "/bots/agent"):
		var change crewBotChange
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&change) != nil || decoder.Decode(new(any)) != io.EOF {
			externalError(w, 400, "invalid_arguments", "invalid bot change")
			return
		}
		if !userAccessForClaims(claims).CanEdit {
			externalError(w, 403, "not_allowed", "Your account cannot change projects.")
			return
		}
		state, err := applyCrewBotChange(r.Context(), caller, change)
		if err != nil {
			externalError(w, 400, "bot_refused", err.Error())
			return
		}
		externalJSON(w, state)
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/bots/call"):
		var body struct {
			Bot          string         `json:"bot"`
			Function     string         `json:"function"`
			Args         map[string]any `json:"args"`
			SubmissionID string         `json:"submission_id"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&body) != nil || decoder.Decode(new(any)) != io.EOF {
			externalError(w, 400, "invalid_arguments", "invalid bot call")
			return
		}
		api.crewBotCall(w, r, caller, body.Bot, body.Function, body.Args, body.SubmissionID)
	default:
		externalError(w, 404, "not_found", "Unknown bots request.")
	}
}

const externalBotsTool = "bots"

func externalBotsDefinition(add func(string, string, bool, bool, map[string]any, ...string)) {
	strList := map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
	add(externalBotsTool, "Bots: agents of projects on the server that their makers share for others to use without joining the project. list: the bots shared with you and your own (each with its id, icon, name, purpose, project and buttons; ask is always one). ask (bot, message, optional inbox_id): ask a bot; read its answer with messages action=read and the inbox_id. call (bot, function, input): press one of its buttons, a project function; read the result with functions action=status. Makers: get (crew_id, agent) shows whether one of your agents in that project is a bot and what can be chosen; set (crew_id, agent, on, and any of icon, name, purpose, everyone, people, teams, functions) makes it a bot, changes it or stops it (on=false). Only the person who made the agent (the project's owner or a co-owner) sets it. everyone=true shares it with anyone at the company; otherwise people (user ids) and teams (team_id from get's team_choices). functions are the project's functions shown as its buttons. A bot is not co-owners or Users (people who work in the project: crew_owners) and not the dashboard (shared by its link).", true, false, map[string]any{
		"action":        map[string]any{"type": "string", "enum": []any{"list", "ask", "call", "get", "set"}},
		"bot":           externalString("ask, call: the bot's id from list."),
		"message":       externalString("ask: your message."),
		"inbox_id":      externalString("ask: the inbox from an earlier ask, to continue it."),
		"function":      externalString("call: the button's name from list."),
		"input":         map[string]any{"type": "object", "description": "call: the function's input."},
		"submission_id": externalString("ask, call: optional retry key."),
		"crew_id":       externalString("get, set: the project's id."),
		"agent":         externalString("get, set: your agent in that project, by name or chat id."),
		"on":            map[string]any{"type": "boolean", "description": "set: true makes it a bot, false stops it."},
		"icon":          externalString("set: one emoji."),
		"name":          externalString("set: the bot's name (the agent's name)."),
		"purpose":       externalString("set: one plain sentence on what it does."),
		"everyone":      map[string]any{"type": "boolean", "description": "set: share with anyone at the company."},
		"people":        strList,
		"teams":         strList,
		"functions":     strList,
	}, "action")
}

// crewBotAgentKey finds the caller's agent in a project by chat id or name.
func crewBotAgentKey(ctx context.Context, caller, root, agent string) string {
	agent = strings.TrimSpace(agent)
	rec, ok := crewOwnerRecordAt(root)
	if !ok || agent == "" || strings.Contains(agent, codeChatSideMarker) {
		return agent
	}
	store := defaultProductConversationRegistryStore()
	records, _ := store.projectChats(ctx, caller, rec.Product, rec.ProjectID)
	identities, _ := store.agentIdentities(ctx, caller, rec.Product, rec.ProjectID)
	for _, record := range records {
		name := firstNonEmptyTrimmed(identities[record.ConversationKey].Name, record.Title)
		if strings.EqualFold(name, agent) || strings.TrimPrefix(record.ConversationKey, rec.ProjectID+codeChatSideMarker) == agent {
			return record.ConversationKey
		}
	}
	return agent
}

func (api *StreamingAPI) externalBotsCall(w http.ResponseWriter, r *http.Request, args map[string]any) {
	claims := GetUserFromContext(r.Context())
	if claims == nil || claims.UserID == "" || !userAllowedProduct(claims, crewProfileID) {
		externalError(w, 403, "not_allowed", "Bots need the projects product on this account.")
		return
	}
	if IsLocalMode() && !IsMultiUserMode() {
		externalError(w, 404, "server_only", "Bots are shared from projects on the server.")
		return
	}
	caller := sanitizeUserIDForPath(claims.UserID)
	str := func(key string) string {
		value, _ := args[key].(string)
		return strings.TrimSpace(value)
	}
	token := claims.AccessToken
	switch str("action") {
	case "list":
		externalJSON(w, map[string]any{"bots": crewBotsFor(r.Context(), caller)})
	case "ask":
		if token != nil && !token.Allows("crews:run") {
			externalError(w, 403, "insufficient_scope", "Asking a bot needs a connection that may run projects.")
			return
		}
		bot := str("bot")
		projectID, _, _ := strings.Cut(bot, codeChatSideMarker)
		rec, ok := crewOwnerRecordAt(crewProjectRootByID(projectID))
		if !ok || !crewBotAdmits(rec, bot, caller) {
			externalError(w, 404, "not_found", "This bot is not shared with you. See bots action=list.")
			return
		}
		call := map[string]any{"crew_id": projectID, "chat_id": bot, "message": str("message")}
		for _, key := range []string{"inbox_id", "submission_id"} {
			if v := str(key); v != "" {
				call[key] = v
			}
		}
		api.externalCrewCall(w, r, "ask_crew", call)
	case "call":
		if token != nil && !token.Allows("crews:run") {
			externalError(w, 403, "insufficient_scope", "Pressing a bot's button needs a connection that may run projects.")
			return
		}
		input, _ := args["input"].(map[string]any)
		api.crewBotCall(w, r, caller, str("bot"), str("function"), input, str("submission_id"))
	case "get", "set":
		root := crewProjectRootByID(str("crew_id"))
		if root == "" {
			externalError(w, 404, "not_found", "Project not found. Use its id from workspace action=list.")
			return
		}
		key := crewBotAgentKey(r.Context(), caller, root, str("agent"))
		if str("action") == "get" {
			state, err := crewBotState(r.Context(), caller, root, key)
			if err != nil {
				externalError(w, 403, "not_allowed", err.Error())
				return
			}
			externalJSON(w, state)
			return
		}
		if (token != nil && !token.Allows("crews:write")) || !userAccessForClaims(claims).CanEdit {
			externalError(w, 403, "insufficient_scope", "Changing a bot needs a connection that may change projects.")
			return
		}
		change := crewBotChange{Workspace: root, Agent: key}
		if v, ok := args["on"].(bool); ok {
			change.On = &v
		}
		if v, ok := args["everyone"].(bool); ok {
			change.Everyone = &v
		}
		for field, dst := range map[string]**string{"icon": &change.Icon, "name": &change.Name, "purpose": &change.Purpose} {
			if v, ok := args[field].(string); ok {
				*dst = &v
			}
		}
		for field, dst := range map[string]**[]string{"people": &change.People, "teams": &change.Teams, "functions": &change.Functions} {
			if raw, ok := args[field].([]any); ok {
				list := []string{}
				for _, item := range raw {
					if s, ok := item.(string); ok {
						list = append(list, s)
					}
				}
				*dst = &list
			}
		}
		state, err := applyCrewBotChange(r.Context(), caller, change)
		if err != nil {
			externalError(w, 400, "bot_refused", err.Error())
			return
		}
		externalJSON(w, state)
	default:
		externalError(w, 400, "invalid_arguments", "action must be list, ask, call, get or set.")
	}
}

// registerCrewBotsChatTool gives a server project's own chat the bots tool (same code as MCP), so a maker can make one
// of their agents here a bot from chat, and anyone can list and use the bots shared with them.
func (api *StreamingAPI) registerCrewBotsChatTool(reg definitionToolRegistrar, gate *productToolGate, claims *UserClaims, readOnly bool, root string) error {
	if claims == nil || claims.UserID == "" || readOnly {
		return nil
	}
	rec, ok := crewOwnerRecordAt(root)
	if !ok || !crewLivesOnServer(rec.ProjectID) {
		return nil
	}
	projectID := rec.ProjectID
	var description string
	var props map[string]any
	externalBotsDefinition(func(_ string, d string, _ bool, _ bool, p map[string]any, _ ...string) {
		description, props = d, p
	})
	gate.Declare(externalBotsTool)
	return reg.RegisterCustomToolWithTimeout(externalBotsTool, description+" In this chat, get and set default to this project.", map[string]any{"type": "object", "properties": props, "required": []string{"action"}}, func(ctx context.Context, args map[string]any) (string, error) {
		call := map[string]any{}
		for k, v := range args {
			call[k] = v
		}
		if _, set := call["crew_id"]; !set {
			call["crew_id"] = projectID
		}
		r := httptest.NewRequest(http.MethodPost, "/", nil).WithContext(context.WithValue(ctx, UserContextKey, claims))
		w := httptest.NewRecorder()
		api.externalBotsCall(w, r, call)
		if w.Code >= 300 {
			return "", fmt.Errorf("%s", laptopActionErrorText(w.Body.String()))
		}
		return w.Body.String(), nil
	}, time.Minute, "workspace_advanced")
}
