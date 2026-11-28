package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
	"time"
)

// Bots in a project on a Mac (owner, 28 Nov: "yes it should add it"). Bots live on servers; this Mac reaches every
// server it is signed in to through that saved sign-in, as the person, so the server still decides who may use which
// bot on every call. A Mac project keeps the bots added to it in its own registry record (AddedBots, with BotServers the
// sign-in each one comes from); its agents ask them with the same bots tool, which this Mac passes on to that server.

const macBotsSignIn = "Sign in to your company's server in the AgentWorks app first; bots come from there."

// macSignIns are this Mac's saved sign-ins to servers.
func macSignIns(owner string) ([]desktopConnection, error) {
	saved, err := desktopConnectionsOf(owner)
	if err != nil || len(saved) == 0 {
		return nil, fmt.Errorf(macBotsSignIn)
	}
	return saved, nil
}

// macServerJSON is one request to a server as the person, through the saved sign-in.
func macServerJSON(ctx context.Context, owner string, c desktopConnection, method, route string, input, out any) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	// A refusal comes back as the server's own plain message.
	return crewTeamRemoteJSON(ctx, owner, c, method, route, input, out)
}

// macServerBots are the bots a server shares with the person.
func macServerBots(ctx context.Context, owner string, c desktopConnection) ([]crewBotView, error) {
	var out struct {
		Bots []crewBotView `json:"bots"`
	}
	if err := macServerJSON(ctx, owner, c, http.MethodGet, "/api/bots", nil, &out); err != nil {
		return nil, err
	}
	return out.Bots, nil
}

// macBot is a bot as a Mac lists it: with the server it is on.
type macBot struct {
	crewBotView
	Server     string `json:"server"`
	ServerName string `json:"server_name"`
}

// macAllBots are the bots shared with the person on every server this Mac is signed in to. A server that cannot be
// reached is left out; the error is returned only when none answered.
func macAllBots(ctx context.Context, owner string) ([]macBot, error) {
	conns, err := macSignIns(owner)
	if err != nil {
		return nil, err
	}
	out, answered, lastErr := []macBot{}, 0, error(nil)
	for _, c := range conns {
		bots, err := macServerBots(ctx, owner, c)
		if err != nil {
			lastErr = err
			continue
		}
		answered++
		for _, bot := range bots {
			out = append(out, macBot{crewBotView: bot, Server: c.ID, ServerName: c.Name})
		}
	}
	if answered == 0 {
		return nil, lastErr
	}
	return out, nil
}

// macBotServer is the sign-in a bot comes from: the one the project recorded for it, else the server that shares it.
func macBotServer(ctx context.Context, owner string, rec projectOwnerRecord, bot string) (desktopConnection, error) {
	conns, err := macSignIns(owner)
	if err != nil {
		return desktopConnection{}, err
	}
	if id := rec.BotServers[bot]; id != "" {
		if i := slices.IndexFunc(conns, func(c desktopConnection) bool { return c.ID == id }); i >= 0 {
			return conns[i], nil
		}
		return desktopConnection{}, fmt.Errorf("This bot comes from a server this Mac is no longer signed in to. Sign in to it again in the AgentWorks app.")
	}
	for _, c := range conns {
		bots, err := macServerBots(ctx, owner, c)
		if err == nil && slices.ContainsFunc(bots, func(view crewBotView) bool { return view.ID == bot }) {
			return c, nil
		}
	}
	return desktopConnection{}, fmt.Errorf("This bot is not shared with you on any server this Mac is signed in to. See bots action=list.")
}

// macProjectRecord is the registry record of a project on this Mac.
func macProjectRecord(projectID string) (string, projectOwnerRecord, error) {
	root := crewProjectRootByID(strings.TrimSpace(projectID))
	rec, ok := crewOwnerRecordAt(root)
	if root == "" || !ok {
		return "", projectOwnerRecord{}, fmt.Errorf("Project not found.")
	}
	return root, rec, nil
}

// macProjectBots are the bots added to a project on this Mac, as their servers share them with the person now;
// unavailable are those no longer shared, or on a server this Mac can no longer reach.
func macProjectBots(ctx context.Context, owner, projectID string) ([]crewBotView, []string, error) {
	_, rec, err := macProjectRecord(projectID)
	if err != nil {
		return nil, nil, err
	}
	out, unavailable := []crewBotView{}, []string{}
	if len(rec.AddedBots) == 0 {
		return out, unavailable, nil
	}
	conns, err := macSignIns(owner)
	if err != nil {
		return nil, nil, err
	}
	shared := map[string][]crewBotView{} // by sign-in, each server read once
	for _, id := range rec.AddedBots {
		server := rec.BotServers[id]
		bots, read := shared[server]
		if !read {
			if i := slices.IndexFunc(conns, func(c desktopConnection) bool { return c.ID == server }); i >= 0 {
				bots, _ = macServerBots(ctx, owner, conns[i])
			}
			shared[server] = bots
		}
		i := slices.IndexFunc(bots, func(bot crewBotView) bool { return bot.ID == id })
		if i < 0 {
			unavailable = append(unavailable, id)
			continue
		}
		view := bots[i]
		view.InProjects = nil
		out = append(out, view)
	}
	return out, unavailable, nil
}

