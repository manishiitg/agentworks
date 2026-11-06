package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// One People list for a Crew (owner and reviewer, 6 Nov): a person is added once and given one of two levels. "Works
// with me" is the Crew's Team (a Team member; invited until they accept). "Dashboards only" is the Crew's Share to
// chosen people (the owner's Run people: all this Crew's dashboards, can ask this Crew, no files). It is a view and one
// change route over the two records that already exist, so no access rule is new: the Team record and the Share record
// are saved by the same code as before (handleCrewTeam, handleDashboardShare). The server writes every line; the card
// only shows them.
const (
	crewPeopleWorks      = "works"
	crewPeopleDashboards = "dashboards"
	crewPeopleNone       = "none"

	crewKindServer    = "server"
	crewKindMacOnline = "mac_online"
	crewKindMacNever  = "mac_never_online"

	crewPeopleNeverOnlineLine = "Turn on Mac online to share this Crew."
	crewPeoplePutOnlineLine   = "To give someone dashboards only, with Ask and buttons, put this Crew online first."
)

type crewPeopleLevel struct {
	Key       string `json:"key"`
	Label     string `json:"label"`
	Line      string `json:"line"`
	Available bool   `json:"available"`
	// Why is the plain reason a level cannot be chosen on this Crew.
	Why string `json:"why,omitempty"`
}

type crewPeoplePerson struct {
	UserID     string `json:"user_id"`
	Name       string `json:"name"`
	Email      string `json:"email,omitempty"`
	Level      string `json:"level"`
	LevelLabel string `json:"level_label"`
	Line       string `json:"line"`
	Invited    bool   `json:"invited,omitempty"`
	Owner      bool   `json:"owner,omitempty"`
	You        bool   `json:"you,omitempty"`
	// Moves are the levels (and "none" to remove) this viewer may change the person to.
	Moves []string `json:"moves,omitempty"`
}

type crewPeopleGets struct {
	Label      string `json:"label"`
	Dashboards string `json:"dashboards_only"`
	Works      string `json:"works_with_me"`
}

type crewPeopleView struct {
	Kind   string `json:"kind"`
	TeamOn bool   `json:"team_on"`
	Intro  string `json:"intro"`
	// Blocked is the one line of a Crew that was never online: the card greys the list.
	Blocked string `json:"blocked,omitempty"`
	// PutOnline is shown beside "Dashboards only" on a Crew that lives on a Mac.
	PutOnline string `json:"put_online,omitempty"`
	// Everyone is set when the Crew is open to every account on the server: nobody needs dashboards only.
	Everyone string `json:"everyone,omitempty"`
	// CanAdd: this viewer may add people to Works with me; CanShare: the owner alone may give dashboards only.
	CanAdd   bool               `json:"can_add"`
	CanShare bool               `json:"can_share"`
	Owner    crewPeoplePerson   `json:"owner"`
	People   []crewPeoplePerson `json:"people"`
	Levels   []crewPeopleLevel  `json:"levels"`
	Gets     []crewPeopleGets   `json:"gets"`
}

// crewPeopleKind says what kind of Crew this is, as the People list needs it: a server Crew (both levels), a Mac Crew
// that is online (its Team only: dashboards only needs the Crew online) or a Mac Crew that was never online.
func crewPeopleKind(project string) string {
	if crewRelayTwin(project) {
		return crewKindMacOnline
	}
	if IsLocalMode() {
		if b, _ := crewroot.ReadSync(project); b != nil && b.Side == "local" && b.PeerProjectID != "" {
			return crewKindMacOnline
		}
		return crewKindMacNever
	}
	return crewKindServer
}

func crewPeopleLevels(kind string) []crewPeopleLevel {
	levels := []crewPeopleLevel{
		{Key: crewPeopleWorks, Label: "Works with me", Line: "Their own copy of the shared files, their agents, your shared agents and the dashboards. Edits sync both ways.", Available: kind != crewKindMacNever},
		{Key: crewPeopleDashboards, Label: "Dashboards only", Line: "Opens all this Crew's dashboards and can ask this Crew. Sees no files or agents' chats.", Available: kind == crewKindServer},
	}
	switch kind {
	case crewKindMacNever:
		levels[0].Why, levels[1].Why = crewPeopleNeverOnlineLine, crewPeopleNeverOnlineLine
	case crewKindMacOnline:
		levels[1].Why = crewPeoplePutOnlineLine
	}
	return levels
}

// crewPeopleGetsTable is what each level gets, in the server's words.
func crewPeopleGetsTable() []crewPeopleGets {
	return []crewPeopleGets{
		{"Dashboards", "All this Crew's dashboards", "All of them"},
		{"Ask this Crew", "Yes", "Yes, plus their own agents"},
		{"Shared files", "No", "Yes, edit both ways"},
		{"Agents' chats", "No", "Yes, unless kept private"},
		{"Change the Crew", "No", "Add folders and agents"},
		{"Team on/off, remove folders", "No", "No: owner only"},
	}
}

// crewPeopleGetsLines is what a level gives a person, for the confirm box.
func crewPeopleGetsLines(level string) []string {
	switch level {
	case crewPeopleWorks:
		return []string{"The shared files, with their own copy that syncs both ways", "Their own agents here, and your shared agents", "All this Crew's dashboards", "Asking this Crew"}
	case crewPeopleDashboards:
		return []string{"All this Crew's dashboards", "Asking this Crew"}
	}
	return nil
}

// crewPeopleShareRecord reads the Crew's Share record: open to everyone, and the people it names.
func crewPeopleShareRecord(root string) (everyone bool, named []string) {
	if crewSharedWithOthers(root) {
		return true, nil
	}
	if id, ok := projectIdentityOf(root); ok && isProjectProfileID(id.Product) {
		if rec, found := defaultProjectOwners().Lookup(id.Product, id.Folder); found {
			named = slices.Clone(rec.RunPeople)
		}
	}
	return false, named
}

// crewPeopleLevelOf is a person's level on the two records: the Team wins, since it includes the dashboards.
func crewPeopleLevelOf(t crewTeam, named []string, user string) string {
	if slices.ContainsFunc(t.People, func(p crewTeamPerson) bool { return p.UserID == user }) {
		return crewPeopleWorks
	}
	if slices.Contains(named, user) {
		return crewPeopleDashboards
	}
	return crewPeopleNone
}

func crewPeopleLevelLabel(level string) string {
	switch level {
	case crewPeopleWorks:
		return "Works with me"
	case crewPeopleDashboards:
		return "Dashboards only"
	}
	return "Not on this Crew"
}

func crewPeopleLine(level string, invited, teamOn bool) string {
	switch {
	case level == crewPeopleDashboards:
		return "All this Crew's dashboards. Can ask this Crew. No files."
	case level == crewPeopleWorks && invited:
		return "Will get files, agents and dashboards when they accept."
	case level == crewPeopleWorks && !teamOn:
		return "Gets files, agents and dashboards when you turn the Team on."
	case level == crewPeopleWorks:
		return "Files, agents and dashboards. Has their own copy."
	}
	return ""
}

// crewPeopleMoves are the changes a viewer may make to a person: any builder adds or removes Works with me (as the Team
// does today); dashboards only, and moving between the levels, is the owner's alone (the Share is the owner's act).
func crewPeopleMoves(kind, level string, owner bool) []string {
	if kind == crewKindMacNever {
		return nil
	}
	var moves []string
	switch level {
	case crewPeopleWorks:
		if owner && kind == crewKindServer {
			moves = append(moves, crewPeopleDashboards)
		}
		moves = append(moves, crewPeopleNone)
	case crewPeopleDashboards:
		if owner {
			moves = append(moves, crewPeopleWorks, crewPeopleNone)
		}
	}
	return moves
}