// setMacProjectBot adds a bot a server shares with the person to a project on this Mac, or takes it out. server picks
// the sign-in (its id or name); without it, the server that shares the bot.
func setMacProjectBot(ctx context.Context, owner, projectID, bot, server string, add bool) error {
	root, _, err := macProjectRecord(projectID)
	if err != nil {
		return err
	}
	from := ""
	if add {
		var c desktopConnection
		if strings.TrimSpace(server) == "" {
			if c, err = macBotServer(ctx, owner, projectOwnerRecord{}, bot); err != nil {
				return err
			}
		} else {
			conns, err := macSignIns(owner)
			if err != nil {
				return err
			}
			if c, err = pickServerConnection(conns, server); err != nil {
				return err
			}
			shared, err := macServerBots(ctx, owner, c)
			if err != nil {
				return err
			}
			if !slices.ContainsFunc(shared, func(view crewBotView) bool { return view.ID == bot }) {
				return fmt.Errorf("This bot is not shared with you on %s.", c.Name)
			}
		}
		from = c.ID
	}
	identity, ok := projectIdentityOf(root)
	if !ok {
		return fmt.Errorf("Project unavailable")
	}
	err = defaultProjectOwners().modify(func(projects map[string]projectOwnerRecord) (bool, error) {
		id := projectOwnerKey(identity.Product, identity.Folder)
		record, exists := projects[id]
		if !exists {
			return false, fmt.Errorf("Project unavailable")
		}
		if containsID(record.AddedBots, bot) == add && (!add || record.BotServers[bot] == from) {
			return false, nil
		}
		next := slices.DeleteFunc(slices.Clone(record.AddedBots), func(b string) bool { return b == bot })
		servers := maps.Clone(record.BotServers)
		delete(servers, bot)
		if add {
			if len(next) >= 50 {
				return false, fmt.Errorf("A project can have at most 50 bots.")
			}
			next = append(next, bot)
			if servers == nil {
				servers = map[string]string{}
			}
			servers[bot] = from
		}
		if len(next) == 0 {
			next = nil
		}
		if len(servers) == 0 {
			servers = nil
		}
		record.AddedBots, record.BotServers = next, servers
		projects[id] = record
		return true, nil
	})
	if err == nil {
		log.Printf("[CREW_BOT] mac project=%s bot=%s action=%s", projectID, bot, map[bool]string{true: "add_to_project", false: "remove_from_project"}[add])
	}
	return err
}

// macBotProject is a project on this Mac a bot can be added to, with the bots already in it and their servers.
type macBotProject struct {
	ProjectID  string            `json:"project_id"`
	Title      string            `json:"title"`
	AddedBots  []string          `json:"added_bots"`
	BotServers map[string]string `json:"bot_servers"`
}

func macBotProjects() []macBotProject {
	records, err := defaultProjectOwners().All()
	if err != nil {
		return []macBotProject{}
	}
	out := []macBotProject{}
	for _, rec := range records {
		if rec.Product != crewProfileID || rec.ProjectID == "" || crewRelayTwin(rec.ProjectID) {
			continue
		}
		_, title := crewOwnerAndTitle(rec.ProjectID)
		added, servers := rec.AddedBots, rec.BotServers
		if added == nil {
			added = []string{}
		}
		if servers == nil {
			servers = map[string]string{}
		}
		out = append(out, macBotProject{ProjectID: rec.ProjectID, Title: firstNonEmptyTrimmed(title, rec.Folder), AddedBots: added, BotServers: servers})
	}
	sort.SliceStable(out, func(i, j int) bool { return strings.ToLower(out[i].Title) < strings.ToLower(out[j].Title) })
	return out
}