func (api *StreamingAPI) crewPeopleViewFor(claims *UserClaims, t crewTeam) crewPeopleView {
	project := t.ProjectID
	kind := crewPeopleKind(project)
	owner := claims.UserID == t.OwnerID
	root := crewProjectRootByID(project)
	everyone, named := crewPeopleShareRecord(root)
	directory, _ := loadUserDirectory()
	email := func(id string) string {
		if directory != nil {
			if record := directory.byID(id); record != nil {
				return record.Email
			}
		}
		return ""
	}
	view := crewPeopleView{Kind: kind, TeamOn: t.On, Intro: "Add someone once. Choose how they use this Crew.", Levels: crewPeopleLevels(kind), Gets: crewPeopleGetsTable(), CanAdd: kind != crewKindMacNever && crewTeamCanManage(t, claims.UserID), CanShare: owner && kind == crewKindServer, People: []crewPeoplePerson{}}
	switch kind {
	case crewKindMacNever:
		view.Blocked = crewPeopleNeverOnlineLine
	case crewKindMacOnline:
		view.PutOnline = crewPeoplePutOnlineLine
	}
	if everyone && kind == crewKindServer {
		view.Everyone = "This Crew is open to everyone on this server: they can open its dashboards and ask it."
	}
	view.Owner = crewPeoplePerson{UserID: t.OwnerID, Name: crewPersonShortName(t.OwnerID), Email: email(t.OwnerID), Level: "owner", LevelLabel: "Owner", Line: "Owner", Owner: true, You: owner}
	add := func(id string, invited bool, level string) {
		view.People = append(view.People, crewPeoplePerson{UserID: id, Name: crewPersonShortName(id), Email: email(id), Level: level, LevelLabel: crewPeopleLevelLabel(level),
			Line: crewPeopleLine(level, invited, t.On), Invited: invited, You: id == claims.UserID, Moves: crewPeopleMoves(kind, level, owner)})
	}
	for _, p := range t.People {
		if crewTeamAccountActive(p.UserID) {
			add(p.UserID, p.Invited, crewPeopleWorks)
		}
	}
	for _, id := range named {
		if crewTeamAccountActive(id) && !slices.ContainsFunc(t.People, func(p crewTeamPerson) bool { return p.UserID == id }) {
			add(id, false, crewPeopleDashboards)
		}
	}
	return view
}

// handleCrewPeopleView is GET /crew-teams/{project}/people: the one People list, for the owner and the Team.
func (api *StreamingAPI) handleCrewPeopleView(w http.ResponseWriter, r *http.Request) {
	t, claims, ok := crewTeamForTab(w, r, false)
	if !ok {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	externalJSON(w, api.crewPeopleViewFor(claims, t))
}

type crewPeopleChange struct {
	Person          string `json:"person"`
	Level           string `json:"level"`
	Confirm         bool   `json:"confirm"`
	CreateDashboard bool   `json:"create_dashboard"`
}

// crewPeoplePreview is what the confirm box says: who changes, and what they gain or lose.
func crewPeoplePreview(name, from, to string, teamOn, everyone bool) map[string]any {
	gains, loses := []string{}, []string{}
	for _, line := range crewPeopleGetsLines(to) {
		if !slices.Contains(crewPeopleGetsLines(from), line) {
			gains = append(gains, line)
		}
	}
	for _, line := range crewPeopleGetsLines(from) {
		if !slices.Contains(crewPeopleGetsLines(to), line) {
			loses = append(loses, line)
		}
	}
	notes := []string{}
	if everyone && from != crewPeopleNone {
		// Open to everyone: leaving a level does not take the dashboards or Ask away.
		loses = slices.DeleteFunc(loses, func(line string) bool {
			return line == "All this Crew's dashboards" || line == "Asking this Crew"
		})
		notes = append(notes, "This Crew is open to everyone on this server, so they can still open its dashboards and ask it.")
	}
	if to == crewPeopleWorks && from == crewPeopleNone {
		notes = append(notes, "They are invited, and join when they accept.")
	}
	if to == crewPeopleWorks && !teamOn {
		notes = append(notes, "The Team is off, so they get this when you turn it on.")
	}
	summary := fmt.Sprintf("%s will be %s.", name, map[string]string{crewPeopleWorks: "added to Works with me", crewPeopleDashboards: "given dashboards only", crewPeopleNone: "removed from this Crew"}[to])
	if from != crewPeopleNone && to != crewPeopleNone {
		summary = fmt.Sprintf("%s will move from %s to %s.", name, strings.ToLower(crewPeopleLevelLabel(from)), strings.ToLower(crewPeopleLevelLabel(to)))
	}
	return map[string]any{"person": name, "from": from, "to": to, "summary": summary, "gains": gains, "loses": loses, "notes": notes}
}

// changeCrewPerson adds, moves or removes one person. Nothing is saved until the person confirms what it gives and takes.
func (api *StreamingAPI) changeCrewPerson(w http.ResponseWriter, r *http.Request, claims *UserClaims, project string, in crewPeopleChange) {
	person, level := strings.TrimSpace(in.Person), strings.TrimSpace(in.Level)
	if person == "" || !slices.Contains([]string{crewPeopleWorks, crewPeopleDashboards, crewPeopleNone}, level) {
		externalError(w, 400, "invalid_person", "Choose one person and a level: works, dashboards or none.")
		return
	}
	t, err := readCrewTeam(project)
	if err != nil || !crewTeamCanManage(t, claims.UserID) {
		externalError(w, 403, "team_owner_required", "Join this Crew's Team to change its people.")
		return
	}
	kind := crewPeopleKind(project)
	switch {
	case kind == crewKindMacNever:
		externalError(w, 409, "mac_never_online", crewPeopleNeverOnlineLine)
		return
	case kind == crewKindMacOnline && level == crewPeopleDashboards:
		externalError(w, 409, "crew_online_required", crewPeoplePutOnlineLine)
		return
	case person == t.OwnerID:
		externalError(w, 400, "invalid_person", "The owner is already here.")
		return
	case person == claims.UserID && level == crewPeopleNone:
		externalError(w, 400, "invalid_person", "Use Leave Team to leave it yourself.")
		return
	case level != crewPeopleNone && !crewTeamAccountActive(person):
		externalError(w, 400, "invalid_person", "Choose active people from your company.")
		return
	}
	root := crewProjectRootByID(project)
	everyone, named := crewPeopleShareRecord(root)
	from := crewPeopleLevelOf(t, named, person)
	name := crewPersonShortName(person)
	if from == level || everyone && level == crewPeopleDashboards && from == crewPeopleNone {
		externalJSON(w, map[string]any{"changed": false, "note": map[bool]string{true: "This Crew is open to everyone on this server, so they can already open its dashboards.", false: name + " is already there."}[everyone && level == crewPeopleDashboards], "view": api.crewPeopleViewFor(claims, t)})
		return
	}
	// Dashboards only is the Share, the owner's act; so is moving someone between the levels (the same check as before).
	if (from == crewPeopleDashboards || level == crewPeopleDashboards) && claims.UserID != t.OwnerID {
		externalError(w, 403, "team_owner_only", fmt.Sprintf("Only %s can give people dashboards only.", crewPersonShortName(t.OwnerID)))
		return
	}
	if !in.Confirm {
		externalJSON(w, map[string]any{"confirm_needed": true, "preview": crewPeoplePreview(name, from, level, t.On, everyone), "next": "Nothing changed yet. Show the person this summary in plain words and ask. Change it only when they agree: call again with the same arguments and confirm=true."})
		return
	}
	run := func(w http.ResponseWriter, h func(http.ResponseWriter, *http.Request)) bool {
		rec := httptest.NewRecorder()
		h(rec, r)
		if rec.Code == http.StatusConflict && strings.Contains(rec.Body.String(), "dashboard") && !strings.Contains(rec.Body.String(), "Mac") {
			// The Share save's own words are for a tool; say it plainly with the next step.
			externalError(w, http.StatusConflict, "dashboard_needed", "This Crew has no dashboard yet, so there is nothing to show this person. Say yes and I'll create a standard dashboard and add them, or publish your own dashboard first and then add them.")
			return false
		}
		if rec.Code >= 300 {
			w.Header().Set("Content-Type", rec.Header().Get("Content-Type"))
			w.WriteHeader(rec.Code)
			_, _ = w.Write(rec.Body.Bytes())
			return false
		}
		return true
	}
	// The Team half: the Team save on the current record with this one change, as the Team tab's people route does.
	team := func(w http.ResponseWriter, add bool) bool {
		current, err := readCrewTeam(project)
		if err != nil {
			externalError(w, 403, "team_owner_required", "Join this Crew's Team to change its people.")
			return false
		}
		if add && !slices.ContainsFunc(current.People, func(p crewTeamPerson) bool { return p.UserID == person }) {
			current.People = append(current.People, crewTeamPerson{UserID: person})
		}
		if !add {
			current.People = slices.DeleteFunc(slices.Clone(current.People), func(p crewTeamPerson) bool { return p.UserID == person })
		}
		current.People = slices.DeleteFunc(current.People, func(p crewTeamPerson) bool { return !crewTeamAccountActive(p.UserID) && p.UserID != person })
		return run(w, func(rec http.ResponseWriter, req *http.Request) { api.putCrewTeam(rec, req, project, current) })
	}
	// The Share half: the Share save with the named people this change leaves (the same save as the Share screen).
	share := func(w http.ResponseWriter, ids []string) bool {
		if everyone {
			return true // open to everyone: there is no list to keep
		}
		directory, _ := loadUserDirectory()
		emails := []string{}
		for _, id := range ids {
			var record *UserRecord
			if directory != nil {
				record = directory.byID(id)
			}
			if record != nil && strings.TrimSpace(record.Email) != "" {
				emails = append(emails, record.Email)
			} else {
				externalError(w, 400, "invalid_person", "This person has no email on file, so they can't be given dashboards only. Ask an admin to add their email, or choose Works with me for them.")
				return false
			}
		}
		target, err := api.dashboardTarget(r.Context(), claims, root)
		if err != nil {
			dashboardError(w, err)
			return false
		}
		mode := runSharePeople
		if len(emails) == 0 {
			mode = runSharePrivate
		}
		return run(w, func(rec http.ResponseWriter, req *http.Request) {
			api.handleDashboardShare(rec, req, claims, target, "share", mode, emails, in.CreateDashboard)
		})
	}
	without := slices.DeleteFunc(slices.Clone(named), func(id string) bool { return id == person })
	with := append(slices.Clone(without), person)
	ok := true
	switch {
	case from == crewPeopleNone && level == crewPeopleWorks:
		ok = team(w, true)
	case from == crewPeopleNone && level == crewPeopleDashboards:
		ok = share(w, with)
	case from == crewPeopleWorks && level == crewPeopleDashboards:
		ok = share(w, with) && team(w, false)
	case from == crewPeopleDashboards && level == crewPeopleWorks:
		ok = team(w, true) && share(w, without)
	case from == crewPeopleWorks && level == crewPeopleNone:
		ok = team(w, false)
	case from == crewPeopleDashboards && level == crewPeopleNone:
		ok = share(w, without)
	}
	if !ok {
		return
	}
	fresh, err := readCrewTeam(project)
	if err != nil {
		fresh = t
	}
	externalJSON(w, map[string]any{"changed": true, "view": api.crewPeopleViewFor(claims, fresh)})
}

// putCrewTeam saves a Team record through the Team's own PUT (handleCrewTeam), so the invite, removal and access rules
// stay in one place.
func (api *StreamingAPI) putCrewTeam(w http.ResponseWriter, r *http.Request, project string, t crewTeam) {
	body, _ := json.Marshal(t)
	save := r.Clone(r.Context())
	save.Method = http.MethodPut
	save.Body = io.NopCloser(bytes.NewReader(body))
	save.ContentLength = int64(len(body))
	api.handleCrewTeam(w, mux.SetURLVars(save, map[string]string{"project": project}))
}