// handleMacProjectBots serves the bots routes on a Mac: the projects here (to add a bot to) and each one's bots.
// GET  /api/bots/mac-projects                  this Mac's projects, with the bots added to each and their servers
// GET  /api/bots/project?project_id=           the bots added to a project here
// POST /api/bots/project                       add a server's bot to a project here (server: the sign-in), or take it out
func (api *StreamingAPI) handleMacProjectBots(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	if claims == nil || claims.UserID == "" {
		externalError(w, 401, "authentication_required", "Sign in to use bots.")
		return
	}
	owner := claims.UserID
	path := strings.TrimSuffix(r.URL.Path, "/")
	switch {
	case r.Method == http.MethodGet && strings.HasSuffix(path, "/bots/mac-projects"):
		externalJSON(w, map[string]any{"projects": macBotProjects()})
	case r.Method == http.MethodGet && strings.HasSuffix(path, "/bots/project"):
		bots, unavailable, err := macProjectBots(r.Context(), owner, r.URL.Query().Get("project_id"))
		if err != nil {
			externalError(w, 409, "bots_unavailable", err.Error())
			return
		}
		externalJSON(w, map[string]any{"bots": bots, "unavailable": unavailable})
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/bots/project"):
		var body struct {
			ProjectID string `json:"project_id"`
			Bot       string `json:"bot"`
			Add       bool   `json:"add"`
			Server    string `json:"server"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&body) != nil || decoder.Decode(new(any)) != io.EOF {
			externalError(w, 400, "invalid_arguments", "invalid request")
			return
		}
		if err := setMacProjectBot(r.Context(), owner, body.ProjectID, strings.TrimSpace(body.Bot), body.Server, body.Add); err != nil {
			externalError(w, 400, "bot_refused", err.Error())
			return
		}
		bots, unavailable, _ := macProjectBots(r.Context(), owner, body.ProjectID)
		externalJSON(w, map[string]any{"bots": bots, "unavailable": unavailable})
	default:
		externalError(w, 404, "server_only", "Bots are made and used on the server; a project on this Mac can add them.")
	}
}

// registerMacProjectBotsTool gives a Mac project's chat the bots tool: add and remove change this project; list, ask,
// read and call go to the servers this Mac is signed in to, as the person; making a bot (get, set) happens in a project
// on a server.
func (api *StreamingAPI) registerMacProjectBotsTool(reg definitionToolRegistrar, gate *productToolGate, claims *UserClaims, projectID string) error {
	var description string
	var props map[string]any
	externalBotsDefinition(func(_ string, d string, _ bool, _ bool, p map[string]any, _ ...string) {
		description, props = d, p
	})
	owner := claims.UserID
	gate.Declare(externalBotsTool)
	return reg.RegisterCustomToolWithTimeout(externalBotsTool, description+" This project is on your Mac: its bots come from the servers this Mac is signed in to; add and remove default to this project.", map[string]any{"type": "object", "properties": props, "required": []string{"action"}}, func(ctx context.Context, args map[string]any) (string, error) {
		str := func(key string) string { return strings.TrimSpace(externalArg(args, key)) }
		project := str("crew_id")
		if project == "" {
			project = projectID
		}
		reply := func(value any) (string, error) {
			data, err := json.Marshal(value)
			return string(data), err
		}
		_, rec, err := macProjectRecord(project)
		if err != nil {
			return "", err
		}
		switch str("action") {
		case "add", "remove":
			if err := setMacProjectBot(ctx, owner, project, str("bot"), "", str("action") == "add"); err != nil {
				return "", err
			}
			bots, unavailable, err := macProjectBots(ctx, owner, project)
			if err != nil {
				return "", err
			}
			return reply(map[string]any{"project_bots": bots, "unavailable": unavailable})
		case "list":
			shared, err := macAllBots(ctx, owner)
			if err != nil {
				return "", err
			}
			bots, unavailable, _ := macProjectBots(ctx, owner, project)
			return reply(map[string]any{"bots": shared, "project_bots": bots, "project_bots_unavailable": unavailable})
		case "ask", "call":
			c, err := macBotServer(ctx, owner, rec, str("bot"))
			if err != nil {
				return "", err
			}
			var out map[string]any
			if str("action") == "ask" {
				err = macServerJSON(ctx, owner, c, http.MethodPost, "/api/bots/ask", map[string]string{"bot": str("bot"), "message": str("message"), "inbox_id": str("inbox_id"), "submission_id": str("submission_id")}, &out)
			} else {
				input, _ := args["input"].(map[string]any)
				err = macServerJSON(ctx, owner, c, http.MethodPost, "/api/bots/call", map[string]any{"bot": str("bot"), "function": str("function"), "args": input, "submission_id": str("submission_id")}, &out)
			}
			if err != nil {
				return "", err
			}
			return reply(out)
		case "read":
			// The answer is on the server the ask went to; try each sign-in until one has it.
			conns, err := macSignIns(owner)
			if err != nil {
				return "", err
			}
			for _, c := range conns {
				var out map[string]any
				if err = macServerJSON(ctx, owner, c, http.MethodGet, "/api/bots/read?inbox_id="+url.QueryEscape(str("inbox_id")), nil, &out); err == nil {
					return reply(out)
				}
			}
			return "", err
		case "get", "set":
			return "", fmt.Errorf("Bots are made from agents of projects on a server. This project is on your Mac: it can add and use bots, not make them.")
		default:
			return "", fmt.Errorf("action must be list, ask, read, call, add or remove.")
		}
	}, time.Minute, "workspace_advanced")
}
